package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// MiddlewareConfig configures the bearer-token middleware.
type MiddlewareConfig struct {
	Verifier     Verifier
	Scopes       []string
	PublicURL    string
	EndpointPath string
	Logger       *slog.Logger
}

// Middleware protects an http.Handler with OAuth 2.0 bearer-token
// authentication. Unauthenticated requests receive a 401 with a
// WWW-Authenticate challenge that points clients at the protected resource
// metadata document.
type Middleware struct {
	cfg MiddlewareConfig
}

// NewMiddleware builds the authentication middleware.
func NewMiddleware(cfg MiddlewareConfig) *Middleware {
	return &Middleware{cfg: cfg}
}

// Wrap returns a handler that authenticates the request before delegating.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			m.challenge(w, r, "invalid_request", "missing bearer token")
			return
		}

		identity, err := m.cfg.Verifier.Verify(r.Context(), token)
		if err != nil {
			code := "invalid_token"
			if errors.Is(err, ErrInsufficientScope) {
				code = "insufficient_scope"
			}
			if m.cfg.Logger != nil {
				m.cfg.Logger.Debug("authentication failed", "error", err, "code", code)
			}
			m.challenge(w, r, code, err.Error())
			return
		}

		if m.cfg.Logger != nil {
			m.cfg.Logger.Debug("authenticated request", "user", identity.Username(), "subject", identity.Subject)
		}

		next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), identity)))
	})
}

func (m *Middleware) challenge(w http.ResponseWriter, r *http.Request, code, description string) {
	params := make([]string, 0, 4)
	if metadataURL := MetadataURL(m.cfg.PublicURL, m.cfg.EndpointPath, r); metadataURL != "" {
		params = append(params, fmt.Sprintf("resource_metadata=%q", metadataURL))
	}
	if len(m.cfg.Scopes) > 0 {
		params = append(params, fmt.Sprintf("scope=%q", strings.Join(m.cfg.Scopes, " ")))
	}
	params = append(params, fmt.Sprintf("error=%q", code))
	if description != "" {
		params = append(params, fmt.Sprintf("error_description=%q", description))
	}

	w.Header().Set("WWW-Authenticate", "Bearer "+strings.Join(params, ", "))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}
