package acme

import (
	"encoding/pem"
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

func TestHTTPClientCAFile(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, certPEM(t, srv), 0o600); err != nil {
		t.Fatal(err)
	}
	hc, err := httpClient(ca)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatalf("CA file not trusted: %v", err)
	}
	resp.Body.Close()
}

func TestHTTPClientBadCAFile(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := httpClient(ca); err == nil {
		t.Fatal("want an error for a file with no certificates")
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

func certPEM(t *testing.T, srv *httptest.Server) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
}
