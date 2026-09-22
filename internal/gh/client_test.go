package gh

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGitHub serves canned GraphQL and REST responses.
type fakeGitHub struct {
	t       *testing.T
	graphql func(query string, vars map[string]any) (int, string)
	rest    func(r *http.Request) (int, string)
	srv     *httptest.Server
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{t: t}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, `{"message":"Bad credentials"}`, 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/graphql" {
			var req struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			code, resp := f.graphql(req.Query, req.Variables)
			w.WriteHeader(code)
			w.Write([]byte(resp))
			return
		}
		if f.rest == nil {
			http.Error(w, `{"message":"no rest handler"}`, 500)
			return
		}
		code, resp := f.rest(r)
		w.WriteHeader(code)
		w.Write([]byte(resp))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) client(token string) *Client {
	fac := &ClientFactory{APIURL: f.srv.URL + "/api/v3", GraphQLURL: f.srv.URL + "/graphql"}
	return fac.New(token)
}

const prNode = `{"number": 1, "title": "T", "url": "u", "isDraft": false, "state": "OPEN", "reviewDecision": "REVIEW_REQUIRED",
 "updatedAt": "2026-01-01T00:00:00Z", "createdAt": "2026-01-01T00:00:00Z", "additions": 3, "deletions": 1, "changedFiles": 2, "headRefOid": "h",
 "repository": {"nameWithOwner": "acme/api"}, "author": {"login": "alice"},
 "commits": {"nodes": [{"commit": {"statusCheckRollup": {"state": "FAILURE"}}}]},
 "reviewRequests": {"nodes": [{"requestedReviewer": {"login": "bob"}}, {"requestedReviewer": {"slug": "core"}}]},
 "latestReviews": {"nodes": [{"author": {"login": "carol"}, "state": "APPROVED"}]}}`

func TestInbox(t *testing.T) {
	f := newFakeGitHub(t)
	f.graphql = func(query string, vars map[string]any) (int, string) {
		if !strings.Contains(query, "needsReview: search") {
			return 400, `{"errors":[{"message":"unexpected query"}]}`
		}
		if q, _ := vars["needsReview"].(string); !strings.Contains(q, "review-requested:@me") || !strings.HasSuffix(q, "repo:acme/api") {
			return 400, `{"errors":[{"message":"bad needsReview query"}]}`
		}
		return 200, `{"data": {"needsReview": {"nodes": [` + prNode + `, {}]}, "returned": {"nodes": []}, "approved": {"nodes": []}, "waiting": {"nodes": []}, "drafts": {"nodes": []}, "merged": {"nodes": []}}}`
	}
	in, err := f.client("tok").Inbox(context.Background(), "repo:acme/api")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.NeedsReview) != 1 || len(in.Returned) != 0 {
		t.Fatalf("%+v", in)
	}
	p := in.NeedsReview[0]
	if p.Owner() != "acme" || p.Repo() != "api" || p.CheckState() != "FAILURE" {
		t.Fatalf("%+v", p)
	}
	if rr := p.RequestedReviewers(); len(rr) != 2 || rr[0] != "bob" || rr[1] != "core" {
		t.Fatalf("reviewers %v", rr)
	}
	if repos := in.Repos(); len(repos) != 1 || repos[0] != "acme/api" {
		t.Fatalf("repos %v", repos)
	}
}

func TestInboxPartialErrors(t *testing.T) {
	f := newFakeGitHub(t)
	f.graphql = func(query string, vars map[string]any) (int, string) {
		return 200, `{"data": {"needsReview": {"nodes": [` + prNode + `]}, "returned": null}, "errors": [{"message": "rate limited"}]}`
	}
	in, err := f.client("tok").Inbox(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.NeedsReview) != 1 || len(in.Warnings) != 1 || in.Warnings[0] != "rate limited" {
		t.Fatalf("%+v", in)
	}
}

func TestUnauthorized(t *testing.T) {
	f := newFakeGitHub(t)
	f.graphql = func(string, map[string]any) (int, string) { return 200, `{}` }
	if _, err := f.client("bad").Inbox(context.Background(), ""); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("want ErrReauthRequired, got %v", err)
	}
	if _, err := f.client("bad").Viewer(context.Background()); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("want ErrReauthRequired from REST, got %v", err)
	}
}

