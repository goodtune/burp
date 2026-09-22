package vaultstore

import (
	"context"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/goodtune/burp/internal/store"
	"github.com/goodtune/burp/internal/store/storetest"
)

// memKV is an in-memory KV v2 look-alike with Vault's list semantics.
type memKV struct {
	mu   sync.Mutex
	data map[string]map[string]any
}

func newMemKV() *memKV { return &memKV{data: map[string]map[string]any{}} }

func (m *memKV) Read(_ context.Context, key string) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return nil, nil
	}
	cp := make(map[string]any, len(v))
	for k, x := range v {
		cp[k] = x
	}
	return cp, nil
}

func (m *memKV) Write(_ context.Context, key string, data map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string]any, len(data))
	for k, x := range data {
		cp[k] = x
	}
	m.data[key] = cp
	return nil
}

func (m *memKV) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *memKV) List(_ context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]struct{}{}
	for k := range m.data {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := strings.TrimPrefix(k, prefix)
		if i := strings.Index(rest, "/"); i >= 0 {
			seen[rest[:i+1]] = struct{}{}
		} else {
			seen[rest] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func TestContractInMemory(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		return NewWithKV(newMemKV())
	})
}

// TestContractRealVault runs against a live Vault when BURP_TEST_VAULT_ADDR
// and BURP_TEST_VAULT_TOKEN are set (for example `vault server -dev`).
func TestContractRealVault(t *testing.T) {
	addr, tok := os.Getenv("BURP_TEST_VAULT_ADDR"), os.Getenv("BURP_TEST_VAULT_TOKEN")
	if addr == "" || tok == "" {
		t.Skip("BURP_TEST_VAULT_ADDR / BURP_TEST_VAULT_TOKEN not set")
	}
	n := 0
	storetest.Run(t, func(t *testing.T) store.Store {
		n++
		s, err := New(context.Background(), Config{Addr: addr, Token: tok, AuthMethod: AuthToken, Mount: "secret", Path: "burp-test-" + t.Name()})
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestKeyLayout(t *testing.T) {
	kv := newMemKV()
	s := NewWithKV(kv)
	ctx := context.Background()
	if err := s.SetFileMark(ctx, &store.FileMark{UserID: 1, Owner: "o", Repo: "r", Number: 2, Path: "a/b.go", HeadSHA: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDraft(ctx, &store.Draft{ID: "d", UserID: 1, Owner: "o", Repo: "r", Number: 2, Path: "a", Side: "RIGHT", Line: 1, Body: "b"}); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range kv.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "drafts/1/o/r/2/d" || !strings.HasPrefix(keys[1], "marks/1/o/r/2/") {
		t.Fatalf("unexpected keys %v", keys)
	}
}

func TestEncodingHelpers(t *testing.T) {
	m := map[string]any{"n": "42", "bad": "x", "t": "2026-01-02T03:04:05Z", "badt": "nope"}
	if i64(m, "n") != 42 || i64(m, "bad") != 0 || i64(m, "missing") != 0 {
		t.Fatal("i64")
	}
	if tm(m, "t").IsZero() || !tm(m, "badt").IsZero() || !tm(m, "missing").IsZero() {
		t.Fatal("tm")
	}
	if pathHash("a") == pathHash("b") || len(pathHash("a")) != 32 {
		t.Fatal("pathHash")
	}
}

func TestVaultPaths(t *testing.T) {
	k := &vaultKV{cfg: Config{Mount: "kv", Path: "burp"}}
	if k.dataPath("users/1") != "kv/data/burp/users/1" || k.metadataPath("users/") != "kv/metadata/burp/users/" {
		t.Fatal(k.dataPath("users/1"), k.metadataPath("users/"))
	}
}
