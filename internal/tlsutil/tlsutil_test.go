package tlsutil

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHTTPClientCAFile(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, certPEM(t, srv), 0o600); err != nil {
		t.Fatal(err)
	}
	hc, err := HTTPClient(ca, 0)
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
	if _, err := HTTPClient(ca, 0); err == nil {
		t.Fatal("want an error for a file with no certificates")
	}
}

func certPEM(t *testing.T, srv *httptest.Server) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
}
