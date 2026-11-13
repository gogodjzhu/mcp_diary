package user

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// fileVersion is the schema version written into the store file.
const fileVersion = 1

// Store is the on-disk user database. All mutations take the same lock and are
// written with an atomic rename, so a crash never leaves a partial file.
type Store struct {
	path   string
	secret []byte

	mu   sync.Mutex
	data storeData
}

type storeData struct {
	Version     int                     `json:"version"`
	Users       map[ID]*User            `json:"users"`
	Identities  map[string]*Identity    `json:"identities"`
	EmailIndex  map[string]ID           `json:"email_index"`
	Credentials map[ID]*Credential      `json:"credentials"`
	Pending     map[string]*PendingCode `json:"pending"`
	DailySent   map[string]int          `json:"daily_sent"`
}

// Open loads the store at path, creating it if it does not exist. secret keys
// the verification-code HMACs; it must stay stable for codes to stay
// redeemable across restarts.
func Open(path string, secret []byte) (*Store, error) {
	if path == "" {
		return nil, errors.New("user store path is required")
	}
	if len(secret) == 0 {
		return nil, errors.New("user store secret is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create user store directory: %w", err)
	}

	s := &Store{path: path, secret: append([]byte(nil), secret...), data: newStoreData()}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := s.persist(); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read user store: %w", err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, fmt.Errorf("parse user store: %w", err)
		}
	}
	s.data.normalize()
	return s, nil
}

func newStoreData() storeData {
	return storeData{
		Version:     fileVersion,
		Users:       map[ID]*User{},
		Identities:  map[string]*Identity{},
		EmailIndex:  map[string]ID{},
		Credentials: map[ID]*Credential{},
		Pending:     map[string]*PendingCode{},
		DailySent:   map[string]int{},
	}
}

func (d *storeData) normalize() {
	if d.Users == nil {
		d.Users = map[ID]*User{}
	}
	if d.Identities == nil {
		d.Identities = map[string]*Identity{}
	}
	if d.EmailIndex == nil {
		d.EmailIndex = map[string]ID{}
	}
	if d.Credentials == nil {
		d.Credentials = map[ID]*Credential{}
	}
	if d.Pending == nil {
		d.Pending = map[string]*PendingCode{}
	}
	if d.DailySent == nil {
		d.DailySent = map[string]int{}
	}
}

// Path returns the store file path.
func (s *Store) Path() string { return s.path }

// Get returns a copy of the user.
func (s *Store) Get(id ID) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.data.Users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return *u, nil
}

// FindByEmail returns the user owning the address, if any.
func (s *Store) FindByEmail(email string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.data.EmailIndex[NormalizeEmail(email)]
	if !ok {
		return User{}, false
	}
	u, ok := s.data.Users[id]
	if !ok {
		return User{}, false
	}
	return *u, true
}

// FindByIdentity returns the user bound to a provider subject, if any.
func (s *Store) FindByIdentity(provider, subject string) (User, Identity, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ident, ok := s.data.Identities[identityKey(provider, subject)]
	if !ok {
		return User{}, Identity{}, false
	}
	u, ok := s.data.Users[ident.UserID]
	if !ok {
		return User{}, Identity{}, false
	}
	return *u, *ident, true
}

// Identity is one identity of a user.
func (s *Store) Identity(id ID, provider string) (Identity, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ident := range s.data.Identities {
		if ident.UserID == id && ident.Provider == provider {
			return *ident, true
		}
	}
	return Identity{}, false
}

// Identities lists every login method bound to a user.
func (s *Store) Identities(id ID) []Identity {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Identity
	for _, ident := range s.data.Identities {
		if ident.UserID == id {
			out = append(out, *ident)
		}
	}
	return out
}

// Credential returns the email credential of a user, if one is set.
func (s *Store) Credential(id ID) (Credential, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Credentials[id]
	if !ok {
		return Credential{}, false
	}
	return *c, true
}

// LoginMethodCount counts the ways a user can still sign in: each bound
// identity, plus the password credential when the email identity is absent.
func (s *Store) LoginMethodCount(id ID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loginMethodCount(id)
}

func (s *Store) loginMethodCount(id ID) int {
	n := 0
	hasEmail := false
	for _, ident := range s.data.Identities {
		if ident.UserID == id {
			n++
			if ident.Provider == ProviderEmail {
				hasEmail = true
			}
		}
	}
	if _, ok := s.data.Credentials[id]; ok && !hasEmail {
		n++
	}
	return n
}

