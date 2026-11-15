package config

import (
	"strings"
	"testing"
	"time"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func minimal() map[string]string {
	return map[string]string{
		"BURP_BASE_URL":              "https://burp.example.com/",
		"BURP_GITHUB_CLIENT_ID":      "Iv1.abc",
		"BURP_GITHUB_CLIENT_SECRET":  "shh",
		"BURP_GITHUB_WEBHOOK_SECRET": "hook",
		"BURP_GITHUB_APP_SLUG":       "burp",
		"BURP_ENCRYPTION_KEY":        strings.Repeat("ab", 32),
	}
}

func TestLoadMinimal(t *testing.T) {
	cfg, err := LoadFrom(envFrom(minimal()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8080" || cfg.BaseURL != "https://burp.example.com" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.Store.Driver != DriverSQLite || cfg.Store.DSN != "burp.db" {
		t.Fatalf("unexpected store defaults: %+v", cfg.Store)
	}
	if !cfg.SecureCookies() || cfg.CallbackURL() != "https://burp.example.com/auth/callback" {
		t.Fatalf("unexpected derived values")
	}
	if cfg.SessionTTL != 30*24*time.Hour {
		t.Fatalf("session ttl %v", cfg.SessionTTL)
	}
	if cfg.GitHub.GraphQLURL() != "https://api.github.com/graphql" {
		t.Fatalf("graphql url %s", cfg.GitHub.GraphQLURL())
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		mut  func(map[string]string)
		want string
	}{
		{"missing client id", func(m map[string]string) { delete(m, "BURP_GITHUB_CLIENT_ID") }, "BURP_GITHUB_CLIENT_ID"},
		{"missing secret", func(m map[string]string) { delete(m, "BURP_GITHUB_CLIENT_SECRET") }, "BURP_GITHUB_CLIENT_SECRET"},
		{"missing webhook secret", func(m map[string]string) { delete(m, "BURP_GITHUB_WEBHOOK_SECRET") }, "BURP_GITHUB_WEBHOOK_SECRET"},
		{"http base url", func(m map[string]string) { m["BURP_BASE_URL"] = "http://burp" }, "https"},
		{"bad base url", func(m map[string]string) { m["BURP_BASE_URL"] = "nope" }, "absolute"},
		{"missing key", func(m map[string]string) { delete(m, "BURP_ENCRYPTION_KEY") }, "BURP_ENCRYPTION_KEY"},
		{"bad driver", func(m map[string]string) { m["BURP_STORE_DRIVER"] = "mysql" }, "BURP_STORE_DRIVER"},
		{"postgres needs dsn", func(m map[string]string) { m["BURP_STORE_DRIVER"] = "postgres" }, "BURP_DATABASE_DSN"},
		{"vault needs addr", func(m map[string]string) { m["BURP_STORE_DRIVER"] = "vault" }, "BURP_VAULT_ADDR"},
		{"vault k8s role", func(m map[string]string) {
			m["BURP_STORE_DRIVER"] = "vault"
			m["BURP_VAULT_ADDR"] = "http://vault:8200"
		}, "BURP_VAULT_K8S_ROLE"},
		{"vault approle", func(m map[string]string) {
			m["BURP_STORE_DRIVER"] = "vault"
			m["BURP_VAULT_ADDR"] = "http://vault:8200"
			m["BURP_VAULT_AUTH_METHOD"] = "approle"
		}, "BURP_VAULT_ROLE_ID"},
		{"bad app id", func(m map[string]string) { m["BURP_GITHUB_APP_ID"] = "-1" }, "BURP_GITHUB_APP_ID"},
		{"slug or key", func(m map[string]string) { delete(m, "BURP_GITHUB_APP_SLUG") }, "BURP_GITHUB_APP_SLUG"},
		{"key needs app id", func(m map[string]string) { m["BURP_GITHUB_PRIVATE_KEY"] = "pem" }, "BURP_GITHUB_APP_ID"},
		{"bad bool", func(m map[string]string) { m["BURP_DEV_MODE"] = "maybe" }, "BURP_DEV_MODE"},
		{"bad duration", func(m map[string]string) { m["BURP_SESSION_TTL"] = "soon" }, "BURP_SESSION_TTL"},
		{"bad log level", func(m map[string]string) { m["BURP_LOG_LEVEL"] = "loud" }, "BURP_LOG_LEVEL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := minimal()
			tc.mut(m)
			_, err := LoadFrom(envFrom(m))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestDevModeRelaxes(t *testing.T) {
	m := map[string]string{
		"BURP_DEV_MODE":             "true",
		"BURP_BASE_URL":             "http://localhost:8080",
		"BURP_GITHUB_CLIENT_ID":     "id",
		"BURP_GITHUB_CLIENT_SECRET": "secret",
		"BURP_GITHUB_APP_SLUG":      "burp",
	}
	cfg, err := LoadFrom(envFrom(m))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SecureCookies() {
		t.Fatal("expected insecure cookies on http")
	}
}

func TestVaultKubernetes(t *testing.T) {
	m := minimal()
	delete(m, "BURP_ENCRYPTION_KEY")
	m["BURP_STORE_DRIVER"] = "vault"
	m["BURP_VAULT_ADDR"] = "http://vault:8200"
	m["BURP_VAULT_K8S_ROLE"] = "burp"
	m["BURP_VAULT_K8S_MOUNT"] = "/kubernetes/prod/"
	cfg, err := LoadFrom(envFrom(m))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Vault.AuthMethod != VaultAuthKubernetes || cfg.Vault.K8sMount != "kubernetes/prod" {
		t.Fatalf("unexpected vault config %+v", cfg.Vault)
	}
}

func TestPrivateKeyNewlines(t *testing.T) {
	m := minimal()
	m["BURP_GITHUB_APP_ID"] = "12"
	m["BURP_GITHUB_PRIVATE_KEY"] = `-----BEGIN\nabc\n-----END`
	cfg, err := LoadFrom(envFrom(m))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.GitHub.PrivateKey, "\nabc\n") {
		t.Fatalf("newlines not unescaped: %q", cfg.GitHub.PrivateKey)
	}
}

func TestGHESGraphQL(t *testing.T) {
	g := GitHub{APIURL: "https://ghe.example.com/api/v3"}
	if g.GraphQLURL() != "https://ghe.example.com/api/graphql" {
		t.Fatal(g.GraphQLURL())
	}
}

func TestACMEDisabledByDefault(t *testing.T) {
	cfg, err := LoadFrom(envFrom(minimal()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ACME.Enabled() {
		t.Fatalf("acme enabled without a directory: %+v", cfg.ACME)
	}
}

func TestACMEDefaults(t *testing.T) {
	m := minimal()
	m["BURP_BASE_URL"] = "https://burp.example.com:12000"
	m["BURP_ACME_DIRECTORY"] = "https://vault:8200/v1/pki/roles/lan/acme/directory"
	cfg, err := LoadFrom(envFrom(m))
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.ACME
	if !a.Enabled() {
		t.Fatal("acme not enabled")
	}
	if len(a.Domains) != 1 || a.Domains[0] != "burp.example.com" {
		t.Fatalf("domains default to the base URL host without the port: %q", a.Domains)
	}
	if a.CacheDir != "acme" || a.TLSALPNListen != "" || a.HTTPListen != "" {
		t.Fatalf("unexpected defaults: %+v", a)
	}
}

func TestACMEDomainsAndListeners(t *testing.T) {
	m := minimal()
	m["BURP_ACME_DIRECTORY"] = "https://vault:8200/v1/pki/roles/lan/acme/directory"
	m["BURP_ACME_DOMAINS"] = " burp.example.com, Alt.Example.com ,"
	m["BURP_ACME_TLS_ALPN_LISTEN"] = ":443"
	m["BURP_ACME_HTTP_LISTEN"] = ":80"
	m["BURP_ACME_CACHE_DIR"] = "/var/lib/burp/acme"
	cfg, err := LoadFrom(envFrom(m))
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.ACME
	if strings.Join(a.Domains, ",") != "burp.example.com,alt.example.com" {
		t.Fatalf("domains %q", a.Domains)
	}
	if a.TLSALPNListen != ":443" || a.HTTPListen != ":80" || a.CacheDir != "/var/lib/burp/acme" {
		t.Fatalf("unexpected: %+v", a)
	}
}

func TestACMEErrors(t *testing.T) {
	tests := []struct {
		name string
		mut  func(map[string]string)
		want string
	}{
		{"http base url", func(m map[string]string) {
			m["BURP_DEV_MODE"] = "true"
			m["BURP_BASE_URL"] = "http://burp.example.com"
		}, "BURP_ACME_DIRECTORY requires an https BURP_BASE_URL"},
		{"relative directory", func(m map[string]string) { m["BURP_ACME_DIRECTORY"] = "vault/acme" }, "BURP_ACME_DIRECTORY must be an absolute https URL"},
		{"missing ca file", func(m map[string]string) { m["BURP_ACME_CA_FILE"] = "/nonexistent/ca.pem" }, "BURP_ACME_CA_FILE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := minimal()
			m["BURP_ACME_DIRECTORY"] = "https://vault:8200/v1/pki/roles/lan/acme/directory"
			tc.mut(m)
			_, err := LoadFrom(envFrom(m))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}
