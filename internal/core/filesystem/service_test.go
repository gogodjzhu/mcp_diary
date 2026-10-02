package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestReadFileIgnoresReadLimit(t *testing.T) {
	svc, _ := newTestService(t, false)
	ctx := context.Background()

	content := strings.Repeat("x", 64) // twice the 16-byte MaxReadBytes of the test service
	if _, err := svc.Write(ctx, "big.txt", content, WriteOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read clamps to MaxReadBytes and reports truncation; ReadFile ignores the limit.
	limited, err := svc.Read(ctx, "big.txt", 0, 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(limited.Content) != 16 || !limited.Truncated {
		t.Fatalf("Read returned %d bytes truncated=%v, want 16/truncated", len(limited.Content), limited.Truncated)
	}

	b, err := svc.ReadFile(ctx, "big.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(b) != content {
		t.Fatalf("ReadFile returned %d bytes, want %d", len(b), len(content))
	}

	if _, err := svc.ReadFile(ctx, "missing.txt"); err == nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadFile(missing) err = %v, want ErrNotFound", err)
	}
}

func TestWriteAtomic(t *testing.T) {
	svc, root := newTestService(t, false)
	ctx := context.Background()

	res, err := svc.WriteAtomic(ctx, "nested/store.json", `{"v":1}`, WriteOptions{CreateDirs: true, Mode: 0o600})
	if err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if !res.Created {
		t.Fatalf("Created = false, want true")
	}

	b, err := os.ReadFile(filepath.Join(root, "nested", "store.json"))
	if err != nil || string(b) != `{"v":1}` {
		t.Fatalf("on-disk = %q err = %v", b, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "nested", "store.json.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file left behind: %v", err)
	}

	// Replace is atomic and preserves content integrity.
	if _, err := svc.WriteAtomic(ctx, "nested/store.json", `{"v":2}`, WriteOptions{Mode: 0o600}); err != nil {
		t.Fatalf("WriteAtomic replace: %v", err)
	}
	b, _ = os.ReadFile(filepath.Join(root, "nested", "store.json"))
	if string(b) != `{"v":2}` {
		t.Fatalf("after replace = %q", b)
	}
}

func TestWriteAtomicRefusesSymlinkedTemp(t *testing.T) {
	svc, root := newTestService(t, false)
	ctx := context.Background()

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "data", "store.json.tmp")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := svc.WriteAtomic(ctx, "data/store.json", "x", WriteOptions{}); err == nil || !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("WriteAtomic err = %v, want ErrOutsideRoot", err)
	}
	b, err := os.ReadFile(filepath.Join(outside, "secret.txt"))
	if err != nil || string(b) != "secret" {
		t.Fatalf("outside file was modified: %q %v", b, err)
	}
}

func TestWriteAtomicRespectsReadOnly(t *testing.T) {
	svc, _ := newTestService(t, true)
	if _, err := svc.WriteAtomic(context.Background(), "x.txt", "y", WriteOptions{}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("err = %v, want ErrReadOnly", err)
	}
}
