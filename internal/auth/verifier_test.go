package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func baseClaims(overrides map[string]any) map[string]any {
	claims := map[string]any{
		"sub":            "1234567890",
		"email":          "alice@example.com",
		"email_verified": "true",
		"aud":            "test-client",
		"scope":          "openid email profile",
		"exp":            strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10),
	}
	for k, v := range overrides {
		if v == nil {
			delete(claims, k)
			continue
		}
		claims[k] = v
	}
	return claims
}

func newTokenInfoServer(t *testing.T, claims map[string]any) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Query().Get("access_token") != "good-token" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_token"})
			return
		}
		_ = json.NewEncoder(w).Encode(claims)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func newTestVerifier(t *testing.T, claims map[string]any, opts VerifierOptions) (*OpaqueVerifier, *int32) {
	t.Helper()
	server, calls := newTokenInfoServer(t, claims)
	if opts.TokenInfoURL == "" {
		opts.TokenInfoURL = server.URL
	}
	if opts.Audience == "" {
		opts.Audience = "test-client"
	}
	if opts.Scopes == nil {
		opts.Scopes = []string{"openid", "email"}
	}
	return NewOpaqueVerifier(opts), calls
}

func TestOpaqueVerifierSuccess(t *testing.T) {
	verifier, _ := newTestVerifier(t, baseClaims(nil), VerifierOptions{RequireVerifiedEmail: true})

	identity, err := verifier.Verify(context.Background(), "good-token")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if identity.Email != "alice@example.com" || identity.Subject != "1234567890" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	if identity.Username() != "alice@example.com" {
		t.Fatalf("username = %q", identity.Username())
	}
	if len(identity.Scopes) == 0 {
		t.Fatalf("expected scopes, got %+v", identity.Scopes)
	}
}

func TestOpaqueVerifierRejections(t *testing.T) {
	cases := []struct {
		name   string
		claims map[string]any
		want   error
	}{
		{"bad audience", baseClaims(map[string]any{"aud": "other"}), ErrInvalidToken},
		{"expired", baseClaims(map[string]any{"exp": strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)}), ErrInvalidToken},
		{"missing scope", baseClaims(map[string]any{"scope": "openid"}), ErrInsufficientScope},
		{"unverified email", baseClaims(map[string]any{"email_verified": "false"}), ErrInvalidToken},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verifier, _ := newTestVerifier(t, tc.claims, VerifierOptions{RequireVerifiedEmail: true})
			_, err := verifier.Verify(context.Background(), "good-token")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestOpaqueVerifierRejectsUnknownToken(t *testing.T) {
	verifier, _ := newTestVerifier(t, baseClaims(nil), VerifierOptions{})
	if _, err := verifier.Verify(context.Background(), "bad-token"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestOpaqueVerifierCachesResult(t *testing.T) {
	verifier, calls := newTestVerifier(t, baseClaims(nil), VerifierOptions{CacheTTL: time.Minute})

	for range 3 {
		if _, err := verifier.Verify(context.Background(), "good-token"); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("token info called %d times, want 1", got)
	}
}
