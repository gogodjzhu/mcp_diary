// Package persist provides the JSON persistence helpers shared by the domain
// stores (diary and diarysync): decode a workspace-relative JSON document and
// atomically replace one.
package persist

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"

	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
)

// fileMode is applied to store files: owner read/write only.
const fileMode fs.FileMode = 0o600

// Load decodes the JSON document at path into dest. A missing or empty file is
// not an error: dest is left untouched so callers keep their initialized zero
// values.
func Load(ctx context.Context, fsys *filesystem.Service, path string, dest any) error {
	b, err := fsys.ReadFile(ctx, path)
	if err != nil {
		if errors.Is(err, filesystem.ErrNotFound) {
			return nil
		}
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, dest)
}

// Save atomically replaces the JSON document at path with an indented encoding
// of value, creating parent directories as needed.
func Save(ctx context.Context, fsys *filesystem.Service, path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fsys.WriteAtomic(ctx, path, string(b), filesystem.WriteOptions{
		CreateDirs: true,
		Mode:       fileMode,
	})
	return err
}
