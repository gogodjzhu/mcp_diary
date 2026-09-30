package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newTestService(t *testing.T, readOnly bool) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	svc, err := New(Options{Root: root, ReadOnly: readOnly, MaxReadBytes: 16})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, root
}

func TestWriteReadAppend(t *testing.T) {
	svc, root := newTestService(t, false)
	ctx := context.Background()

	res, err := svc.Write(ctx, "notes/today.txt", "hello", WriteOptions{CreateDirs: true})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !res.Created || res.Bytes != 5 {
		t.Fatalf("unexpected write result: %+v", res)
	}

	if _, err := svc.Append(ctx, "notes/today.txt", " world", WriteOptions{}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := svc.Read(ctx, "notes/today.txt", 0, 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Content != "hello world" {
		t.Fatalf("content = %q, want %q", got.Content, "hello world")
	}

	if data, err := os.ReadFile(filepath.Join(root, "notes", "today.txt")); err != nil || string(data) != "hello world" {
		t.Fatalf("file on disk = %q, err = %v", data, err)
	}
}

func TestReadOffsetAndTruncation(t *testing.T) {
	svc, _ := newTestService(t, false)
	ctx := context.Background()

	if _, err := svc.Write(ctx, "payload.txt", "0123456789", WriteOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := svc.Read(ctx, "payload.txt", 2, 3)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Content != "234" {
		t.Fatalf("content = %q, want %q", got.Content, "234")
	}
	if !got.Truncated || got.Size != 10 || got.Offset != 2 {
		t.Fatalf("unexpected read metadata: %+v", got)
	}
}

func TestListOrdersDirectoriesFirst(t *testing.T) {
	svc, _ := newTestService(t, false)
	ctx := context.Background()

	if err := svc.Mkdir(ctx, "sub", true); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if _, err := svc.Write(ctx, "b.txt", "b", WriteOptions{}); err != nil {
		t.Fatalf("Write b: %v", err)
	}
	if _, err := svc.Write(ctx, "a.txt", "a", WriteOptions{}); err != nil {
		t.Fatalf("Write a: %v", err)
	}

	entries, err := svc.List(ctx, ".")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	if !entries[0].IsDir || entries[0].Name != "sub" {
		t.Fatalf("first entry = %+v, want directory sub", entries[0])
	}
	if entries[1].Name != "a.txt" || entries[2].Name != "b.txt" {
		t.Fatalf("file order = %q, %q", entries[1].Name, entries[2].Name)
	}
}

func TestStatAndDelete(t *testing.T) {
	svc, _ := newTestService(t, false)
	ctx := context.Background()

	if _, err := svc.Write(ctx, "dir/file.txt", "data", WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	info, err := svc.Stat(ctx, "dir/file.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.IsDir || info.Size != 4 {
		t.Fatalf("unexpected stat: %+v", info)
	}

	if err := svc.Delete(ctx, "dir", false); !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("Delete non-recursive dir err = %v, want ErrIsDirectory", err)
	}

	if err := svc.Delete(ctx, "dir", true); err != nil {
		t.Fatalf("Delete recursive: %v", err)
	}
	if _, err := svc.Stat(ctx, "dir"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat deleted err = %v, want ErrNotFound", err)
	}
}

func TestDeleteRootRefused(t *testing.T) {
	svc, _ := newTestService(t, false)
	if err := svc.Delete(context.Background(), ".", true); err == nil {
		t.Fatal("expected deleting root to fail")
	}
}

func TestReadOnlyBlocksMutations(t *testing.T) {
	svc, _ := newTestService(t, true)
	ctx := context.Background()

	if _, err := svc.Write(ctx, "x.txt", "x", WriteOptions{}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Write err = %v, want ErrReadOnly", err)
	}
	if _, err := svc.Append(ctx, "x.txt", "x", WriteOptions{}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Append err = %v, want ErrReadOnly", err)
	}
	if err := svc.Mkdir(ctx, "d", true); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Mkdir err = %v, want ErrReadOnly", err)
	}
	if err := svc.Delete(ctx, "x", false); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Delete err = %v, want ErrReadOnly", err)
	}
}

func TestSandboxRejectsEscapes(t *testing.T) {
	svc, _ := newTestService(t, false)
	ctx := context.Background()

	cases := []string{
		"../outside.txt",
		"../../etc/passwd",
		filepath.Join("/etc", "passwd"),
	}
	for _, p := range cases {
		if _, err := svc.Read(ctx, p, 0, 0); !errors.Is(err, ErrOutsideRoot) {
			t.Fatalf("Read(%q) err = %v, want ErrOutsideRoot", p, err)
		}
	}
}

func TestSandboxRejectsSymlinkEscape(t *testing.T) {
	svc, root := newTestService(t, false)
	ctx := context.Background()

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := svc.Read(ctx, "escape/secret.txt", 0, 0); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Read via symlink err = %v, want ErrOutsideRoot", err)
	}

	// Writing through a symlinked directory must also be rejected.
	if _, err := svc.Write(ctx, "escape/new.txt", "x", WriteOptions{}); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Write via symlink err = %v, want ErrOutsideRoot", err)
	}
}
