// Package workspace resolves the sandboxed filesystem an MCP request is allowed
// to operate on. With authentication enabled every user gets a private
// directory underneath the shared users directory, fully isolated from the
// base root and from other users.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gogodjzhu/mcp-diary/internal/auth/identity"
	"github.com/gogodjzhu/mcp-diary/internal/filesystem"
)

// ErrUnauthenticated is returned when a per-user workspace is requested but the
// context carries no authenticated identity.
var ErrUnauthenticated = errors.New("no authenticated identity in request context")

// Config configures a Manager.
type Config struct {
	// Root is the base workspace directory. When PerUser is false this is the
	// directory clients operate on directly.
	Root string
	// UsersDir is where per-user directories are created. Relative paths are
	// resolved against Root. Defaults to "users".
	UsersDir string
	// PerUser enables per-identity workspace isolation.
	PerUser bool
	// ReadOnly disables mutating operations in every workspace.
	ReadOnly bool
	// MaxReadBytes caps a single read.
	MaxReadBytes int64
}

// Manager resolves the filesystem for the current request.
type Manager struct {
	root         string
	usersDir     string
	perUser      bool
	readOnly     bool
	maxReadBytes int64

	anonymous *filesystem.Service

	mu       sync.Mutex
	services map[string]*filesystem.Service
}

// New creates a workspace manager and validates the base root.
func New(cfg Config) (*Manager, error) {
	anonymous, err := filesystem.New(filesystem.Options{
		Root:         cfg.Root,
		ReadOnly:     cfg.ReadOnly,
		MaxReadBytes: cfg.MaxReadBytes,
	})
	if err != nil {
		return nil, err
	}

	usersDir := cfg.UsersDir
	if usersDir == "" {
		usersDir = "users"
	}
	if !filepath.IsAbs(usersDir) {
		usersDir = filepath.Join(anonymous.Root(), usersDir)
	}

	return &Manager{
		root:         anonymous.Root(),
		usersDir:     filepath.Clean(usersDir),
		perUser:      cfg.PerUser,
		readOnly:     cfg.ReadOnly,
		maxReadBytes: cfg.MaxReadBytes,
		anonymous:    anonymous,
		services:     make(map[string]*filesystem.Service),
	}, nil
}

// Filesystem returns the sandboxed filesystem for the request in ctx.
func (m *Manager) Filesystem(ctx context.Context) (*filesystem.Service, error) {
	if !m.perUser {
		return m.anonymous, nil
	}

	identity, ok := identity.IdentityFrom(ctx)
	if !ok {
		return nil, ErrUnauthenticated
	}

	return m.serviceFor(identity)
}

func (m *Manager) serviceFor(identity *identity.Identity) (*filesystem.Service, error) {
	slug := identity.Slug()

	m.mu.Lock()
	defer m.mu.Unlock()

	if svc, ok := m.services[slug]; ok {
		return svc, nil
	}

	dir := filepath.Join(m.usersDir, slug)
	if err := ensureWithin(m.usersDir, dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create user workspace: %w", err)
	}

	svc, err := filesystem.New(filesystem.Options{
		Root:         dir,
		ReadOnly:     m.readOnly,
		MaxReadBytes: m.maxReadBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("open user workspace: %w", err)
	}

	m.services[slug] = svc
	return svc, nil
}

// Root returns the base workspace root.
func (m *Manager) Root() string { return m.root }

// UsersDir returns the directory that holds per-user workspaces.
func (m *Manager) UsersDir() string { return m.usersDir }

// PerUser reports whether workspaces are isolated per identity.
func (m *Manager) PerUser() bool { return m.perUser }

// ReadOnly reports whether mutating operations are disabled.
func (m *Manager) ReadOnly() bool { return m.readOnly }

func ensureWithin(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}
	if rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("workspace path %q escapes users directory", target)
	}
	return nil
}
