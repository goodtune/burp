package gh

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fakeTokenServer(t *testing.T, handler func(form map[string]string) (int, string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" || r.Method != http.MethodPost {
			http.Error(w, "wrong endpoint", 404)
			return
		}
		if r.Header.Get("Accept") != "application/json" {
			http.Error(w, "accept", 400)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		form := map[string]string{}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		code, body := handler(form)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
}

func TestAuthorizeURL(t *testing.T) {
	o := &OAuth{ClientID: "id", WebURL: "https://github.com", RedirectURL: "https://burp/auth/callback"}
	u := o.AuthorizeURL("st")
	if !strings.HasPrefix(u, "https://github.com/login/oauth/authorize?") || !strings.Contains(u, "client_id=id") || !strings.Contains(u, "state=st") || !strings.Contains(u, "redirect_uri=https%3A%2F%2Fburp%2Fauth%2Fcallback") {
		t.Fatal(u)
	}
	if strings.Contains(u, "scope") {
		t.Fatal("github apps must not send scopes")
	}
}

func TestExchange(t *testing.T) {
	srv := fakeTokenServer(t, func(form map[string]string) (int, string) {
		if form["code"] != "c0de" || form["client_id"] != "id" || form["client_secret"] != "sec" || form["redirect_uri"] != "https://burp/auth/callback" {
			return 400, `{"error":"bad_form"}`
		}
		return 200, `{"access_token":"ghu_a","refresh_token":"ghr_r","expires_in":28800,"refresh_token_expires_in":15897600,"token_type":"bearer"}`
	})
	defer srv.Close()
	o := &OAuth{ClientID: "id", ClientSecret: "sec", WebURL: srv.URL, RedirectURL: "https://burp/auth/callback"}
	tr, err := o.Exchange(context.Background(), "c0de")
	if err != nil {
		t.Fatal(err)
	}
	if tr.AccessToken != "ghu_a" || tr.RefreshToken != "ghr_r" {
		t.Fatalf("%+v", tr)
	}
	now := time.Unix(0, 0)
	if tr.AccessExpiry(now) != now.Add(8*time.Hour) || tr.RefreshExpiry(now) != now.Add(184*24*time.Hour) {
		t.Fatalf("expiries %v %v", tr.AccessExpiry(now), tr.RefreshExpiry(now))
	}
	if (TokenResponse{}).AccessExpiry(now) != now.Add(8*time.Hour) || (TokenResponse{}).RefreshExpiry(now) != now.Add(180*24*time.Hour) {
		t.Fatal("defaults")
	}
}

func TestRefreshErrors(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		permanent bool
		wantErr   string
	}{
		{"bad refresh token", 200, `{"error":"bad_refresh_token","error_description":"The refresh token passed is incorrect or expired."}`, true, "bad_refresh_token"},
		{"transient", 200, `{"error":"server_error"}`, false, "server_error"},
		{"http error", 502, `bad gateway`, false, "502"},
		{"no token", 200, `{}`, false, "no access token"},
		{"garbage", 200, `{`, false, "parsing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeTokenServer(t, func(form map[string]string) (int, string) {
				if form["grant_type"] != "refresh_token" || form["refresh_token"] != "ghr_r" {
					return 400, `{"error":"bad_form"}`
				}
				return tc.status, tc.body
			})
			defer srv.Close()
			o := &OAuth{ClientID: "id", ClientSecret: "sec", WebURL: srv.URL}
			_, err := o.Refresh(context.Background(), "ghr_r")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v", err)
			}
			var oe *OAuthError
			if errors.As(err, &oe) != (tc.status == 200 && strings.HasPrefix(tc.body, `{"error"`)) {
				t.Fatalf("OAuthError classification wrong: %v", err)
			}
			if oe != nil && oe.Permanent() != tc.permanent {
				t.Fatalf("permanent = %v", oe.Permanent())
			}
		})
	}
}
