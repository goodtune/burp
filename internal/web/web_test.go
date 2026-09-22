package web

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goodtune/burp/internal/bus"
	"github.com/goodtune/burp/internal/config"
	"github.com/goodtune/burp/internal/crypto"
	"github.com/goodtune/burp/internal/gh"
	"github.com/goodtune/burp/internal/store"
	"github.com/goodtune/burp/internal/store/sqlstore"
)

// fakeGitHub is a scripted GitHub for the web handlers.
type fakeGitHub struct {
	mu       sync.Mutex
	srv      *httptest.Server
	reviews  []map[string]any
	comments []string
	replies  []string
	resolved []string
	merged   bool
	head     string
	threads  string
}

const prJSON = `{
  "id": "PR_1", "number": 5, "title": "Add rate limiting", "bodyHTML": "<p>body</p>", "url": "https://github.com/acme/api/pull/5",
  "state": "OPEN", "isDraft": false, "merged": false, "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN", "reviewDecision": "REVIEW_REQUIRED",
  "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-02T00:00:00Z", "additions": 3, "deletions": 1, "changedFiles": 2,
  "baseRefName": "main", "headRefName": "feat", "headRefOid": "HEAD", "baseRefOid": "BASE", "viewerCanUpdate": true, "viewerDidAuthor": false,
  "author": {"login": "alice"}, "repository": {"nameWithOwner": "acme/api", "squashMergeAllowed": true, "mergeCommitAllowed": true, "viewerPermission": "WRITE"},
  "labels": {"nodes": [{"name": "bug", "color": "ff0000"}]},
  "commits": {"totalCount": 2, "nodes": [{"commit": {"oid": "C1", "abbreviatedOid": "C1", "messageHeadline": "first"}}, {"commit": {"oid": "HEAD", "abbreviatedOid": "HEAD", "messageHeadline": "second"}}]},
  "headCommit": {"nodes": [{"commit": {"statusCheckRollup": {"state": "SUCCESS", "contexts": {"nodes": [{"__typename": "CheckRun", "name": "unit", "status": "COMPLETED", "conclusion": "SUCCESS"}]}}}}]},
  "reviewRequests": {"nodes": [{"requestedReviewer": {"login": "me"}}]},
  "latestReviews": {"nodes": []}, "reviews": {"nodes": []},
  "reviewThreads": {"nodes": THREADS},
  "comments": {"nodes": [{"databaseId": 1, "author": {"login": "dave"}, "bodyHTML": "<p>LGTM but</p>", "createdAt": "2026-01-02T00:00:00Z"}]}
}`

const threadsJSON = `[{"id": "T1", "isResolved": false, "viewerCanResolve": true, "path": "a.go", "line": 2, "diffSide": "RIGHT",
  "comments": {"nodes": [{"id": "C", "databaseId": 10, "author": {"login": "carol"}, "body": "hm", "bodyHTML": "<p>hm</p>", "createdAt": "2026-01-02T00:00:00Z"}]}},
 {"id": "T2", "isResolved": true, "isOutdated": true, "path": "a.go", "line": null, "originalLine": 9, "comments": {"nodes": []}}]`

