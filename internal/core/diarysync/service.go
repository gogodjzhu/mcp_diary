package diarysync

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
)

type Runner interface {
	SyncAll(ctx context.Context, fs *filesystem.Service, mediumID string) error
}

type Service struct {
	now      func() time.Time
	codec    *Codec
	registry *Registry
	runner   Runner

	mu     sync.Mutex
	stores map[string]*Store
}

func (s *Service) Registry() *Registry { return s.registry }

func (s *Service) SetRunner(runner Runner) { s.runner = runner }

func New(codec *Codec, registry *Registry, now func() time.Time) *Service {
	if now == nil {
		now = func() time.Time { return time.Now() }
	}
	if codec == nil {
		codec = &Codec{}
	}
	if registry == nil {
		registry = NewRegistry()
	}
	return &Service{
		now:      now,
		codec:    codec,
		registry: registry,
		stores:   map[string]*Store{},
	}
}

func (s *Service) storeFor(ctx context.Context, fs *filesystem.Service) (*Store, error) {
	if fs == nil {
		return nil, fmt.Errorf("no filesystem bound to the request")
	}
	root := fs.Root()
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.stores[root]; ok {
		return st, nil
	}
	st, err := loadStore(ctx, fs, s.codec, s.now)
	if err != nil {
		return nil, err
	}
	s.stores[root] = st
	return st, nil
}

func (s *Service) List(ctx context.Context, fs *filesystem.Service) ([]PublicMedium, error) {
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return nil, err
	}
	return st.List(ctx)
}

func (s *Service) Get(ctx context.Context, fs *filesystem.Service, id string) (*PublicMedium, error) {
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return nil, err
	}
	return st.Get(ctx, id)
}

func (s *Service) Upsert(ctx context.Context, fs *filesystem.Service, in UpsertIn) (*PublicMedium, error) {
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return nil, err
	}
	return st.Upsert(ctx, in)
}

func (s *Service) Delete(ctx context.Context, fs *filesystem.Service, id string) error {
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return err
	}
	return st.Delete(ctx, id)
}

func (s *Service) State(ctx context.Context, fs *filesystem.Service, mediumID string) (*SyncState, error) {
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return nil, err
	}
	return st.GetState(ctx, mediumID)
}

func (s *Service) RecordFailure(ctx context.Context, fs *filesystem.Service, mediumID, message string) error {
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return err
	}
	return st.RecordFailure(ctx, mediumID, message)
}

func (s *Service) ListViews(ctx context.Context, fs *filesystem.Service) ([]MediumView, error) {
	media, err := s.List(ctx, fs)
	if err != nil {
		return nil, err
	}
	out := make([]MediumView, 0, len(media))
	for _, m := range media {
		st, err := s.State(ctx, fs, m.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, MediumView{PublicMedium: m, State: *st})
	}
	return out, nil
}

func (s *Service) Trigger(ctx context.Context, fs *filesystem.Service, mediumID string) (*SyncState, error) {
	pub, err := s.Get(ctx, fs, mediumID)
	if err != nil {
		return nil, err
	}
	if !pub.Enabled {
		st, stateErr := s.State(ctx, fs, mediumID)
		if stateErr != nil {
			return nil, stateErr
		}
		return st, ErrMediumDisabled
	}
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return nil, err
	}
	if s.runner == nil {
		if err := st.RecordFailure(ctx, mediumID, ErrEngineNotConfigured.Error()); err != nil {
			return nil, err
		}
		state, stateErr := st.GetState(ctx, mediumID)
		if stateErr != nil {
			return nil, stateErr
		}
		return state, ErrEngineNotConfigured
	}
	if err := st.MarkSyncing(ctx, mediumID); err != nil {
		return nil, err
	}
	if err := s.runner.SyncAll(ctx, fs, mediumID); err != nil {
		if state, stateErr := st.GetState(ctx, mediumID); stateErr == nil && state.Status == StatusSyncing {
			_ = st.RecordFailure(ctx, mediumID, err.Error())
		}
		state, stateErr := st.GetState(ctx, mediumID)
		if stateErr != nil {
			return nil, err
		}
		return state, err
	}
	if state, stateErr := st.GetState(ctx, mediumID); stateErr == nil && state.Status == StatusSyncing {
		if err := st.RecordSuccess(ctx, mediumID); err != nil {
			return nil, err
		}
	}
	return st.GetState(ctx, mediumID)
}

func (s *Service) Push(ctx context.Context, fs *filesystem.Service, mediumID string, doc Document) (Result, error) {
	if doc.Kind == DocumentAttachment {
		return Result{}, ErrAttachmentNotSupported
	}
	st, provider, err := s.open(ctx, fs, mediumID)
	if err != nil {
		return Result{}, err
	}
	if err := st.MarkSyncing(ctx, mediumID); err != nil {
		return Result{}, err
	}
	result, err := provider.Push(ctx, doc)
	if err != nil {
		_ = st.RecordFailure(ctx, mediumID, err.Error())
		return Result{}, err
	}
	if result.Path == "" {
		result.Path = doc.Path
	}
	if result.SyncedAt.IsZero() {
		result.SyncedAt = s.now()
	}
	if err := st.RecordPush(ctx, mediumID, doc, result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s *Service) Remove(ctx context.Context, fs *filesystem.Service, mediumID string, ref DocumentRef) (Result, error) {
	if ref.Kind == DocumentAttachment {
		return Result{}, ErrAttachmentNotSupported
	}
	st, provider, err := s.open(ctx, fs, mediumID)
	if err != nil {
		return Result{}, err
	}
	if err := st.MarkSyncing(ctx, mediumID); err != nil {
		return Result{}, err
	}
	result, err := provider.Delete(ctx, ref)
	if err != nil {
		_ = st.RecordFailure(ctx, mediumID, err.Error())
		return Result{}, err
	}
	if result.Path == "" {
		result.Path = ref.Path
	}
	if result.SyncedAt.IsZero() {
		result.SyncedAt = s.now()
	}
	if err := st.RecordDelete(ctx, mediumID, ref, result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s *Service) RemoteStatus(ctx context.Context, fs *filesystem.Service, mediumID string, ref DocumentRef) (RemoteStatus, error) {
	_, provider, err := s.open(ctx, fs, mediumID)
	if err != nil {
		return RemoteStatus{}, err
	}
	return provider.Status(ctx, ref)
}

// Fetch reads the current remote body of ref. It is used by the sync engine to
// merge pre-existing remote content instead of overwriting it on first sync.
func (s *Service) Fetch(ctx context.Context, fs *filesystem.Service, mediumID string, ref DocumentRef) ([]byte, bool, error) {
	_, provider, err := s.open(ctx, fs, mediumID)
	if err != nil {
		return nil, false, err
	}
	return provider.Get(ctx, ref)
}

// RecordSynced marks a document as managed by the medium without issuing a
// remote write. Used when a first-sync merge finds nothing new to push.
func (s *Service) RecordSynced(ctx context.Context, fs *filesystem.Service, mediumID, path string, src Source) error {
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return err
	}
	return st.RecordSynced(ctx, mediumID, path, src)
}

func (s *Service) open(ctx context.Context, fs *filesystem.Service, mediumID string) (*Store, Provider, error) {
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return nil, nil, err
	}
	medium, credential, err := st.openMedium(ctx, mediumID)
	if err != nil {
		return nil, nil, err
	}
	provider, err := s.registry.Open(medium, credential)
	if err != nil {
		return nil, nil, err
	}
	return st, provider, nil
}
