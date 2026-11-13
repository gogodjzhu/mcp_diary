package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"

	"github.com/gogodjzhu/mcp-diary/internal/transport/httpapi"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestEmailLoginEndToEnd drives the whole email login path the browser drives:
// start the authorization flow, submit the password, follow the callback, and
// exchange the resulting code. It then calls /api/me with the issued token,
// which is the request that used to 401.
func TestEmailLoginEndToEnd(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	issuer := "http://" + listener.Addr().String()

	o := newTestOAuthAt(t, issuer)
	if _, err := o.users.CreateEmailUser("alice@example.com", "secret123", "Alice", time.Now()); err != nil {
		t.Fatalf("create user: %v", err)
	}

	mux := http.NewServeMux()
	o.RegisterRoutes(mux, "/mcp")
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/done?"+r.URL.RawQuery, http.StatusFound)
	})
	// The real web handler, so the test reads the identity the way production does.
	mux.Handle("/api/", o.Protect(httpapi.New(httpapi.Config{Users: o.Users()})))

	ts := httptest.NewUnstartedServer(mux)
	ts.Listener = listener
	ts.Start()
	t.Cleanup(ts.Close)

	verifier := "verifier-0123456789abcdef0123456789abcdefXYZ"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	clientState := "clientstate1234567890abcdef"

	client := ts.Client()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	start := issuer + "/oauth/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {WebClientID},
		"redirect_uri":          {issuer + "/auth/callback"},
		"scope":                 {"mcp"},
		"state":                 {clientState},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"resource":              {issuer + "/mcp"},
	}.Encode()
	resp, err := noRedirect.Get(start)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	loginURL := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.Contains(loginURL, "/login?state=") {
		t.Fatalf("authorize -> %d %q body %s", resp.StatusCode, loginURL, raw)
	}

	form := url.Values{
		"email":    {"alice@example.com"},
		"password": {"secret123"},
		"state":    {mustQuery(t, loginURL, "state")},
	}
	resp, err = noRedirect.PostForm(issuer+"/login/email", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	callback := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.Contains(callback, "/oauth/callback/email") {
		t.Fatalf("login -> %d %q", resp.StatusCode, callback)
	}

	resp, err = noRedirect.Get(issuer + callback)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	back := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback -> %d %s", resp.StatusCode, raw)
	}
	if got := mustQuery(t, back, "state"); got != clientState {
		t.Fatalf("client state = %q, want %q", got, clientState)
	}
	authCode := mustQuery(t, back, "code")
	if authCode == "" {
		t.Fatalf("no authorization code in %q", back)
	}

	exchange := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {authCode},
		"redirect_uri":  {issuer + "/auth/callback"},
		"client_id":     {WebClientID},
		"code_verifier": {verifier},
		"resource":      {issuer + "/mcp"},
	}
	resp, err = client.PostForm(issuer+"/oauth/token", exchange)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var token map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token -> %d %v", resp.StatusCode, token)
	}
	access, _ := token["access_token"].(string)
	if access == "" {
		t.Fatalf("no access token: %v", token)
	}

	apiReq, _ := http.NewRequest(http.MethodGet, issuer+"/api/me", nil)
	apiReq.Header.Set("Authorization", "Bearer "+access)
	resp, err = client.Do(apiReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var me map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&me)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/me -> %d %v", resp.StatusCode, me)
	}
	if me["email"] != "alice@example.com" || me["email_verified"] != true {
		t.Fatalf("/api/me = %v", me)
	}
	if id, _ := me["user_id"].(string); !strings.HasPrefix(id, "usr_") {
		t.Fatalf("user_id = %v", me["user_id"])
	}
}

func mustQuery(t *testing.T, rawURL, key string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}
