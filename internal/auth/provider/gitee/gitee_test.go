package gitee

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"
)

func TestValidateTokenReadsProfileAndEmail(t *testing.T) {
	var sawUserAgent bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "" {
			sawUserAgent = true
		}
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"id":42,"login":"alice","name":"Alice","avatar_url":"http://a"}`))
		case "/emails":
			_, _ = w.Write([]byte(`[{"email":"alice@gitee.com","state":"confirmed"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := &Provider{
		client:    srv.Client(),
		userURL:   srv.URL + "/user",
		emailsURL: srv.URL + "/emails",
	}
	info, err := p.ValidateToken(context.Background(), "token")
	if err != nil {
		t.Fatal(err)
	}
	if !sawUserAgent {
		t.Error("requests must carry a user agent; Gitee rejects those without one")
	}
	if info.ID != "42" || info.Email != "alice@gitee.com" || !info.EmailVerified || info.Name != "Alice" {
		t.Fatalf("info = %+v", info)
	}
}

func TestValidateTokenToleratesMissingEmail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			_, _ = w.Write([]byte(`{"id":7,"login":"bob"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	p := &Provider{client: srv.Client(), userURL: srv.URL + "/user", emailsURL: srv.URL + "/emails"}
	info, err := p.ValidateToken(context.Background(), "token")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "7" || info.Email != "" || info.Name != "bob" {
		t.Fatalf("info = %+v", info)
	}
}

func TestExchangeCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "auth-code" {
			t.Errorf("code = %q", r.Form.Get("code"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"atk","refresh_token":"rtk","expires_in":86400,"token_type":"bearer"}`))
	}))
	defer srv.Close()

	p, err := New(Config{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/cb"})
	if err != nil {
		t.Fatal(err)
	}
	p.cfg.Endpoint = oauth2.Endpoint{TokenURL: srv.URL, AuthStyle: oauth2.AuthStyleInParams}

	tok, err := p.ExchangeCode(context.Background(), "auth-code", "")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "atk" || tok.RefreshToken != "rtk" {
		t.Fatalf("token = %+v", tok)
	}
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected an error for an empty config")
	}
}
