// Package config loads burp's configuration from environment variables.
// Every setting is prefixed with BURP_. There is no configuration file:
// burp is designed to run as a container or a systemd unit where the
// environment is the configuration surface.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Store drivers.
const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
	DriverVault    = "vault"
)

// Vault auth methods.
const (
	VaultAuthKubernetes = "kubernetes"
	VaultAuthAppRole    = "approle"
	VaultAuthToken      = "token"
)

// Config is the fully resolved configuration.
type Config struct {
	// Listen is the address the HTTP server binds, e.g. ":8080".
	Listen string
	// BaseURL is the public URL users reach burp on. It is used to build
	// the OAuth redirect URI and to decide whether cookies are Secure.
	BaseURL string
	// DevMode relaxes a few checks for local development (plain-HTTP
	// cookies, optional encryption key with sqlite).
	DevMode bool
	// LogLevel is debug, info, warn or error. LogFormat is text or json.
	LogLevel  string
	LogFormat string
	// SessionTTL bounds a browser session.
	SessionTTL time.Duration
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration

	GitHub GitHub
	Store  Store
	Vault  Vault
}

// GitHub holds the GitHub App settings.
type GitHub struct {
	// URL is the GitHub web origin (https://github.com or a GHES host).
	URL string
	// APIURL is the REST/GraphQL API origin (https://api.github.com or
	// https://ghes.example.com/api/v3; GraphQL is derived from it).
	APIURL string
	// AppID is the numeric GitHub App id. AppSlug is the URL slug used to
	// build the "install" link. Slug is optional when PrivateKey is set:
	// burp resolves it from GET /app at startup.
	AppID   int64
	AppSlug string
	// ClientID and ClientSecret are the App's OAuth credentials used for
	// the user-to-server flow and token refresh.
	ClientID     string
	ClientSecret string
	// WebhookSecret validates X-Hub-Signature-256 on incoming deliveries.
	WebhookSecret string
	// PrivateKey is the PEM-encoded App private key (optional; enables
	// app-authenticated calls such as GET /app).
	PrivateKey string
}

// Store holds storage settings.
type Store struct {
	// Driver is sqlite, postgres or vault.
	Driver string
	// DSN is the sqlite path or the postgres connection string.
	DSN string
	// EncryptionKey is the hex-encoded 32-byte AES key used with the SQL
	// drivers. Not used with vault.
	EncryptionKey string
}

// Vault holds Vault backend settings.
type Vault struct {
	Addr       string
	Mount      string
	Path       string
	AuthMethod string
	// Kubernetes auth.
	K8sRole      string
	K8sMount     string
	K8sTokenPath string
	// AppRole auth.
	RoleID   string
	SecretID string
	// Token auth (development only).
	Token string
}

// Load reads the environment and validates the result.
func Load() (*Config, error) {
	return LoadFrom(os.Getenv)
}

