package acme

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/goodtune/burp/internal/config"
)

func TestRedirectHandler(t *testing.T) {
	h := RedirectHandler("https://burp.example.com:12000/")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://burp.example.com/inbox?q=is%3Aopen", nil))
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "https://burp.example.com:12000/inbox?q=is%3Aopen" {
		t.Fatalf("location %q", got)
	}
}

func TestNewCreatesCacheDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme")
	m, err := New(config.ACME{Directory: "https://vault:8200/v1/pki/roles/lan/acme/directory", Domains: []string{"burp.example.com"}, CacheDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if m.Client.DirectoryURL != "https://vault:8200/v1/pki/roles/lan/acme/directory" {
		t.Fatalf("directory %q", m.Client.DirectoryURL)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("cache dir: %v %v", fi, err)
	}
	if err := m.HostPolicy(t.Context(), "other.example.com"); err == nil {
		t.Fatal("host policy accepted a name outside the configured domains")
	}
}
