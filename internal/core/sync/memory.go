package sync

import (
	"context"
	stdsync "sync"
	"time"
)

type MemoryProvider struct {
	kind Kind
	now  func() time.Time

	mu    stdsync.Mutex
	files map[string]memoryFile
}

type memoryFile struct {
	body      []byte
	remoteID  string
	updatedAt time.Time
}

func NewMemoryProvider(kind Kind, now func() time.Time) *MemoryProvider {
	if now == nil {
		now = func() time.Time { return time.Now() }
	}
	if kind == "" {
		kind = KindGitHub
	}
	return &MemoryProvider{
		kind:  kind,
		now:   now,
		files: map[string]memoryFile{},
	}
}

func MemoryFactory(kind Kind, now func() time.Time) Factory {
	shared := NewMemoryProvider(kind, now)
	return func(Medium, string) (Provider, error) {
		return shared, nil
	}
}

func (p *MemoryProvider) Kind() Kind { return p.kind }

func (p *MemoryProvider) Push(ctx context.Context, doc Document) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if doc.Kind == DocumentAttachment {
		return Result{}, ErrAttachmentNotSupported
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	body := append([]byte(nil), doc.Body...)
	p.files[doc.Path] = memoryFile{
		body:      body,
		remoteID:  "mem:" + doc.Path,
		updatedAt: now,
	}
	return Result{Path: doc.Path, RemoteID: "mem:" + doc.Path, SyncedAt: now}, nil
}

func (p *MemoryProvider) Delete(ctx context.Context, ref DocumentRef) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.files, ref.Path)
	return Result{Path: ref.Path, SyncedAt: now}, nil
}

func (p *MemoryProvider) Status(ctx context.Context, ref DocumentRef) (RemoteStatus, error) {
	if err := ctx.Err(); err != nil {
		return RemoteStatus{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	f, ok := p.files[ref.Path]
	if !ok {
		return RemoteStatus{Path: ref.Path, Exists: false}, nil
	}
	return RemoteStatus{Path: ref.Path, Exists: true, RemoteID: f.remoteID, UpdatedAt: f.updatedAt}, nil
}

func (p *MemoryProvider) Body(path string) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, ok := p.files[path]
	if !ok {
		return nil
	}
	return append([]byte(nil), f.body...)
}
