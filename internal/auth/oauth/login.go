package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	oauthlib "github.com/giantswarm/mcp-oauth"
	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"
	"golang.org/x/oauth2"

	"github.com/gogodjzhu/mcp-diary/internal/auth/provider/multi"
	"github.com/gogodjzhu/mcp-diary/internal/auth/user"
	"github.com/gogodjzhu/mcp-diary/internal/auth/verify"
)

// loginCodeTTL bounds how long a completed local login may wait for the
// library to redeem it.
const loginCodeTTL = 60 * time.Second

// bindSessionTTL bounds an in-progress account-linking flow.
const bindSessionTTL = 10 * time.Minute

// loginAttemptWindow and loginAttemptMax throttle password guessing.
const (
	loginAttemptWindow = 10 * time.Minute
	loginAttemptMax    = 5
	loginIPWindow      = time.Minute
	loginIPMax         = 10
)

// loginCode is a one-time code the local email provider redeems. It never
// leaves the process.
type loginCode struct {
	userID    user.ID
	expiresAt time.Time
}

// bindSession tracks an in-progress "link this provider" flow.
type bindSession struct {
	userID    user.ID
	provider  string
	expiresAt time.Time
}

// Login bundles the browser-facing login surface: the provider picker, the
// email/password form and the account-linking callbacks.
type Login struct {
	o          *OAuth
	dispatcher *multi.Provider
	users      *user.Store
	verifier   *verify.Service
	email      *emailProvider

	mu       sync.Mutex
	codes    map[string]loginCode
	binds    map[string]bindSession
	attempts map[string][]time.Time
}

func newLogin(o *OAuth, dispatcher *multi.Provider, users *user.Store, verifier *verify.Service, email *emailProvider) *Login {
	return &Login{
		o:          o,
		dispatcher: dispatcher,
		users:      users,
		verifier:   verifier,
		email:      email,
		codes:      map[string]loginCode{},
		binds:      map[string]bindSession{},
		attempts:   map[string][]time.Time{},
	}
}

// RegisterRoutes mounts the login surface. These routes are unauthenticated:
// the provider state or a bind session is what authorizes them.
func (l *Login) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/login", l.handlePage)
	mux.HandleFunc("/login/start", l.handleStart)
	mux.HandleFunc("/login/email", l.handleEmail)
	mux.HandleFunc("/login/forgot", l.handleForgot)
	mux.HandleFunc("/auth/email/code", l.handleCode)
	mux.HandleFunc("/auth/email/register", l.handleRegister)
	mux.HandleFunc("/auth/email/reset", l.handleReset)
	mux.HandleFunc("/account/link/start", l.handleLinkStart)
	mux.HandleFunc("/account/link/callback/", l.handleLinkCallback)
}

// handlePage renders the provider picker. MCP clients land here because they
// carry no preference; the browser UI skips it by going straight to start.
func (l *Login) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	state := r.URL.Query().Get("state")
	if state == "" {
		http.Error(w, "missing state", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, renderLoginPage(state, l.dispatcher.Names(), l.email != nil))
}

// handleStart redirects to the upstream provider the user picked.
func (l *Login) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provider := r.URL.Query().Get("provider")
	state := r.URL.Query().Get("state")
	if state == "" || !l.dispatcher.Has(provider) {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}
	ctx := multi.WithProvider(r.Context(), provider)
	challenge := l.providerChallenge(r.Context(), state)
	authURL := l.upstreamAuthURL(ctx, provider, state, challenge)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// upstreamAuthURL asks the selected provider for its own authorization URL,
// bypassing the dispatcher which would only point back at the login page.
// challenge is the library's server-to-provider PKCE challenge; the library
// sends the matching verifier when it exchanges the code, so the authorization
// request must carry it or the provider rejects the exchange.
func (l *Login) upstreamAuthURL(ctx context.Context, provider, state, challenge string) string {
	upstream, _, err := l.dispatcher.Resolve(ctx)
	if err != nil {
		return "/login?state=" + state
	}
	method := ""
	if challenge != "" {
		method = "S256"
	}
	return upstream.AuthorizationURL(state, challenge, method, nil, nil)
}

