// Package diarymeta stores per-workspace diary metadata preferences and
// enriches committed entries with derived metadata: the Chinese lunar date, the
// weekday and (when enabled and a location is configured) the day's weather.
package diarymeta

import (
	"context"
	"errors"
	"sync"

	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
	"github.com/gogodjzhu/mcp-diary/internal/core/persist"
)

const settingsPath = ".mcp-diary/settings.json"

// Settings holds the per-workspace metadata preferences.
type Settings struct {
	LunarEnabled    bool   `json:"lunar_enabled"`
	WeatherEnabled  bool   `json:"weather_enabled"`
	WeatherLocation string `json:"weather_location,omitempty"`
}

// ErrNoWorkspace is returned when no filesystem is bound to the request.
var ErrNoWorkspace = errors.New("no filesystem bound to the request")

// Service caches one settings store per workspace root.
type Service struct {
	mu     sync.Mutex
	stores map[string]*store
}

// New builds an empty settings service.
func New() *Service {
	return &Service{stores: map[string]*store{}}
}

type store struct {
	mu   sync.Mutex
	fs   *filesystem.Service
	data Settings
}

func (s *Service) storeFor(ctx context.Context, fs *filesystem.Service) (*store, error) {
	if fs == nil {
		return nil, ErrNoWorkspace
	}
	root := fs.Root()
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.stores[root]; ok {
		return st, nil
	}
	st := &store{fs: fs}
	if err := persist.Load(ctx, fs, settingsPath, &st.data); err != nil {
		return nil, err
	}
	s.stores[root] = st
	return st, nil
}

// Get returns the settings for the workspace behind fs.
func (s *Service) Get(ctx context.Context, fs *filesystem.Service) (Settings, error) {
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return Settings{}, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.data, nil
}

// Save replaces the settings for the workspace behind fs.
func (s *Service) Save(ctx context.Context, fs *filesystem.Service, in Settings) (Settings, error) {
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return Settings{}, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.data = in
	if err := persist.Save(ctx, st.fs, settingsPath, st.data); err != nil {
		return Settings{}, err
	}
	return st.data, nil
}
