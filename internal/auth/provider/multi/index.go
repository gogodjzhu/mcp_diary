package multi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

type stateKey struct{}

// WithState returns a context carrying the provider state from the callback.
// The library validates the provider token with a fresh context, so the state
// is the only handle available there for recovering which provider was chosen.
func WithState(ctx context.Context, state string) context.Context {
	return context.WithValue(ctx, stateKey{}, state)
}

func stateFrom(ctx context.Context) string {
	state, _ := ctx.Value(stateKey{}).(string)
	return state
}

// noteState records which provider a callback state belongs to.
func (p *Provider) noteState(ctx context.Context, name string) {
	state := stateFrom(ctx)
	if state == "" || name == "" {
		return
	}
	p.mu.Lock()
	p.stateIndex[state] = name
	p.persistIndex()
	p.mu.Unlock()
}

type indexFile struct {
	State  map[string]string `json:"state,omitempty"`
	Access map[string]string `json:"access,omitempty"`
}

func (p *Provider) loadIndex() {
	if p.indexPath == "" {
		return
	}
	raw, err := os.ReadFile(p.indexPath)
	if err != nil {
		return
	}
	var stored indexFile
	if json.Unmarshal(raw, &stored) != nil {
		return
	}
	p.mu.Lock()
	if stored.State != nil {
		p.stateIndex = stored.State
	}
	if stored.Access != nil {
		p.accessIndex = stored.Access
	}
	p.mu.Unlock()
}

// persistIndex writes the indexes. The caller must hold p.mu.
func (p *Provider) persistIndex() {
	if p.indexPath == "" {
		return
	}
	stored := indexFile{State: map[string]string{}, Access: map[string]string{}}
	for k, v := range p.stateIndex {
		stored.State[k] = v
	}
	for k, v := range p.accessIndex {
		stored.Access[k] = v
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p.indexPath), 0o700)
	_ = os.WriteFile(p.indexPath, raw, 0o600)
}
