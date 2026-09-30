package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrInvalidToken is returned when a bearer token is missing, malformed,
// expired, minted for a different audience or otherwise not trustworthy.
var ErrInvalidToken = errors.New("invalid access token")

// ErrInsufficientScope is returned when a valid token lacks a required scope.
var ErrInsufficientScope = errors.New("insufficient scope")

// Verifier validates a bearer token and extracts the caller identity.
type Verifier interface {
	Verify(ctx context.Context, token string) (*Identity, error)
}

// VerifierOptions configures an OpaqueVerifier.
type VerifierOptions struct {
	// TokenInfoURL validates the opaque access token and returns its claims.
	TokenInfoURL string
	// UserInfoURL optionally enriches the identity with profile data.
	UserInfoURL string
	// Audience is the expected "aud"/"azp" value. Empty disables the check.
	Audience string
	// Scopes are the scopes a token must contain.
	Scopes []string
	// RequireVerifiedEmail rejects identities whose email is not verified.
	RequireVerifiedEmail bool
	// CacheTTL bounds how long successful verifications are cached.
	CacheTTL time.Duration
	// HTTPClient performs the provider calls.
	HTTPClient *http.Client
}

// OpaqueVerifier validates opaque OAuth access tokens by asking the
// authorization server (Google's tokeninfo endpoint by default). It is the
// correct approach for providers such as Google, whose access tokens are not
// self-contained JWTs.
type OpaqueVerifier struct {
	opts   VerifierOptions
	client *http.Client
	cache  *tokenCache
}

var _ Verifier = (*OpaqueVerifier)(nil)

// NewOpaqueVerifier builds an opaque-token verifier.
func NewOpaqueVerifier(opts VerifierOptions) *OpaqueVerifier {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &OpaqueVerifier{
		opts:   opts,
		client: client,
		cache:  newTokenCache(),
	}
}

// Verify implements Verifier.
func (v *OpaqueVerifier) Verify(ctx context.Context, token string) (*Identity, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrInvalidToken
	}

	key := hashToken(token)
	if identity := v.cache.get(key); identity != nil {
		return identity, nil
	}

	claims, err := v.fetchTokenInfo(ctx, token)
	if err != nil {
		return nil, err
	}
	if err := v.checkClaims(claims); err != nil {
		return nil, err
	}

	identity := identityFromClaims(claims)
	if identity.Subject == "" && identity.Email == "" {
		return nil, fmt.Errorf("%w: token carries neither subject nor email", ErrInvalidToken)
	}

	if v.opts.UserInfoURL != "" && identity.Name == "" {
		if name, email, subject, err := v.fetchUserInfo(ctx, token); err == nil {
			if identity.Name == "" {
				identity.Name = name
			}
			if identity.Email == "" {
				identity.Email = email
			}
			if identity.Subject == "" {
				identity.Subject = subject
			}
		}
	}

	v.cache.set(key, identity, v.cacheTTL(claims))
	return identity, nil
}

func (v *OpaqueVerifier) fetchTokenInfo(ctx context.Context, token string) (map[string]any, error) {
	if v.opts.TokenInfoURL == "" {
		return nil, fmt.Errorf("%w: no token info endpoint configured", ErrInvalidToken)
	}

	endpoint, err := url.Parse(v.opts.TokenInfoURL)
	if err != nil {
		return nil, fmt.Errorf("parse token info url: %w", err)
	}
	q := endpoint.Query()
	q.Set("access_token", token)
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("verify token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read token info response: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, fmt.Errorf("%w: unreadable token info response", ErrInvalidToken)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		detail := stringClaim(claims, "error_description")
		if detail == "" {
			detail = stringClaim(claims, "error")
		}
		if detail == "" {
			detail = strings.TrimSpace(string(body))
		}
		return nil, fmt.Errorf("%w: provider rejected token (%d): %s", ErrInvalidToken, resp.StatusCode, detail)
	}

	return claims, nil
}

func (v *OpaqueVerifier) fetchUserInfo(ctx context.Context, token string) (name, email, subject string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.opts.UserInfoURL, nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", "", "", fmt.Errorf("userinfo returned status %d", resp.StatusCode)
	}

	var claims map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&claims); err != nil {
		return "", "", "", err
	}
	return stringClaim(claims, "name"), stringClaim(claims, "email"), stringClaim(claims, "sub"), nil
}

