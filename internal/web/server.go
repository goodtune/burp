// Package web is burp's HTTP layer: server-rendered HTML pages whose live
// regions are patched over Server-Sent Events using datastar. There is no
// client-side application code; every interaction is a datastar action
// (@get/@post) answered with HTML fragments and signal patches.
package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/goodtune/burp/internal/bus"
	"github.com/goodtune/burp/internal/config"
	"github.com/goodtune/burp/internal/gh"
	"github.com/goodtune/burp/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

const (
	sessionCookie = "burp_session"
	stateTTL      = 10 * time.Minute
	// inboxRefresh is the periodic full refresh of an open inbox stream,
	// catching PRs in repositories that have not installed the App.
	inboxRefresh = 60 * time.Second
	// eventDebounce coalesces bursts of webhook events into one render.
	eventDebounce = 2 * time.Second
	keepalive     = 25 * time.Second
	maxBody       = 256 << 10
)

// Server holds the dependencies of every handler.
type Server struct {
	cfg     *config.Config
	store   store.Store
	tokens  *gh.Tokens
	oauth   *gh.OAuth
	clients *gh.ClientFactory
	bus     *bus.Bus
	logger  *slog.Logger
	tmpl    *template.Template
	appSlug string
	mux     *http.ServeMux
	started time.Time

	// compareCache remembers which paths changed between two commits so
	// that "changed since you reviewed" markers do not re-hit GitHub.
	compareMu    sync.Mutex
	compareCache map[string]map[string]bool
}

// Deps are the injected collaborators.
type Deps struct {
	Config  *config.Config
	Store   store.Store
	Tokens  *gh.Tokens
	OAuth   *gh.OAuth
	Clients *gh.ClientFactory
	Bus     *bus.Bus
	Logger  *slog.Logger
	AppSlug string
}

// New builds the server and its routes.
func New(d Deps) (*Server, error) {
	s := &Server{
		cfg: d.Config, store: d.Store, tokens: d.Tokens, oauth: d.OAuth, clients: d.Clients,
		bus: d.Bus, logger: d.Logger, appSlug: d.AppSlug, started: time.Now(),
		compareCache: map[string]map[string]bool{},
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	t, err := template.New("").Funcs(s.funcs()).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}
	s.tmpl = t
	s.routes()
	return s, nil
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	m := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	m.Handle("GET /static/", http.StripPrefix("/static/", cacheControl(http.FileServer(http.FS(static)))))
	m.HandleFunc("GET /healthz", s.handleHealth)
	m.HandleFunc("GET /readyz", s.handleReady)
	m.HandleFunc("POST /webhooks/github", s.handleWebhook)

	m.HandleFunc("GET /{$}", s.handleIndex)
	m.HandleFunc("GET /auth/login", s.handleLogin)
	m.HandleFunc("GET /auth/callback", s.handleCallback)
	m.HandleFunc("POST /auth/logout", s.handleLogout)
	m.HandleFunc("POST /auth/logout-all", s.handleLogoutAll)

	m.Handle("GET /inbox", s.requireUser(s.handleInbox))
	m.Handle("GET /inbox/stream", s.requireUser(s.handleInboxStream))
	m.Handle("GET /settings", s.requireUser(s.handleSettings))
	m.Handle("POST /settings/delete-data", s.requireUser(s.handleDeleteData))

	pr := "/pr/{owner}/{repo}/{number}"
	m.Handle("GET "+pr, s.requireUser(s.handlePR))
	m.Handle("GET "+pr+"/stream", s.requireUser(s.handlePRStream))
	m.Handle("GET "+pr+"/view", s.requireUser(s.handlePRView))
	m.Handle("POST "+pr+"/key", s.requireUser(s.handlePRKey))
	m.Handle("POST "+pr+"/mark", s.requireUser(s.handlePRMark))
	m.Handle("POST "+pr+"/draft", s.requireUser(s.handlePRDraft))
	m.Handle("DELETE "+pr+"/draft", s.requireUser(s.handlePRDraftDelete))
	m.Handle("POST "+pr+"/submit", s.requireUser(s.handlePRSubmit))
	m.Handle("POST "+pr+"/reply", s.requireUser(s.handlePRReply))
	m.Handle("POST "+pr+"/resolve", s.requireUser(s.handlePRResolve))
	m.Handle("POST "+pr+"/comment", s.requireUser(s.handlePRComment))
	m.Handle("POST "+pr+"/merge", s.requireUser(s.handlePRMerge))
	m.Handle("POST "+pr+"/ready", s.requireUser(s.handlePRReady))
	s.mux = m
}

func cacheControl(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		h.ServeHTTP(w, r)
	})
}

// ---- sessions -----------------------------------------------------------

type ctxKey struct{}

// userFrom returns the signed-in user or nil.
func userFrom(ctx context.Context) *store.User {
	u, _ := ctx.Value(ctxKey{}).(*store.User)
	return u
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// currentUser resolves the session cookie.
func (s *Server) currentUser(r *http.Request) *store.User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	sess, err := s.store.GetSession(r.Context(), hashToken(c.Value))
	if err != nil || !sess.ExpiresAt.After(time.Now()) {
		return nil
	}
	u, err := s.store.GetUser(r.Context(), sess.UserID)
	if err != nil {
		return nil
	}
	return u
}