// CreateOAuthUser records a first login through an external provider. The
// account is created unverified: callers gate business endpoints until the
// address is confirmed.
func (s *Store) CreateOAuthUser(provider, subject string, profile Profile, now time.Time) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.data.Identities[identityKey(provider, subject)]; ok {
		return User{}, ErrIdentityTaken
	}
	id, err := NewID()
	if err != nil {
		return User{}, err
	}
	email := NormalizeEmail(profile.Email)
	if email != "" {
		if owner, ok := s.data.EmailIndex[email]; ok && owner != id {
			return User{}, ErrEmailTaken
		}
	}

	u := &User{
		ID:            id,
		DisplayName:   profile.Name,
		PrimaryEmail:  email,
		EmailVerified: false,
		EmailRequired: true,
		CreatedAt:     now,
		UpdatedAt:     now,
		LastLoginAt:   now,
	}
	s.data.Users[id] = u
	s.data.Identities[identityKey(provider, subject)] = &Identity{
		Provider:      provider,
		Subject:       subject,
		UserID:        id,
		Email:         email,
		EmailVerified: profile.EmailVerified,
		Name:          profile.Name,
		AvatarURL:     profile.AvatarURL,
		LinkedAt:      now,
		LastLoginAt:   now,
	}
	if email != "" {
		s.data.EmailIndex[email] = id
	}
	if err := s.persist(); err != nil {
		return User{}, err
	}
	return *u, nil
}

// Profile is what an external provider reports about a user.
type Profile struct {
	Email         string
	EmailVerified bool
	Name          string
	AvatarURL     string
}

// TouchLogin records a successful login through an existing identity.
func (s *Store) TouchLogin(provider, subject string, now time.Time) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ident, ok := s.data.Identities[identityKey(provider, subject)]
	if !ok {
		return User{}, ErrNotFound
	}
	u, ok := s.data.Users[ident.UserID]
	if !ok {
		return User{}, ErrNotFound
	}
	ident.LastLoginAt = now
	u.LastLoginAt = now
	u.UpdatedAt = now
	if err := s.persist(); err != nil {
		return User{}, err
	}
	return *u, nil
}

// CreateEmailUser registers an account from a verified address and password.
// The caller has already checked the verification code.
func (s *Store) CreateEmailUser(email, password, displayName string, now time.Time) (User, error) {
	email = NormalizeEmail(email)
	if !ValidEmail(email) {
		return User{}, ErrInvalidEmail
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.data.EmailIndex[email]; ok {
		return User{}, ErrEmailTaken
	}
	id, err := NewID()
	if err != nil {
		return User{}, err
	}
	u := &User{
		ID:            id,
		DisplayName:   displayName,
		PrimaryEmail:  email,
		EmailVerified: true,
		EmailRequired: false,
		CreatedAt:     now,
		UpdatedAt:     now,
		LastLoginAt:   now,
	}
	s.data.Users[id] = u
	s.data.EmailIndex[email] = id
	s.data.Credentials[id] = &Credential{UserID: id, Email: email, PasswordHash: hash, UpdatedAt: now}
	s.data.Identities[identityKey(ProviderEmail, email)] = &Identity{
		Provider:      ProviderEmail,
		Subject:       email,
		UserID:        id,
		Email:         email,
		EmailVerified: true,
		LinkedAt:      now,
		LastLoginAt:   now,
	}
	delete(s.data.Pending, pendingKey(PurposeRegister, email))
	if err := s.persist(); err != nil {
		return User{}, err
	}
	return *u, nil
}

// AuthenticateEmail checks an email-and-password pair. Both failure modes
// return ErrInvalidPassword so callers cannot tell them apart.
func (s *Store) AuthenticateEmail(email, password string, now time.Time) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.data.EmailIndex[NormalizeEmail(email)]
	if !ok {
		// Spend the same derivation time as a real check.
		_ = VerifyPassword("", password)
		return User{}, ErrInvalidPassword
	}
	cred, ok := s.data.Credentials[id]
	if !ok || !VerifyPassword(cred.PasswordHash, password) {
		return User{}, ErrInvalidPassword
	}
	u := s.data.Users[id]
	u.LastLoginAt = now
	u.UpdatedAt = now
	if ident, ok := s.data.Identities[identityKey(ProviderEmail, cred.Email)]; ok {
		ident.LastLoginAt = now
	}
	if err := s.persist(); err != nil {
		return User{}, err
	}
	return *u, nil
}

// SetDisplayName renames a user.
func (s *Store) SetDisplayName(id ID, name string, now time.Time) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.data.Users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	u.DisplayName = name
	u.UpdatedAt = now
	if err := s.persist(); err != nil {
		return User{}, err
	}
	return *u, nil
}

