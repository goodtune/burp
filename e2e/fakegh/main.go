// Command fakegh is a scripted GitHub (OAuth, REST and GraphQL) used by the
// Playwright end-to-end tests. It is not part of burp itself.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
)

var (
	mu       sync.Mutex
	head     = "HEAD"
	threads  = threadsJSON
	reviews  []string
	comments []string
	replies  []string
	resolved int
	viewed   int
	merged   bool
)

const prJSON = `{
  "id": "PR_1", "number": 5, "title": "Add rate limiting to /tokens", "bodyHTML": "<p>Adds a token bucket limiter.</p>", "url": "https://github.com/acme/api/pull/5",
  "state": "OPEN", "isDraft": false, "merged": false, "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN", "reviewDecision": "REVIEW_REQUIRED",
  "createdAt": "2026-09-20T00:00:00Z", "updatedAt": "2026-09-22T00:00:00Z", "additions": 3, "deletions": 1, "changedFiles": 2,
  "baseRefName": "main", "headRefName": "feat/ratelimit", "headRefOid": "HEAD", "baseRefOid": "BASE", "viewerCanUpdate": true, "viewerDidAuthor": false,
  "author": {"login": "alice"}, "repository": {"nameWithOwner": "acme/api", "squashMergeAllowed": true, "mergeCommitAllowed": true, "viewerPermission": "WRITE"},
  "labels": {"nodes": [{"name": "bug", "color": "d73a4a"}]},
  "commits": {"totalCount": 2, "nodes": [{"commit": {"oid": "C1", "abbreviatedOid": "C1", "messageHeadline": "first"}}, {"commit": {"oid": "HEAD", "abbreviatedOid": "HEAD", "messageHeadline": "second"}}]},
  "headCommit": {"nodes": [{"commit": {"statusCheckRollup": {"state": "FAILURE", "contexts": {"nodes": [{"__typename": "CheckRun", "name": "unit", "status": "COMPLETED", "conclusion": "SUCCESS"}, {"__typename": "CheckRun", "name": "e2e", "status": "COMPLETED", "conclusion": "FAILURE", "detailsUrl": "https://example.com/e2e"}]}}}}]},
  "reviewRequests": {"nodes": [{"requestedReviewer": {"login": "me"}}]},
  "latestReviews": {"nodes": []}, "reviews": {"nodes": []},
  "reviewThreads": {"nodes": THREADS},
  "files": {"pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": [{"path": "docs/config.md", "viewerViewedState": "UNVIEWED"}, {"path": "internal/proxy/ratelimit.go", "viewerViewedState": "UNVIEWED"}]},
  "comments": {"nodes": [{"databaseId": 1, "author": {"login": "dave"}, "bodyHTML": "<p>LGTM but please add tests</p>", "createdAt": "2026-09-22T00:00:00Z"}]}
}`

const threadsJSON = `[{"id": "T1", "isResolved": false, "viewerCanResolve": true, "path": "internal/proxy/ratelimit.go", "line": 2, "diffSide": "RIGHT",
  "comments": {"nodes": [{"id": "C", "databaseId": 10, "author": {"login": "carol"}, "body": "hm", "bodyHTML": "<p>Consider golang.org/x/time/rate here.</p>", "createdAt": "2026-09-22T00:00:00Z"}]}}]`

const inboxNode = `{"number": 5, "title": "Add rate limiting to /tokens", "url": "u", "state": "OPEN", "reviewDecision": "REVIEW_REQUIRED",
 "updatedAt": "2026-09-22T00:00:00Z", "createdAt": "2026-09-20T00:00:00Z", "additions": 3, "deletions": 1, "changedFiles": 2, "headRefOid": "HEAD",
 "repository": {"nameWithOwner": "acme/api"}, "author": {"login": "alice"},
 "commits": {"nodes": [{"commit": {"statusCheckRollup": {"state": "FAILURE"}}}]}, "reviewRequests": {"nodes": [{"requestedReviewer": {"login": "me"}}]}, "latestReviews": {"nodes": []}}`

