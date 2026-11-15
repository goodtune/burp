// Package acme obtains and renews burp's TLS certificate from an ACME
// directory, such as a HashiCorp Vault PKI role's /acme/directory endpoint.
//
// Certificates are requested on first use and renewed in the background
// once a third of their lifetime remains, which suits short-lived (for
// example seven-day) certificates from an internal CA.
package acme

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/goodtune/burp/internal/config"
	"github.com/goodtune/burp/internal/tlsutil"
)

// New builds a certificate manager for cfg. It does not contact the ACME
// server; use Warm to obtain the certificate before the first visitor does.
func New(cfg config.ACME) (*autocert.Manager, error) {
	if !cfg.Enabled() {
		return nil, errors.New("acme: no directory configured")
	}
	if len(cfg.Domains) == 0 {
		return nil, errors.New("acme: no domains configured")
	}
	if err := os.MkdirAll(cfg.CacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("acme: cache dir: %w", err)
	}
	hc, err := tlsutil.HTTPClient(cfg.CAFile, time.Minute)
	if err != nil {
		return nil, fmt.Errorf("acme: %w", err)
	}
	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(cfg.CacheDir),
		HostPolicy: autocert.HostWhitelist(cfg.Domains...),
		Email:      cfg.Email,
		Client:     &acme.Client{DirectoryURL: cfg.Directory, HTTPClient: hc},
	}, nil
}

// Warm obtains (or loads from the cache) the certificate for every domain
// and logs the result, so problems show at startup rather than as a failed
// TLS handshake in a browser. The hello advertises ECDSA support so the
// manager requests an EC key, which internal CAs such as Vault roles with
// key_type=ec require.
func Warm(ctx context.Context, m *autocert.Manager, domains []string, logger *slog.Logger) {
	for _, d := range domains {
		hello := &tls.ClientHelloInfo{
			ServerName:       d,
			SignatureSchemes: []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
			SupportedCurves:  []tls.CurveID{tls.CurveP256},
			CipherSuites:     []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
		}
		cert, err := m.GetCertificate(hello)
		if err != nil {
			if ctx.Err() == nil {
				logger.Error("acme: could not obtain certificate", "domain", d, "error", err)
			}
			continue
		}
		if cert.Leaf != nil {
			logger.Info("acme: certificate ready", "domain", d, "issuer", cert.Leaf.Issuer.CommonName, "not_after", cert.Leaf.NotAfter)
		}
	}
}

// RedirectHandler sends every request to the same path on baseURL. It backs
// the extra challenge listeners so a visitor who lands on :80 or :443 ends
// up on burp itself.
func RedirectHandler(baseURL string) http.Handler {
	base := strings.TrimRight(baseURL, "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, base+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}

// Listen binds addr, naming the purpose in the error so a port clash with
// another service is easy to diagnose.
func Listen(addr, purpose string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%s listener on %s: %w", purpose, addr, err)
	}
	return ln, nil
}
