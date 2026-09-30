package oauthserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"

	"github.com/giantswarm/mcp-oauth/security"
	"github.com/giantswarm/mcp-oauth/storage"
)

// FileStore is a small file-backed implementation of storage.Combined
// (TokenStore + ClientStore + FlowStore) plus token metadata. It keeps
// everything in memory and persists a JSON snapshot to disk after every
// mutation, so a single-process deployment survives restarts without an
// external database.
//
// Token metadata is required: the authorization server reads the refresh
// token's client binding from it (OAuth 2.1 §6) and rejects a refresh that
// has none. Refresh-token families and the unified provider-token layout are
// intentionally not implemented.
type FileStore struct {
	mu        sync.Mutex
	path      string
	encryptor *security.Encryptor
	data      storeData
}

// refreshRecord maps an issued refresh token to its owner and expiry.
type refreshRecord struct {
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// storedToken is the on-disk form of an oauth2.Token. The token's extra map
// (id_token, scope) is unexported and dropped by encoding/json, so it is kept
// beside the token. A refresh grant reads id_token back out of it.
type storedToken struct {
	Token *oauth2.Token  `json:"token"`
	Extra map[string]any `json:"extra,omitempty"`
}

type storeData struct {
	Clients        map[string]*storage.Client             `json:"clients,omitempty"`
	Tokens         map[string]*storedToken                `json:"tokens,omitempty"`
	UserInfo       map[string]*storage.UserInfo           `json:"user_info,omitempty"`
	Refresh        map[string]refreshRecord               `json:"refresh,omitempty"`
	ClientIPCounts map[string]int                         `json:"client_ip_counts,omitempty"`
	AuthStates     map[string]*storage.AuthorizationState `json:"auth_states,omitempty"`
	AuthCodes      map[string]*storage.AuthorizationCode  `json:"auth_codes,omitempty"`
	TokenMetadata  map[string]*storage.TokenMetadata      `json:"token_metadata,omitempty"`
}

var (
	_ storage.Combined            = (*FileStore)(nil)
	_ storage.TokenMetadataStore  = (*FileStore)(nil)
	_ storage.TokenMetadataGetter = (*FileStore)(nil)
)

// NewFileStore opens (or creates) the store at path. A nil encryptor stores
// token material in plain text, which is only acceptable for local
// development.
func NewFileStore(path string, encryptor *security.Encryptor) (*FileStore, error) {
	s := &FileStore{
		path:      path,
		encryptor: encryptor,
		data:      newStoreData(),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func newStoreData() storeData {
	return storeData{
		Clients:        map[string]*storage.Client{},
		Tokens:         map[string]*storedToken{},
		UserInfo:       map[string]*storage.UserInfo{},
		Refresh:        map[string]refreshRecord{},
		ClientIPCounts: map[string]int{},
		AuthStates:     map[string]*storage.AuthorizationState{},
		AuthCodes:      map[string]*storage.AuthorizationCode{},
		TokenMetadata:  map[string]*storage.TokenMetadata{},
	}
}

func (s *FileStore) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read oauth store: %w", err)
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return fmt.Errorf("parse oauth store: %w", err)
	}
	if s.data.Clients == nil {
		s.data.Clients = map[string]*storage.Client{}
	}
	if s.data.Tokens == nil {
		s.data.Tokens = map[string]*storedToken{}
	}
	if s.data.UserInfo == nil {
		s.data.UserInfo = map[string]*storage.UserInfo{}
	}
	if s.data.Refresh == nil {
		s.data.Refresh = map[string]refreshRecord{}
	}
	if s.data.ClientIPCounts == nil {
		s.data.ClientIPCounts = map[string]int{}
	}
	if s.data.AuthStates == nil {
		s.data.AuthStates = map[string]*storage.AuthorizationState{}
	}
	if s.data.AuthCodes == nil {
		s.data.AuthCodes = map[string]*storage.AuthorizationCode{}
	}
	if s.data.TokenMetadata == nil {
		s.data.TokenMetadata = map[string]*storage.TokenMetadata{}
	}
	return nil
}

// persist writes the snapshot atomically (temp file + rename). The caller
// holds s.mu.
func (s *FileStore) persist() error {
	raw, err := json.Marshal(&s.data)
	if err != nil {
		return fmt.Errorf("encode oauth store: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create oauth store dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".oauth-store-*")
	if err != nil {
		return fmt.Errorf("create oauth store temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write oauth store: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod oauth store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close oauth store: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace oauth store: %w", err)
	}
	return nil
}

// pack captures the token and the extra fields encoding/json would drop.
// Sensitive fields are encrypted; a failure is returned rather than stored in
// the clear.
func (s *FileStore) pack(token *oauth2.Token) (*storedToken, error) {
	if token == nil {
		return nil, fmt.Errorf("token cannot be nil")
	}
	extra := storage.ExtractTokenExtra(token)
	sealed := *token
	if s.encryptor != nil && s.encryptor.IsEnabled() {
		enc, err := encryptField(s.encryptor, sealed.AccessToken)
		if err != nil {
			return nil, fmt.Errorf("encrypt access token: %w", err)
		}
		sealed.AccessToken = enc
		enc, err = encryptField(s.encryptor, sealed.RefreshToken)
		if err != nil {
			return nil, fmt.Errorf("encrypt refresh token: %w", err)
		}
		sealed.RefreshToken = enc
		extra, err = storage.EncryptExtraFields(extra, s.encryptor)
		if err != nil {
			return nil, err
		}
	}
	return &storedToken{Token: &sealed, Extra: extra}, nil
}

// unpack reverses pack.
func (s *FileStore) unpack(stored *storedToken) (*oauth2.Token, error) {
	if stored == nil || stored.Token == nil {
		return nil, fmt.Errorf("%w: empty token", storage.ErrTokenUndecryptable)
	}
	opened := *stored.Token
	extra := stored.Extra
	if s.encryptor != nil && s.encryptor.IsEnabled() {
		dec, err := decryptField(s.encryptor, opened.AccessToken)
		if err != nil {
			return nil, fmt.Errorf("%w: access token", storage.ErrTokenUndecryptable)
		}
		opened.AccessToken = dec
		dec, err = decryptField(s.encryptor, opened.RefreshToken)
		if err != nil {
			return nil, fmt.Errorf("%w: refresh token", storage.ErrTokenUndecryptable)
		}
		opened.RefreshToken = dec
		extra, err = storage.DecryptExtraFields(extra, s.encryptor)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", storage.ErrTokenUndecryptable, err)
		}
	}
	if len(extra) > 0 {
		return opened.WithExtra(extra), nil
	}
	return &opened, nil
}

func encryptField(enc *security.Encryptor, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return enc.Encrypt(value)
}

func decryptField(enc *security.Encryptor, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return enc.Decrypt(value)
}

// ---------------------------------------------------------------------------
// TokenStore
// ---------------------------------------------------------------------------

func (s *FileStore) SaveToken(_ context.Context, userID string, token *oauth2.Token) error {
	if userID == "" {
		return fmt.Errorf("userID cannot be empty")
	}
	if token == nil {
		return fmt.Errorf("token cannot be nil")
	}
	packed, err := s.pack(token)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Tokens[userID] = packed
	return s.persist()
}

func (s *FileStore) GetToken(_ context.Context, userID string) (*oauth2.Token, error) {
	s.mu.Lock()
	stored, ok := s.data.Tokens[userID]
	s.mu.Unlock()
	if !ok || stored == nil || stored.Token == nil {
		return nil, fmt.Errorf("%w: %s", storage.ErrTokenNotFound, userID)
	}
	if security.IsTokenExpired(stored.Token.Expiry) && stored.Token.RefreshToken == "" {
		return nil, fmt.Errorf("%w: %s", storage.ErrTokenExpired, userID)
	}
	return s.unpack(stored)
}

func (s *FileStore) DeleteToken(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Tokens, userID)
	return s.persist()
}

func (s *FileStore) SaveUserInfo(_ context.Context, userID string, info *storage.UserInfo) error {
	if userID == "" {
		return fmt.Errorf("userID cannot be empty")
	}
	if info == nil {
		return fmt.Errorf("userInfo cannot be nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.UserInfo[userID] = info
	return s.persist()
}

func (s *FileStore) GetUserInfo(_ context.Context, userID string) (*storage.UserInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.data.UserInfo[userID]
	if !ok {
		return nil, storage.ErrUserInfoNotFound
	}
	return info, nil
}

func (s *FileStore) SaveRefreshToken(_ context.Context, refreshToken, userID string, expiresAt time.Time) error {
	if refreshToken == "" {
		return fmt.Errorf("refresh token cannot be empty")
	}
	if userID == "" {
		return fmt.Errorf("userID cannot be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Refresh[refreshToken] = refreshRecord{UserID: userID, ExpiresAt: expiresAt}
	return s.persist()
}

func (s *FileStore) GetRefreshTokenInfo(_ context.Context, refreshToken string) (string, error) {
	s.mu.Lock()
	rec, ok := s.data.Refresh[refreshToken]
	s.mu.Unlock()
	if !ok {
		return "", storage.ErrTokenNotFound
	}
	if !rec.ExpiresAt.IsZero() && security.IsTokenExpired(rec.ExpiresAt) {
		return "", storage.ErrTokenExpired
	}
	return rec.UserID, nil
}

func (s *FileStore) DeleteRefreshToken(_ context.Context, refreshToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Refresh, refreshToken)
	return s.persist()
}

func (s *FileStore) AtomicGetAndDeleteRefreshToken(_ context.Context, refreshToken string) (string, string, *oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.data.Refresh[refreshToken]
	if !ok {
		return "", "", nil, fmt.Errorf("%w: %s", storage.ErrTokenNotFound, storage.ErrMsgRefreshTokenNotFoundOrUsed)
	}
	if !rec.ExpiresAt.IsZero() && security.IsTokenExpired(rec.ExpiresAt) {
		return "", "", nil, fmt.Errorf("%w: refresh token expired", storage.ErrTokenExpired)
	}
	storedToken, ok := s.data.Tokens[refreshToken]
	if !ok {
		return "", "", nil, fmt.Errorf("%w: provider token not found", storage.ErrTokenNotFound)
	}
	providerToken, err := s.unpack(storedToken)
	if err != nil {
		return "", "", nil, err
	}

	var clientID string
	if metadata, ok := s.data.TokenMetadata[refreshToken]; ok && metadata != nil {
		clientID = metadata.ClientID
	}

	delete(s.data.Refresh, refreshToken)
	delete(s.data.Tokens, refreshToken)
	delete(s.data.TokenMetadata, refreshToken)
	if err := s.persist(); err != nil {
		return "", "", nil, err
	}
	return rec.UserID, clientID, providerToken, nil
}

// SaveTokenMetadata persists ownership and audience metadata for an issued
// token. The authorization server uses it to bind refresh tokens to the
// client they were issued to.
func (s *FileStore) SaveTokenMetadata(_ context.Context, tokenID string, metadata storage.TokenMetadata) error {
	if tokenID == "" || metadata.UserID == "" || metadata.ClientID == "" {
		return fmt.Errorf("tokenID, userID, and clientID cannot be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := metadata
	s.data.TokenMetadata[tokenID] = &copied
	return s.persist()
}

// GetTokenMetadata returns the metadata saved for tokenID.
func (s *FileStore) GetTokenMetadata(tokenID string) (*storage.TokenMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	metadata, ok := s.data.TokenMetadata[tokenID]
	if !ok || metadata == nil {
		return nil, fmt.Errorf("token metadata: %w", storage.ErrTokenNotFound)
	}
	copied := *metadata
	return &copied, nil
}

// ---------------------------------------------------------------------------
// ClientStore
// ---------------------------------------------------------------------------

func (s *FileStore) SaveClient(_ context.Context, client *storage.Client) error {
	if client == nil || client.ClientID == "" {
		return fmt.Errorf("invalid client")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Clients[client.ClientID] = client
	return s.persist()
}

func (s *FileStore) GetClient(_ context.Context, clientID string) (*storage.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	client, ok := s.data.Clients[clientID]
	if !ok {
		return nil, storage.ErrClientNotFound
	}
	return client, nil
}

func (s *FileStore) ValidateClientSecret(ctx context.Context, clientID, clientSecret string) error {
	client, err := s.GetClient(ctx, clientID)

	hashToCompare := storage.DummyBcryptHash
	public := false
	if err == nil {
		if client.IsPublic() {
			public = true
		} else if client.ClientSecretHash != "" {
			hashToCompare = client.ClientSecretHash
		}
	}

	bcryptErr := bcrypt.CompareHashAndPassword([]byte(hashToCompare), []byte(clientSecret))

	if public && err == nil {
		return nil
	}
	if err != nil {
		return storage.ErrInvalidClientCredentials
	}
	if bcryptErr != nil {
		return storage.ErrInvalidClientCredentials
	}
	return nil
}

func (s *FileStore) ListClients(_ context.Context) ([]*storage.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clients := make([]*storage.Client, 0, len(s.data.Clients))
	for _, client := range s.data.Clients {
		clients = append(clients, client)
	}
	return clients, nil
}

func (s *FileStore) DeleteClient(_ context.Context, clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Clients[clientID]; !ok {
		return storage.ErrClientNotFound
	}
	delete(s.data.Clients, clientID)
	return s.persist()
}

func (s *FileStore) CheckIPLimit(_ context.Context, ip string, maxClientsPerIP int) error {
	if maxClientsPerIP <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.ClientIPCounts[ip] >= maxClientsPerIP {
		return storage.ErrClientIPLimitExceeded
	}
	return nil
}

func (s *FileStore) TrackClientIP(_ context.Context, _ string, ip string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.ClientIPCounts[ip]++
	return s.persist()
}

// ---------------------------------------------------------------------------
// FlowStore
// ---------------------------------------------------------------------------

func (s *FileStore) SaveAuthorizationState(_ context.Context, state *storage.AuthorizationState) error {
	if state == nil || state.StateID == "" {
		return fmt.Errorf("%s", storage.ErrMsgInvalidAuthorizationState)
	}
	if state.ProviderState == "" {
		return fmt.Errorf("%s", storage.ErrMsgProviderStateRequired)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.AuthStates[state.StateID] = state
	s.data.AuthStates[state.ProviderState] = state
	return s.persist()
}

func (s *FileStore) GetAuthorizationState(_ context.Context, stateID string) (*storage.AuthorizationState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.data.AuthStates[stateID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", storage.ErrAuthorizationStateNotFound, stateID)
	}
	if state.HasExpired() {
		return nil, fmt.Errorf("%w: authorization state expired", storage.ErrTokenExpired)
	}
	return state, nil
}

func (s *FileStore) GetAuthorizationStateByProviderState(_ context.Context, providerState string) (*storage.AuthorizationState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.data.AuthStates[providerState]
	if !ok {
		return nil, fmt.Errorf("%w: provider state", storage.ErrAuthorizationStateNotFound)
	}
	if state.HasExpired() {
		return nil, fmt.Errorf("%w: authorization state expired", storage.ErrTokenExpired)
	}
	return state, nil
}

func (s *FileStore) DeleteAuthorizationState(_ context.Context, stateID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.data.AuthStates[stateID]
	if !ok {
		return nil
	}
	delete(s.data.AuthStates, state.StateID)
	delete(s.data.AuthStates, state.ProviderState)
	return s.persist()
}

func (s *FileStore) SaveAuthorizationCode(_ context.Context, code *storage.AuthorizationCode) error {
	if code == nil || code.Code == "" {
		return fmt.Errorf("invalid authorization code")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.AuthCodes[code.Code] = code
	return s.persist()
}

func (s *FileStore) GetAuthorizationCode(_ context.Context, code string) (*storage.AuthorizationCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	authCode, ok := s.data.AuthCodes[code]
	if !ok {
		return nil, storage.ErrAuthorizationCodeNotFound
	}
	if authCode.HasExpired() {
		delete(s.data.AuthCodes, code)
		_ = s.persist()
		return nil, fmt.Errorf("%w: authorization code expired", storage.ErrTokenExpired)
	}
	copyCode := *authCode
	return &copyCode, nil
}

func (s *FileStore) AtomicCheckAndMarkAuthCodeUsed(_ context.Context, code string) (*storage.AuthorizationCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	authCode, ok := s.data.AuthCodes[code]
	if !ok {
		return nil, storage.ErrAuthorizationCodeNotFound
	}
	if authCode.HasExpired() {
		delete(s.data.AuthCodes, code)
		_ = s.persist()
		return nil, fmt.Errorf("%w: authorization code expired", storage.ErrTokenExpired)
	}
	if authCode.Used {
		copyCode := *authCode
		return &copyCode, storage.ErrAuthorizationCodeUsed
	}
	authCode.Used = true
	if err := s.persist(); err != nil {
		return nil, err
	}
	copyCode := *authCode
	return &copyCode, nil
}

func (s *FileStore) DeleteAuthorizationCode(_ context.Context, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.AuthCodes, code)
	return s.persist()
}

// Close satisfies a symmetric close contract; the file store has no background
// goroutines to stop.
func (s *FileStore) Close() error { return nil }