func (v *OpaqueVerifier) checkClaims(claims map[string]any) error {
	now := time.Now()

	if exp := intClaim(claims, "exp"); exp > 0 && now.After(time.Unix(exp, 0)) {
		return fmt.Errorf("%w: token expired", ErrInvalidToken)
	}
	if nbf := intClaim(claims, "nbf"); nbf > 0 && now.Before(time.Unix(nbf, 0)) {
		return fmt.Errorf("%w: token not yet valid", ErrInvalidToken)
	}

	if audience := v.opts.Audience; audience != "" {
		if !audienceMatches(claims, audience) {
			return fmt.Errorf("%w: audience mismatch", ErrInvalidToken)
		}
	}

	if len(v.opts.Scopes) > 0 {
		granted := claimScopes(claims)
		for _, required := range v.opts.Scopes {
			if !granted[required] {
				return fmt.Errorf("%w: missing scope %q", ErrInsufficientScope, required)
			}
		}
	}

	email := stringClaim(claims, "email")
	if v.opts.RequireVerifiedEmail && email != "" {
		if verified, ok := boolClaim(claims, "email_verified"); !ok || !verified {
			return fmt.Errorf("%w: email is not verified", ErrInvalidToken)
		}
	}

	return nil
}

func (v *OpaqueVerifier) cacheTTL(claims map[string]any) time.Duration {
	ttl := v.opts.CacheTTL
	if ttl <= 0 {
		return 0
	}
	if exp := intClaim(claims, "exp"); exp > 0 {
		if remaining := time.Until(time.Unix(exp, 0)); remaining < ttl {
			ttl = remaining
		}
	}
	if ttl < time.Second {
		ttl = time.Second
	}
	return ttl
}

func identityFromClaims(claims map[string]any) *Identity {
	return &Identity{
		Subject: stringClaim(claims, "sub"),
		Email:   stringClaim(claims, "email"),
		Name:    stringClaim(claims, "name"),
		Scopes:  scopeList(claimScopes(claims)),
	}
}

func audienceMatches(claims map[string]any, expected string) bool {
	for _, key := range []string{"aud", "azp", "client_id"} {
		for _, candidate := range stringList(claims[key]) {
			if candidate == expected {
				return true
			}
		}
	}
	return false
}

func claimScopes(claims map[string]any) map[string]bool {
	granted := make(map[string]bool)
	if raw, ok := claims["scope"]; ok {
		for _, scope := range stringList(raw) {
			for _, field := range strings.Fields(scope) {
				granted[field] = true
			}
		}
	}
	for _, scope := range stringList(claims["scp"]) {
		granted[scope] = true
	}
	return granted
}

func scopeList(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for scope := range set {
		out = append(out, scope)
	}
	return out
}

func stringClaim(claims map[string]any, key string) string {
	value, ok := claims[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

func intClaim(claims map[string]any, key string) int64 {
	raw := stringClaim(claims, key)
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		if f, ferr := strconv.ParseFloat(raw, 64); ferr == nil {
			return int64(f)
		}
		return 0
	}
	return value
}

func boolClaim(claims map[string]any, key string) (bool, bool) {
	value, ok := claims[key]
	if !ok {
		return false, false
	}
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		parsed, err := strconv.ParseBool(v)
		return parsed, err == nil
	default:
		return false, false
	}
}

func stringList(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return []string{v}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type cacheEntry struct {
	identity  *Identity
	expiresAt time.Time
}

type tokenCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
}

func newTokenCache() *tokenCache {
	return &tokenCache{entries: make(map[string]cacheEntry)}
}

func (c *tokenCache) get(key string) *Identity {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		return nil
	}
	return entry.identity
}

func (c *tokenCache) set(key string, identity *Identity, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) > 4096 {
		now := time.Now()
		for k, entry := range c.entries {
			if now.After(entry.expiresAt) {
				delete(c.entries, k)
			}
		}
	}
	c.entries[key] = cacheEntry{identity: identity, expiresAt: time.Now().Add(ttl)}
}
