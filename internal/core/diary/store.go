package diary

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
)

// storePath is the workspace-relative location of the diary store file.
const storePath = ".mcp-diary/diary.json"

type storeFile struct {
	Sessions     map[string]*Session `json:"sessions"`
	Entries      map[string]*Entry   `json:"entries"`
	Idempotency  map[string]any      `json:"idempotency"`
	DateSessions map[string]string   `json:"date_sessions"`
	DateEntries  map[string]string   `json:"date_entries"`
}

// Store persists one workspace's diary data. Every IO operation goes through
// the sandboxed filesystem.Service so the store is confined to the workspace
// root and honours its read-only flag.
type Store struct {
	mu   sync.Mutex
	fs   *filesystem.Service
	now  func() time.Time
	data storeFile
}

func newStore(fs *filesystem.Service, now func() time.Time) *Store {
	if now == nil {
		now = func() time.Time { return time.Now() }
	}
	return &Store{
		fs:  fs,
		now: now,
		data: storeFile{
			Sessions:     map[string]*Session{},
			Entries:      map[string]*Entry{},
			Idempotency:  map[string]any{},
			DateSessions: map[string]string{},
			DateEntries:  map[string]string{},
		},
	}
}

func loadStore(ctx context.Context, fs *filesystem.Service, now func() time.Time) (*Store, error) {
	s := newStore(fs, now)
	b, err := fs.ReadFile(ctx, storePath)
	if err != nil {
		if errors.Is(err, filesystem.ErrNotFound) {
			return s, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, err
	}
	if s.data.Sessions == nil {
		s.data.Sessions = map[string]*Session{}
	}
	if s.data.Entries == nil {
		s.data.Entries = map[string]*Entry{}
	}
	if s.data.Idempotency == nil {
		s.data.Idempotency = map[string]any{}
	}
	if s.data.DateSessions == nil {
		s.data.DateSessions = map[string]string{}
	}
	if s.data.DateEntries == nil {
		s.data.DateEntries = map[string]string{}
	}
	return s, nil
}

// persistLocked atomically replaces the store file. Callers must hold s.mu.
func (s *Store) persistLocked(ctx context.Context) error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	_, err = s.fs.WriteAtomic(ctx, storePath, string(b), filesystem.WriteOptions{
		CreateDirs: true,
		Mode:       0o600,
	})
	return err
}

func cloneSession(src *Session) *Session {
	if src == nil {
		return nil
	}
	cp := *src
	return &cp
}

func cloneEntry(src *Entry) *Entry {
	if src == nil {
		return nil
	}
	cp := *src
	return &cp
}

func (s *Store) listEntries(from, to string) []*Entry {
	out := make([]*Entry, 0, len(s.data.Entries))
	for _, e := range s.data.Entries {
		if from != "" && e.DiaryDate < from {
			continue
		}
		if to != "" && e.DiaryDate > to {
			continue
		}
		out = append(out, cloneEntry(e))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DiaryDate == out[j].DiaryDate {
			return out[i].EntryID < out[j].EntryID
		}
		return out[i].DiaryDate < out[j].DiaryDate
	})
	return out
}
