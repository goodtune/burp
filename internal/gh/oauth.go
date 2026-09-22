// Package gh integrates burp with GitHub. burp is a GitHub App; users
// authorise it through the user-to-server OAuth flow and burp then calls
// the REST and GraphQL APIs as that user. Webhook deliveries from the App
// tell burp when a pull request changed.
package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxTokenResponse = 64 * 1024

// OAuth performs the user-to-server token exchange for a GitHub App.
type OAuth struct {
	ClientID     string
	ClientSecret string
	// WebURL is the GitHub web origin (https://github.com).
	WebURL string
	// RedirectURL is the callback registered with the App.
	RedirectURL string
	HTTPClient  *http.Client
}

// TokenResponse is GitHub's token endpoint response. GitHub Apps with
// expiring user tokens return an 8 hour access token and a 6 month
// refresh token.
type TokenResponse struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresIn             int    `json:"expires_in"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
	TokenType             string `json:"token_type"`
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
}

// AccessExpiry converts ExpiresIn to an absolute time, defaulting to 8h
// when GitHub omits it (non-expiring tokens).
func (t TokenResponse) AccessExpiry(now time.Time) time.Time {
	if t.ExpiresIn <= 0 {
		return now.Add(8 * time.Hour)
	}
	return now.Add(time.Duration(t.ExpiresIn) * time.Second)
}

// RefreshExpiry converts RefreshTokenExpiresIn to an absolute time,
// defaulting to six months.
func (t TokenResponse) RefreshExpiry(now time.Time) time.Time {
	if t.RefreshTokenExpiresIn <= 0 {
		return now.Add(6 * 30 * 24 * time.Hour)
	}
	return now.Add(time.Duration(t.RefreshTokenExpiresIn) * time.Second)
}

// AuthorizeURL is the page the browser is sent to. GitHub Apps do not take
// scopes: permissions come from the App's configuration.
func (o *OAuth) AuthorizeURL(state string) string {
	q := url.Values{
		"client_id":    {o.ClientID},
		"redirect_uri": {o.RedirectURL},
		"state":        {state},
	}
	return o.WebURL + "/login/oauth/authorize?" + q.Encode()
}

// Exchange trades an authorisation code for tokens.
func (o *OAuth) Exchange(ctx context.Context, code string) (*TokenResponse, error) {
	return o.post(ctx, url.Values{
		"client_id":     {o.ClientID},
		"client_secret": {o.ClientSecret},
		"code":          {code},
		"redirect_uri":  {o.RedirectURL},
	})
}

// Refresh trades a refresh token for a new token pair.
func (o *OAuth) Refresh(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	return o.post(ctx, url.Values{
		"client_id":     {o.ClientID},
		"client_secret": {o.ClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
}

func (o *OAuth) post(ctx context.Context, form url.Values) (*TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.WebURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client := o.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponse))
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	if tr.Error != "" {
		return nil, &OAuthError{Code: tr.Error, Description: tr.ErrorDescription}
	}
	if tr.AccessToken == "" {
		return nil, errors.New("token response contained no access token")
	}
	return &tr, nil
}

// OAuthError is a structured error from GitHub's token endpoint.
type OAuthError struct {
	Code        string
	Description string
}

func (e *OAuthError) Error() string {
	if e.Description != "" {
		return e.Code + ": " + e.Description
	}
	return e.Code
}

// Permanent reports whether the error means the user must sign in again
// (the refresh token was revoked or expired).
func (e *OAuthError) Permanent() bool {
	switch e.Code {
	case "bad_refresh_token", "invalid_grant", "unauthorized_client", "incorrect_client_credentials":
		return true
	}
	return false
}
