// Package filesystem provides a sandboxed file service. Every path handled by
// the service is resolved underneath a single workspace root, so MCP clients
// can never read or write arbitrary files on the host.
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

// Service is a sandboxed filesystem rooted at a single directory.
type Service struct {
	root         string
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

	// Resolve symlinks once so the sandbox boundary is stable.
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root symlinks: %w", err)
	}

	maxRead := opts.MaxReadBytes
	if maxRead <= 0 {
		maxRead = 1 << 20
	}

	return &Service{
		root:         filepath.Clean(real),
		readOnly:     opts.ReadOnly,
		maxReadBytes: maxRead,
	}, nil
}

// Root returns the absolute, symlink-resolved workspace root.
func (s *Service) Root() string { return s.root }

// ReadOnly reports whether mutating operations are disabled.
func (s *Service) ReadOnly() bool { return s.readOnly }

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

	abs, err := s.resolve(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, err
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

	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()

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
		Path:      s.rel(abs),
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

	abs, err := s.resolve(path)
	if err != nil {
		return nil, err
	}

	b, err := os.ReadFile(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, err
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

	abs, err := s.resolve(path)
	if err != nil {
		return nil, err
	}
	if abs == s.root {
		return nil, fmt.Errorf("%w: %s", ErrIsDirectory, path)
	}
	if opts.Mode == 0 {
		opts.Mode = 0o644
	}
	if opts.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, err
		}
	}

	_, statErr := os.Stat(abs)
	exists := statErr == nil

	// Refuse to follow a symlink planted at the temporary path: writing
	// through it could escape the sandbox.
	tmp := abs + ".tmp"
	if tmpInfo, err := os.Lstat(tmp); err == nil && tmpInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %s", ErrOutsideRoot, path+".tmp")
	}

	if err := os.WriteFile(tmp, []byte(content), opts.Mode); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}

	return &WriteResult{Path: s.rel(abs), Bytes: len(content), Created: !exists}, nil
}

// Write creates or overwrites a file with the given content.
func (s *Service) Write(ctx context.Context, path, content string, opts WriteOptions) (*WriteResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.readOnly {
		return nil, ErrReadOnly
	}

	abs, err := s.resolve(path)
	if err != nil {
		return nil, err
	}
	if abs == s.root {
		return nil, fmt.Errorf("%w: %s", ErrIsDirectory, path)
	}

	if opts.Mode == 0 {
		opts.Mode = 0o644
	}
	overwrite := !opts.OverwriteSet || opts.Overwrite

	if opts.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, err
		}
	}

	_, statErr := os.Stat(abs)
	exists := statErr == nil
	if exists && !overwrite {
		return nil, fmt.Errorf("file already exists: %s", path)
	}

	if err := os.WriteFile(abs, []byte(content), opts.Mode); err != nil {
		return nil, err
	}

	return &WriteResult{Path: s.rel(abs), Bytes: len(content), Created: !exists}, nil
}

// Append appends content to a file, creating it when necessary.
func (s *Service) Append(ctx context.Context, path, content string, opts WriteOptions) (*WriteResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.readOnly {
		return nil, ErrReadOnly
	}

	abs, err := s.resolve(path)
	if err != nil {
		return nil, err
	}
	if abs == s.root {
		return nil, fmt.Errorf("%w: %s", ErrIsDirectory, path)
	}

	if opts.Mode == 0 {
		opts.Mode = 0o644
	}
	if opts.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, err
		}
	}

	_, statErr := os.Stat(abs)
	created := errors.Is(statErr, fs.ErrNotExist)

	f, err := os.OpenFile(abs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, opts.Mode)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	n, err := f.WriteString(content)
	if err != nil {
		return nil, err
	}

	return &WriteResult{Path: s.rel(abs), Bytes: n, Created: created}, nil
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

	abs, err := s.resolve(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, path)
	}

	dirEntries, err := os.ReadDir(abs)
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
			Path:    s.rel(filepath.Join(abs, de.Name())),
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

	abs, err := s.resolve(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Lstat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, err
	}

	return &FileInfo{
		Path:    s.rel(abs),
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

	abs, err := s.resolve(path)
	if err != nil {
		return err
	}
	if abs == s.root {
		return fmt.Errorf("refusing to delete workspace root")
	}

	info, err := os.Lstat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return err
	}

	if info.IsDir() {
		if !recursive {
			return fmt.Errorf("%w: %s (set recursive=true to delete)", ErrIsDirectory, path)
		}
		return os.RemoveAll(abs)
	}

	return os.Remove(abs)
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

	abs, err := s.resolve(path)
	if err != nil {
		return err
	}
	if abs == s.root {
		return nil
	}

	if all {
		return os.MkdirAll(abs, 0o755)
	}
	return os.Mkdir(abs, 0o755)
}

// resolve turns a client supplied path into an absolute path guaranteed to live
// underneath the workspace root, rejecting lexical and symlink escapes.
func (s *Service) resolve(path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		p = "."
	}

	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Join(s.root, p)
	}

	if !s.within(abs) {
		return "", fmt.Errorf("%w: %q", ErrOutsideRoot, path)
	}

	// Guard against symlinks pointing outside the workspace.
	resolved, err := evalExisting(abs)
	if err != nil {
		return "", err
	}
	if !s.within(resolved) {
		return "", fmt.Errorf("%w: %q", ErrOutsideRoot, path)
	}

	return abs, nil
}

func (s *Service) within(p string) bool {
	if p == s.root {
		return true
	}
	return strings.HasPrefix(p, s.root+string(os.PathSeparator))
}

// rel returns a slash-separated path relative to the workspace root.
func (s *Service) rel(abs string) string {
	r, err := filepath.Rel(s.root, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(r)
}

// evalExisting resolves symlinks for the longest existing prefix of p and
// re-appends the non-existent tail, so checks work for files about to be
// created.
func evalExisting(p string) (string, error) {
	cur := filepath.Clean(p)
	var tail []string

	for {
		if _, err := os.Lstat(cur); err == nil {
			real, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			parts := append([]string{real}, tail...)
			return filepath.Join(parts...), nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(p), nil
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
}
