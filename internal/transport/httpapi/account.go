package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/auth/identity"
	"github.com/gogodjzhu/mcp-diary/internal/auth/user"
	"github.com/gogodjzhu/mcp-diary/internal/auth/verify"
)

// accountView is what /api/me and the account page render.
type accountView struct {
	UserID        string         `json:"user_id"`
	Subject       string         `json:"subject"`
	Email         string         `json:"email,omitempty"`
	Username      string         `json:"username,omitempty"`
	DisplayName   string         `json:"display_name,omitempty"`
	PrimaryEmail  string         `json:"primary_email,omitempty"`
	EmailVerified bool           `json:"email_verified"`
	EmailRequired bool           `json:"email_required"`
	HasPassword   bool           `json:"has_password"`
	Scopes        []string       `json:"scopes"`
	Identities    []identityView `json:"identities"`
}

type identityView struct {
	Provider string `json:"provider"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	LinkedAt string `json:"linked_at"`
}

func (h *Handler) accountView(id user.ID) (accountView, error) {
	u, err := h.cfg.Users.Get(id)
	if err != nil {
		return accountView{}, err
	}
	_, hasPassword := h.cfg.Users.Credential(id)
	var idents []identityView
	for _, ident := range h.cfg.Users.Identities(id) {
		idents = append(idents, identityView{
			Provider: ident.Provider,
			Email:    ident.Email,
			Name:     ident.Name,
			LinkedAt: ident.LinkedAt.UTC().Format(time.RFC3339),
		})
	}
	if idents == nil {
		idents = []identityView{}
	}
	name := u.DisplayName
	if name == "" {
		name = u.PrimaryEmail
	}
	return accountView{
		UserID:        string(u.ID),
		Subject:       string(u.ID),
		Email:         u.PrimaryEmail,
		Username:      name,
		DisplayName:   u.DisplayName,
		PrimaryEmail:  u.PrimaryEmail,
		EmailVerified: u.EmailVerified,
		EmailRequired: u.EmailRequired,
		HasPassword:   hasPassword,
		Scopes:        []string{},
		Identities:    idents,
	}, nil
}

func (h *Handler) handleAccount(w http.ResponseWriter, r *http.Request, rest string) {
	if h.cfg.Users == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "accounts are not configured")
		return
	}
	id, ok := currentUser(w, r)
	if !ok {
		return
	}
	switch {
	case rest == "profile" && r.Method == http.MethodPatch:
		h.handleProfile(w, r, id)
	case rest == "email/code" && r.Method == http.MethodPost:
		h.handleEmailCode(w, r, id)
	case rest == "email" && r.Method == http.MethodPost:
		h.handleEmailBind(w, r, id)
	case rest == "password" && r.Method == http.MethodPut:
		h.handlePassword(w, r, id)
	case rest == "identities/link" && r.Method == http.MethodPost:
		h.handleLink(w, r, id)
	case strings.HasPrefix(rest, "identities/") && r.Method == http.MethodDelete:
		h.handleUnlink(w, r, id, strings.TrimPrefix(rest, "identities/"))
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown API endpoint")
	}
}

func (h *Handler) handleProfile(w http.ResponseWriter, r *http.Request, id user.ID) {
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON")
		return
	}
	name := strings.TrimSpace(body.DisplayName)
	if len(name) > 80 {
		writeError(w, http.StatusBadRequest, "invalid_request", "显示名过长")
		return
	}
	if _, err := h.cfg.Users.SetDisplayName(id, name, time.Now()); err != nil {
		writeAccountError(w, err)
		return
	}
	h.writeAccount(w, id)
}

func (h *Handler) handleEmailCode(w http.ResponseWriter, r *http.Request, id user.ID) {
	if h.cfg.Verifier == nil {
		writeError(w, http.StatusNotFound, "disabled", "email verification is disabled")
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON")
		return
	}
	email := user.NormalizeEmail(body.Email)
	if !user.ValidEmail(email) {
		writeError(w, http.StatusBadRequest, "invalid_email", "邮箱格式不正确")
		return
	}
	if owner, taken := h.cfg.Users.FindByEmail(email); taken && owner.ID != id {
		writeError(w, http.StatusConflict, "email_taken", "该邮箱已绑定到其他账号")
		return
	}
	purpose := user.PurposeBind
	if u, err := h.cfg.Users.Get(id); err == nil && u.EmailVerified && u.PrimaryEmail != "" {
		purpose = user.PurposeChange
	}
	if err := h.cfg.Verifier.Issue(email, purpose, r.RemoteAddr); err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *Handler) handleEmailBind(w http.ResponseWriter, r *http.Request, id user.ID) {
	if h.cfg.Verifier == nil {
		writeError(w, http.StatusNotFound, "disabled", "email verification is disabled")
		return
	}
	var body struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON")
		return
	}
	email := user.NormalizeEmail(body.Email)
	purpose := user.PurposeBind
	if u, err := h.cfg.Users.Get(id); err == nil && u.EmailVerified && u.PrimaryEmail != "" {
		purpose = user.PurposeChange
	}
	if err := h.cfg.Verifier.Redeem(email, purpose, body.Code); err != nil {
		writeAccountError(w, err)
		return
	}
	if _, err := h.cfg.Users.SetPrimaryEmail(id, email, time.Now()); err != nil {
		writeAccountError(w, err)
		return
	}
	h.writeAccount(w, id)
}

func (h *Handler) handlePassword(w http.ResponseWriter, r *http.Request, id user.ID) {
	var body struct {
		OldPassword string `json:"old_password"`
		Password    string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON")
		return
	}
	if err := h.cfg.Users.SetPassword(id, body.OldPassword, body.Password, time.Now()); err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *Handler) handleLink(w http.ResponseWriter, r *http.Request, id user.ID) {
	if h.cfg.Linker == nil {
		writeError(w, http.StatusNotFound, "disabled", "account linking is disabled")
		return
	}
	var body struct {
		Provider string `json:"provider"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON")
		return
	}
	url, err := h.cfg.Linker.StartLink(id, body.Provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "不支持的登录方式")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": url})
}

