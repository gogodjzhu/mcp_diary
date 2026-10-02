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
	EntryID   string    `json:"entry_id"`
	DiaryDate string    `json:"diary_date"`
	Content   string    `json:"content"`
	Status    string    `json:"status"`
	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
