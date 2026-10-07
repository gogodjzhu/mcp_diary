package diarysync

import (
	"errors"
	"fmt"
	"net/http"
)

var (
	ErrNotFound               = errors.New("sync medium not found")
	ErrUnknownKind            = errors.New("unknown storage medium kind")
	ErrNoEncryptionKey        = errors.New("sync encryption key is required to store credentials")
	ErrInvalidKey             = errors.New("sync encryption key must be 32 bytes encoded as base64 or hex")
	ErrAttachmentNotSupported = errors.New("attachment sync is not implemented")
	ErrSecretKey              = errors.New("settings must not contain credentials")
	ErrInvalidCiphertext      = errors.New("invalid credential ciphertext")
	ErrMediumIDRequired       = errors.New("medium id is required")
	ErrMediumNameRequired     = errors.New("medium name is required")
	ErrMediumKindRequired     = errors.New("medium kind is required")
	ErrGitHubRepoRequired     = errors.New("GitHub 存储需要填写 owner 和 repo。请使用细粒度 PAT，并授予目标仓库 Contents: Read and write 权限")
	ErrGitHubCredential       = errors.New("新建 GitHub 存储时必须填写 Personal Access Token。请使用细粒度 PAT，并授予目标仓库 Contents: Read and write 权限")
	ErrGitHubSettings         = errors.New("GitHub 存储设置无效")
	ErrEngineStopped          = errors.New("sync engine stopped")
	ErrInvalidMonth           = errors.New("invalid diary month")
	ErrEngineNotConfigured    = errors.New("sync engine is not configured")
	ErrMediumDisabled         = errors.New("storage medium is disabled")
)

// FieldError is a validation failure for a specific medium setting.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string {
	if e == nil {
		return ""
	}
	if e.Field == "" {
		return e.Message
	}
	return e.Message
}

func (e *FieldError) Unwrap() error { return ErrGitHubSettings }

// HTTPStatusError carries an upstream HTTP status so retry logic can decide
// without inspecting error text.
type HTTPStatusError struct {
	Status  int
	Message string
	Hint    string
	Detail  string
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return ""
	}
	msg := e.Message
	if e.Detail != "" {
		if msg != "" {
			msg += "（" + e.Detail + "）"
		} else {
			msg = e.Detail
		}
	}
	return msg
}

func (e *HTTPStatusError) StatusCode() int {
	if e == nil || e.Status == 0 {
		return http.StatusBadGateway
	}
	return e.Status
}

// VerifyError is a failed connection probe. The medium is not saved.
type VerifyError struct {
	Message string
}

func (e *VerifyError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// StatusCode returns the upstream HTTP status when err (or an error it wraps)
// carries one. ok is false for errors that are not HTTP failures.
func StatusCode(err error) (int, bool) {
	var hs interface{ StatusCode() int }
	if errors.As(err, &hs) {
		code := hs.StatusCode()
		if code > 0 {
			return code, true
		}
	}
	return 0, false
}

// Hint returns a short repair suggestion when the error carries one.
func Hint(err error) string {
	var hs *HTTPStatusError
	if errors.As(err, &hs) {
		return hs.Hint
	}
	return ""
}

func httpStatusError(status int, message, hint, detail string) *HTTPStatusError {
	return &HTTPStatusError{Status: status, Message: message, Hint: hint, Detail: detail}
}

func fieldErrorf(field, format string, args ...any) error {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}