const inboxNode = `{"number": 5, "title": "Add rate limiting", "url": "u", "state": "OPEN", "reviewDecision": "REVIEW_REQUIRED",
 "updatedAt": "2026-01-02T00:00:00Z", "createdAt": "2026-01-01T00:00:00Z", "additions": 3, "deletions": 1, "changedFiles": 2, "headRefOid": "HEAD",
 "repository": {"nameWithOwner": "acme/api"}, "author": {"login": "alice"},
 "commits": {"nodes": [{"commit": {"statusCheckRollup": {"state": "SUCCESS"}}}]}, "reviewRequests": {"nodes": []}, "latestReviews": {"nodes": []}}`

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{head: "HEAD", threads: threadsJSON}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/login/oauth/access_token":
			if !strings.Contains(string(body), "code=good") && !strings.Contains(string(body), "refresh_token") {
				w.Write([]byte(`{"error":"bad_verification_code"}`))
				return
			}
			w.Write([]byte(`{"access_token":"ghu_tok","refresh_token":"ghr_tok","expires_in":28800,"refresh_token_expires_in":15897600}`))
		case r.Header.Get("Authorization") != "Bearer ghu_tok":
			w.WriteHeader(401)
			w.Write([]byte(`{"message":"Bad credentials"}`))
		case r.URL.Path == "/graphql":
			var req struct {
				Query string `json:"query"`
				Vars  map[string]any
			}
			json.Unmarshal(body, &req)
			switch {
			case strings.Contains(req.Query, "needsReview: search"):
				w.Write([]byte(`{"data": {"needsReview": {"nodes": [` + inboxNode + `]}, "returned": {"nodes": []}, "approved": {"nodes": []}, "waiting": {"nodes": []}, "drafts": {"nodes": []}, "merged": {"nodes": []}}}`))
			case strings.Contains(req.Query, "resolveReviewThread") || strings.Contains(req.Query, "unresolveReviewThread"):
				f.resolved = append(f.resolved, req.Query[:20])
				w.Write([]byte(`{"data": {}}`))
			case strings.Contains(req.Query, "pullRequest(number"):
				pr := strings.Replace(prJSON, "THREADS", f.threads, 1)
				pr = strings.ReplaceAll(pr, `"HEAD"`, `"`+f.head+`"`)
				if f.merged {
					pr = strings.Replace(pr, `"merged": false`, `"merged": true`, 1)
					pr = strings.Replace(pr, `"state": "OPEN"`, `"state": "MERGED"`, 1)
				}
				w.Write([]byte(`{"data": {"repository": {"pullRequest": ` + pr + `}}}`))
			default:
				w.Write([]byte(`{"errors": [{"message": "unexpected query"}]}`))
			}
		case r.URL.Path == "/api/v3/user":
			w.Write([]byte(`{"id": 77, "login": "me", "avatar_url": "https://a/77"}`))
		case strings.HasSuffix(r.URL.Path, "/pulls/5/files"):
			if r.URL.Query().Get("page") != "" && r.URL.Query().Get("page") != "1" {
				w.Write([]byte(`[]`))
				return
			}
			w.Write([]byte(`[{"filename": "a.go", "status": "modified", "additions": 2, "deletions": 1, "patch": "@@ -1,2 +1,3 @@\n context\n-old line\n+new line\n+another"},
			{"filename": "docs/b.md", "status": "added", "additions": 1, "deletions": 0, "patch": "@@ -0,0 +1 @@\n+# hi"}]`))
		case strings.Contains(r.URL.Path, "/compare/"):
			w.Write([]byte(`{"files": [{"filename": "a.go", "status": "modified", "patch": "@@ -1 +1 @@\n-x\n+y"}]}`))
		case strings.HasSuffix(r.URL.Path, "/pulls/5/reviews"):
			var rv map[string]any
			json.Unmarshal(body, &rv)
			f.reviews = append(f.reviews, rv)
			w.Write([]byte(`{"id": 1}`))
		case strings.HasSuffix(r.URL.Path, "/pulls/5/comments"):
			f.replies = append(f.replies, string(body))
			w.WriteHeader(201)
			w.Write([]byte(`{"id": 2}`))
		case strings.HasSuffix(r.URL.Path, "/issues/5/comments"):
			f.comments = append(f.comments, string(body))
			w.WriteHeader(201)
			w.Write([]byte(`{"id": 3}`))
		case strings.HasSuffix(r.URL.Path, "/pulls/5/merge"):
			f.merged = true
			w.Write([]byte(`{"merged": true}`))
		case strings.HasSuffix(r.URL.Path, "/user/installations"):
			w.Write([]byte(`{"total_count": 1, "installations": [{"id": 9, "account": {"login": "acme", "type": "Organization"}, "html_url": "https://github.com/x", "repository_selection": "all"}]}`))
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"message": "not found: ` + r.URL.Path + `"}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

