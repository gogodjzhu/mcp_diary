package diarysync

import (
	"context"
	"fmt"
	"sync"
)

type Provider interface {
	Kind() Kind
	Push(ctx context.Context, doc Document) (Result, error)
	Delete(ctx context.Context, ref DocumentRef) (Result, error)
	Status(ctx context.Context, ref DocumentRef) (RemoteStatus, error)
	// Get reads the current remote body of ref. exists is false when the
	// document does not exist yet; a nil error with exists=true and an empty
	// body means the remote file exists but is empty.
	Get(ctx context.Context, ref DocumentRef) (body []byte, exists bool, err error)
	// Verify probes whether the medium can be reached with the current
	// credentials. Implementations return a structured result; a non-nil error
	// means the probe itself could not run (invalid settings, missing token).
	Verify(ctx context.Context) (VerifyResult, error)
}

type Factory interface {
	Open(medium Medium, credential string) (Provider, error)
	// Probe reports whether media of this kind must pass Verify before save.
	Probe() bool
}

type Registry struct {
	mu        sync.Mutex
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

func (r *Registry) probes(kind Kind) bool {
	r.mu.Lock()
	factory := r.factories[kind]
	r.mu.Unlock()
	return factory != nil && factory.Probe()
}

func (r *Registry) Open(medium Medium, credential string) (Provider, error) {
	r.mu.Lock()
	factory, ok := r.factories[medium.Kind]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKind, medium.Kind)
	}
	return factory.Open(medium, credential)
}