func (h *Handler) handleUnlink(w http.ResponseWriter, r *http.Request, id user.ID, provider string) {
	provider = strings.Trim(provider, "/")
	if provider == user.ProviderEmail {
		var body struct {
			Password string `json:"password"`
		}
		_ = decodeJSON(r, &body)
		cred, ok := h.cfg.Users.Credential(id)
		if ok && !user.VerifyPassword(cred.PasswordHash, body.Password) {
			writeError(w, http.StatusForbidden, "invalid_password", "密码不正确")
			return
		}
	}
	if err := h.cfg.Users.UnlinkIdentity(id, provider, time.Now()); err != nil {
		writeAccountError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if h.cfg.Users == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "accounts are not configured")
		return
	}
	id, ok := currentUser(w, r)
	if !ok {
		return
	}
	var body struct {
		ConfirmEmail string `json:"confirm_email"`
		Password     string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON")
		return
	}
	u, err := h.cfg.Users.Get(id)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	if user.NormalizeEmail(body.ConfirmEmail) != u.PrimaryEmail {
		writeError(w, http.StatusBadRequest, "invalid_request", "请输入主邮箱以确认删除")
		return
	}
	if cred, has := h.cfg.Users.Credential(id); has && !user.VerifyPassword(cred.PasswordHash, body.Password) {
		writeError(w, http.StatusForbidden, "invalid_password", "密码不正确")
		return
	}
	dir := ""
	if h.cfg.Workspaces != nil {
		dir = h.cfg.Workspaces.UserDir(string(id))
	}
	if err := h.cfg.Users.Delete(id, dir); err != nil {
		writeAccountError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeAccount(w http.ResponseWriter, id user.ID) {
	view, err := h.accountView(id)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func currentUser(w http.ResponseWriter, r *http.Request) (user.ID, bool) {
	ident, ok := identity.IdentityFrom(r.Context())
	if !ok || !user.ID(ident.UserID).Valid() {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing authenticated identity")
		return "", false
	}
	return user.ID(ident.UserID), true
}

func writeAccountError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, user.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "用户不存在")
	case errors.Is(err, user.ErrEmailTaken):
		writeError(w, http.StatusConflict, "email_taken", "该邮箱已绑定到其他账号")
	case errors.Is(err, user.ErrIdentityTaken):
		writeError(w, http.StatusConflict, "identity_taken", "该账号已绑定到其他用户")
	case errors.Is(err, user.ErrLastLoginMethod):
		writeError(w, http.StatusConflict, "last_login_method", "不能解绑最后一个登录方式")
	case errors.Is(err, user.ErrInvalidPassword):
		writeError(w, http.StatusForbidden, "invalid_password", "密码不正确")
	case errors.Is(err, user.ErrPasswordTooShort), errors.Is(err, user.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, "weak_password", "密码至少 8 位，且必须同时包含字母和数字")
	case errors.Is(err, verify.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, "rate_limited", "请求过于频繁，请稍后再试")
	case errors.Is(err, verify.ErrQuotaReached):
		writeError(w, http.StatusServiceUnavailable, "quota_reached", "今日验证码额度已用完")
	case errors.Is(err, verify.ErrSendFailed):
		writeError(w, http.StatusBadGateway, "email_send_failed", "验证码发送失败")
	case errors.Is(err, user.ErrInvalidCode), errors.Is(err, user.ErrNoPendingCode):
		writeError(w, http.StatusBadRequest, "invalid_code", "验证码不正确")
	case errors.Is(err, user.ErrCodeExpired):
		writeError(w, http.StatusBadRequest, "code_expired", "验证码已过期")
	case errors.Is(err, user.ErrTooManyAttempts):
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "验证码尝试次数过多")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "内部错误")
	}
}
