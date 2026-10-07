// Package filesystem provides a sandboxed file service. Every path handled by
// the service is resolved through an os.Root opened on a single workspace
// directory, so MCP clients can never read or write arbitrary files on the
// host: os.Root rejects both lexical traversal and symlinks that reference a
// location outside the root.
package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Options configures a Service.
type Options struct {
	// Root is the workspace directory all operations are confined to.
	Root string
	// ReadOnly disables every mutating operation when true.
	ReadOnly bool
	// MaxReadBytes caps the number of bytes returned by a single read. It
	// defaults to 1 MiB when unset.
	MaxReadBytes int64
}

// Service is a sandboxed filesystem rooted at a single directory. It is safe
// for concurrent use.
type Service struct {
	root         *os.Root
	rootPath     string
	readOnly     bool
	maxReadBytes int64
}

// New validates the workspace root and returns a ready-to-use Service.
func New(opts Options) (*Service, error) {
	root := opts.Root
	if root == "" {
		root = "."
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root %q is not a directory", abs)
	}

	// Resolve symlinks once so the sandbox boundary and the reported root are
	// stable even if the directory is reached through a link.
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root symlinks: %w", err)
	}

	handle, err := os.OpenRoot(real)
	if err != nil {
		return nil, fmt.Errorf("open workspace root %q: %w", real, err)
	}

	maxRead := opts.MaxReadBytes
	if maxRead <= 0 {
		maxRead = 1 << 20
	}

	return &Service{
		root:         handle,
		rootPath:     filepath.Clean(real),
		readOnly:     opts.ReadOnly,
		maxReadBytes: maxRead,
	}, nil
}

// Root returns the absolute, symlink-resolved workspace root.
func (s *Service) Root() string { return s.rootPath }

// ReadOnly reports whether mutating operations are disabled.
func (s *Service) ReadOnly() bool { return s.readOnly }

// Close releases the directory handle backing the sandbox.
func (s *Service) Close() error {
	if s.root == nil {
		return nil
	}
	return s.root.Close()
}

// FileContent is the result of a read operation.
type FileContent struct {
	// Path is the workspace-relative, slash-separated path.
	Path string `json:"path"`
	// Size is the total file size in bytes.
	Size int64 `json:"size"`
	// Content is the returned (possibly truncated) file body.
	Content string `json:"content"`
	// Offset is the byte offset the content starts at.
	Offset int64 `json:"offset"`
	// Truncated reports whether content stops before the end of the file.
	Truncated bool `json:"truncated"`
}

// Read returns up to limit bytes of the file starting at offset. A limit of
// zero (or one greater than the configured maximum) is clamped to the service
// maximum.
func (s *Service) Read(ctx context.Context, path string, offset, limit int64) (*FileContent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	name, err := s.resolve(path)
	if err != nil {
		return nil, err
	}

	f, err := s.root.Open(name)
	if err != nil {
		return nil, mapErr(path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, mapErr(path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, path)
	}

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > s.maxReadBytes {
		limit = s.maxReadBytes
	}

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
	}

	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, err
	}

	total := info.Size()
	return &FileContent{
		Path:      relName(name),
		Size:      total,
		Content:   string(data),
		Offset:    offset,
		Truncated: offset+int64(len(data)) < total,
	}, nil
}

// WriteOptions tunes a write or append operation.
type WriteOptions struct {
	// CreateDirs creates missing parent directories when true.
	CreateDirs bool
	// Mode is the permission applied to newly created files. Defaults to 0644.
	Mode fs.FileMode
	// Overwrite permits replacing an existing file. Defaults to true.
	Overwrite bool
	// OverwriteSet records whether Overwrite was explicitly provided.
	OverwriteSet bool
}

// WriteResult describes the outcome of a write or append operation.
type WriteResult struct {
	Path    string `json:"path"`
	Bytes   int    `json:"bytes"`
	Created bool   `json:"created"`
}

