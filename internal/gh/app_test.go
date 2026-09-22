package gh

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testPEM(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	b := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return string(b), key
}

func TestAppJWTAndInfo(t *testing.T) {
	pemStr, key := testPEM(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/app" {
			http.Error(w, r.URL.Path, 404)
			return
		}
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		tok, err := jwt.ParseWithClaims(auth, &jwt.RegisteredClaims{}, func(*jwt.Token) (any, error) { return &key.PublicKey, nil })
		if err != nil || !tok.Valid || tok.Claims.(*jwt.RegisteredClaims).Issuer != "123" {
			http.Error(w, "bad jwt", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": 123, "slug": "burp-dev", "name": "burp (dev)", "html_url": "https://github.com/apps/burp-dev"}`))
	}))
	defer srv.Close()
	app, err := NewApp(123, pemStr, srv.URL+"/api/v3", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.JWT(time.Now()); err != nil {
		t.Fatal(err)
	}
	info, err := app.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Slug != "burp-dev" || info.ID != 123 || info.Name != "burp (dev)" {
		t.Fatalf("%+v", info)
	}
	if _, err := NewApp(1, "not a key", "", nil); err == nil {
		t.Fatal("expected pem error")
	}
}
