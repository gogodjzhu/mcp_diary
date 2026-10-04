package sync

import (
	"context"
	"fmt"
	stdsync "sync"
)

type Provider interface {
	Kind() Kind
	Push(ctx context.Context, doc Document) (Result, error)
	Delete(ctx context.Context, ref DocumentRef) (Result, error)
	Status(ctx context.Context, ref DocumentRef) (RemoteStatus, error)
}

type Factory func(medium Medium, credential string) (Provider, error)

type Registry struct {
	mu        stdsync.Mutex
	factories map[Kind]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: map[Kind]Factory{}}
}

func (r *Registry) Register(kind Kind, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[kind] = factory
}

func (r *Registry) Open(medium Medium, credential string) (Provider, error) {
	r.mu.Lock()
	factory, ok := r.factories[medium.Kind]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKind, medium.Kind)
	}
	return factory(medium, credential)
}
