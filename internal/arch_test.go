package internal_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// archRules maps an internal package group to the package groups it must not
// import. Groups are the first path segments below internal/. The rules encode
// the dependency direction: platform <- auth <- core <- transport <- app/cli.
var archRules = map[string][]string{
	"platform":  {"core", "transport", "auth", "app", "cli"},
	"auth":      {"core", "transport", "app", "cli"},
	"core":      {"transport", "auth/oauth", "app", "cli"},
	"transport": {"app", "cli", "auth/oauth"},
	"app":       {},
	"cli":       {},
}

// groupOf returns the package group (and the remainder of the path) for an
// internal import path, e.g. "github.com/gogodjzhu/mcp-diary/internal/auth/oauth"
// -> ("auth", "auth/oauth").
func groupOf(importPath string) (group, rest string, ok bool) {
	const prefix = "github.com/gogodjzhu/mcp-diary/internal/"
	if !strings.HasPrefix(importPath, prefix) {
		return "", "", false
	}
	rest = strings.TrimPrefix(importPath, prefix)
	group, _, _ = strings.Cut(rest, "/")
	return group, rest, true
}

func TestInternalLayering(t *testing.T) {
	fset := token.NewFileSet()

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == "arch_test.go" {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		selfGroup, selfRest, ok := groupOf("github.com/gogodjzhu/mcp-diary/internal/" + strings.TrimSuffix(path, ".go"))
		if !ok {
			return nil
		}

		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			_, rest, ok := groupOf(importPath)
			if !ok {
				continue
			}
			for _, forbidden := range archRules[selfGroup] {
				if rest == forbidden || strings.HasPrefix(rest, forbidden+"/") {
					t.Errorf("%s: imports %q: %s must not depend on %s", path, importPath, selfRest, forbidden)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
