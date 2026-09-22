package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/goodtune/burp/internal/gh"
	"github.com/goodtune/burp/internal/store"
)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if u := s.currentUser(r); u != nil {
		http.Redirect(w, r, "/inbox", http.StatusSeeOther)
		return
	}
	d := s.page(r, "Sign in")
	switch r.URL.Query().Get("reason") {
	case "expired":
		d.Flash = "Your GitHub authorisation expired. Please sign in again."
	case "denied":
		d.Flash = "GitHub authorisation was cancelled."
	case "error":
		d.Flash = "Signing in failed. Please try again."
	}
	s.render(w, "index", map[string]any{"Page": d, "ReturnTo": sanitizeReturnTo(r.URL.Query().Get("return_to"))})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	state := randomToken()
	st := &store.OAuthState{State: state, ReturnTo: sanitizeReturnTo(r.URL.Query().Get("return_to")), ExpiresAt: time.Now().Add(stateTTL)}
	if err := s.store.PutOAuthState(r.Context(), st); err != nil {
		s.logger.Error("storing oauth state", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, s.oauth.AuthorizeURL(state), http.StatusSeeOther)
}

// handleCallback completes the OAuth flow. GitHub also redirects here after
// the App is installed ("installation_id" + "setup_action"); when that
// redirect carries a code it is exchanged like a normal sign-in.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("error") != "" {
		s.logger.Info("oauth denied", "error", q.Get("error"))
		http.Redirect(w, r, "/?reason=denied", http.StatusSeeOther)
		return
	}
	code := q.Get("code")
	if code == "" {
		if q.Get("installation_id") != "" {
			// Installed without authorising; send them to sign in.
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	returnTo := "/inbox"
	if q.Get("setup_action") == "" {
		st, err := s.store.ConsumeOAuthState(r.Context(), q.Get("state"))
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.logger.Error("consuming oauth state", "error", err)
			}
			http.Error(w, "invalid or expired state; please sign in again", http.StatusBadRequest)
			return
		}
		if st.ReturnTo != "" {
			returnTo = st.ReturnTo
		}
	}
	tr, err := s.oauth.Exchange(r.Context(), code)
	if err != nil {
		s.logger.Error("oauth exchange failed", "error", err)
		http.Redirect(w, r, "/?reason=error", http.StatusSeeOther)
		return
	}
	viewer, err := s.clients.New(tr.AccessToken).Viewer(r.Context())
	if err != nil {
		s.logger.Error("fetching viewer failed", "error", err)
		http.Redirect(w, r, "/?reason=error", http.StatusSeeOther)
		return
	}
	u := &store.User{ID: viewer.ID, Login: viewer.Login, AvatarURL: viewer.AvatarURL}
	if err := s.store.UpsertUser(r.Context(), u); err != nil {
		s.logger.Error("upserting user", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if _, err := s.tokens.Save(r.Context(), u.ID, tr); err != nil {
		s.logger.Error("saving credential", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	tok := randomToken()
	sess := &store.Session{TokenHash: hashToken(tok), UserID: u.ID, ExpiresAt: time.Now().Add(s.cfg.SessionTTL)}
	if err := s.store.CreateSession(r.Context(), sess); err != nil {
		s.logger.Error("creating session", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.setSessionCookie(w, tok, int(s.cfg.SessionTTL.Seconds()))
	s.logger.Info("signed in", "user", u.Login, "id", u.ID)
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = s.store.DeleteSession(r.Context(), hashToken(c.Value))
	}
	s.setSessionCookie(w, "", -1)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleLogoutAll drops every session and the stored GitHub credential.
func (s *Server) handleLogoutAll(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if u := s.currentUser(r); u != nil {
		_ = s.store.DeleteUserSessions(r.Context(), u.ID)
		_ = s.store.DeleteCredential(r.Context(), u.ID)
	}
	s.setSessionCookie(w, "", -1)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// sanitizeReturnTo keeps redirects on-site.
func sanitizeReturnTo(v string) string {
	if v == "" || !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.HasPrefix(v, "/\\") {
		return ""
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return v
}

// ---- settings -----------------------------------------------------------

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	d := s.page(r, "Settings")
	data := map[string]any{"Page": d}
	marks, drafts, err := s.store.CountUserData(r.Context(), u.ID)
	if err != nil {
		s.logger.Error("counting user data", "error", err)
	}
	data["Marks"], data["Drafts"] = marks, drafts
	if exp, err := s.tokens.Expiry(r.Context(), u.ID); err == nil && !exp.IsZero() {
		data["TokenExpiry"] = exp
	}
	if client, err := s.clientFor(r.Context(), u); err == nil {
		insts, err := client.Installations(r.Context())
		if err != nil {
			data["InstallError"] = userMessage(err)
		}
		data["Installations"] = insts
	} else {
		if errors.Is(err, gh.ErrReauthRequired) {
			http.Redirect(w, r, "/?reason=expired", http.StatusSeeOther)
			return
		}
		data["InstallError"] = userMessage(err)
	}
	s.render(w, "settings", data)
}

func (s *Server) handleDeleteData(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if err := s.store.DeleteUserData(r.Context(), u.ID); err != nil {
		s.logger.Error("deleting user data", "error", err)
	}
	sse := newSSE(w, r)
	_ = sse.Redirect("/settings")
}
