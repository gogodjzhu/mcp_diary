package diarysync

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/diary"
)

const monthLayout = "2006-01"

// daySeparator is the line that introduces each day block in the exported
// markdown: six full-width em dashes.
const daySeparator = "——————"

// dateLeadRE matches a day header line that starts with a YYYY-MM-DD date,
// optionally followed by "，农历，星期，天气" metadata.
var dateLeadRE = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})`)

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
	return fmt.Sprintf("%s/%s.md", year, month)
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
	blocks := make([]string, 0, len(monthEntries))
	for _, e := range monthEntries {
		blocks = append(blocks, renderDayBlock(e))
	}
	return []byte(strings.Join(blocks, "\n"))
}

// renderDayBlock renders a single day as its own separator-introduced block.
// The trailing newline is part of the block; callers join blocks with an extra
// newline to leave a blank line between days.
func renderDayBlock(e diary.Entry) string {
	var b strings.Builder
	b.WriteString(daySeparator)
	b.WriteString("\n\n")
	b.WriteString(FormatDayHeader(e.DiaryDate, e.Meta))
	b.WriteString("\n")
	content := strings.TrimRight(e.Content, "\n")
	if content != "" {
		b.WriteString("\n")
		b.WriteString(content)
		b.WriteString("\n")
	}
	return b.String()
}

// FormatDayHeader builds the day header line shared by rendering and merging:
// "YYYY-MM-DD，农历，星期，天气". Fields that are unavailable are omitted while
// keeping the remaining order. The weekday falls back to a computed value when
// the entry has no stored metadata.
func FormatDayHeader(date string, meta *diary.EntryMeta) string {
	fields := []string{date}
	if meta != nil && meta.Lunar != "" {
		fields = append(fields, meta.Lunar)
	}
	weekday := ""
	if meta != nil {
		weekday = meta.Weekday
	}
	if weekday == "" {
		if computed, err := weekdayCN(date); err == nil {
			weekday = computed
		}
	}
	if weekday != "" {
		fields = append(fields, weekday)
	}
	if meta != nil && meta.Weather != "" {
		fields = append(fields, meta.Weather)
	}
	return strings.Join(fields, "，")
}

var weekdayNames = [...]string{"日", "一", "二", "三", "四", "五", "六"}

func weekdayCN(date string) (string, error) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", err
	}
	return weekdayNames[t.Weekday()], nil
}

// dayBlock is one day section parsed out of a remote month file. raw preserves
// the original bytes so merges never rewrite existing content.
type dayBlock struct {
	date string
	raw  string
}

// ParseMonth splits a month file into its preamble and day blocks. It accepts
// both the exported format (a daySeparator line followed by a date header) and
// the legacy format ("## YYYY-MM-DD") so pre-existing remote files merge
// cleanly.
func ParseMonth(body []byte) (preamble string, blocks []dayBlock) {
	lines := splitLines(body)
	var starts []int
	var dates []string
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimRight(lines[i], "\r\n")
		if strings.TrimSpace(trimmed) == daySeparator {
			j := i + 1
			for j < len(lines) && strings.TrimSpace(strings.TrimRight(lines[j], "\r\n")) == "" {
				j++
			}
			if j < len(lines) {
				if date, ok := dateFromHeader(lines[j]); ok {
					starts = append(starts, i)
					dates = append(dates, date)
				}
			}
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			if date, ok := dateFromHeader(strings.TrimPrefix(trimmed, "## ")); ok {
				starts = append(starts, i)
				dates = append(dates, date)
			}
		}
	}
	if len(starts) == 0 {
		return string(body), nil
	}
	if starts[0] > 0 {
		preamble = strings.Join(lines[:starts[0]], "")
	}
	for k, start := range starts {
		end := len(lines)
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		blocks = append(blocks, dayBlock{
			date: dates[k],
			raw:  strings.Join(lines[start:end], ""),
		})
	}
	return preamble, blocks
}

// MergeMonth reconciles remote with the local month entries.
//
// The medium only ever replaces or removes day blocks whose dates are listed
// in managed (the dates it wrote last time). Every other block is treated as
// pre-existing remote content and preserved byte-for-byte. Local days that do
// not exist remotely are appended. Remote preamble (bytes before the first
// day block) is preserved as-is, is not rewritten, and is emitted once.
//
// It returns the merged body plus whether it differs from remote. A nil body
// with changed=true means nothing remains in the file and it should be
// deleted.
func MergeMonth(remote []byte, entries []diary.Entry, month string, managed []string) ([]byte, bool) {
	local := EntriesForMonth(entries, month)
	if len(remote) == 0 {
		body := RenderMonth(entries, month)
		return body, len(body) > 0
	}

	managedSet := make(map[string]struct{}, len(managed))
	for _, d := range managed {
		managedSet[d] = struct{}{}
	}
	localByDate := make(map[string]diary.Entry, len(local))
	for _, e := range local {
		if _, ok := localByDate[e.DiaryDate]; !ok {
			localByDate[e.DiaryDate] = e
		}
	}

	preamble, blocks := ParseMonth(remote)
	if len(blocks) == 0 {
		// The remote file is not in a shape we understand: never destroy it,
		// just append local days that are not already visible.
		if len(local) == 0 {
			return remote, false
		}
		return appendDays(remote, local), true
	}

	remoteDates := make(map[string]struct{}, len(blocks))
	var out []string
	for _, b := range blocks {
		remoteDates[b.date] = struct{}{}
		if _, isManaged := managedSet[b.date]; isManaged {
			if e, ok := localByDate[b.date]; ok {
				out = append(out, renderDayBlock(e))
			}
			// Managed but no longer local: drop it.
			continue
		}
		out = append(out, strings.TrimRight(b.raw, "\n")+"\n")
	}
	for _, e := range local {
		if _, ok := remoteDates[e.DiaryDate]; ok {
			continue
		}
		out = append(out, renderDayBlock(e))
	}
	if len(out) == 0 {
		return nil, true
	}
	merged := []byte(strings.Join(out, "\n"))
	if !strings.HasSuffix(string(merged), "\n") {
		merged = append(merged, '\n')
	}
	if preamble != "" {
		merged = append([]byte(preamble), merged...)
	}
	if bytes.Equal(merged, remote) {
		return remote, false
	}
	return merged, true
}

func appendDays(remote []byte, days []diary.Entry) []byte {
	var b strings.Builder
	b.Write(bytes.TrimRight(remote, "\r\n"))
	b.WriteString("\n\n")
	for i, e := range days {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(renderDayBlock(e))
	}
	return []byte(b.String())
}

// dateFromHeader extracts a leading YYYY-MM-DD from a day header line.
func dateFromHeader(line string) (string, bool) {
	m := dateLeadRE.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", false
	}
	if _, err := time.Parse("2006-01-02", m[1]); err != nil {
		return "", false
	}
	return m[1], true
}

// splitLines splits body into lines, keeping the trailing newline on each line
// so a reassembly round-trips the original bytes.
func splitLines(body []byte) []string {
	if len(body) == 0 {
		return nil
	}
	parts := strings.SplitAfter(string(body), "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func SourceFrom(entries []diary.Entry) Source {
	src := Source{
		EntryIDs: make([]string, 0, len(entries)),
		Dates:    make([]string, 0, len(entries)),
	}
	for _, e := range entries {
		src.EntryIDs = append(src.EntryIDs, e.EntryID)
		src.Dates = append(src.Dates, e.DiaryDate)
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
