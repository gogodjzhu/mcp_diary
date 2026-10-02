package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gogodjzhu/mcp-diary/internal/auth/identity"
	"github.com/gogodjzhu/mcp-diary/internal/filesystem"
)

func TestAnonymousWorkspaceUsesRoot(t *testing.T) {
	root := t.TempDir()
	manager, err := New(Config{Root: root, MaxReadBytes: 1024})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	svc, err := manager.Filesystem(context.Background())
	if err != nil {
		t.Fatalf("Filesystem: %v", err)
	}
	if svc.Root() != mustAbs(t, root) {
		t.Fatalf("root = %q, want %q", svc.Root(), mustAbs(t, root))
	}
}

func TestPerUserWorkspaceIsolation(t *testing.T) {
	root := t.TempDir()
	manager, err := New(Config{Root: root, UsersDir: "users", PerUser: true, MaxReadBytes: 1024})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	aliceCtx := identity.WithIdentity(context.Background(), &identity.Identity{Email: "alice@example.com", Subject: "1"})
	bobCtx := identity.WithIdentity(context.Background(), &identity.Identity{Email: "bob@example.com", Subject: "2"})

	aliceFS, err := manager.Filesystem(aliceCtx)
	if err != nil {
		t.Fatalf("alice Filesystem: %v", err)
	}
	bobFS, err := manager.Filesystem(bobCtx)
	if err != nil {
		t.Fatalf("bob Filesystem: %v", err)
	}

	if aliceFS.Root() == bobFS.Root() {
		t.Fatalf("users share a workspace root: %s", aliceFS.Root())
	}
	wantAlice := filepath.Join(mustAbs(t, root), "users", "alice@example.com")
	if aliceFS.Root() != wantAlice {
		t.Fatalf("alice root = %q, want %q", aliceFS.Root(), wantAlice)
	}

	if _, err := aliceFS.Write(aliceCtx, "secret.txt", "alice", filesystem.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("alice write: %v", err)
	}
	if _, err := bobFS.Read(bobCtx, "secret.txt", 0, 0); err == nil {
		t.Fatal("bob must not be able to read alice's file")
	}
}

func TestPerUserWorkspaceRequiresIdentity(t *testing.T) {
	manager, err := New(Config{Root: t.TempDir(), PerUser: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := manager.Filesystem(context.Background()); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestUserDirectoryPermissions(t *testing.T) {
	root := t.TempDir()
	manager, err := New(Config{Root: root, PerUser: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := identity.WithIdentity(context.Background(), &identity.Identity{Email: "alice@example.com"})
	if _, err := manager.Filesystem(ctx); err != nil {
		t.Fatalf("Filesystem: %v", err)
	}

	info, err := os.Stat(filepath.Join(mustAbs(t, root), "users", "alice@example.com"))
	if err != nil {
		t.Fatalf("stat user dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("user dir perm = %o, want 700", perm)
	}
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}
