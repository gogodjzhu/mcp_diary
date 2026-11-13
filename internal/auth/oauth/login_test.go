package oauth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/auth/user"
	"github.com/gogodjzhu/mcp-diary/internal/platform/config"
	"github.com/gogodjzhu/mcp-diary/internal/platform/logging"
)

func newTestOAuth(t *testing.T) *OAuth {
	t.Helper()
	return newTestOAuthAt(t, "https://mcp.test")
}

func newTestOAuthAt(t *testing.T, issuer string) *OAuth {
	t.Helper()
	cfg := config.Default().Auth
	cfg.Enabled = true
	cfg.PublicURL = issuer
	cfg.GoogleClientID = "google-client"
	cfg.GoogleClientSecret = "google-secret"
	cfg.EmailEnabled = true

	dir := t.TempDir()
	users, err := user.Open(filepath.Join(dir, "users.json"), []byte("test-secret"))
	if err != nil {
		t.Fatalf("open user store: %v", err)
	}
	logger, err := logging.New("error", "text")
	if err != nil {
		t.Fatal(err)
	}
	o, err := New(cfg, "/mcp", filepath.Join(dir, "oauth.json"), logger, Dependencies{Users: users})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = o.Close() })
	return o
}

func serve(t *testing.T, o *OAuth) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	o.RegisterRoutes(mux, "/mcp")
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestLoginPageListsProviders(t *testing.T) {
	o := newTestOAuth(t)
	ts := serve(t, o)

	resp, err := ts.Client().Get(ts.URL + "/login?state=abc123")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, want := range []string{"/login/start?provider=google", "/login/email?state=abc123"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("login page missing %q", want)
		}
	}
}

func TestLoginPageRequiresState(t *testing.T) {
	o := newTestOAuth(t)
	ts := serve(t, o)

	resp, err := ts.Client().Get(ts.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestEmailLoginThrottlesRepeatedFailures(t *testing.T) {
	o := newTestOAuth(t)
	ts := serve(t, o)
	client := ts.Client()
	// Follow no redirects so the response is the form, not the callback.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	form := url.Values{"email": {"nobody@example.com"}, "password": {"wrong9999"}, "state": {"s"}}
	var last int
	for i := 0; i < loginAttemptMax+1; i++ {
		resp, err := client.PostForm(ts.URL+"/login/email", form)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		last = resp.StatusCode
		if i < loginAttemptMax && !strings.Contains(string(body), "邮箱或密码错误") {
			t.Fatalf("attempt %d body = %s", i, body)
		}
	}
	if last != http.StatusOK {
		t.Fatalf("throttled response status = %d", last)
	}
	resp, err := client.PostForm(ts.URL+"/login/email", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "尝试次数过多") {
		t.Fatalf("expected throttle message, got %s", body)
	}
}

func TestCallbackShimRejectsUnknownPath(t *testing.T) {
	o := newTestOAuth(t)
	ts := serve(t, o)

	resp, err := ts.Client().Get(ts.URL + "/oauth/callback/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGateBlocksUnverifiedUser(t *testing.T) {
	o := newTestOAuth(t)

	u, err := o.users.CreateOAuthUser(user.ProviderGoogle, "sub", user.Profile{Email: "a@b.co"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ident, err := o.identityFor(u.ID, user.ProviderGoogle)
	if err != nil {
		t.Fatal(err)
	}
	if !ident.EmailRequired {
		t.Fatal("fresh oauth user must require email confirmation")
	}

	gated := o.Gate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/sync/media", nil)
	req = req.WithContext(o.withIdentity(req.Context(), ident))
	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), emailBindingCode) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if rec.Header().Get("X-Profile-URL") == "" {
		t.Fatal("missing X-Profile-URL header")
	}
}
