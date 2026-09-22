package gh

import (
	"context"
	"crypto/rsa"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	ghub "github.com/google/go-github/v91/github"
)

// AppInfo describes the GitHub App as registered.
type AppInfo struct {
	ID      int64
	Slug    string
	Name    string
	HTMLURL string
}

// App authenticates as the GitHub App itself (JWT). burp only needs this
// to resolve the App's slug and name; every user-facing call is made as
// the user.
type App struct {
	id  int64
	key *rsa.PrivateKey
	rt  http.RoundTripper
	api string
}

// NewApp parses the PEM private key.
func NewApp(appID int64, pem string, apiURL string, base http.RoundTripper) (*App, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pem))
	if err != nil {
		return nil, fmt.Errorf("parsing github app private key: %w", err)
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &App{id: appID, key: key, rt: base, api: apiURL}, nil
}

// JWT signs a short-lived App JWT.
func (a *App) JWT(now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(a.id, 10),
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(a.key)
}

type jwtTransport struct {
	app *App
}

func (t jwtTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tok, err := t.app.JWT(time.Now())
	if err != nil {
		return nil, err
	}
	r2 := r.Clone(r.Context())
	r2.Header.Set("Authorization", "Bearer "+tok)
	return t.app.rt.RoundTrip(r2)
}

// Info fetches the App's metadata.
func (a *App) Info(ctx context.Context) (*AppInfo, error) {
	opts := []ghub.ClientOptionsFunc{ghub.WithHTTPClient(&http.Client{Transport: jwtTransport{app: a}, Timeout: 30 * time.Second})}
	if a.api != "" && a.api != "https://api.github.com" {
		opts = append(opts, ghub.WithEnterpriseURLs(a.api, a.api))
	}
	client, err := ghub.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	app, _, err := client.Apps.Get(ctx, "")
	if err != nil {
		return nil, restErr(err)
	}
	return &AppInfo{ID: app.GetID(), Slug: app.GetSlug(), Name: app.GetName(), HTMLURL: app.GetHTMLURL()}, nil
}