// ReadFile returns the entire content of a file, ignoring the MaxReadBytes
// limit. It exists for server-side persistence (such as the diary store);
// client-facing reads must go through Read, which enforces the limit.
func (s *Service) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	name, err := s.resolve(path)
	if err != nil {
		return nil, err
	}

	b, err := s.root.ReadFile(name)
	if err != nil {
		return nil, mapErr(path, err)
	}
	return b, nil
}

// WriteAtomic writes content by creating a temporary file next to the target
// and renaming it onto the target, so readers never observe a partially
// written file. It is intended for server-side persistence; client-facing
// writes should use Write.
func (s *Service) WriteAtomic(ctx context.Context, path, content string, opts WriteOptions) (*WriteResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.readOnly {
		return nil, ErrReadOnly
	}

	name, err := s.resolve(path)
	if err != nil {
		return nil, err
	}
	if name == "." {
		return nil, fmt.Errorf("%w: %s", ErrIsDirectory, path)
	}
	if opts.Mode == 0 {
		opts.Mode = 0o644
	}
	if opts.CreateDirs {
		if dir := filepath.Dir(name); dir != "." {
			if err := s.root.MkdirAll(dir, 0o755); err != nil {
				return nil, mapErr(path, err)
			}
		}
	}

	_, statErr := s.root.Stat(name)
	exists := statErr == nil

	// os.Root refuses to open a temporary path that is (or is reached through)
	// a symlink escaping the root, so a planted link cannot redirect the write.
	tmp := name + ".tmp"
	f, err := s.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, opts.Mode)
	if err != nil {
		return nil, mapErr(path+".tmp", err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		_ = s.root.Remove(tmp)
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = s.root.Remove(tmp)
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = s.root.Remove(tmp)
		return nil, err
	}
	if err := s.root.Rename(tmp, name); err != nil {
		_ = s.root.Remove(tmp)
		return nil, mapErr(path, err)
	}

	return &WriteResult{Path: relName(name), Bytes: len(content), Created: !exists}, nil
}

// Write creates or overwrites a file with the given content.
func (s *Service) Write(ctx context.Context, path, content string, opts WriteOptions) (*WriteResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.readOnly {
		return nil, ErrReadOnly
	}

	name, err := s.resolve(path)
	if err != nil {
		return nil, err
	}
	if name == "." {
		return nil, fmt.Errorf("%w: %s", ErrIsDirectory, path)
	}

	if opts.Mode == 0 {
		opts.Mode = 0o644
	}
	overwrite := !opts.OverwriteSet || opts.Overwrite

	if opts.CreateDirs {
		if dir := filepath.Dir(name); dir != "." {
			if err := s.root.MkdirAll(dir, 0o755); err != nil {
				return nil, mapErr(path, err)
			}
		}
	}

	_, statErr := s.root.Stat(name)
	exists := statErr == nil
	if exists && !overwrite {
		return nil, fmt.Errorf("file already exists: %s", path)
	}

	f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, opts.Mode)
	if err != nil {
		return nil, mapErr(path, err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return nil, err
	}

	return &WriteResult{Path: relName(name), Bytes: len(content), Created: !exists}, nil
}

// Append appends content to a file, creating it when necessary.
func (s *Service) Append(ctx context.Context, path, content string, opts WriteOptions) (*WriteResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.readOnly {
		return nil, ErrReadOnly
	}

	name, err := s.resolve(path)
	if err != nil {
		return nil, err
	}
	if name == "." {
		return nil, fmt.Errorf("%w: %s", ErrIsDirectory, path)
	}

	if opts.Mode == 0 {
		opts.Mode = 0o644
	}
	if opts.CreateDirs {
		if dir := filepath.Dir(name); dir != "." {
			if err := s.root.MkdirAll(dir, 0o755); err != nil {
				return nil, mapErr(path, err)
			}
		}
	}

	_, statErr := s.root.Stat(name)
	created := errors.Is(statErr, fs.ErrNotExist)

	f, err := s.root.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, opts.Mode)
	if err != nil {
		return nil, mapErr(path, err)
	}
	defer f.Close()

	n, err := f.WriteString(content)
	if err != nil {
		return nil, err
	}

	return &WriteResult{Path: relName(name), Bytes: n, Created: created}, nil
}