// isDatastar reports whether the request came from a datastar action.
func isDatastar(r *http.Request) bool {
	return r.Header.Get("Datastar-Request") == "true"
}

// requireUser authenticates the request. Mutating datastar requests must
// also carry the Datastar-Request header and a matching Origin, which
// together with SameSite cookies blocks cross-site request forgery.
func (s *Server) requireUser(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil {
			if isDatastar(r) {
				sse := datastar.NewSSE(w, r)
				_ = sse.Redirect("/?reason=expired")
				return
			}
			http.Redirect(w, r, "/?return_to="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !isDatastar(r) || !s.sameOrigin(r) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return false
	}
	o, err := url.Parse(origin)
	if err != nil {
		return false
	}
	base, _ := url.Parse(s.cfg.BaseURL)
	return strings.EqualFold(o.Host, base.Host)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/", HttpOnly: true,
		Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: maxAge,
	})
}

// clientFor returns a GitHub client acting as the user.
func (s *Server) clientFor(ctx context.Context, u *store.User) (*gh.Client, error) {
	tok, err := s.tokens.AccessToken(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return s.clients.New(tok), nil
}

// ---- rendering ----------------------------------------------------------

// render executes a named template into w.
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.logger.Error("render failed", "template", name, "error", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	_, _ = buf.WriteTo(w)
}

// fragment renders a named template to a string for an SSE patch.
func (s *Server) fragment(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("rendering %s: %w", name, err)
	}
	return buf.String(), nil
}

// patch renders a fragment and sends it as a patch-elements event.
func (s *Server) patch(sse *datastar.ServerSentEventGenerator, name string, data any, opts ...datastar.PatchElementOption) error {
	html, err := s.fragment(name, data)
	if err != nil {
		return err
	}
	return sse.PatchElements(html, opts...)
}

// errorBox patches an inline error into the element with the given id.
func (s *Server) errorBox(sse *datastar.ServerSentEventGenerator, id, msg, retry string) error {
	return s.patch(sse, "error_box", map[string]any{"ID": id, "Message": msg, "Retry": retry})
}

// userMessage turns an error into text safe to show.
func userMessage(err error) string {
	var ae *gh.APIError
	switch {
	case errors.Is(err, gh.ErrReauthRequired):
		return "Your GitHub authorisation expired. Sign in again."
	case errors.As(err, &ae):
		return fmt.Sprintf("GitHub returned %d: %s", ae.Status, ae.Message)
	}
	var ge gh.GraphQLErrors
	if errors.As(err, &ge) {
		return ge.Error()
	}
	return err.Error()
}

// handleAPIError reports an error on an SSE stream, redirecting to sign in
// when the credential is gone.
func (s *Server) handleAPIError(sse *datastar.ServerSentEventGenerator, id string, err error, retry string) {
	if errors.Is(err, gh.ErrReauthRequired) {
		_ = sse.Redirect("/?reason=expired")
		return
	}
	s.logger.Warn("request failed", "error", err)
	_ = s.errorBox(sse, id, userMessage(err), retry)
}

// ---- misc handlers ------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "ok uptime=%s\n", time.Since(s.started).Truncate(time.Second))
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		http.Error(w, "store unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintln(w, "ready")
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 25<<20)
	d, err := gh.ParseWebhook(r, s.cfg.GitHub.WebhookSecret)
	if err != nil {
		s.logger.Warn("webhook rejected", "error", err)
		http.Error(w, "invalid webhook", http.StatusBadRequest)
		return
	}
	for _, topic := range d.Topics {
		s.bus.Publish(bus.Event{Topic: topic, Kind: d.Event, Action: d.Action})
	}
	s.logger.Info("webhook", "event", d.Event, "action", d.Action, "repo", d.Repo, "delivery", d.ID, "topics", len(d.Topics))
	w.WriteHeader(http.StatusAccepted)
}

// installURL is where users go to install the App.
func (s *Server) installURL() string {
	if s.appSlug == "" {
		return ""
	}
	return s.cfg.GitHub.URL + "/apps/" + s.appSlug + "/installations/new"
}

// prKey parses the path values.
func prKey(r *http.Request) (store.PRKey, error) {
	n, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || n <= 0 {
		return store.PRKey{}, errors.New("invalid pull request number")
	}
	owner, repo := r.PathValue("owner"), r.PathValue("repo")
	if !validName(owner) || !validName(repo) {
		return store.PRKey{}, errors.New("invalid repository")
	}
	return store.PRKey{Owner: owner, Repo: repo, Number: n}, nil
}

func validName(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return s != "." && s != ".."
}

// base shared by page templates.
type pageData struct {
	User       *store.User
	Title      string
	InstallURL string
	DevMode    bool
	Flash      string
}

func (s *Server) page(r *http.Request, title string) pageData {
	return pageData{User: userFrom(r.Context()), Title: title, InstallURL: s.installURL(), DevMode: s.cfg.DevMode}
}
