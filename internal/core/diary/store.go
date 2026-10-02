package diary

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type storeFile struct {
	Sessions     map[string]*Session `json:"sessions"`
	Entries      map[string]*Entry   `json:"entries"`
	Idempotency  map[string]any      `json:"idempotency"`
	DateSessions map[string]string   `json:"date_sessions"`
	DateEntries  map[string]string   `json:"date_entries"`
}

type Store struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
	data storeFile
}

func newStore(path string, now func() time.Time) *Store {
	if now == nil {
		now = func() time.Time { return time.Now() }
	}
	return &Store{
		path: path,
		now:  now,
		data: storeFile{
			Sessions:     map[string]*Session{},
			Entries:      map[string]*Entry{},
			Idempotency:  map[string]any{},
			DateSessions: map[string]string{},
			DateEntries:  map[string]string{},
		},
	}
}

func loadStore(path string, now func() time.Time) (*Store, error) {
	s := newStore(path, now)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("read diary store: %w", err)
	}
	if len(b) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, fmt.Errorf("decode diary store: %w", err)
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

func (s *Store) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create diary store dir: %w", err)
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode diary store: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write diary store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace diary store: %w", err)
	}
	return nil
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