func main() {
	addr := ":9999"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		log.Printf("%s %s %s", r.Method, r.URL.Path, truncate(string(body)))
		switch {
		case r.URL.Path == "/login/oauth/authorize":
			q := r.URL.Query()
			http.Redirect(w, r, q.Get("redirect_uri")+"?code=good&state="+q.Get("state"), 302)
			return
		case r.URL.Path == "/login/oauth/access_token":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"ghu_tok","refresh_token":"ghr_tok","expires_in":28800,"refresh_token_expires_in":15897600}`))
			return
		case r.URL.Path == "/state":
			fmt.Fprintf(w, "reviews=%d comments=%d replies=%d resolved=%d viewed=%d merged=%v\n%s\n", len(reviews), len(comments), len(replies), resolved, viewed, merged, strings.Join(reviews, "\n"))
			return
		case r.URL.Path == "/reset":
			head, threads, reviews, comments, replies, resolved, viewed, merged = "HEAD", threadsJSON, nil, nil, nil, 0, 0, false
			fmt.Fprintln(w, "reset")
			return
		case r.URL.Path == "/push":
			head = "HEAD2"
			fmt.Fprintln(w, "head is now HEAD2")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer ghu_tok" {
			w.WriteHeader(401)
			w.Write([]byte(`{"message":"Bad credentials"}`))
			return
		}
		switch {
		case r.URL.Path == "/graphql" || r.URL.Path == "/api/graphql":
			var req struct {
				Query string `json:"query"`
			}
			json.Unmarshal(body, &req)
			switch {
			case strings.Contains(req.Query, "needsReview: search"):
				w.Write([]byte(`{"data": {"needsReview": {"nodes": [` + inboxNode + `]}, "returned": {"nodes": []}, "approved": {"nodes": []}, "waiting": {"nodes": []}, "drafts": {"nodes": []}, "merged": {"nodes": []}}}`))
			case strings.Contains(req.Query, "esolveReviewThread"):
				resolved++
				w.Write([]byte(`{"data": {}}`))
			case strings.Contains(req.Query, "FileAsViewed"):
				viewed++
				w.Write([]byte(`{"data": {}}`))
			case strings.Contains(req.Query, "pullRequest(number"):
				pr := strings.Replace(prJSON, "THREADS", threads, 1)
				pr = strings.ReplaceAll(pr, `"HEAD"`, `"`+head+`"`)
				if merged {
					pr = strings.Replace(pr, `"merged": false`, `"merged": true`, 1)
					pr = strings.Replace(pr, `"state": "OPEN"`, `"state": "MERGED"`, 1)
				}
				w.Write([]byte(`{"data": {"repository": {"pullRequest": ` + pr + `}}}`))
			default:
				w.Write([]byte(`{"errors": [{"message": "unexpected query"}]}`))
			}
		case r.URL.Path == "/api/v3/user":
			w.Write([]byte(`{"id": 77, "login": "me", "avatar_url": ""}`))
		case strings.HasSuffix(r.URL.Path, "/pulls/5/files"):
			if p := r.URL.Query().Get("page"); p != "" && p != "1" {
				w.Write([]byte(`[]`))
				return
			}
			w.Write([]byte(`[{"filename": "internal/proxy/ratelimit.go", "status": "added", "additions": 3, "deletions": 0, "patch": "@@ -0,0 +1,3 @@\n+package proxy\n+\n+type limiter struct{}"},
			{"filename": "docs/config.md", "status": "modified", "additions": 1, "deletions": 1, "patch": "@@ -1,2 +1,2 @@\n # Config\n-old text\n+new text"}]`))
		case strings.Contains(r.URL.Path, "/compare/"):
			w.Write([]byte(`{"files": [{"filename": "docs/config.md", "status": "modified", "patch": "@@ -1 +1 @@\n-x\n+y"}]}`))
		case strings.HasSuffix(r.URL.Path, "/pulls/5/reviews"):
			reviews = append(reviews, string(body))
			w.Write([]byte(`{"id": 1}`))
		case strings.HasSuffix(r.URL.Path, "/pulls/5/comments"):
			replies = append(replies, string(body))
			w.WriteHeader(201)
			w.Write([]byte(`{"id": 2}`))
		case strings.HasSuffix(r.URL.Path, "/issues/5/comments"):
			comments = append(comments, string(body))
			w.WriteHeader(201)
			w.Write([]byte(`{"id": 3}`))
		case strings.HasSuffix(r.URL.Path, "/pulls/5/merge"):
			merged = true
			w.Write([]byte(`{"merged": true}`))
		case strings.HasSuffix(r.URL.Path, "/user/installations"):
			w.Write([]byte(`{"total_count": 1, "installations": [{"id": 9, "account": {"login": "acme", "type": "Organization"}, "html_url": "https://github.com/x", "repository_selection": "all"}]}`))
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"message": "not found"}`))
		}
	})
	log.Println("fakegh listening on", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func truncate(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}
