package mcpui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestViewsRenderBridge(t *testing.T) {
	bridge, err := os.ReadFile(filepath.Join("assets", "bridge.js"))
	if err != nil {
		t.Fatalf("read bridge: %v", err)
	}

	views := Views()
	if len(views) != 2 {
		t.Fatalf("Views() returned %d views, want 2", len(views))
	}

	seen := make(map[string]bool)
	for _, view := range views {
		if !strings.HasPrefix(view.URI, "ui://") {
			t.Errorf("%s: URI %q must use the ui:// scheme", view.Name, view.URI)
		}
		if seen[view.URI] {
			t.Errorf("%s: duplicate URI %q", view.Name, view.URI)
		}
		seen[view.URI] = true
		if view.HTML, err = Render(view.HTML); err != nil {
			t.Fatalf("%s: render: %v", view.Name, err)
		}
		if strings.Contains(string(view.HTML), bridgeMarker) {
			t.Errorf("%s: bridge placeholder was not replaced", view.Name)
		}
		if !bytes.Contains(view.HTML, bridge) {
			t.Errorf("%s: bridge script missing", view.Name)
		}
		if !bytes.Contains(view.HTML, []byte(`<meta charset="utf-8"`)) {
			t.Errorf("%s: missing charset declaration", view.Name)
		}
	}
}

func TestRenderIsIdempotent(t *testing.T) {
	raw := mustAsset("assets/entry.html")
	once, err := Render(raw)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	twice, err := Render(once)
	if err != nil {
		t.Fatalf("Render twice: %v", err)
	}
	if !bytes.Equal(once, twice) {
		t.Error("Render is not idempotent")
	}
}

func TestViewsHaveTools(t *testing.T) {
	metas := ToolMetas()
	for _, view := range Views() {
		if len(view.Tools) == 0 {
			t.Errorf("%s: no tools associated", view.Name)
		}
		for _, tool := range view.Tools {
			meta := metas[tool]
			ui, ok := meta["ui"].(map[string]any)
			if !ok {
				t.Fatalf("%s: tool %q has no ui metadata", view.Name, tool)
			}
			if ui["resourceUri"] != view.URI {
				t.Errorf("%s: tool %q maps to %v", view.Name, tool, ui["resourceUri"])
			}
			if meta["openai/outputTemplate"] != view.URI {
				t.Errorf("%s: tool %q missing ChatGPT outputTemplate alias", view.Name, tool)
			}
		}
	}
}

func TestMetaIsJSONEncodable(t *testing.T) {
	data, err := json.Marshal(View{URI: EntriesURI}.Meta())
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	var decoded map[string]map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if _, ok := decoded["ui"]["csp"]; !ok {
		t.Error("meta.ui.csp missing")
	}
}

// TestEntriesViewHasNoRaceProneReload guards the ChatGPT regression: tool
// input must only seed the query, and the render must come from onResult or
// the sequenced onToolResult, never from a request started by onInput.
func TestEntriesViewHasNoRaceProneReload(t *testing.T) {
	html := string(mustAsset("assets/entries.html"))
	if strings.Contains(html, "window.diary.onInput(function (input) {\n          readQuery(input);\n          // Rerun") {
		t.Fatal("onInput must not start a reload")
	}
	for _, want := range []string{
		"window.diary.onInput(readQuery)",
		"onToolResult(",
		"ask-delete",
		"deleteDiaryEntry",
		"expected_revision",
		"updateModelContext(",
		"B.sequence()",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("entries.html missing %q", want)
		}
	}
	if strings.Contains(html, "expandedIndex") {
		t.Error("entries.html still contains the pre-refresh render path")
	}
}

func TestBridgeExposesToolResultStream(t *testing.T) {
	js := string(mustAsset("assets/bridge.js"))
	for _, want := range []string{
		"onToolResult",
		"parseResult",
		"sequence",
		"updateModelContext",
		"ui/update-model-context",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("bridge.js missing %q", want)
		}
	}
}

func TestUIOnlyToolsAreDeclared(t *testing.T) {
	uiOnly := RegistryVisibility()
	if _, ok := uiOnly["deleteDiaryEntry"]; !ok {
		t.Fatal("deleteDiaryEntry must be app-only")
	}
	// Tools with the UI binding must never be app-only.
	for name := range ToolMetas() {
		if _, ok := uiOnly[name]; ok {
			t.Errorf("%s is both a render tool and app-only", name)
		}
	}
}
