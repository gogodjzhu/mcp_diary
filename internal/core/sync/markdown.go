package sync

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/diary"
)

const monthLayout = "2006-01"

func MonthKey(date string) (string, error) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidMonth, date)
	}
	return t.Format(monthLayout), nil
}

func MonthPath(month string) string {
	year := month
	if len(month) >= 4 {
		year = month[:4]
	}
	return fmt.Sprintf("diary/%s/%s.md", year, month)
}

func MonthsFromDates(dates ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(dates))
	for _, date := range dates {
		month, err := MonthKey(date)
		if err != nil {
			continue
		}
		if _, ok := seen[month]; ok {
			continue
		}
		seen[month] = struct{}{}
		out = append(out, month)
	}
	sort.Strings(out)
	return out
}

func EntriesForMonth(entries []diary.Entry, month string) []diary.Entry {
	out := make([]diary.Entry, 0)
	for _, e := range entries {
		key, err := MonthKey(e.DiaryDate)
		if err != nil || key != month {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DiaryDate == out[j].DiaryDate {
			return out[i].EntryID > out[j].EntryID
		}
		return out[i].DiaryDate > out[j].DiaryDate
	})
	return out
}

func RenderMonth(entries []diary.Entry, month string) []byte {
	monthEntries := EntriesForMonth(entries, month)
	if len(monthEntries) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", month)
	currentDay := ""
	for _, e := range monthEntries {
		if e.DiaryDate != currentDay {
			currentDay = e.DiaryDate
			fmt.Fprintf(&b, "\n## %s\n\n", e.DiaryDate)
		} else {
			b.WriteString("\n")
		}
		content := strings.TrimRight(e.Content, "\n")
		if content == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(content)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func SourceFrom(entries []diary.Entry) Source {
	src := Source{EntryIDs: make([]string, 0, len(entries))}
	for _, e := range entries {
		src.EntryIDs = append(src.EntryIDs, e.EntryID)
		if e.Revision > src.Revision {
			src.Revision = e.Revision
		}
		if e.UpdatedAt.After(src.UpdatedAt) {
			src.UpdatedAt = e.UpdatedAt
		}
	}
	return src
}

func MonthUnchanged(state *SyncState, path string, src Source) bool {
	if state == nil {
		return false
	}
	ds, ok := state.Documents[path]
	if !ok {
		return false
	}
	if ds.Revision != src.Revision || !sameIDs(ds.EntryIDs, src.EntryIDs) {
		return false
	}
	return true
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([]string(nil), a...)
	right := append([]string(nil), b...)
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