// Entry describes a directory entry returned by List.
type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	ModTime time.Time `json:"mod_time"`
}

// List returns the entries of a directory, directories first then files, each
// group sorted by name.
func (s *Service) List(ctx context.Context, path string) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	name, err := s.resolve(path)
	if err != nil {
		return nil, err
	}

	f, err := s.root.Open(name)
	if err != nil {
		return nil, mapErr(path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, mapErr(path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, path)
	}

	dirEntries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fi, err := de.Info()
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{
			Name:    de.Name(),
			Path:    relName(filepath.Join(name, de.Name())),
			IsDir:   de.IsDir(),
			Size:    fi.Size(),
			Mode:    fi.Mode().String(),
			ModTime: fi.ModTime(),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})

	return entries, nil
}

// FileInfo describes a single path.
type FileInfo struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	ModTime time.Time `json:"mod_time"`
}

// Stat returns metadata about a path without following symlinks.
func (s *Service) Stat(ctx context.Context, path string) (*FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	name, err := s.resolve(path)
	if err != nil {
		return nil, err
	}

	info, err := s.root.Lstat(name)
	if err != nil {
		return nil, mapErr(path, err)
	}

	return &FileInfo{
		Path:    relName(name),
		Name:    info.Name(),
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		ModTime: info.ModTime(),
	}, nil
}

// Delete removes a file or directory. Directories require recursive to be
// deleted; the workspace root itself can never be deleted.
func (s *Service) Delete(ctx context.Context, path string, recursive bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.readOnly {
		return ErrReadOnly
	}

	name, err := s.resolve(path)
	if err != nil {
		return err
	}
	if name == "." {
		return fmt.Errorf("refusing to delete workspace root")
	}

	info, err := s.root.Lstat(name)
	if err != nil {
		return mapErr(path, err)
	}

	if info.IsDir() {
		if !recursive {
			return fmt.Errorf("%w: %s (set recursive=true to delete)", ErrIsDirectory, path)
		}
		return mapErr(path, s.root.RemoveAll(name))
	}

	return mapErr(path, s.root.Remove(name))
}

// Mkdir creates a directory. When all is true missing parents are created and
// existing directories are accepted.
func (s *Service) Mkdir(ctx context.Context, path string, all bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.readOnly {
		return ErrReadOnly
	}

	name, err := s.resolve(path)
	if err != nil {
		return err
	}
	if name == "." {
		return nil
	}

	if all {
		return mapErr(path, s.root.MkdirAll(name, 0o755))
	}
	return mapErr(path, s.root.Mkdir(name, 0o755))
}

// resolve turns a caller-supplied path into a name suitable for os.Root,
// rejecting lexical escapes. Symlink escapes are rejected independently by
// os.Root when the operation is performed.
func (s *Service) resolve(path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		p = "."
	}

	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(s.rootPath, filepath.Clean(p))
		if err != nil {
			return "", fmt.Errorf("%w: %q", ErrOutsideRoot, path)
		}
		p = rel
	}

	p = filepath.Clean(p)
	if p == ".." || strings.HasPrefix(p, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %q", ErrOutsideRoot, path)
	}
	return p, nil
}

// mapErr translates os.Root/os errors into the package sentinels. os.Root
// rejects traversal with an unexported sentinel wrapped in a *fs.PathError and
// exposes no exported predicate, so the escape case is matched by its stable
// message.
func mapErr(path string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	if strings.Contains(err.Error(), "escapes from parent") {
		return fmt.Errorf("%w: %q", ErrOutsideRoot, path)
	}
	return err
}

// relName renders a root-relative name as a slash-separated path.
func relName(name string) string {
	return filepath.ToSlash(filepath.Clean(name))
}
