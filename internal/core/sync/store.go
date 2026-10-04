package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	stdsync "sync"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
)

const (
	mediaPath = ".mcp-diary/sync-media.json"
	statePath = ".mcp-diary/sync-state.json"
)

type mediaFile struct {
	Media map[string]*Medium `json:"media"`
}

type stateFile struct {
	States map[string]*SyncState `json:"states"`
}

type Store struct {
	mu    stdsync.Mutex
	fs    *filesystem.Service
	now   func() time.Time
	codec *Codec
	media mediaFile
	state stateFile
}

func newStore(fs *filesystem.Service, codec *Codec, now func() time.Time) *Store {
	if now == nil {
		now = func() time.Time { return time.Now() }
	}
	if codec == nil {
		codec = &Codec{}
	}
	return &Store{
		fs:    fs,
		now:   now,
		codec: codec,
		media: mediaFile{Media: map[string]*Medium{}},
		state: stateFile{States: map[string]*SyncState{}},
	}
}

func loadStore(ctx context.Context, fs *filesystem.Service, codec *Codec, now func() time.Time) (*Store, error) {
	s := newStore(fs, codec, now)
	if err := loadJSON(ctx, fs, mediaPath, &s.media); err != nil {
		return nil, err
	}
	if s.media.Media == nil {
		s.media.Media = map[string]*Medium{}
	}
	if err := loadJSON(ctx, fs, statePath, &s.state); err != nil {
		return nil, err
	}
	if s.state.States == nil {
		s.state.States = map[string]*SyncState{}
	}
	return s, nil
}

func loadJSON(ctx context.Context, fs *filesystem.Service, path string, dest any) error {
	b, err := fs.ReadFile(ctx, path)
	if err != nil {
		if errors.Is(err, filesystem.ErrNotFound) {
			return nil
		}
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, dest)
}

func (s *Store) persistMediaLocked(ctx context.Context) error {
	b, err := json.MarshalIndent(s.media, "", "  ")
	if err != nil {
		return err
	}
	_, err = s.fs.WriteAtomic(ctx, mediaPath, string(b), filesystem.WriteOptions{
		CreateDirs: true,
		Mode:       0o600,
	})
	return err
}

func (s *Store) persistStateLocked(ctx context.Context) error {
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	_, err = s.fs.WriteAtomic(ctx, statePath, string(b), filesystem.WriteOptions{
		CreateDirs: true,
		Mode:       0o600,
	})
	return err
}

func (s *Store) List(ctx context.Context) ([]PublicMedium, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PublicMedium, 0, len(s.media.Media))
	for _, m := range s.media.Media {
		out = append(out, cloneMedium(m).Public())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *Store) Get(ctx context.Context, id string) (*PublicMedium, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.media.Media[id]
	if m == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	pub := cloneMedium(m).Public()
	return &pub, nil
}

type UpsertIn struct {
	ID         string
	Kind       Kind
	Name       string
	Enabled    *bool
	Settings   map[string]string
	Credential string
}

func (s *Store) Upsert(ctx context.Context, in UpsertIn) (*PublicMedium, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ErrMediumNameRequired
	}
	if err := rejectSecretSettings(in.Settings); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	var m *Medium
	if in.ID != "" {
		m = s.media.Media[in.ID]
		if m == nil {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, in.ID)
		}
		if in.Kind != "" && in.Kind != m.Kind {
			return nil, fmt.Errorf("%w: kind cannot change", ErrUnknownKind)
		}
	} else {
		if !in.Kind.Valid() {
			if in.Kind == "" {
				return nil, ErrMediumKindRequired
			}
			return nil, fmt.Errorf("%w: %s", ErrUnknownKind, in.Kind)
		}
		m = &Medium{
			ID:        newID("sm"),
			Kind:      in.Kind,
			CreatedAt: now,
		}
	}

	m.Name = name
	if in.Enabled != nil {
		m.Enabled = *in.Enabled
	} else if in.ID == "" {
		m.Enabled = true
	}
	if in.Settings != nil {
		m.Settings = cloneSettings(in.Settings)
	}
	if in.Credential != "" {
		ciphertext, err := s.codec.Encrypt(in.Credential)
		if err != nil {
			return nil, err
		}
		m.Secret = Secret{Ciphertext: ciphertext}
	}
	m.UpdatedAt = now
	s.media.Media[m.ID] = m
	if _, ok := s.state.States[m.ID]; !ok {
		s.state.States[m.ID] = &SyncState{
			MediumID:  m.ID,
			Status:    StatusIdle,
			Documents: map[string]DocumentState{},
		}
		if err := s.persistStateLocked(ctx); err != nil {
			return nil, err
		}
	}
	if err := s.persistMediaLocked(ctx); err != nil {
		return nil, err
	}
	pub := cloneMedium(m).Public()
	return &pub, nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return ErrMediumIDRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.media.Media[id] == nil {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(s.media.Media, id)
	delete(s.state.States, id)
	if err := s.persistMediaLocked(ctx); err != nil {
		return err
	}
	return s.persistStateLocked(ctx)
}