func TestPullRequest(t *testing.T) {
	f := newFakeGitHub(t)
	f.graphql = func(query string, vars map[string]any) (int, string) {
		if strings.Contains(query, "resolveReviewThread") {
			if vars["id"] != "T1" {
				return 200, `{"errors":[{"message":"bad thread id"}]}`
			}
			return 200, `{"data":{"resolveReviewThread":{"thread":{"id":"T","isResolved":true}}}}`
		}
		if vars["owner"] != "acme" || vars["name"] != "api" || vars["number"] != float64(5) {
			return 200, `{"errors":[{"message":"bad vars"}]}`
		}
		return 200, `{"data": {"repository": {"pullRequest": {
			"id": "PR_1", "number": 5, "title": "Add thing", "state": "OPEN", "mergeable": "MERGEABLE", "mergeStateStatus": "BLOCKED",
			"reviewDecision": "CHANGES_REQUESTED", "headRefOid": "head", "baseRefOid": "base", "viewerCanUpdate": true,
			"author": {"login": "alice"}, "repository": {"nameWithOwner": "acme/api", "squashMergeAllowed": true},
			"commits": {"totalCount": 2, "nodes": [{"commit": {"oid": "c1", "abbreviatedOid": "c1"}}, {"commit": {"oid": "head", "abbreviatedOid": "head"}}]},
			"headCommit": {"nodes": [{"commit": {"statusCheckRollup": {"state": "FAILURE", "contexts": {"nodes": [
				{"__typename": "CheckRun", "name": "unit", "status": "COMPLETED", "conclusion": "SUCCESS", "detailsUrl": "d1", "checkSuite": {"app": {"name": "GitHub Actions"}}},
				{"__typename": "CheckRun", "name": "e2e", "status": "COMPLETED", "conclusion": "FAILURE", "checkSuite": {"app": {"name": "CircleCI"}}},
				{"__typename": "CheckRun", "name": "lint", "status": "IN_PROGRESS"},
				{"__typename": "CheckRun", "name": "opt", "status": "COMPLETED", "conclusion": "SKIPPED"},
				{"__typename": "CheckRun", "name": "neu", "status": "COMPLETED", "conclusion": "NEUTRAL"},
				{"__typename": "StatusContext", "context": "ci/legacy", "state": "PENDING", "targetUrl": "t"},
				{"__typename": "StatusContext", "context": "ci/ok", "state": "SUCCESS"},
				{"__typename": "StatusContext", "context": "ci/bad", "state": "ERROR"}
			]}}}}]},
			"reviewRequests": {"nodes": [{"requestedReviewer": {"login": "bob"}}]},
			"reviewThreads": {"nodes": [
				{"id": "T1", "isResolved": false, "path": "a.go", "line": 3, "diffSide": "RIGHT", "comments": {"nodes": [{"databaseId": 10, "author": {"login": "carol"}, "body": "hm"}]}},
				{"id": "T2", "isResolved": true, "isOutdated": true, "path": "a.go", "line": null, "originalLine": 9, "comments": {"nodes": []}},
				{"id": "T3", "isResolved": true, "path": "b.go", "comments": {"nodes": []}}
			]}
		}}}}`
	}
	c := f.client("tok")
	pr, err := c.PullRequest(context.Background(), "acme", "api", 5)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Owner() != "acme" || pr.Repo() != "api" || pr.Title != "Add thing" || pr.Commits.TotalCount != 2 {
		t.Fatalf("%+v", pr)
	}
	state, checks := pr.Checks()
	if state != "FAILURE" || len(checks) != 8 {
		t.Fatalf("checks %s %d", state, len(checks))
	}
	wantOutcome := []string{"success", "failure", "pending", "skipped", "neutral", "pending", "success", "failure"}
	wantLabel := []string{"unit", "CircleCI / e2e", "lint", "opt", "neu", "ci/legacy", "ci/ok", "ci/bad"}
	for i, ck := range checks {
		if ck.Outcome() != wantOutcome[i] || ck.Label() != wantLabel[i] {
			t.Fatalf("check %d: %s %s", i, ck.Outcome(), ck.Label())
		}
	}
	if checks[0].URL() != "d1" || checks[5].URL() != "t" {
		t.Fatal("check urls")
	}
	if u, total := pr.UnresolvedThreads(); u != 1 || total != 3 {
		t.Fatalf("threads %d/%d", u, total)
	}
	th := pr.ReviewThreads.Nodes
	if th[0].AnchorLine() != 3 || th[1].AnchorLine() != 9 || th[2].AnchorLine() != 0 {
		t.Fatal("anchor lines")
	}
	if rr := pr.RequestedReviewers(); len(rr) != 1 || rr[0] != "bob" {
		t.Fatal("reviewers")
	}
	if err := c.ResolveThread(context.Background(), "T1", true); err != nil {
		t.Fatal(err)
	}

	// Missing PR surfaces as a 404 APIError.
	f.graphql = func(string, map[string]any) (int, string) {
		return 200, `{"data": {"repository": null}, "errors": [{"type": "NOT_FOUND", "message": "Could not resolve"}]}`
	}
	_, err = c.PullRequest(context.Background(), "acme", "api", 5)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 404 {
		t.Fatalf("want 404, got %v", err)
	}
}

