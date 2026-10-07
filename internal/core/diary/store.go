package diary

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
	"github.com/gogodjzhu/mcp-diary/internal/core/persist"
)

// storePath is the workspace-relative location of the diary store file.
const storePath = ".mcp-diary/diary.json"

type storeFile struct {
	Sessions     map[string]*Session        `json:"sessions"`
	Entries      map[string]*Entry          `json:"entries"`
	Idempotency  map[string]json.RawMessage `json:"idempotency"`
	DateSessions map[string]string          `json:"date_sessions"`
	DateEntries  map[string]string          `json:"date_entries"`
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
			Idempotency:  map[string]json.RawMessage{},
			DateSessions: map[string]string{},
			DateEntries:  map[string]string{},
		},
	}
}

func loadStore(ctx context.Context, fs *filesystem.Service, now func() time.Time) (*Store, error) {
	s := newStore(fs, now)
	if err := persist.Load(ctx, fs, storePath, &s.data); err != nil {
		return nil, err
	}
	if s.data.Sessions == nil {
		s.data.Sessions = map[string]*Session{}
	}
	if s.data.Entries == nil {
		s.data.Entries = map[string]*Entry{}
	}
	if s.data.Idempotency == nil {
		s.data.Idempotency = map[string]json.RawMessage{}
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
	return persist.Save(ctx, s.fs, storePath, s.data)
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
	cp.Meta = cloneMeta(src.Meta)
	return &cp
}

func cloneMeta(src *EntryMeta) *EntryMeta {
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