type harness struct {
	t      *testing.T
	gh     *fakeGitHub
	srv    *Server
	app    *httptest.Server
	store  *sqlstore.Store
	bus    *bus.Bus
	cookie *http.Cookie
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := newFakeGitHub(t)
	st, err := sqlstore.Open(context.Background(), sqlstore.SQLite, filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	key, _ := crypto.GenerateKey()
	enc, _ := crypto.NewEncryptor(key)
	cfg, err := config.LoadFrom(func(k string) string {
		return map[string]string{
			"BURP_DEV_MODE": "true", "BURP_BASE_URL": "http://burp.test", "BURP_GITHUB_CLIENT_ID": "id", "BURP_GITHUB_CLIENT_SECRET": "sec",
			"BURP_GITHUB_WEBHOOK_SECRET": "hook", "BURP_GITHUB_APP_SLUG": "burp", "BURP_GITHUB_URL": f.srv.URL, "BURP_GITHUB_API_URL": f.srv.URL + "/api/v3",
		}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	oauth := &gh.OAuth{ClientID: "id", ClientSecret: "sec", WebURL: f.srv.URL, RedirectURL: cfg.CallbackURL()}
	tokens := &gh.Tokens{Store: st, Enc: enc, OAuth: oauth}
	b := bus.New()
	srv, err := New(Deps{Config: cfg, Store: st, Tokens: tokens, OAuth: oauth, Bus: b, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), AppSlug: "burp",
		Clients: &gh.ClientFactory{APIURL: f.srv.URL + "/api/v3", GraphQLURL: f.srv.URL + "/graphql"}})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(srv.Handler())
	t.Cleanup(app.Close)
	return &harness{t: t, gh: f, srv: srv, app: app, store: st, bus: b}
}

// signIn runs the OAuth callback and keeps the session cookie.
func (h *harness) signIn() {
	h.t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(h.app.URL + "/auth/login")
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	state := loc.Query().Get("state")
	if resp.StatusCode != 303 || state == "" || loc.Query().Get("client_id") != "id" {
		h.t.Fatalf("login redirect: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, err = client.Get(h.app.URL + "/auth/callback?code=good&state=" + state)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/inbox" {
		h.t.Fatalf("callback: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			h.cookie = c
		}
	}
	if h.cookie == nil {
		h.t.Fatal("no session cookie")
	}
}

func (h *harness) request(method, path string, signals any, datastarReq bool) *http.Response {
	h.t.Helper()
	var body io.Reader
	u := h.app.URL + path
	if signals != nil {
		js, _ := json.Marshal(signals)
		if method == http.MethodGet || method == http.MethodDelete {
			sep := "?"
			if strings.Contains(u, "?") {
				sep = "&"
			}
			u += sep + "datastar=" + url.QueryEscape(string(js))
		} else {
			body = strings.NewReader(string(js))
		}
	}
	req, _ := http.NewRequest(method, u, body)
	if h.cookie != nil {
		req.AddCookie(h.cookie)
	}
	if datastarReq {
		req.Header.Set("Datastar-Request", "true")
		req.Header.Set("Origin", "http://burp.test")
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp
}

// readSSE reads the stream until the given number of events or timeout.
func readSSE(t *testing.T, resp *http.Response, want int, timeout time.Duration) []string {
	t.Helper()
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("not an SSE response: %s %d %s", ct, resp.StatusCode, b)
	}
	var events []string
	var cur strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 8<<20)
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if cur.Len() > 0 {
					events = append(events, cur.String())
					cur.Reset()
					if len(events) >= want {
						return
					}
				}
				continue
			}
			cur.WriteString(line + "\n")
		}
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for %d events, got %d: %v", want, len(events), events)
	}
	return events
}

func joinEvents(ev []string) string { return strings.Join(ev, "\n---\n") }

// ---- tests --------------------------------------------------------------

func TestIndexAndAuthGuards(t *testing.T) {
	h := newHarness(t)
	resp := h.request(http.MethodGet, "/", nil, false)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Sign in with GitHub") || !strings.Contains(string(body), "/apps/burp/installations/new") {
		t.Fatalf("index: %d %s", resp.StatusCode, body)
	}
	resp = h.request(http.MethodGet, "/inbox", nil, false)
	if resp.StatusCode != 303 || !strings.Contains(resp.Header.Get("Location"), "return_to=%2Finbox") {
		t.Fatalf("inbox unauthenticated: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp = h.request(http.MethodGet, "/inbox/stream", nil, true)
	ev := readSSE(t, resp, 1, 2*time.Second)
	if !strings.Contains(joinEvents(ev), "window.location") {
		t.Fatalf("datastar request should be redirected: %v", ev)
	}
	resp = h.request(http.MethodGet, "/healthz", nil, false)
	if resp.StatusCode != 200 {
		t.Fatal("healthz")
	}
	resp = h.request(http.MethodGet, "/readyz", nil, false)
	if resp.StatusCode != 200 {
		t.Fatal("readyz")
	}
	resp = h.request(http.MethodGet, "/static/app.css", nil, false)
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") == "" {
		t.Fatal("static")
	}
}

