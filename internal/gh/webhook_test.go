package gh

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

func signedRequest(event, secret, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-GitHub-Event", event)
	r.Header.Set("X-GitHub-Delivery", "d-1")
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return r
}

const prPayload = `{
  "action": "review_requested",
  "number": 42,
  "pull_request": {"number": 42, "user": {"login": "alice"}, "requested_reviewers": [{"login": "bob"}], "assignees": [{"login": "carol"}], "head": {"sha": "abc"}},
  "requested_reviewer": {"login": "bob"},
  "repository": {"name": "api", "full_name": "acme/api", "owner": {"login": "acme"}},
  "sender": {"login": "alice"}
}`

func topics(d *Delivery) []string {
	out := append([]string(nil), d.Topics...)
	sort.Strings(out)
	return out
}

func TestParseWebhookPullRequest(t *testing.T) {
	d, err := ParseWebhook(signedRequest("pull_request", "s3cret", prPayload), "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "d-1" || d.Event != "pull_request" || d.Action != "review_requested" || d.Repo != "acme/api" {
		t.Fatalf("%+v", d)
	}
	want := []string{"pr:acme/api#42", "repo:acme/api", "sha:abc", "user:alice", "user:bob", "user:carol"}
	got := topics(d)
	if len(got) != len(want) {
		t.Fatalf("topics %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("topics %v want %v", got, want)
		}
	}
}

func TestParseWebhookBadSignature(t *testing.T) {
	if _, err := ParseWebhook(signedRequest("pull_request", "wrong", prPayload), "s3cret"); err == nil {
		t.Fatal("expected signature error")
	}
	if _, err := ParseWebhook(signedRequest("pull_request", "", prPayload), "s3cret"); err == nil {
		t.Fatal("expected missing signature error")
	}
	// Empty secret disables validation.
	if _, err := ParseWebhook(signedRequest("pull_request", "", prPayload), ""); err != nil {
		t.Fatal(err)
	}
}

func TestParseWebhookOtherEvents(t *testing.T) {
	tests := []struct {
		event string
		body  string
		want  []string
	}{
		{"check_run", `{"action":"completed","check_run":{"head_sha":"h1","pull_requests":[{"number":7}]},"repository":{"name":"api","full_name":"acme/api","owner":{"login":"acme"}}}`,
			[]string{"pr:acme/api#7", "repo:acme/api", "sha:h1"}},
		{"check_suite", `{"action":"completed","check_suite":{"head_sha":"h2","pull_requests":[{"number":8}]},"repository":{"name":"api","full_name":"acme/api","owner":{"login":"acme"}}}`,
			[]string{"pr:acme/api#8", "repo:acme/api", "sha:h2"}},
		{"status", `{"sha":"h3","state":"success","repository":{"name":"api","full_name":"acme/api","owner":{"login":"acme"}}}`,
			[]string{"repo:acme/api", "sha:h3"}},
		{"issue_comment", `{"action":"created","issue":{"number":9,"user":{"login":"dave"},"pull_request":{"url":"x"}},"repository":{"name":"api","full_name":"acme/api","owner":{"login":"acme"}}}`,
			[]string{"pr:acme/api#9", "repo:acme/api", "user:dave"}},
		{"issue_comment", `{"action":"created","issue":{"number":9,"user":{"login":"dave"}},"repository":{"name":"api","full_name":"acme/api","owner":{"login":"acme"}}}`,
			nil},
		{"pull_request_review", `{"action":"submitted","review":{"user":{"login":"erin"}},"pull_request":{"number":3,"user":{"login":"alice"},"head":{"sha":"h"}},"repository":{"name":"api","full_name":"acme/api","owner":{"login":"acme"}}}`,
			[]string{"pr:acme/api#3", "repo:acme/api", "sha:h", "user:alice", "user:erin"}},
		{"pull_request_review_comment", `{"action":"created","comment":{"user":{"login":"erin"}},"pull_request":{"number":3,"user":{"login":"alice"}},"repository":{"name":"api","full_name":"acme/api","owner":{"login":"acme"}}}`,
			[]string{"pr:acme/api#3", "repo:acme/api", "user:alice", "user:erin"}},
		{"pull_request_review_thread", `{"action":"resolved","pull_request":{"number":3,"user":{"login":"alice"}},"repository":{"name":"api","full_name":"acme/api","owner":{"login":"acme"}}}`,
			[]string{"pr:acme/api#3", "repo:acme/api", "user:alice"}},
		{"push", `{"ref":"refs/heads/x","repository":{"name":"api","full_name":"acme/api","owner":{"login":"acme"}}}`,
			[]string{"repo:acme/api"}},
		{"ping", `{"zen":"hi"}`, nil},
		{"installation", `{"action":"created","installation":{"id":1}}`, nil},
		{"totally_unknown", `{}`, nil},
	}
	for _, tc := range tests {
		t.Run(tc.event, func(t *testing.T) {
			d, err := ParseWebhook(signedRequest(tc.event, "s", tc.body), "s")
			if err != nil {
				t.Fatal(err)
			}
			got := topics(d)
			if len(got) != len(tc.want) {
				t.Fatalf("topics %v want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("topics %v want %v", got, tc.want)
				}
			}
		})
	}
}