// LoadFrom reads configuration from the given lookup function; it exists so
// tests can supply an environment without mutating the process.
func LoadFrom(getenv func(string) string) (*Config, error) {
	env := func(key, def string) string {
		if v := strings.TrimSpace(getenv("BURP_" + key)); v != "" {
			return v
		}
		return def
	}
	envBool := func(key string, def bool) (bool, error) {
		v := env(key, "")
		if v == "" {
			return def, nil
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			return false, fmt.Errorf("BURP_%s: %w", key, err)
		}
		return b, nil
	}
	envDur := func(key string, def time.Duration) (time.Duration, error) {
		v := env(key, "")
		if v == "" {
			return def, nil
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			return 0, fmt.Errorf("BURP_%s: %w", key, err)
		}
		return d, nil
	}

	var errs []error
	cfg := &Config{
		Listen:    env("LISTEN", ":8080"),
		BaseURL:   strings.TrimRight(env("BASE_URL", "http://localhost:8080"), "/"),
		LogLevel:  strings.ToLower(env("LOG_LEVEL", "info")),
		LogFormat: strings.ToLower(env("LOG_FORMAT", "text")),
	}
	var err error
	if cfg.DevMode, err = envBool("DEV_MODE", false); err != nil {
		errs = append(errs, err)
	}
	if cfg.SessionTTL, err = envDur("SESSION_TTL", 30*24*time.Hour); err != nil {
		errs = append(errs, err)
	}
	if cfg.ShutdownTimeout, err = envDur("SHUTDOWN_TIMEOUT", 15*time.Second); err != nil {
		errs = append(errs, err)
	}

	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		errs = append(errs, errors.New("BURP_BASE_URL must be an absolute http(s) URL"))
	} else if u.Scheme != "https" && !cfg.DevMode {
		errs = append(errs, errors.New("BURP_BASE_URL must use https unless BURP_DEV_MODE=true"))
	}

	// GitHub.
	g := &cfg.GitHub
	g.URL = strings.TrimRight(env("GITHUB_URL", "https://github.com"), "/")
	g.APIURL = strings.TrimRight(env("GITHUB_API_URL", "https://api.github.com"), "/")
	if v := env("GITHUB_APP_ID", ""); v != "" {
		id, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || id <= 0 {
			errs = append(errs, errors.New("BURP_GITHUB_APP_ID must be a positive integer"))
		}
		g.AppID = id
	}
	g.AppSlug = env("GITHUB_APP_SLUG", "")
	g.ClientID = env("GITHUB_CLIENT_ID", "")
	g.ClientSecret = env("GITHUB_CLIENT_SECRET", "")
	g.WebhookSecret = env("GITHUB_WEBHOOK_SECRET", "")
	g.PrivateKey = env("GITHUB_PRIVATE_KEY", "")
	if g.PrivateKey == "" {
		if p := env("GITHUB_PRIVATE_KEY_FILE", ""); p != "" {
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				errs = append(errs, fmt.Errorf("BURP_GITHUB_PRIVATE_KEY_FILE: %w", rerr))
			}
			g.PrivateKey = string(b)
		}
	}
	// Allow "\n" escaped PEM from environment files.
	g.PrivateKey = strings.ReplaceAll(g.PrivateKey, `\n`, "\n")
	if g.ClientID == "" {
		errs = append(errs, errors.New("BURP_GITHUB_CLIENT_ID is required"))
	}
	if g.ClientSecret == "" {
		errs = append(errs, errors.New("BURP_GITHUB_CLIENT_SECRET is required"))
	}
	if g.WebhookSecret == "" && !cfg.DevMode {
		errs = append(errs, errors.New("BURP_GITHUB_WEBHOOK_SECRET is required unless BURP_DEV_MODE=true"))
	}
	if g.AppSlug == "" && g.PrivateKey == "" {
		errs = append(errs, errors.New("BURP_GITHUB_APP_SLUG is required unless BURP_GITHUB_PRIVATE_KEY is set (it is then resolved from the API)"))
	}
	if g.PrivateKey != "" && g.AppID == 0 {
		errs = append(errs, errors.New("BURP_GITHUB_APP_ID is required when BURP_GITHUB_PRIVATE_KEY is set"))
	}

	// Store.
	s := &cfg.Store
	s.Driver = strings.ToLower(env("STORE_DRIVER", DriverSQLite))
	s.DSN = env("DATABASE_DSN", "")
	s.EncryptionKey = env("ENCRYPTION_KEY", "")
	switch s.Driver {
	case DriverSQLite:
		if s.DSN == "" {
			s.DSN = "burp.db"
		}
	case DriverPostgres:
		if s.DSN == "" {
			errs = append(errs, errors.New("BURP_DATABASE_DSN is required for the postgres driver"))
		}
	case DriverVault:
	default:
		errs = append(errs, fmt.Errorf("BURP_STORE_DRIVER must be one of %s, %s, %s", DriverSQLite, DriverPostgres, DriverVault))
	}
	if s.Driver != DriverVault && s.EncryptionKey == "" && !cfg.DevMode {
		errs = append(errs, errors.New("BURP_ENCRYPTION_KEY is required for the sqlite and postgres drivers unless BURP_DEV_MODE=true"))
	}

	// Vault.
	v := &cfg.Vault
	v.Addr = env("VAULT_ADDR", getenv("VAULT_ADDR"))
	v.Mount = env("VAULT_MOUNT", "secret")
	v.Path = strings.Trim(env("VAULT_PATH", "burp"), "/")
	v.AuthMethod = strings.ToLower(env("VAULT_AUTH_METHOD", VaultAuthKubernetes))
	v.K8sRole = env("VAULT_K8S_ROLE", "")
	v.K8sMount = strings.Trim(env("VAULT_K8S_MOUNT", "kubernetes"), "/")
	v.K8sTokenPath = env("VAULT_K8S_TOKEN_PATH", "/var/run/secrets/kubernetes.io/serviceaccount/token")
	v.RoleID = env("VAULT_ROLE_ID", "")
	v.SecretID = env("VAULT_SECRET_ID", "")
	v.Token = env("VAULT_TOKEN", getenv("VAULT_TOKEN"))
	if s.Driver == DriverVault {
		if v.Addr == "" {
			errs = append(errs, errors.New("BURP_VAULT_ADDR is required for the vault driver"))
		}
		switch v.AuthMethod {
		case VaultAuthKubernetes:
			if v.K8sRole == "" {
				errs = append(errs, errors.New("BURP_VAULT_K8S_ROLE is required for kubernetes auth"))
			}
		case VaultAuthAppRole:
			if v.RoleID == "" || v.SecretID == "" {
				errs = append(errs, errors.New("BURP_VAULT_ROLE_ID and BURP_VAULT_SECRET_ID are required for approle auth"))
			}
		case VaultAuthToken:
			if v.Token == "" {
				errs = append(errs, errors.New("BURP_VAULT_TOKEN is required for token auth"))
			}
		default:
			errs = append(errs, fmt.Errorf("BURP_VAULT_AUTH_METHOD must be one of %s, %s, %s", VaultAuthKubernetes, VaultAuthAppRole, VaultAuthToken))
		}
	}

	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, errors.New("BURP_LOG_LEVEL must be debug, info, warn or error"))
	}
	switch cfg.LogFormat {
	case "text", "json":
	default:
		errs = append(errs, errors.New("BURP_LOG_FORMAT must be text or json"))
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cfg, nil
}

// SecureCookies reports whether session cookies should carry the Secure flag.
func (c *Config) SecureCookies() bool {
	return strings.HasPrefix(c.BaseURL, "https://")
}

// CallbackURL is the OAuth redirect URI registered with the GitHub App.
func (c *Config) CallbackURL() string {
	return c.BaseURL + "/auth/callback"
}

// GraphQLURL derives the GraphQL endpoint from the API origin.
func (g GitHub) GraphQLURL() string {
	if strings.HasSuffix(g.APIURL, "/api/v3") {
		return strings.TrimSuffix(g.APIURL, "/api/v3") + "/api/graphql"
	}
	return g.APIURL + "/graphql"
}