func (s *Store) credential(ctx context.Context, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.media.Media[id]
	if m == nil {
		return "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return s.codec.Decrypt(m.Secret.Ciphertext)
}

func (s *Store) openMedium(ctx context.Context, id string) (Medium, string, error) {
	if err := ctx.Err(); err != nil {
		return Medium{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.media.Media[id]
	if m == nil {
		return Medium{}, "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	credential, err := s.codec.Decrypt(m.Secret.Ciphertext)
	if err != nil {
		return Medium{}, "", err
	}
	out := cloneMedium(m)
	out.Secret = Secret{}
	return *out, credential, nil
}

func (s *Store) GetState(ctx context.Context, mediumID string) (*SyncState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state.States[mediumID]
	if st == nil {
		if s.media.Media[mediumID] == nil {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, mediumID)
		}
		return &SyncState{MediumID: mediumID, Status: StatusIdle, Documents: map[string]DocumentState{}}, nil
	}
	return cloneState(st), nil
}

func (s *Store) RecordPush(ctx context.Context, mediumID string, doc Document, result Result) error {
	return s.mutateState(ctx, mediumID, func(st *SyncState) {
		st.Status = StatusSucceeded
		st.LastError = ""
		synced := result.SyncedAt
		if synced.IsZero() {
			synced = s.now()
		}
		st.LastSyncedAt = &synced
		if st.Documents == nil {
			st.Documents = map[string]DocumentState{}
		}
		st.Documents[result.Path] = DocumentState{
			Path:         result.Path,
			RemoteID:     result.RemoteID,
			EntryIDs:     cloneStrings(doc.Source.EntryIDs),
			Revision:     doc.Source.Revision,
			LastSyncedAt: synced,
		}
	})
}

func (s *Store) RecordDelete(ctx context.Context, mediumID string, ref DocumentRef, result Result) error {
	return s.mutateState(ctx, mediumID, func(st *SyncState) {
		st.Status = StatusSucceeded
		st.LastError = ""
		synced := result.SyncedAt
		if synced.IsZero() {
			synced = s.now()
		}
		st.LastSyncedAt = &synced
		delete(st.Documents, ref.Path)
	})
}

func (s *Store) RecordFailure(ctx context.Context, mediumID, message string) error {
	return s.mutateState(ctx, mediumID, func(st *SyncState) {
		st.Status = StatusFailed
		st.LastError = message
	})
}

func (s *Store) MarkSyncing(ctx context.Context, mediumID string) error {
	return s.mutateState(ctx, mediumID, func(st *SyncState) {
		st.Status = StatusSyncing
		st.LastError = ""
	})
}

func (s *Store) RecordSuccess(ctx context.Context, mediumID string) error {
	return s.mutateState(ctx, mediumID, func(st *SyncState) {
		st.Status = StatusSucceeded
		st.LastError = ""
		synced := s.now()
		st.LastSyncedAt = &synced
	})
}

func (s *Store) mutateState(ctx context.Context, mediumID string, fn func(*SyncState)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(mediumID) == "" {
		return ErrMediumIDRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.media.Media[mediumID] == nil {
		return fmt.Errorf("%w: %s", ErrNotFound, mediumID)
	}
	st := s.state.States[mediumID]
	if st == nil {
		st = &SyncState{MediumID: mediumID, Status: StatusIdle, Documents: map[string]DocumentState{}}
		s.state.States[mediumID] = st
	}
	fn(st)
	return s.persistStateLocked(ctx)
}

func rejectSecretSettings(settings map[string]string) error {
	for k := range settings {
		key := strings.ToLower(k)
		if strings.Contains(key, "token") || strings.Contains(key, "secret") || strings.Contains(key, "password") || strings.Contains(key, "credential") {
			return fmt.Errorf("%w: %s", ErrSecretKey, k)
		}
	}
	return nil
}