// SetPrimaryEmail records a verified address as the user's primary email.
func (s *Store) SetPrimaryEmail(id ID, email string, now time.Time) (User, error) {
	email = NormalizeEmail(email)
	if !ValidEmail(email) {
		return User{}, ErrInvalidEmail
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.data.Users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	if owner, taken := s.data.EmailIndex[email]; taken && owner != id {
		return User{}, ErrEmailTaken
	}
	if prev := u.PrimaryEmail; prev != "" && prev != email {
		if s.data.EmailIndex[prev] == id {
			delete(s.data.EmailIndex, prev)
		}
	}
	u.PrimaryEmail = email
	u.EmailVerified = true
	u.EmailRequired = false
	u.UpdatedAt = now
	s.data.EmailIndex[email] = id
	if err := s.persist(); err != nil {
		return User{}, err
	}
	return *u, nil
}

// SetPassword sets or replaces the password. When oldPassword is non-empty it
// must match the current one.
func (s *Store) SetPassword(id ID, oldPassword, newPassword string, now time.Time) error {
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.data.Users[id]
	if !ok {
		return ErrNotFound
	}
	if cred, exists := s.data.Credentials[id]; exists {
		if oldPassword == "" || !VerifyPassword(cred.PasswordHash, oldPassword) {
			return ErrInvalidPassword
		}
	}
	email := u.PrimaryEmail
	s.data.Credentials[id] = &Credential{UserID: id, Email: email, PasswordHash: hash, UpdatedAt: now}
	if email != "" {
		if _, bound := s.data.Identities[identityKey(ProviderEmail, email)]; !bound {
			s.data.Identities[identityKey(ProviderEmail, email)] = &Identity{
				Provider:      ProviderEmail,
				Subject:       email,
				UserID:        id,
				Email:         email,
				EmailVerified: u.EmailVerified,
				LinkedAt:      now,
			}
		}
	}
	u.UpdatedAt = now
	return s.persist()
}

// ResetPassword replaces the password without the old one. The caller has
// already checked the reset code.
func (s *Store) ResetPassword(email, newPassword string, now time.Time) (User, error) {
	hash, err := HashPassword(newPassword)
	if err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.data.EmailIndex[NormalizeEmail(email)]
	if !ok {
		return User{}, ErrNotFound
	}
	u := s.data.Users[id]
	s.data.Credentials[id] = &Credential{UserID: id, Email: u.PrimaryEmail, PasswordHash: hash, UpdatedAt: now}
	delete(s.data.Pending, pendingKey(PurposeReset, email))
	u.UpdatedAt = now
	if err := s.persist(); err != nil {
		return User{}, err
	}
	return *u, nil
}

// LinkIdentity binds an external identity to an existing user.
func (s *Store) LinkIdentity(id ID, provider, subject string, profile Profile, now time.Time) (Identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.data.Users[id]; !ok {
		return Identity{}, ErrNotFound
	}
	key := identityKey(provider, subject)
	if existing, ok := s.data.Identities[key]; ok && existing.UserID != id {
		return Identity{}, ErrIdentityTaken
	}
	ident := &Identity{
		Provider:      provider,
		Subject:       subject,
		UserID:        id,
		Email:         NormalizeEmail(profile.Email),
		EmailVerified: profile.EmailVerified,
		Name:          profile.Name,
		AvatarURL:     profile.AvatarURL,
		LinkedAt:      now,
		LastLoginAt:   now,
	}
	s.data.Identities[key] = ident
	s.data.Users[id].UpdatedAt = now
	if err := s.persist(); err != nil {
		return Identity{}, err
	}
	return *ident, nil
}

// UnlinkIdentity removes a login method. It refuses to remove the last one.
func (s *Store) UnlinkIdentity(id ID, provider string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.data.Users[id]; !ok {
		return ErrNotFound
	}
	var key string
	for k, ident := range s.data.Identities {
		if ident.UserID == id && ident.Provider == provider {
			key = k
			break
		}
	}
	if key == "" {
		return ErrNotFound
	}
	if s.loginMethodCount(id) < 2 {
		return ErrLastLoginMethod
	}
	delete(s.data.Identities, key)
	if provider == ProviderEmail {
		delete(s.data.Credentials, id)
	}
	s.data.Users[id].UpdatedAt = now
	return s.persist()
}

// Delete removes a user and everything bound to them. workspaceDir, when
// non-empty, is removed as well.
func (s *Store) Delete(id ID, workspaceDir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.data.Users[id]
	if !ok {
		return ErrNotFound
	}
	for key, ident := range s.data.Identities {
		if ident.UserID == id {
			delete(s.data.Identities, key)
		}
	}
	if u.PrimaryEmail != "" && s.data.EmailIndex[u.PrimaryEmail] == id {
		delete(s.data.EmailIndex, u.PrimaryEmail)
	}
	for email, owner := range s.data.EmailIndex {
		if owner == id {
			delete(s.data.EmailIndex, email)
		}
	}
	delete(s.data.Credentials, id)
	delete(s.data.Users, id)
	if err := s.persist(); err != nil {
		return err
	}
	if workspaceDir != "" {
		if err := os.RemoveAll(workspaceDir); err != nil {
			return fmt.Errorf("remove workspace: %w", err)
		}
	}
	return nil
}

// PutPendingCode stores a freshly issued code, replacing any previous code for
// the same purpose and address.
func (s *Store) PutPendingCode(purpose, email, code string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Pending[pendingKey(purpose, email)] = &PendingCode{
		Purpose:   purpose,
		Email:     NormalizeEmail(email),
		CodeHash:  HashCode(s.secret, purpose, email, code),
		ExpiresAt: now.Add(codeTTL),
		CreatedAt: now,
	}
	return s.persist()
}

// RedeemCode checks a code and consumes it. A wrong code counts as an attempt;
// too many attempts invalidate it.
func (s *Store) RedeemCode(purpose, email, code string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := pendingKey(purpose, email)
	pending, ok := s.data.Pending[key]
	if !ok {
		return ErrNoPendingCode
	}
	if now.After(pending.ExpiresAt) {
		delete(s.data.Pending, key)
		_ = s.persist()
		return ErrCodeExpired
	}
	if pending.Attempts >= maxCodeAttempt {
		delete(s.data.Pending, key)
		_ = s.persist()
		return ErrTooManyAttempts
	}
	if !CodeMatches(s.secret, purpose, email, code, pending.CodeHash) {
		pending.Attempts++
		if pending.Attempts >= maxCodeAttempt {
			delete(s.data.Pending, key)
		}
		_ = s.persist()
		return ErrInvalidCode
	}
	delete(s.data.Pending, key)
	return s.persist()
}

// EmailRegistered reports whether an address already belongs to a user.
func (s *Store) EmailRegistered(email string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data.EmailIndex[NormalizeEmail(email)]
	return ok
}

// AddDailySent increments the send counter for the UTC day of now and reports
// the new total.
func (s *Store) AddDailySent(now time.Time, n int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	day := now.UTC().Format("2006-01-02")
	s.data.DailySent[day] += n
	total := s.data.DailySent[day]
	if err := s.persist(); err != nil {
		return 0, err
	}
	return total, nil
}

// DailySent returns the send counter for the UTC day of now.
func (s *Store) DailySent(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.DailySent[now.UTC().Format("2006-01-02")]
}

// Sweep removes expired codes, stale daily counters and abandoned unverified
// accounts. It returns the ids of the removed accounts so the caller can drop
// their workspace directories.
func (s *Store) Sweep(now time.Time, abandonAfter time.Duration) ([]ID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, pending := range s.data.Pending {
		if now.After(pending.ExpiresAt) {
			delete(s.data.Pending, key)
		}
	}
	cutoff := now.UTC().AddDate(0, 0, -14).Format("2006-01-02")
	for day := range s.data.DailySent {
		if day < cutoff {
			delete(s.data.DailySent, day)
		}
	}

	var removed []ID
	deadline := now.Add(-abandonAfter)
	for id, u := range s.data.Users {
		if !u.EmailRequired || u.LastLoginAt.After(deadline) {
			continue
		}
		for key, ident := range s.data.Identities {
			if ident.UserID == id {
				delete(s.data.Identities, key)
			}
		}
		if u.PrimaryEmail != "" {
			delete(s.data.EmailIndex, u.PrimaryEmail)
		}
		delete(s.data.Credentials, id)
		delete(s.data.Users, id)
		removed = append(removed, id)
	}
	if err := s.persist(); err != nil {
		return nil, err
	}
	return removed, nil
}

func (s *Store) persist() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode user store: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".users-*.tmp")
	if err != nil {
		return fmt.Errorf("create user store temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod user store temp file: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write user store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close user store: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace user store: %w", err)
	}
	cleanup = false
	return nil
}

func identityKey(provider, subject string) string {
	return provider + ":" + subject
}

func pendingKey(purpose, email string) string {
	return purpose + ":" + NormalizeEmail(email)
}
