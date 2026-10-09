package diarysync

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
	if !strings.HasPrefix(got, daySeparator+"\n\n") {
		t.Fatalf("should start with the day separator, got %q", got)
	}
	if strings.Contains(got, "# 2026-10") {
		t.Fatalf("month title should be gone:\n%s", got)
	}
	if !strings.Contains(got, "2026-10-04，日") || !strings.Contains(got, "2026-10-01，四") {
		t.Fatalf("missing day headers with weekday:\n%s", got)
	}
	if strings.Index(got, "2026-10-04") > strings.Index(got, "2026-10-01") {
		t.Fatalf("days not descending:\n%s", got)
	}
	if strings.Contains(got, "other month") {
		t.Fatal("leaked other month")
	}
	if MonthPath("2026-10") != "2026/2026-10.md" {
		t.Fatalf("path = %s", MonthPath("2026-10"))
	}
}

func TestMergeMonthPreservesUnownedRemoteDays(t *testing.T) {
	remote := []byte(daySeparator + "\n\n2026-10-01，四\n\nold remote day\n")
	entries := []diary.Entry{
		{EntryID: "de_1", DiaryDate: "2026-10-01", Content: "local overwrite attempt", Revision: 1},
		{EntryID: "de_2", DiaryDate: "2026-10-04", Content: "new local day", Revision: 1},
	}
	// managed is nil: the medium owns nothing yet, so 10-01 stays remote.
	got, changed := MergeMonth(remote, entries, "2026-10", nil)
	if !changed {
		t.Fatal("expected change")
	}
	if strings.Contains(string(got), "local overwrite attempt") {
		t.Fatalf("existing remote day was overwritten:\n%s", got)
	}
	if !strings.Contains(string(got), "old remote day") {
		t.Fatalf("existing remote day lost:\n%s", got)
	}
	if !strings.Contains(string(got), "new local day") || !strings.Contains(string(got), "2026-10-04") {
		t.Fatalf("new local day missing:\n%s", got)
	}
}

func TestMergeMonthReplacesOnlyManagedDays(t *testing.T) {
	remote := []byte(
		daySeparator + "\n\n2026-10-02，五\n\nremote only\n" +
			daySeparator + "\n\n2026-10-01，四\n\nmanaged old\n",
	)
	entries := []diary.Entry{
		{EntryID: "de_1", DiaryDate: "2026-10-01", Content: "managed new", Revision: 2},
		{EntryID: "de_2", DiaryDate: "2026-10-03", Content: "brand new", Revision: 1},
	}
	got, changed := MergeMonth(remote, entries, "2026-10", []string{"2026-10-01"})
	if !changed {
		t.Fatal("expected change")
	}
	body := string(got)
	if strings.Contains(body, "managed old") || !strings.Contains(body, "managed new") {
		t.Fatalf("managed day not replaced:\n%s", body)
	}
	if !strings.Contains(body, "remote only") {
		t.Fatalf("unowned remote day lost:\n%s", body)
	}
	if !strings.Contains(body, "brand new") {
		t.Fatalf("new local day missing:\n%s", body)
	}
}

func TestMergeMonthDropsManagedDayWhenDeletedLocally(t *testing.T) {
	remote := []byte(
		daySeparator + "\n\n2026-10-02，五\n\nkeep me\n" +
			daySeparator + "\n\n2026-10-01，四\n\ndelete me\n",
	)
	got, changed := MergeMonth(remote, nil, "2026-10", []string{"2026-10-01"})
	if !changed {
		t.Fatal("expected change")
	}
	body := string(got)
	if strings.Contains(body, "delete me") {
		t.Fatalf("managed day should be dropped:\n%s", body)
	}
	if !strings.Contains(body, "keep me") {
		t.Fatalf("unowned remote day should stay:\n%s", body)
	}
}

func TestMergeMonthDeletesWhenOnlyManagedRemains(t *testing.T) {
	remote := []byte(daySeparator + "\n\n2026-10-01，四\n\nonly managed\n")
	got, changed := MergeMonth(remote, nil, "2026-10", []string{"2026-10-01"})
	if !changed {
		t.Fatal("expected change")
	}
	if got != nil {
		t.Fatalf("expected nil body (delete), got:\n%s", got)
	}
}

func TestMergeMonthNoChangeReturnsRemote(t *testing.T) {
	remote := []byte(daySeparator + "\n\n2026-10-01，四\n\nremote only\n")
	entries := []diary.Entry{}
	got, changed := MergeMonth(remote, entries, "2026-10", nil)
	if changed || string(got) != string(remote) {
		t.Fatalf("expected remote unchanged, got changed=%v:\n%s", changed, got)
	}
}

func TestMergeMonthUnmanagedAdjacentDaysStayStable(t *testing.T) {
	remote := []byte(
		daySeparator + "\n\n2026-10-03，六\n\nthird\n\n" +
			daySeparator + "\n\n2026-10-02，五\n\nsecond\n\n" +
			daySeparator + "\n\n2026-10-01，四\n\nfirst\n",
	)
	first, changed := MergeMonth(remote, nil, "2026-10", nil)
	if changed {
		t.Fatalf("canonical unmanaged days should not change on first merge, got:\n%s", first)
	}
	if string(first) != string(remote) {
		t.Fatalf("first merge bytes differ from remote:\n%s", first)
	}
	second, changed := MergeMonth(first, nil, "2026-10", nil)
	if changed {
		t.Fatal("second merge must report changed=false")
	}
	if string(second) != string(first) {
		t.Fatalf("second merge bytes differ:\n%s", second)
	}
}

func TestMergeMonthParsesLegacyHeading(t *testing.T) {
	remote := []byte("# 2026-10\n\n## 2026-10-01\n\nlegacy day\n")
	entries := []diary.Entry{
		{EntryID: "de_1", DiaryDate: "2026-10-01", Content: "local", Revision: 1},
		{EntryID: "de_2", DiaryDate: "2026-10-02", Content: "added", Revision: 1},
	}
	// 10-01 is pre-existing (unmanaged) so the legacy block is kept; 10-02 is
	// appended.
	got, _ := MergeMonth(remote, entries, "2026-10", nil)
	body := string(got)
	if strings.Count(body, "2026-10-01") != 1 {
		t.Fatalf("legacy day duplicated:\n%s", body)
	}
	if !strings.Contains(body, "legacy day") || !strings.Contains(body, "added") {
		t.Fatalf("merge result wrong:\n%s", body)
	}
}

func TestRenderMonthIncludesMetadata(t *testing.T) {
	entries := []diary.Entry{
		{
			EntryID:   "de_1",
			DiaryDate: "2026-09-30",
			Content:   "with meta",
			Revision:  1,
			Meta:      &diary.EntryMeta{Lunar: "八月二十", Weekday: "三", Weather: "🌧️"},
		},
	}
	got := string(RenderMonth(entries, "2026-09"))
	if !strings.Contains(got, "2026-09-30，八月二十，三，🌧️") {
		t.Fatalf("header missing metadata:\n%s", got)
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