func TestRESTCalls(t *testing.T) {
	f := newFakeGitHub(t)
	var seen []string
	f.rest = func(r *http.Request) (int, string) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+string(body))
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/5/files"):
			if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
				return 200, `[{"filename": "a.go", "status": "modified", "additions": 1, "deletions": 2, "patch": "@@ -1 +1 @@\n-a\n+b", "sha": "s"}]`
			}
			return 200, `[]`
		case strings.Contains(r.URL.Path, "/compare/"):
			return 200, `{"files": [{"filename": "b.go", "status": "added", "patch": "@@ -0,0 +1 @@\n+x", "previous_filename": ""}]}`
		case strings.HasSuffix(r.URL.Path, "/pulls/5/reviews"):
			if !strings.Contains(string(body), `"event":"APPROVE"`) || !strings.Contains(string(body), `"start_line":2`) || strings.Contains(string(body), `"start_side":"LEFT"`) {
				return 422, `{"message":"bad review body"}`
			}
			return 200, `{"id": 1}`
		case strings.HasSuffix(r.URL.Path, "/pulls/5/comments"):
			return 201, `{"id": 2}`
		case strings.HasSuffix(r.URL.Path, "/issues/5/comments"):
			return 201, `{"id": 3}`
		case strings.HasSuffix(r.URL.Path, "/pulls/5/merge"):
			if !strings.Contains(string(body), `"merge_method":"squash"`) || !strings.Contains(string(body), `"sha":"head"`) {
				return 422, `{"message":"bad merge body"}`
			}
			return 200, `{"merged": true}`
		case strings.HasSuffix(r.URL.Path, "/user/installations"):
			return 200, `{"total_count": 1, "installations": [{"id": 9, "account": {"login": "acme", "type": "Organization"}, "html_url": "https://github.com/organizations/acme/settings/installations/9", "repository_selection": "all"}]}`
		case strings.HasSuffix(r.URL.Path, "/pulls/5/requested_reviewers"):
			return 201, `{"number": 5}`
		case strings.HasSuffix(r.URL.Path, "/user"):
			return 200, `{"id": 77, "login": "me", "avatar_url": "av"}`
		}
		return 404, `{"message":"nope"}`
	}
	c := f.client("tok")
	ctx := context.Background()
	v, err := c.Viewer(ctx)
	if err != nil || v.ID != 77 || v.Login != "me" || v.AvatarURL != "av" {
		t.Fatalf("viewer %+v %v", v, err)
	}
	files, err := c.Files(ctx, "acme", "api", 5)
	if err != nil || len(files) != 1 || files[0].Path != "a.go" || files[0].Deletions != 2 || files[0].SHA != "s" {
		t.Fatalf("files %+v %v", files, err)
	}
	cmp, err := c.Compare(ctx, "acme", "api", "c1", "head")
	if err != nil || len(cmp) != 1 || cmp[0].Status != "added" {
		t.Fatalf("compare %+v %v", cmp, err)
	}
	err = c.SubmitReview(ctx, "acme", "api", 5, "head", "APPROVE", "lgtm", []ReviewComment{
		{Path: "a.go", Body: "x", Line: 4, Side: "RIGHT", StartLine: 2},
		{Path: "a.go", Body: "y", Line: 4, Side: "LEFT", StartLine: 9}, // invalid start > line is dropped
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Reply(ctx, "acme", "api", 5, 10, "reply"); err != nil {
		t.Fatal(err)
	}
	if err := c.IssueComment(ctx, "acme", "api", 5, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := c.Merge(ctx, "acme", "api", 5, "squash", "head"); err != nil {
		t.Fatal(err)
	}
	insts, err := c.Installations(ctx)
	if err != nil || len(insts) != 1 || insts[0].Account != "acme" || insts[0].AccountType != "Organization" || insts[0].ID != 9 || insts[0].Selection != "all" {
		t.Fatalf("installations %+v %v", insts, err)
	}
	if err := c.RequestReviewers(ctx, "acme", "api", 5, []string{"bob"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Merge(ctx, "acme", "api", 6, "squash", "head"); err == nil {
		t.Fatal("expected 404")
	} else {
		var ae *APIError
		if !errors.As(err, &ae) || ae.Status != 404 || !strings.Contains(ae.Error(), "nope") {
			t.Fatalf("got %v", err)
		}
	}
	if len(seen) < 9 {
		t.Fatalf("calls %v", seen)
	}
}

func TestInboxQueries(t *testing.T) {
	q := InboxQueries("  ")
	if len(q) != 6 || strings.HasSuffix(q["drafts"], " ") {
		t.Fatalf("%v", q)
	}
	if !strings.HasSuffix(InboxQueries("repo:a/b")["merged"], " repo:a/b") {
		t.Fatal("filter not appended")
	}
}

func TestSplitRepo(t *testing.T) {
	if splitRepo("a/b", 0) != "a" || splitRepo("a/b", 1) != "b" || splitRepo("solo", 0) != "solo" || splitRepo("solo", 1) != "" {
		t.Fatal("splitRepo")
	}
	if prRef("a", "b", 1) != "a/b#1" {
		t.Fatal("prRef")
	}
}
