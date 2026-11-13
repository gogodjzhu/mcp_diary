// Package user is the user store behind every login path. It owns the user
// records, the provider identities bound to them, the email credentials and the
// pending email-verification codes. The authorization server (internal/auth/oauth)
// and the web access layer read it; it depends on neither, so the auth layering
// rule holds.
package user

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/argon2"
)

// ID is a stable, path-safe user identifier. It carries no personal data, so it
// can be a directory name and a token subject without leaking anything.
type ID string

// IDPrefix marks an identifier issued by this store. Tokens whose subject does
// not start with it were issued before the user store existed and are rejected.
const IDPrefix = "usr_"

// Provider names stored on identities.
const (
	ProviderGoogle = "google"
	ProviderGitee  = "gitee"
	ProviderEmail  = "email"
)

// Purposes of an email verification code.
const (
	PurposeRegister = "register"
	PurposeReset    = "reset"
	PurposeBind     = "bind"
	PurposeChange   = "change"
)

// Password hashing parameters. They are encoded into the hash string, so a
// stored hash stays verifiable even if these change later.
const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// Verification-code rules.
const (
	codeDigits     = 6
	codeTTL        = 10 * time.Minute
	maxCodeAttempt = 5
)

// Sentinel errors. Callers map them onto HTTP status codes.
var (
	ErrNotFound         = errors.New("user not found")
	ErrEmailTaken       = errors.New("email already registered")
	ErrIdentityTaken    = errors.New("identity already linked to another user")
	ErrInvalidCode      = errors.New("invalid verification code")
	ErrCodeExpired      = errors.New("verification code expired")
	ErrTooManyAttempts  = errors.New("too many verification attempts")
	ErrNoPendingCode    = errors.New("no pending verification code")
	ErrInvalidPassword  = errors.New("invalid password")
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
	ErrWeakPassword     = errors.New("password must contain letters and digits")
	ErrLastLoginMethod  = errors.New("cannot remove the last login method")
	ErrNoCredential     = errors.New("no password set")
	ErrInvalidEmail     = errors.New("invalid email address")
)

// User is one account.
type User struct {
	ID            ID        `json:"id"`
	DisplayName   string    `json:"display_name,omitempty"`
	PrimaryEmail  string    `json:"primary_email,omitempty"`
	EmailVerified bool      `json:"email_verified"`
	EmailRequired bool      `json:"email_required,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	LastLoginAt   time.Time `json:"last_login_at"`
}

// Identity is one login method bound to a user.
type Identity struct {
	Provider      string    `json:"provider"`
	Subject       string    `json:"subject"`
	UserID        ID        `json:"user_id"`
	Email         string    `json:"email,omitempty"`
	EmailVerified bool      `json:"email_verified"`
	Name          string    `json:"name,omitempty"`
	AvatarURL     string    `json:"avatar_url,omitempty"`
	LinkedAt      time.Time `json:"linked_at"`
	LastLoginAt   time.Time `json:"last_login_at"`
}

// Credential is the email-and-password login method. At most one per user.
type Credential struct {
	UserID       ID        `json:"user_id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"password_hash"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// PendingCode is a verification code waiting to be redeemed. Only the HMAC of
// the code is stored.
type PendingCode struct {
	Purpose   string    `json:"purpose"`
	Email     string    `json:"email"`
	CodeHash  string    `json:"code_hash"`
	ExpiresAt time.Time `json:"expires_at"`
	Attempts  int       `json:"attempts"`
	CreatedAt time.Time `json:"created_at"`
}

// NewID returns a fresh user id.
func NewID() (ID, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate user id: %w", err)
	}
	return ID(IDPrefix + hex.EncodeToString(buf)), nil
}

// Valid reports whether id has the shape this store issues.
func (id ID) Valid() bool {
	if !strings.HasPrefix(string(id), IDPrefix) {
		return false
	}
	rest := strings.TrimPrefix(string(id), IDPrefix)
	if len(rest) != 32 {
		return false
	}
	_, err := hex.DecodeString(rest)
	return err == nil
}

// NormalizeEmail trims and lowercases an address. The result is what every
// index and comparison uses.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidEmail reports whether email is plausible enough to send mail to. It is
// deliberately strict about shape and loose about the domain: the verification
// code is what proves ownership.
func ValidEmail(email string) bool {
	email = NormalizeEmail(email)
	if len(email) < 3 || len(email) > 254 || strings.ContainsAny(email, " \t\r\n") {
		return false
	}
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" || strings.Contains(domain, "@") {
		return false
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || !strings.Contains(domain, ".") {
		return false
	}
	return true
}

// HashPassword returns an argon2id hash encoding its own parameters.
func HashPassword(password string) (string, error) {
	if err := CheckPasswordStrength(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	sum := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		hex.EncodeToString(salt), hex.EncodeToString(sum)), nil
}

// VerifyPassword reports whether password matches an encoded hash. It runs the
// derivation even when the hash is malformed, so the two outcomes take the
// same time.
func VerifyPassword(encoded, password string) bool {
	salt, want, params, ok := parseArgonHash(encoded)
	dummy := make([]byte, argonSaltLen)
	if !ok {
		_, _ = rand.Read(dummy)
		salt, want, params = dummy, make([]byte, argonKeyLen), defaultArgonParams()
	}
	got := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1 && ok
}

// CheckPasswordStrength rejects passwords that are too short or single-class.
func CheckPasswordStrength(password string) error {
	if len(password) < 8 {
		return ErrPasswordTooShort
	}
	var letter, digit bool
	for _, r := range password {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	if !letter || !digit {
		return ErrWeakPassword
	}
	return nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func defaultArgonParams() argonParams {
	return argonParams{memory: argonMemory, time: argonTime, threads: argonThreads}
}

func parseArgonHash(encoded string) (salt, sum []byte, params argonParams, ok bool) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, argonParams{}, false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memory, &params.time, &params.threads); err != nil {
		return nil, nil, argonParams{}, false
	}
	if params.memory == 0 || params.time == 0 || params.threads == 0 {
		return nil, nil, argonParams{}, false
	}
	salt, err := hex.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return nil, nil, argonParams{}, false
	}
	sum, err = hex.DecodeString(parts[5])
	if err != nil || len(sum) == 0 {
		return nil, nil, argonParams{}, false
	}
	return salt, sum, params, true
}

// NewCode returns a numeric verification code of codeDigits.
func NewCode() (string, error) {
	buf := make([]byte, codeDigits)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}
	var b strings.Builder
	for _, byt := range buf {
		b.WriteByte('0' + byt%10)
	}
	return b.String(), nil
}

// HashCode returns the HMAC of a code bound to its purpose and address, so a
// leaked store file does not reveal usable codes.
func HashCode(secret []byte, purpose, email, code string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "%s:%s:%s", purpose, NormalizeEmail(email), code)
	return hex.EncodeToString(mac.Sum(nil))
}

// CodeMatches reports whether code matches a stored hash, in constant time.
func CodeMatches(secret []byte, purpose, email, code, stored string) bool {
	want := HashCode(secret, purpose, email, code)
	return subtle.ConstantTimeCompare([]byte(want), []byte(stored)) == 1
}

// MaskEmail hides the local part of an address for logs.
func MaskEmail(email string) string {
	email = NormalizeEmail(email)
	local, domain, ok := strings.Cut(email, "@")
	if !ok {
		return "***"
	}
	if len(local) <= 1 {
		return "*@" + domain
	}
	return local[:1] + "***@" + domain
}
