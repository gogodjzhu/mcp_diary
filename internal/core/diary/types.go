package diary

import "time"

const (
	StatusDraft     = "draft"
	StatusCommitted = "committed"
	StatusDiscarded = "discarded"
)

type Session struct {
	SessionID string    `json:"session_id"`
	DiaryDate string    `json:"diary_date"`
	Content   string    `json:"content"`
	Status    string    `json:"status"`
	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Entry struct {
	EntryID   string     `json:"entry_id"`
	DiaryDate string     `json:"diary_date"`
	Content   string     `json:"content"`
	Status    string     `json:"status"`
	Revision  int        `json:"revision"`
	Meta      *EntryMeta `json:"meta,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// EntryMeta is derived source data attached to a committed entry: the Chinese
// lunar date, the weekday and, when enabled, the day's weather. It is
// best-effort and may be partially populated.
type EntryMeta struct {
	Lunar       string    `json:"lunar,omitempty"`
	Weekday     string    `json:"weekday,omitempty"`
	Weather     string    `json:"weather,omitempty"`
	WeatherCode int       `json:"weather_code,omitempty"`
	Location    string    `json:"location,omitempty"`
	Source      string    `json:"source,omitempty"`
	FetchedAt   time.Time `json:"fetched_at,omitempty"`
}

// SessionContent is the result of appending to or replacing a draft session.
type SessionContent struct {
	SessionID string    `json:"session_id"`
	Revision  int       `json:"revision"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CommittedEntry is the result of committing a draft into a formal entry.
type CommittedEntry struct {
	EntryID   string     `json:"entry_id"`
	SessionID string     `json:"session_id"`
	DiaryDate string     `json:"diary_date"`
	Content   string     `json:"content"`
	Status    string     `json:"status"`
	Meta      *EntryMeta `json:"meta,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// DiscardedSession is the result of discarding an uncommitted draft.
type DiscardedSession struct {
	SessionID string    `json:"session_id"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EntryRevision is the result of replacing a committed entry's content.
type EntryRevision struct {
	EntryID   string    `json:"entry_id"`
	Revision  int       `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DeletedEntry is the result of deleting a committed entry.
type DeletedEntry struct {
	EntryID string `json:"entry_id"`
	Deleted bool   `json:"deleted"`
}

// EntrySummary is a single item in a listed page of committed entries.
type EntrySummary struct {
	EntryID   string     `json:"entry_id"`
	DiaryDate string     `json:"diary_date"`
	Content   string     `json:"content"`
	Meta      *EntryMeta `json:"meta,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// EntryList is a page of committed entries.
type EntryList struct {
	Entries  []EntrySummary `json:"entries"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Total    int            `json:"total"`
}

type Envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func OK(data any) Envelope {
	return Envelope{Code: 0, Message: "ok", Data: data}
}

func Fail(code int, message string) Envelope {
	return Envelope{Code: code, Message: message}
}
