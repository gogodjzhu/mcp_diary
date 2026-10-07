package diarysync

import (
	"context"
	"sync"
	"time"
)

type MemoryProvider struct {
	kind Kind
	now  func() time.Time

	mu    sync.Mutex
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

type memoryFactory struct{ provider Provider }

func (f memoryFactory) Probe() bool { return false }

func (f memoryFactory) Open(Medium, string) (Provider, error) { return f.provider, nil }

func MemoryFactory(kind Kind, now func() time.Time) Factory {
	return memoryFactory{provider: NewMemoryProvider(kind, now)}
}

func fixedFactory(provider Provider) Factory { return memoryFactory{provider: provider} }

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

func (p *MemoryProvider) Verify(ctx context.Context) (VerifyResult, error) {
	if err := ctx.Err(); err != nil {
		return VerifyResult{}, err
	}
	return VerifyResult{
		OK: true,
		Checks: []VerifyCheck{
			{Name: "token", OK: true, Message: "token 有效"},
			{Name: "repository", OK: true, Message: "仓库可访问"},
			{Name: "branch", OK: true, Message: "分支存在"},
		},
	}, nil
}

func (p *MemoryProvider) Get(ctx context.Context, ref DocumentRef) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if ref.Kind == DocumentAttachment {
		return nil, false, ErrAttachmentNotSupported
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	f, ok := p.files[ref.Path]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), f.body...), true, nil
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