func TestSignInAndSettings(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	u, err := h.store.GetUser(context.Background(), 77)
	if err != nil || u.Login != "me" {
		t.Fatalf("user not stored: %v %v", u, err)
	}
	c, err := h.store.GetCredential(context.Background(), 77)
	if err != nil || c.AccessToken == "ghu_tok" {
		t.Fatalf("credential must be encrypted: %v %v", c, err)
	}
	resp := h.request(http.MethodGet, "/", nil, false)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/inbox" {
		t.Fatal("signed-in index should redirect to inbox")
	}
	resp = h.request(http.MethodGet, "/settings", nil, false)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "acme") || !strings.Contains(string(body), "@me") {
		t.Fatalf("settings: %d %s", resp.StatusCode, body)
	}
	// Bad callback state.
	resp = h.request(http.MethodGet, "/auth/callback?code=good&state=nope", nil, false)
	if resp.StatusCode != 400 {
		t.Fatalf("bad state: %d", resp.StatusCode)
	}
	resp = h.request(http.MethodGet, "/auth/callback?error=access_denied", nil, false)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/?reason=denied" {
		t.Fatal("denied")
	}
	// Logout requires same origin.
	resp = h.request(http.MethodPost, "/auth/logout", nil, false)
	if resp.StatusCode != 403 {
		t.Fatalf("logout without origin: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, h.app.URL+"/auth/logout", nil)
	req.AddCookie(h.cookie)
	req.Header.Set("Origin", "http://burp.test")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, _ = client.Do(req)
	if resp.StatusCode != 303 {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	resp = h.request(http.MethodGet, "/inbox", nil, false)
	if resp.StatusCode != 303 {
		t.Fatal("session should be gone")
	}
}

func TestInboxStream(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	resp := h.request(http.MethodGet, "/inbox", nil, false)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "@get('/inbox/stream')") {
		t.Fatalf("inbox page: %d %s", resp.StatusCode, body)
	}
	resp = h.request(http.MethodGet, "/inbox/stream", map[string]any{"filter": "repo:acme/api"}, true)
	ev := readSSE(t, resp, 1, 3*time.Second)
	out := joinEvents(ev)
	if !strings.Contains(out, "event: datastar-patch-elements") || !strings.Contains(out, `id="inbox-sections"`) || !strings.Contains(out, "Add rate limiting") || !strings.Contains(out, "Needs your review") {
		t.Fatalf("stream: %s", out)
	}
	if !strings.Contains(out, "filter: <code>repo:acme/api</code>") {
		t.Fatalf("filter not echoed: %s", out)
	}
	// A webhook event for the user triggers a re-render.
	resp = h.request(http.MethodGet, "/inbox/stream", nil, true)
	go func() {
		time.Sleep(300 * time.Millisecond)
		h.bus.Publish(bus.Event{Topic: bus.UserTopic("me"), Kind: "pull_request"})
	}()
	ev = readSSE(t, resp, 2, 6*time.Second)
	if len(ev) != 2 || !strings.Contains(ev[1], "inbox-sections") {
		t.Fatalf("expected re-render after event: %v", ev)
	}
}

func TestPRPageAndActions(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	ctx := context.Background()
	resp := h.request(http.MethodGet, "/pr/acme/api/5", nil, false)
	body, _ := io.ReadAll(resp.Body)
	page := string(body)
	if resp.StatusCode != 200 {
		t.Fatalf("pr page: %d %s", resp.StatusCode, page)
	}
	for _, want := range []string{"Add rate limiting", "a.go", "docs/b.md", "new line", "old line", "@carol", "Needs approval", "r1 · C1", "(latest)", "unresolved", "outdated", "data-signals=", "/pr/acme/api/5/stream", "Submit review", `id="pr-diff"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("page missing %q:\n%s", want, page)
		}
	}
	// Author cannot approve; we are not the author so approve is offered.
	if !strings.Contains(page, `value="APPROVE"`) {
		t.Fatal("approve option missing")
	}

	// CSRF: POST without the datastar header is refused.
	resp = h.request(http.MethodPost, "/pr/acme/api/5/mark", map[string]any{"markPath": "a.go", "marked": true}, false)
	if resp.StatusCode != 403 {
		t.Fatalf("csrf: %d", resp.StatusCode)
	}

	// Mark a.go reviewed.
	resp = h.request(http.MethodPost, "/pr/acme/api/5/mark", map[string]any{"file": "a.go", "markPath": "a.go", "marked": true, "head": "HEAD"}, true)
	ev := readSSE(t, resp, 5, 3*time.Second)
	out := joinEvents(ev)
	if !strings.Contains(out, `id="pr-files"`) || !strings.Contains(out, "<b>1/2</b> reviewed") {
		t.Fatalf("mark: %s", out)
	}
	marks, _ := h.store.ListFileMarks(ctx, 77, store.PRKey{Owner: "acme", Repo: "api", Number: 5})
	if len(marks) != 1 || marks[0].HeadSHA != "HEAD" {
		t.Fatalf("marks %+v", marks)
	}

	// Keyboard: n jumps to the next unreviewed file (docs/b.md).
	resp = h.request(http.MethodPost, "/pr/acme/api/5/key", map[string]any{"file": "a.go", "key": "n"}, true)
	out = joinEvents(readSSE(t, resp, 6, 3*time.Second))
	if !strings.Contains(out, `"file":"docs/b.md"`) || !strings.Contains(out, "# hi") {
		t.Fatalf("key n: %s", out)
	}
	// Keyboard: v toggles split view.
	resp = h.request(http.MethodPost, "/pr/acme/api/5/key", map[string]any{"file": "a.go", "key": "v", "view": "unified"}, true)
	out = joinEvents(readSSE(t, resp, 6, 3*time.Second))
	if !strings.Contains(out, `"view":"split"`) || !strings.Contains(out, `class="diff split"`) {
		t.Fatalf("key v: %s", out)
	}

	// Open the composer on a.go line 2 (RIGHT) and save a draft.
	comp := map[string]any{"path": "a.go", "side": "RIGHT", "line": 3, "startLine": 2, "draftId": "", "body": "consider x"}
	resp = h.request(http.MethodGet, "/pr/acme/api/5/view", map[string]any{"file": "a.go", "composer": comp}, true)
	out = joinEvents(readSSE(t, resp, 5, 3*time.Second))
	if !strings.Contains(out, "Add to review") || !strings.Contains(out, "lines") {
		t.Fatalf("composer not rendered: %s", out)
	}
	resp = h.request(http.MethodPost, "/pr/acme/api/5/draft", map[string]any{"file": "a.go", "composer": comp, "head": "HEAD"}, true)
	out = joinEvents(readSSE(t, resp, 6, 3*time.Second))
	if !strings.Contains(out, "consider x") || !strings.Contains(out, "1 draft") || !strings.Contains(out, `"composer":{"path":""`) {
		t.Fatalf("draft: %s", out)
	}
	drafts, _ := h.store.ListDrafts(ctx, 77, store.PRKey{Owner: "acme", Repo: "api", Number: 5})
	if len(drafts) != 1 || drafts[0].StartLine != 2 || drafts[0].Line != 3 || drafts[0].CommitSHA != "HEAD" {
		t.Fatalf("drafts %+v", drafts)
	}
	// Edit the draft.
	comp["draftId"], comp["body"] = drafts[0].ID, "edited"
	resp = h.request(http.MethodPost, "/pr/acme/api/5/draft", map[string]any{"file": "a.go", "composer": comp}, true)
	readSSE(t, resp, 6, 3*time.Second)
	d, _ := h.store.GetDraft(ctx, 77, drafts[0].ID)
	if d.Body != "edited" {
		t.Fatalf("draft not edited: %+v", d)
	}
	// Empty draft is rejected with an error box.
	resp = h.request(http.MethodPost, "/pr/acme/api/5/draft", map[string]any{"file": "a.go", "composer": map[string]any{"path": "a.go", "side": "RIGHT", "line": 1, "body": "  "}}, true)
	out = joinEvents(readSSE(t, resp, 1, 3*time.Second))
	if !strings.Contains(out, `id="pr-notice"`) || !strings.Contains(out, "needs a line") {
		t.Fatalf("error box: %s", out)
	}

	// Submit the review.
	resp = h.request(http.MethodPost, "/pr/acme/api/5/submit", map[string]any{"file": "a.go", "reviewEvent": "APPROVE", "reviewBody": "ship it", "reviewOpen": true}, true)
	out = joinEvents(readSSE(t, resp, 6, 3*time.Second))
	if !strings.Contains(out, `"reviewOpen":false`) || !strings.Contains(out, `"notice":"Review submitted (approve) with 1 comment(s)."`) {
		t.Fatalf("submit: %s", out)
	}
	h.gh.mu.Lock()
	reviews := h.gh.reviews
	h.gh.mu.Unlock()
	if len(reviews) != 1 || reviews[0]["event"] != "APPROVE" || reviews[0]["commit_id"] != "HEAD" || reviews[0]["body"] != "ship it" {
		t.Fatalf("review posted: %+v", reviews)
	}
	cm := reviews[0]["comments"].([]any)[0].(map[string]any)
	if cm["path"] != "a.go" || cm["line"] != float64(3) || cm["start_line"] != float64(2) || cm["side"] != "RIGHT" || cm["body"] != "edited" {
		t.Fatalf("review comment: %+v", cm)
	}
	if drafts, _ := h.store.ListDrafts(ctx, 77, store.PRKey{Owner: "acme", Repo: "api", Number: 5}); len(drafts) != 0 {
		t.Fatal("drafts should be cleared after submit")
	}

	// Reply, resolve, comment, merge.
	resp = h.request(http.MethodPost, "/pr/acme/api/5/reply", map[string]any{"replyTo": 10, "replyBody": "thanks"}, true)
	out = joinEvents(readSSE(t, resp, 6, 3*time.Second))
	if !strings.Contains(out, `"replyTo":0`) {
		t.Fatalf("reply: %s", out)
	}
	resp = h.request(http.MethodPost, "/pr/acme/api/5/resolve", map[string]any{"threadId": "T1", "resolve": true}, true)
	readSSE(t, resp, 6, 3*time.Second)
	resp = h.request(http.MethodPost, "/pr/acme/api/5/comment", map[string]any{"commentBody": "general"}, true)
	readSSE(t, resp, 6, 3*time.Second)
	resp = h.request(http.MethodPost, "/pr/acme/api/5/merge", map[string]any{"mergeMethod": "squash", "mergeOpen": true}, true)
	out = joinEvents(readSSE(t, resp, 6, 3*time.Second))
	if !strings.Contains(out, `"notice":"Merged."`) || !strings.Contains(out, "action-merged") {
		t.Fatalf("merge: %s", out)
	}
	h.gh.mu.Lock()
	defer h.gh.mu.Unlock()
	if len(h.gh.replies) != 1 || !strings.Contains(h.gh.replies[0], "thanks") || len(h.gh.resolved) != 1 || len(h.gh.comments) != 1 || !h.gh.merged {
		t.Fatalf("github calls: replies=%v resolved=%v comments=%v merged=%v", h.gh.replies, h.gh.resolved, h.gh.comments, h.gh.merged)
	}
}

func TestPRStaleMarksAndRevisions(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	ctx := context.Background()
	key := store.PRKey{Owner: "acme", Repo: "api", Number: 5}
	// Reviewed a.go at an older commit that is no longer on the branch.
	if err := h.store.SetFileMark(ctx, &store.FileMark{UserID: 77, Owner: "acme", Repo: "api", Number: 5, Path: "a.go", HeadSHA: "OLD"}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetFileMark(ctx, &store.FileMark{UserID: 77, Owner: "acme", Repo: "api", Number: 5, Path: "docs/b.md", HeadSHA: "OLD"}); err != nil {
		t.Fatal(err)
	}
	resp := h.request(http.MethodGet, "/pr/acme/api/5", nil, false)
	body, _ := io.ReadAll(resp.Body)
	page := string(body)
	// a.go changed between OLD and HEAD (compare fake returns a.go), b.md did not.
	if !strings.Contains(page, "changed since you reviewed") || !strings.Contains(page, "your last review") || !strings.Contains(page, "<b>1/2</b> reviewed") {
		t.Fatalf("stale marks: %s", page)
	}
	// Compare a revision range.
	resp = h.request(http.MethodGet, "/pr/acme/api/5/view", map[string]any{"base": "C1", "head": "HEAD"}, true)
	out := joinEvents(readSSE(t, resp, 6, 3*time.Second))
	if !strings.Contains(out, "</span>x</td>") || !strings.Contains(out, "</span>y</td>") || !strings.Contains(out, `"base":"C1"`) {
		t.Fatalf("compare view: %s", out)
	}
	// Hide reviewed files.
	resp = h.request(http.MethodGet, "/pr/acme/api/5/view", map[string]any{"hideReviewed": true, "file": "a.go"}, true)
	out = joinEvents(readSSE(t, resp, 5, 3*time.Second))
	if !strings.Contains(out, "1 reviewed file(s) hidden") {
		t.Fatalf("hide reviewed: %s", out)
	}
	_ = key
}

func TestPRStreamPushesOnChange(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	// Connect with the current version so nothing is pushed until a change.
	resp := h.request(http.MethodGet, "/pr/acme/api/5", nil, false)
	body, _ := io.ReadAll(resp.Body)
	i := strings.Index(string(body), `prVersion&#34;:&#34;`)
	if i < 0 {
		t.Fatalf("no version in page: %s", body)
	}
	ver := string(body)[i+len(`prVersion&#34;:&#34;`) : i+len(`prVersion&#34;:&#34;`)+16]
	resp = h.request(http.MethodGet, "/pr/acme/api/5/stream", map[string]any{"prVersion": ver, "head": "HEAD"}, true)
	go func() {
		time.Sleep(200 * time.Millisecond)
		h.gh.mu.Lock()
		h.gh.head = "HEAD2"
		h.gh.mu.Unlock()
		h.bus.Publish(bus.Event{Topic: bus.PRTopic("acme", "api", 5), Kind: "pull_request", Action: "synchronize"})
	}()
	ev := readSSE(t, resp, 2, 6*time.Second)
	out := joinEvents(ev)
	if !strings.Contains(out, `id="pr-header"`) || !strings.Contains(out, "New commits were pushed") || !strings.Contains(out, `"prVersion": "`) {
		t.Fatalf("stream: %s", out)
	}
}

func TestPRNotFoundAndBadKey(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	resp := h.request(http.MethodGet, "/pr/acme/api/abc", nil, false)
	if resp.StatusCode != 400 {
		t.Fatalf("bad number: %d", resp.StatusCode)
	}
	resp = h.request(http.MethodGet, "/pr/acme/bad%24name/5", nil, false)
	if resp.StatusCode != 400 {
		t.Fatalf("bad repo: %d", resp.StatusCode)
	}
	resp = h.request(http.MethodGet, "/pr/acme/api/6", nil, false)
	if resp.StatusCode != 404 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("missing pr: %d %s", resp.StatusCode, b)
	}
}

func TestWebhookEndpoint(t *testing.T) {
	h := newHarness(t)
	sub := h.bus.Open(bus.PRTopic("acme", "api", 42))
	defer sub.Cancel()
	payload := `{"action":"opened","number":42,"pull_request":{"number":42,"user":{"login":"alice"}},"repository":{"name":"api","full_name":"acme/api","owner":{"login":"acme"}}}`
	req, _ := http.NewRequest(http.MethodPost, h.app.URL+"/webhooks/github", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", "sha256=bad")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 400 {
		t.Fatalf("bad signature: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, h.app.URL+"/webhooks/github", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", signature("hook", payload))
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 202 {
		t.Fatalf("webhook: %d", resp.StatusCode)
	}
	select {
	case ev := <-sub.C:
		if ev.Kind != "pull_request" || ev.Action != "opened" {
			t.Fatalf("event %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no bus event")
	}
}

func TestHelpers(t *testing.T) {
	if sanitizeReturnTo("//evil") != "" || sanitizeReturnTo("/ok") != "/ok" || sanitizeReturnTo("/bad\r\n") != "" || sanitizeReturnTo("http://x") != "" {
		t.Fatal("sanitizeReturnTo")
	}
	if !validName("acme-1.x_y") || validName("..") || validName("a/b") || validName("") {
		t.Fatal("validName")
	}
	if relTime(time.Now().Add(-90*time.Second)) != "1m ago" || relTime(time.Now().Add(-3*time.Hour)) != "3h ago" || relTime(time.Now().Add(-48*time.Hour)) != "2d ago" || relTime(time.Time{}) != "" {
		t.Fatal("relTime")
	}
	if string(jsStr(`a"b`)) != `"a\"b"` || pct(1, 0) != 0 || pct(1, 4) != 25 || titleCase("CHANGES_REQUESTED") != "Changes requested" {
		t.Fatal("funcs")
	}
	if basename("a/b/c.go") != "c.go" || dirname("a/b/c.go") != "a/b/" || dirname("c.go") != "" {
		t.Fatal("paths")
	}
	if cleanFilter("  a   b  ") != "a b" {
		t.Fatal("cleanFilter")
	}
	if _, err := dict("a"); err == nil {
		t.Fatal("dict odd")
	}
}
