// Command burp is a web-based code review application for GitHub pull
// requests. It runs as a single binary configured entirely through BURP_*
// environment variables.
//
//	burp serve     start the server (default)
//	burp migrate   apply database migrations and exit
//	burp keygen    print a new BURP_ENCRYPTION_KEY
//	burp version   print the version
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/goodtune/burp/internal/bus"
	"github.com/goodtune/burp/internal/config"
	"github.com/goodtune/burp/internal/crypto"
	"github.com/goodtune/burp/internal/gh"
	"github.com/goodtune/burp/internal/store"
	"github.com/goodtune/burp/internal/store/sqlstore"
	"github.com/goodtune/burp/internal/store/vaultstore"
	"github.com/goodtune/burp/internal/web"
)

var version = "dev"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "migrate":
		err = migrate()
	case "keygen":
		var key string
		key, err = crypto.GenerateKey()
		fmt.Println(key)
	case "version":
		fmt.Println("burp", version)
	case "help", "-h", "--help":
		fmt.Println("usage: burp [serve|migrate|keygen|version]")
	default:
		err = fmt.Errorf("unknown command %q (try: serve, migrate, keygen, version)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "burp:", err)
		os.Exit(1)
	}
}

func newLogger(cfg *config.Config) *slog.Logger {
	var level slog.Level
	_ = level.UnmarshalText([]byte(cfg.LogLevel))
	opts := &slog.HandlerOptions{Level: level}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

// openStore builds the configured backend and the matching encryptor.
func openStore(ctx context.Context, cfg *config.Config, logger *slog.Logger) (store.Store, *crypto.Encryptor, error) {
	switch cfg.Store.Driver {
	case config.DriverVault:
		st, err := vaultstore.New(ctx, vaultstore.Config{
			Addr: cfg.Vault.Addr, Mount: cfg.Vault.Mount, Path: cfg.Vault.Path, AuthMethod: cfg.Vault.AuthMethod,
			K8sRole: cfg.Vault.K8sRole, K8sMount: cfg.Vault.K8sMount, K8sTokenPath: cfg.Vault.K8sTokenPath,
			RoleID: cfg.Vault.RoleID, SecretID: cfg.Vault.SecretID, Token: cfg.Vault.Token,
		})
		if err != nil {
			return nil, nil, err
		}
		logger.Info("store", "driver", "vault", "addr", cfg.Vault.Addr, "auth", cfg.Vault.AuthMethod, "path", cfg.Vault.Mount+"/"+cfg.Vault.Path)
		return st, crypto.NewPassthroughEncryptor(), nil
	default:
		st, err := sqlstore.Open(ctx, cfg.Store.Driver, cfg.Store.DSN)
		if err != nil {
			return nil, nil, err
		}
		var enc *crypto.Encryptor
		if cfg.Store.EncryptionKey == "" {
			logger.Warn("BURP_ENCRYPTION_KEY is not set; credentials are stored in plaintext (dev mode only)")
			enc = crypto.NewPassthroughEncryptor()
		} else {
			enc, err = crypto.NewEncryptor(cfg.Store.EncryptionKey)
			if err != nil {
				st.Close()
				return nil, nil, err
			}
		}
		logger.Info("store", "driver", cfg.Store.Driver)
		return st, enc, nil
	}
}

func migrate() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Store.Driver == config.DriverVault {
		fmt.Println("vault backend needs no migrations")
		return nil
	}
	st, err := sqlstore.Open(context.Background(), cfg.Store.Driver, cfg.Store.DSN)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		return err
	}
	fmt.Println("migrations applied")
	return nil
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := newLogger(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, enc, err := openStore(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer st.Close()
	if sq, ok := st.(*sqlstore.Store); ok {
		if err := sq.Migrate(ctx); err != nil {
			return err
		}
	}

	oauth := &gh.OAuth{
		ClientID: cfg.GitHub.ClientID, ClientSecret: cfg.GitHub.ClientSecret,
		WebURL: cfg.GitHub.URL, RedirectURL: cfg.CallbackURL(),
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
	tokens := &gh.Tokens{Store: st, Enc: enc, OAuth: oauth, Logger: logger}
	clients := &gh.ClientFactory{APIURL: cfg.GitHub.APIURL, GraphQLURL: cfg.GitHub.GraphQLURL()}

	slug := cfg.GitHub.AppSlug
	if cfg.GitHub.PrivateKey != "" {
		app, err := gh.NewApp(cfg.GitHub.AppID, cfg.GitHub.PrivateKey, cfg.GitHub.APIURL, nil)
		if err != nil {
			return err
		}
		info, err := app.Info(ctx)
		if err != nil {
			logger.Warn("could not fetch GitHub App metadata", "error", err)
		} else {
			logger.Info("github app", "id", info.ID, "slug", info.Slug, "name", info.Name)
			if slug == "" {
				slug = info.Slug
			}
		}
	}

	b := bus.New()
	srv, err := web.New(web.Deps{Config: cfg, Store: st, Tokens: tokens, OAuth: oauth, Clients: clients, Bus: b, Logger: logger, AppSlug: slug})
	if err != nil {
		return err
	}

	// Periodic cleanup of expired sessions and OAuth states.
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := st.Cleanup(ctx, time.Now()); err != nil && ctx.Err() == nil {
					logger.Warn("cleanup failed", "error", err)
				}
			}
		}
	}()

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: SSE streams stay open indefinitely.
		IdleTimeout: 120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Listen, "base_url", cfg.BaseURL, "version", version)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}
