// Package storetest contains the contract test every Store backend must
// pass. Backends call Run from their own _test.go with a factory.
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/goodtune/burp/internal/store"
)

// Run executes the contract suite against a fresh store per subtest.
func Run(t *testing.T, open func(t *testing.T) store.Store) {
	t.Helper()
	ctx := context.Background()
	key := store.PRKey{Owner: "acme", Repo: "api", Number: 42}

	t.Run("ping", func(t *testing.T) {
		s := open(t)
		if err := s.Ping(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("users", func(t *testing.T) {
		s := open(t)
		if _, err := s.GetUser(ctx, 1); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
		u := &store.User{ID: 1, Login: "octocat", AvatarURL: "https://a/1"}
		if err := s.UpsertUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetUser(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if got.Login != "octocat" || got.AvatarURL != "https://a/1" || got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Fatalf("round trip mismatch: %+v", got)
		}
		u.Login = "octocat2"
		if err := s.UpsertUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		got2, _ := s.GetUser(ctx, 1)
		if got2.Login != "octocat2" || !got2.CreatedAt.Equal(got.CreatedAt) {
			t.Fatalf("upsert did not keep created_at / update login: %+v vs %+v", got, got2)
		}
	})

	t.Run("credentials", func(t *testing.T) {
		s := open(t)
		if _, err := s.GetCredential(ctx, 7); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
		if err := s.UpsertUser(ctx, &store.User{ID: 7, Login: "x"}); err != nil {
			t.Fatal(err)
		}
		exp := time.Now().Add(8 * time.Hour).Truncate(time.Second)
		rexp := time.Now().Add(180 * 24 * time.Hour).Truncate(time.Second)
		c := &store.Credential{UserID: 7, AccessToken: "enc-a", RefreshToken: "enc-r", AccessExpiresAt: exp, RefreshExpiresAt: rexp}
		if err := s.PutCredential(ctx, c); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetCredential(ctx, 7)
		if err != nil {
			t.Fatal(err)
		}
		if got.AccessToken != "enc-a" || got.RefreshToken != "enc-r" || !got.AccessExpiresAt.Equal(exp) || !got.RefreshExpiresAt.Equal(rexp) || got.UpdatedAt.IsZero() {
			t.Fatalf("round trip mismatch: %+v", got)
		}
		c.AccessToken = "enc-a2"
		if err := s.PutCredential(ctx, c); err != nil {
			t.Fatal(err)
		}
		got, _ = s.GetCredential(ctx, 7)
		if got.AccessToken != "enc-a2" {
			t.Fatalf("update failed: %+v", got)
		}
		if err := s.DeleteCredential(ctx, 7); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetCredential(ctx, 7); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("want ErrNotFound after delete, got %v", err)
		}
		if err := s.DeleteCredential(ctx, 7); err != nil {
			t.Fatalf("deleting missing credential should be a no-op, got %v", err)
		}
	})

	t.Run("sessions", func(t *testing.T) {
		s := open(t)
		if err := s.UpsertUser(ctx, &store.User{ID: 3, Login: "u"}); err != nil {
			t.Fatal(err)
		}
		now := time.Now().Truncate(time.Second)
		live := &store.Session{TokenHash: "h1", UserID: 3, ExpiresAt: now.Add(time.Hour)}
		dead := &store.Session{TokenHash: "h2", UserID: 3, ExpiresAt: now.Add(-time.Hour)}
		other := &store.Session{TokenHash: "h3", UserID: 3, ExpiresAt: now.Add(time.Hour)}
		for _, sess := range []*store.Session{live, dead, other} {
			if err := s.CreateSession(ctx, sess); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.GetSession(ctx, "h1")
		if err != nil {
			t.Fatal(err)
		}
		if got.UserID != 3 || !got.ExpiresAt.Equal(live.ExpiresAt) || got.CreatedAt.IsZero() {
			t.Fatalf("round trip mismatch: %+v", got)
		}
		if _, err := s.GetSession(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
		if err := s.Cleanup(ctx, now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetSession(ctx, "h2"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("expired session should be cleaned, got %v", err)
		}
		if err := s.DeleteSession(ctx, "h1"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetSession(ctx, "h1"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("session should be deleted")
		}
		if err := s.DeleteUserSessions(ctx, 3); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetSession(ctx, "h3"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("all user sessions should be deleted")
		}
	})

	t.Run("oauth state", func(t *testing.T) {
		s := open(t)
		now := time.Now().Truncate(time.Second)
		if err := s.PutOAuthState(ctx, &store.OAuthState{State: "s1", ReturnTo: "/inbox", ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutOAuthState(ctx, &store.OAuthState{State: "s2", ExpiresAt: now.Add(-time.Minute)}); err != nil {
			t.Fatal(err)
		}
		got, err := s.ConsumeOAuthState(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if got.ReturnTo != "/inbox" || got.CreatedAt.IsZero() {
			t.Fatalf("round trip mismatch: %+v", got)
		}
		if _, err := s.ConsumeOAuthState(ctx, "s1"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("state must be single use")
		}
		if _, err := s.ConsumeOAuthState(ctx, "s2"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("expired state must not be consumable")
		}
		if err := s.PutOAuthState(ctx, &store.OAuthState{State: "s3", ExpiresAt: now.Add(-time.Minute)}); err != nil {
			t.Fatal(err)
		}
		if err := s.Cleanup(ctx, now); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("file marks", func(t *testing.T) {
		s := open(t)
		if err := s.UpsertUser(ctx, &store.User{ID: 5, Login: "u"}); err != nil {
			t.Fatal(err)
		}
		marks, err := s.ListFileMarks(ctx, 5, key)
		if err != nil {
			t.Fatal(err)
		}
		if len(marks) != 0 {
			t.Fatalf("expected empty, got %d", len(marks))
		}
		m := &store.FileMark{UserID: 5, Owner: "acme", Repo: "api", Number: 42, Path: "a/b.go", HeadSHA: "sha1"}
		if err := s.SetFileMark(ctx, m); err != nil {
			t.Fatal(err)
		}
		if err := s.SetFileMark(ctx, &store.FileMark{UserID: 5, Owner: "acme", Repo: "api", Number: 42, Path: "c.go", HeadSHA: "sha1"}); err != nil {
			t.Fatal(err)
		}
		// Different PR and user must not leak.
		if err := s.SetFileMark(ctx, &store.FileMark{UserID: 5, Owner: "acme", Repo: "api", Number: 43, Path: "z.go", HeadSHA: "x"}); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertUser(ctx, &store.User{ID: 6, Login: "v"}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetFileMark(ctx, &store.FileMark{UserID: 6, Owner: "acme", Repo: "api", Number: 42, Path: "a/b.go", HeadSHA: "x"}); err != nil {
			t.Fatal(err)
		}
		m.HeadSHA = "sha2"
		if err := s.SetFileMark(ctx, m); err != nil {
			t.Fatal(err)
		}
		marks, err = s.ListFileMarks(ctx, 5, key)
		if err != nil {
			t.Fatal(err)
		}
		if len(marks) != 2 {
			t.Fatalf("expected 2 marks, got %+v", marks)
		}
		byPath := map[string]store.FileMark{}
		for _, mk := range marks {
			byPath[mk.Path] = mk
		}
		if byPath["a/b.go"].HeadSHA != "sha2" || byPath["a/b.go"].MarkedAt.IsZero() || byPath["a/b.go"].UserID != 5 || byPath["a/b.go"].Owner != "acme" || byPath["a/b.go"].Repo != "api" || byPath["a/b.go"].Number != 42 {
			t.Fatalf("round trip mismatch: %+v", byPath["a/b.go"])
		}
		if err := s.ClearFileMark(ctx, 5, key, "a/b.go"); err != nil {
			t.Fatal(err)
		}
		if err := s.ClearFileMark(ctx, 5, key, "missing"); err != nil {
			t.Fatalf("clearing a missing mark should be a no-op, got %v", err)
		}
		marks, _ = s.ListFileMarks(ctx, 5, key)
		if len(marks) != 1 || marks[0].Path != "c.go" {
			t.Fatalf("expected only c.go, got %+v", marks)
		}
	})

	t.Run("drafts", func(t *testing.T) {
		s := open(t)
		if err := s.UpsertUser(ctx, &store.User{ID: 8, Login: "u"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetDraft(ctx, 8, "d1"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
		d := &store.Draft{ID: "d1", UserID: 8, Owner: "acme", Repo: "api", Number: 42, Path: "a.go", Side: "RIGHT", Line: 10, StartLine: 8, CommitSHA: "sha", Body: "nit"}
		if err := s.PutDraft(ctx, d); err != nil {
			t.Fatal(err)
		}
		if err := s.PutDraft(ctx, &store.Draft{ID: "d2", UserID: 8, Owner: "acme", Repo: "api", Number: 42, Path: "b.go", Side: "LEFT", Line: 1, Body: "two"}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutDraft(ctx, &store.Draft{ID: "d3", UserID: 8, Owner: "acme", Repo: "api", Number: 99, Path: "b.go", Side: "LEFT", Line: 1, Body: "other pr"}); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetDraft(ctx, 8, "d1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Path != "a.go" || got.Side != "RIGHT" || got.Line != 10 || got.StartLine != 8 || got.CommitSHA != "sha" || got.Body != "nit" || got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() || got.Owner != "acme" || got.Repo != "api" || got.Number != 42 {
			t.Fatalf("round trip mismatch: %+v", got)
		}
		if _, err := s.GetDraft(ctx, 9, "d1"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("drafts must be scoped to their user")
		}
		d.Body = "edited"
		if err := s.PutDraft(ctx, d); err != nil {
			t.Fatal(err)
		}
		got, _ = s.GetDraft(ctx, 8, "d1")
		if got.Body != "edited" || !got.CreatedAt.Equal(d.CreatedAt) {
			t.Fatalf("update mismatch: %+v", got)
		}
		list, err := s.ListDrafts(ctx, 8, key)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 2 {
			t.Fatalf("expected 2 drafts, got %+v", list)
		}
		if err := s.DeleteDraft(ctx, 8, "d2"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteDraft(ctx, 8, "d2"); err != nil {
			t.Fatalf("deleting a missing draft should be a no-op, got %v", err)
		}
		list, _ = s.ListDrafts(ctx, 8, key)
		if len(list) != 1 || list[0].ID != "d1" {
			t.Fatalf("expected d1 only, got %+v", list)
		}
		marks, drafts, err := s.CountUserData(ctx, 8)
		if err != nil {
			t.Fatal(err)
		}
		if marks != 0 || drafts != 2 {
			t.Fatalf("count = %d marks, %d drafts", marks, drafts)
		}
		if err := s.DeleteDrafts(ctx, 8, key); err != nil {
			t.Fatal(err)
		}
		list, _ = s.ListDrafts(ctx, 8, key)
		if len(list) != 0 {
			t.Fatalf("expected none, got %+v", list)
		}
		if _, err := s.GetDraft(ctx, 8, "d3"); err != nil {
			t.Fatalf("draft on another PR must survive: %v", err)
		}
	})

	t.Run("delete user data", func(t *testing.T) {
		s := open(t)
		if err := s.UpsertUser(ctx, &store.User{ID: 11, Login: "u"}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetFileMark(ctx, &store.FileMark{UserID: 11, Owner: "a", Repo: "b", Number: 1, Path: "p", HeadSHA: "s"}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutDraft(ctx, &store.Draft{ID: "x", UserID: 11, Owner: "a", Repo: "b", Number: 1, Path: "p", Side: "RIGHT", Line: 1, Body: "b"}); err != nil {
			t.Fatal(err)
		}
		marks, drafts, err := s.CountUserData(ctx, 11)
		if err != nil || marks != 1 || drafts != 1 {
			t.Fatalf("count = %d/%d err %v", marks, drafts, err)
		}
		if err := s.DeleteUserData(ctx, 11); err != nil {
			t.Fatal(err)
		}
		marks, drafts, _ = s.CountUserData(ctx, 11)
		if marks != 0 || drafts != 0 {
			t.Fatalf("count after delete = %d/%d", marks, drafts)
		}
	})
}
