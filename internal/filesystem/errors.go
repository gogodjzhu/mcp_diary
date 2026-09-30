package filesystem

import "errors"

var (
	// ErrOutsideRoot is returned when a request tries to touch a path outside
	// the configured workspace root.
	ErrOutsideRoot = errors.New("path escapes workspace root")
	// ErrReadOnly is returned when a mutating operation is attempted on a
	// read-only service.
	ErrReadOnly = errors.New("filesystem is read-only")
	// ErrNotRegular is returned when an operation requires a regular file but
	// the path points to a directory or a special file.
	ErrNotRegular = errors.New("path is not a regular file")
	// ErrIsDirectory is returned when a delete targets a non-empty directory
	// without the recursive option.
	ErrIsDirectory = errors.New("path is a directory")
	// ErrTooLarge is returned when a read would exceed the configured limit.
	ErrTooLarge = errors.New("requested size exceeds limit")
	// ErrNotFound is returned when the target path does not exist.
	ErrNotFound = errors.New("path not found")
)
