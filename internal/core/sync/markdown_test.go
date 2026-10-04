package sync

import (
	"strings"
	"testing"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/diary"
)

func TestRenderMonthOrdersDaysDescending(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	entries := []diary.Entry{
		{EntryID: "de_1", DiaryDate: "2026-10-01", Content: "first", Revision: 1, UpdatedAt: now},
		{EntryID: "de_2", DiaryDate: "2026-10-04", Content: "later", Revision: 2, UpdatedAt: now.Add(time.Hour)},
		{EntryID: "de_3", DiaryDate: "2026-09-30", Content: "other month", Revision: 1, UpdatedAt: now},
	}
	got := string(RenderMonth(entries, "2026-10"))
	if !strings.HasPrefix(got, "# 2026-10\n") {
		t.Fatalf("header = %q", got)
	}
	if strings.Index(got, "## 2026-10-04") > strings.Index(got, "## 2026-10-01") {
		t.Fatalf("days not descending:\n%s", got)
	}
	if strings.Contains(got, "other month") {
		t.Fatal("leaked other month")
	}
	if MonthPath("2026-10") != "diary/2026/2026-10.md" {
		t.Fatalf("path = %s", MonthPath("2026-10"))
	}
}

func TestMonthUnchangedUsesRevisionAndIDs(t *testing.T) {
	src := Source{EntryIDs: []string{"de_2", "de_1"}, Revision: 3}
	st := &SyncState{Documents: map[string]DocumentState{
		"diary/2026/2026-10.md": {EntryIDs: []string{"de_1", "de_2"}, Revision: 3},
	}}
	if !MonthUnchanged(st, "diary/2026/2026-10.md", src) {
		t.Fatal("expected unchanged")
	}
	src.Revision = 4
	if MonthUnchanged(st, "diary/2026/2026-10.md", src) {
		t.Fatal("revision bump should sync")
	}
}
