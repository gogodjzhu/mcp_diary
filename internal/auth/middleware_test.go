package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubVerifier struct {
	identity *Identity
	err      error
}

func (s stubVerifier) Verify(context.Context, string) (*Identity, error) {
	return s.identity, s.err
}

func TestMiddlewareRejectsMissingToken(t *testing.T) {
	called := false
	handler := NewMiddleware(MiddlewareConfig{
		Verifier:     stubVerifier{identity: &Identity{Email: "a@b.com"}},
		Scopes:       []string{"openid", "email"},
		PublicURL:    "https://mcp.example.com",
		EndpointPath: "/mcp",
	}).Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))

	if called {
		t.Fatal("downstream handler must not run without a token")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	challenge := rec.Header().Get("WWW-Authenticate")
	for _, want := range []string{
		`Bearer`,
		`resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/mcp"`,
		`scope="openid email"`,
		`error="invalid_request"`,
	} {
		if !strings.Contains(challenge, want) {
			t.Fatalf("challenge %q missing %q", challenge, want)
		}
	}
}

func TestMiddlewarePropagatesIdentity(t *testing.T) {
	identity := &Identity{Subject: "1", Email: "alice@example.com"}
	var got *Identity

	handler := NewMiddleware(MiddlewareConfig{
		Verifier:     stubVerifier{identity: identity},
		EndpointPath: "/mcp",
	}).Wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = IdentityFrom(r.Context())
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer good-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got == nil || got.Email != "alice@example.com" {
		t.Fatalf("identity not propagated: %+v", got)
	}
}

func TestMiddlewareInvalidToken(t *testing.T) {
	handler := NewMiddleware(MiddlewareConfig{
		Verifier:     stubVerifier{err: ErrInvalidToken},
		EndpointPath: "/mcp",
	}).Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer nope")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("unexpected challenge: %q", rec.Header().Get("WWW-Authenticate"))
	}
}

func TestMiddlewareInsufficientScopeCode(t *testing.T) {
	handler := NewMiddleware(MiddlewareConfig{
		Verifier:     stubVerifier{err: errors.Join(ErrInsufficientScope, errors.New("missing files:write"))},
		EndpointPath: "/mcp",
	}).Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer good-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), `error="insufficient_scope"`) {
		t.Fatalf("unexpected challenge: %q", rec.Header().Get("WWW-Authenticate"))
	}
}

func TestResourceURLDerivation(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://internal/mcp", nil)
	req.Host = "internal"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "mcp.example.com")

	if got := ResourceURL("", "/mcp", req); got != "https://mcp.example.com/mcp" {
		t.Fatalf("ResourceURL = %q", got)
	}
	if got := MetadataURL("", "/mcp", req); got != "https://mcp.example.com/.well-known/oauth-protected-resource/mcp" {
		t.Fatalf("MetadataURL = %q", got)
	}
	if got := MetadataPath("/"); got != "/.well-known/oauth-protected-resource" {
		t.Fatalf("MetadataPath = %q", got)
	}
}
