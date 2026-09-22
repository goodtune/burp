package gh

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goodtune/burp/internal/crypto"
	"github.com/goodtune/burp/internal/store"
	"github.com/goodtune/burp/internal/store/sqlstore"
)

func newTokens(t *testing.T, o *OAuth) *Tokens {
	t.Helper()
	s, err := sqlstore.Open(context.Background(), sqlstore.SQLite, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.UpsertUser(context.Background(), &store.User{ID: 1, Login: "u"}); err != nil {
		t.Fatal(err)
	}
	key, _ := crypto.GenerateKey()
	enc, _ := crypto.NewEncryptor(key)
	return &Tokens{Store: s, Enc: enc, OAuth: o}
}

func TestTokensSaveAndFresh(t *testing.T) {
	tk := newTokens(t, nil)
	ctx := context.Background()
	c, err := tk.Save(ctx, 1, &TokenResponse{AccessToken: "ghu_a", RefreshToken: "ghr_r", ExpiresIn: 3600})
	if err != nil {
		t.Fatal(err)
	}
	if c.AccessToken == "ghu_a" {
		t.Fatal("access token stored in plaintext")
	}
	got, err := tk.AccessToken(ctx, 1)
	if err != nil || got != "ghu_a" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := tk.AccessToken(ctx, 2); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("unknown user should need reauth, got %v", err)
	}
	exp, err := tk.Expiry(ctx, 1)
	if err != nil || exp.IsZero() {
		t.Fatalf("expiry %v %v", exp, err)
	}
	exp, err = tk.Expiry(ctx, 2)
	if err != nil || !exp.IsZero() {
		t.Fatalf("expiry for unknown user %v %v", exp, err)
	}
}

func TestTokensRefresh(t *testing.T) {
	var calls int32
	srv := fakeTokenServer(t, func(form map[string]string) (int, string) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond)
		if form["refresh_token"] != "ghr_r" {
			return 200, `{"error":"bad_refresh_token"}`
		}
		return 200, `{"access_token":"ghu_new","refresh_token":"ghr_new","expires_in":28800}`
	})
	defer srv.Close()
	tk := newTokens(t, &OAuth{ClientID: "id", ClientSecret: "s", WebURL: srv.URL})
	ctx := context.Background()
	if _, err := tk.Save(ctx, 1, &TokenResponse{AccessToken: "ghu_old", RefreshToken: "ghr_r", ExpiresIn: 60}); err != nil {
		t.Fatal(err)
	}
	// Within the leeway: refresh; concurrent callers share one refresh.
	var wg sync.WaitGroup
	results := make([]string, 5)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := tk.AccessToken(ctx, 1)
			if err != nil {
				t.Error(err)
			}
			results[i] = tok
		}(i)
	}
	wg.Wait()
	for _, r := range results {
		if r != "ghu_new" {
			t.Fatalf("got %q", r)
		}
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("refresh called %d times", calls)
	}
	c, _ := tk.Store.GetCredential(ctx, 1)
	if rt, _ := tk.Enc.Decrypt(c.RefreshToken); rt != "ghr_new" {
		t.Fatalf("refresh token not rotated: %q", rt)
	}
}

func TestTokensRefreshRejected(t *testing.T) {
	srv := fakeTokenServer(t, func(form map[string]string) (int, string) {
		return 200, `{"error":"bad_refresh_token"}`
	})
	defer srv.Close()
	tk := newTokens(t, &OAuth{WebURL: srv.URL})
	ctx := context.Background()
	if _, err := tk.Save(ctx, 1, &TokenResponse{AccessToken: "a", RefreshToken: "r", ExpiresIn: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tk.AccessToken(ctx, 1); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("want ErrReauthRequired, got %v", err)
	}
	if _, err := tk.Store.GetCredential(ctx, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("rejected credential should be deleted")
	}
}

func TestTokensRefreshTransientError(t *testing.T) {
	srv := fakeTokenServer(t, func(form map[string]string) (int, string) { return 503, "down" })
	defer srv.Close()
	tk := newTokens(t, &OAuth{WebURL: srv.URL})
	ctx := context.Background()
	tk.Save(ctx, 1, &TokenResponse{AccessToken: "a", RefreshToken: "r", ExpiresIn: 1})
	_, err := tk.AccessToken(ctx, 1)
	if err == nil || errors.Is(err, ErrReauthRequired) {
		t.Fatalf("transient failure must not force reauth: %v", err)
	}
	if _, err := tk.Store.GetCredential(ctx, 1); err != nil {
		t.Fatal("credential must survive a transient failure")
	}
}

func TestTokensExpiredRefreshToken(t *testing.T) {
	tk := newTokens(t, &OAuth{})
	ctx := context.Background()
	now := time.Now()
	tk.Now = func() time.Time { return now }
	tk.Save(ctx, 1, &TokenResponse{AccessToken: "a", RefreshToken: "r", ExpiresIn: 1, RefreshTokenExpiresIn: 2})
	tk.Now = func() time.Time { return now.Add(time.Hour) }
	if _, err := tk.AccessToken(ctx, 1); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("want ErrReauthRequired, got %v", err)
	}
}

func TestTokensNonExpiring(t *testing.T) {
	tk := newTokens(t, &OAuth{})
	ctx := context.Background()
	tk.Save(ctx, 1, &TokenResponse{AccessToken: "ghu_forever"})
	now := time.Now()
	tk.Now = func() time.Time { return now.Add(100 * time.Hour) }
	tok, err := tk.AccessToken(ctx, 1)
	if err != nil || tok != "ghu_forever" {
		t.Fatalf("got %q %v", tok, err)
	}
}