// providerChallenge recovers the PKCE challenge the library generated for this
// authorization. The library stores only the verifier, so the challenge is
// derived from it.
func (l *Login) providerChallenge(ctx context.Context, state string) string {
	authState, err := l.o.store.GetAuthorizationStateByProviderState(ctx, state)
	if err != nil || authState.ProviderCodeVerifier == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(authState.ProviderCodeVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (l *Login) handleEmail(w http.ResponseWriter, r *http.Request) {
	if l.email == nil {
		http.Error(w, "email login is disabled", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		l.renderEmailForm(w, r.URL.Query().Get("state"), "")
	case http.MethodPost:
		l.submitEmail(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (l *Login) submitEmail(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	email := user.NormalizeEmail(r.Form.Get("email"))
	password := r.Form.Get("password")
	state := r.Form.Get("state")
	ip := clientIP(r)

	if !l.allowAttempt(email, ip, time.Now()) {
		l.o.logger.Warn("email login throttled", "email", user.MaskEmail(email), "ip", ip)
		l.renderEmailForm(w, state, "尝试次数过多，请稍后再试")
		return
	}
	u, err := l.users.AuthenticateEmail(email, password, time.Now())
	if err != nil {
		l.recordAttempt(email, ip, time.Now())
		l.o.logger.Info("email login failed", "email", user.MaskEmail(email), "ip", ip)
		l.renderEmailForm(w, state, "邮箱或密码错误")
		return
	}
	code, err := l.issueLoginCode(u.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	l.o.logger.Info("email login succeeded", "user_id", u.ID)
	http.Redirect(w, r, "/oauth/callback/email?code="+code+"&state="+state, http.StatusFound)
}

func (l *Login) handleForgot(w http.ResponseWriter, r *http.Request) {
	if l.email == nil {
		http.Error(w, "email login is disabled", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, renderForgotPage())
}

func (l *Login) handleCode(w http.ResponseWriter, r *http.Request) {
	if l.verifier == nil {
		writeLoginError(w, http.StatusNotFound, "disabled", "email verification is disabled")
		return
	}
	if r.Method != http.MethodPost {
		writeLoginError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	var body struct {
		Email   string `json:"email"`
		Purpose string `json:"purpose"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeLoginError(w, http.StatusBadRequest, "invalid_request", "invalid JSON")
		return
	}
	email := user.NormalizeEmail(body.Email)
	if !user.ValidEmail(email) {
		writeLoginError(w, http.StatusBadRequest, "invalid_email", "邮箱格式不正确")
		return
	}
	switch body.Purpose {
	case user.PurposeRegister:
		if l.users.EmailRegistered(email) {
			writeLoginError(w, http.StatusConflict, "email_taken", "该邮箱已注册，请直接登录")
			return
		}
	case user.PurposeReset:
		// Always answer 200, whether or not the address exists, so the
		// endpoint cannot be used to enumerate accounts.
		if !l.users.EmailRegistered(email) {
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
			return
		}
	default:
		writeLoginError(w, http.StatusBadRequest, "invalid_request", "unsupported purpose")
		return
	}
	if err := l.verifier.Issue(email, body.Purpose, clientIP(r)); err != nil {
		writeVerifyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (l *Login) handleRegister(w http.ResponseWriter, r *http.Request) {
	if l.email == nil || l.verifier == nil {
		writeLoginError(w, http.StatusNotFound, "disabled", "email registration is disabled")
		return
	}
	if r.Method != http.MethodPost {
		writeLoginError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	var body struct {
		Email       string `json:"email"`
		Code        string `json:"code"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		State       string `json:"state"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeLoginError(w, http.StatusBadRequest, "invalid_request", "invalid JSON")
		return
	}
	email := user.NormalizeEmail(body.Email)
	if err := l.verifier.Redeem(email, user.PurposeRegister, body.Code); err != nil {
		writeVerifyError(w, err)
		return
	}
	u, err := l.users.CreateEmailUser(email, body.Password, strings.TrimSpace(body.DisplayName), time.Now())
	if err != nil {
		writeUserError(w, err)
		return
	}
	code, err := l.issueLoginCode(u.ID)
	if err != nil {
		writeLoginError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	l.o.logger.Info("email registration succeeded", "user_id", u.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"status":       "ok",
		"redirect_url": "/oauth/callback/email?code=" + code + "&state=" + body.State,
	})
}

func (l *Login) handleReset(w http.ResponseWriter, r *http.Request) {
	if l.verifier == nil {
		writeLoginError(w, http.StatusNotFound, "disabled", "password reset is disabled")
		return
	}
	if r.Method != http.MethodPost {
		writeLoginError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	var body struct {
		Email       string `json:"email"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeLoginError(w, http.StatusBadRequest, "invalid_request", "invalid JSON")
		return
	}
	email := user.NormalizeEmail(body.Email)
	if err := l.verifier.Redeem(email, user.PurposeReset, body.Code); err != nil {
		writeVerifyError(w, err)
		return
	}
	u, err := l.users.ResetPassword(email, body.NewPassword, time.Now())
	if err != nil {
		writeUserError(w, err)
		return
	}
	if err := l.o.revokeUserTokens(u.ID); err != nil {
		l.o.logger.Warn("failed to revoke tokens after password reset", "user_id", u.ID, "error", err)
	}
	l.o.logger.Info("password reset", "user_id", u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// StartLink begins an account-linking flow for an authenticated user and
// returns the URL the browser should navigate to.
func (l *Login) StartLink(id user.ID, provider string) (string, error) {
	if !l.dispatcher.Has(provider) {
		return "", fmt.Errorf("%w: %s", multi.ErrUnknownProvider, provider)
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	l.mu.Lock()
	l.binds[token] = bindSession{userID: id, provider: provider, expiresAt: time.Now().Add(bindSessionTTL)}
	l.mu.Unlock()
	return "/account/link/start?bind=" + token, nil
}

func (l *Login) handleLinkStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, ok := l.takeBind(r.URL.Query().Get("bind"), false)
	if !ok {
		http.Error(w, "invalid or expired link session", http.StatusBadRequest)
		return
	}
	upstream, ok := l.o.linking[session.provider]
	if !ok {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, upstream.AuthorizationURL(r.URL.Query().Get("bind"), "", "", nil, nil), http.StatusFound)
}

func (l *Login) handleLinkCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provider := strings.TrimPrefix(r.URL.Path, "/account/link/callback/")
	if i := strings.IndexByte(provider, '/'); i >= 0 {
		provider = provider[:i]
	}
	session, ok := l.takeBind(r.URL.Query().Get("state"), true)
	if !ok || session.provider != provider {
		http.Error(w, "invalid or expired link session", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	upstream, ok := l.o.linking[provider]
	if !ok {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}
	ctx := multi.WithProvider(r.Context(), provider)
	tok, err := exchangeForLink(ctx, upstream, code)
	if err != nil {
		l.o.logger.Warn("account link exchange failed", "provider", provider, "error", err)
		http.Error(w, "authorization failed", http.StatusBadGateway)
		return
	}
	info, err := upstream.ValidateToken(ctx, tok.AccessToken)
	if err != nil {
		http.Error(w, "authorization failed", http.StatusBadGateway)
		return
	}
	if _, err := l.users.LinkIdentity(session.userID, provider, info.ID, user.Profile{
		Email:         info.Email,
		EmailVerified: info.EmailVerified,
		Name:          info.Name,
		AvatarURL:     info.Picture,
	}, time.Now()); err != nil {
		if errors.Is(err, user.ErrIdentityTaken) {
			http.Error(w, "该账号已绑定到其他用户", http.StatusConflict)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	l.o.logger.Info("identity linked", "user_id", session.userID, "provider", provider)
	http.Redirect(w, r, "/account?linked="+provider, http.StatusFound)
}

// issueLoginCode mints a one-time code the email provider redeems.
func (l *Login) issueLoginCode(id user.ID) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	l.mu.Lock()
	l.codes[token] = loginCode{userID: id, expiresAt: time.Now().Add(loginCodeTTL)}
	l.mu.Unlock()
	return token, nil
}

// redeemLoginCode consumes a local login code.
func (l *Login) redeemLoginCode(code string) (user.ID, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.codes[code]
	if !ok {
		return "", false
	}
	delete(l.codes, code)
	if time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.userID, true
}

func (l *Login) takeBind(token string, consume bool) (bindSession, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	session, ok := l.binds[token]
	if !ok || time.Now().After(session.expiresAt) {
		delete(l.binds, token)
		return bindSession{}, false
	}
	if consume {
		delete(l.binds, token)
	}
	return session, true
}

func (l *Login) allowAttempt(email, ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(recentAttempts(l.attempts["email:"+email], now, loginAttemptWindow)) >= loginAttemptMax {
		return false
	}
	if len(recentAttempts(l.attempts["ip:"+ip], now, loginIPWindow)) >= loginIPMax {
		return false
	}
	return true
}

func (l *Login) recordAttempt(email, ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts["email:"+email] = append(recentAttempts(l.attempts["email:"+email], now, loginAttemptWindow), now)
	l.attempts["ip:"+ip] = append(recentAttempts(l.attempts["ip:"+ip], now, loginIPWindow), now)
}

func recentAttempts(times []time.Time, now time.Time, window time.Duration) []time.Time {
	cutoff := now.Add(-window)
	out := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}

func (l *Login) renderEmailForm(w http.ResponseWriter, state, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, renderEmailPage(state, message))
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func decodeJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeLoginError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeVerifyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, verify.ErrRateLimited):
		writeLoginError(w, http.StatusTooManyRequests, "rate_limited", "请求过于频繁，请稍后再试")
	case errors.Is(err, verify.ErrQuotaReached):
		writeLoginError(w, http.StatusServiceUnavailable, "quota_reached", "今日验证码额度已用完，请明天再试")
	case errors.Is(err, verify.ErrSendFailed):
		writeLoginError(w, http.StatusBadGateway, "email_send_failed", "验证码发送失败，请稍后重试")
	case errors.Is(err, user.ErrInvalidCode), errors.Is(err, user.ErrNoPendingCode):
		writeLoginError(w, http.StatusBadRequest, "invalid_code", "验证码不正确")
	case errors.Is(err, user.ErrCodeExpired):
		writeLoginError(w, http.StatusBadRequest, "code_expired", "验证码已过期，请重新获取")
	case errors.Is(err, user.ErrTooManyAttempts):
		writeLoginError(w, http.StatusTooManyRequests, "too_many_attempts", "验证码尝试次数过多，请重新获取")
	default:
		writeLoginError(w, http.StatusInternalServerError, "internal_error", "内部错误")
	}
}

func writeUserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, user.ErrEmailTaken):
		writeLoginError(w, http.StatusConflict, "email_taken", "该邮箱已注册")
	case errors.Is(err, user.ErrPasswordTooShort), errors.Is(err, user.ErrWeakPassword):
		writeLoginError(w, http.StatusBadRequest, "weak_password", "密码至少 8 位，且必须同时包含字母和数字")
	case errors.Is(err, user.ErrInvalidEmail):
		writeLoginError(w, http.StatusBadRequest, "invalid_email", "邮箱格式不正确")
	default:
		writeLoginError(w, http.StatusInternalServerError, "internal_error", "内部错误")
	}
}

// emailProvider is the local email/password provider. It satisfies the
// mcp-oauth Provider interface so the library can drive it like any upstream
// provider, but the code it redeems was minted by Login rather than by an
// external authorization server.
type emailProvider struct {
	login *Login
}

func (e *emailProvider) bind(l *Login) { e.login = l }

func (e *emailProvider) Name() string { return user.ProviderEmail }

func (e *emailProvider) DefaultScopes() []string { return []string{"mcp"} }

func (e *emailProvider) AuthorizationURL(state, _, _ string, _ []string, _ *providers.AuthorizationURLOptions) string {
	return "/login/email?state=" + state
}

func (e *emailProvider) ExchangeCode(_ context.Context, code, _ string) (*oauth2.Token, error) {
	id, ok := e.login.redeemLoginCode(code)
	if !ok {
		return nil, errors.New("invalid or expired login code")
	}
	// The access token carries the user id and never expires. There is no
	// upstream token to renew for a local login, and a short expiry makes the
	// library try to refresh it immediately, which this provider cannot do.
	// The library's own issued token is what actually expires.
	return &oauth2.Token{
		AccessToken: "local_" + string(id),
		Expiry:      time.Now().Add(365 * 24 * time.Hour),
	}, nil
}

func (e *emailProvider) ValidateToken(_ context.Context, accessToken string) (*providers.UserInfo, error) {
	id := user.ID(strings.TrimPrefix(accessToken, "local_"))
	if !id.Valid() {
		return nil, errors.New("invalid local token")
	}
	u, err := e.login.users.Get(id)
	if err != nil {
		return nil, err
	}
	return &providers.UserInfo{
		ID:            string(u.ID),
		Email:         u.PrimaryEmail,
		EmailVerified: u.EmailVerified,
		Name:          u.DisplayName,
	}, nil
}

func (e *emailProvider) RefreshToken(context.Context, string) (*oauth2.Token, error) {
	return nil, errors.New("local email provider does not issue refresh tokens")
}

func (e *emailProvider) RevokeToken(context.Context, string) error { return nil }

func (e *emailProvider) HealthCheck(context.Context) error { return nil }

// redirectSchemeGuard references the library type so a future refactor that
// drops the dependency fails to compile here rather than silently.
var _ = oauthlib.ErrInvalidRequest

// callbackShim notes that the callback handlers are mounted by server.go,
// which delegates straight to the library's exported callback.
var _ = oauthhandler.OAuthRoutesOptions{}
