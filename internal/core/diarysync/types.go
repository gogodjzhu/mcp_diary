package diarysync

import (
	"log/slog"
	"time"
)

type Kind string

const (
	KindGitHub    Kind = "github"
	KindQuark     Kind = "quark"
	KindLocalDisk Kind = "local_disk"
)

func (k Kind) Valid() bool {
	switch k {
	case KindGitHub, KindQuark, KindLocalDisk:
		return true
	default:
		return false
	}
}

type DocumentKind string

const (
	DocumentText       DocumentKind = "text"
	DocumentAttachment DocumentKind = "attachment"
)

type Status string

const (
	StatusIdle      Status = "idle"
	StatusSyncing   Status = "syncing"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

type Secret struct {
	Ciphertext string `json:"ciphertext,omitempty"`
}

func (s Secret) String() string {
	if s.Ciphertext == "" {
		return ""
	}
	return "[redacted]"
}

func (s Secret) GoString() string { return s.String() }

func (s Secret) LogValue() slog.Value {
	if s.Ciphertext == "" {
		return slog.StringValue("")
	}
	return slog.StringValue("[redacted]")
}

func (m Medium) LogValue() slog.Value {
	return slog.AnyValue(m.Public())
}

type Medium struct {
	ID        string            `json:"id"`
	Kind      Kind              `json:"kind"`
	Name      string            `json:"name"`
	Enabled   bool              `json:"enabled"`
	Settings  map[string]string `json:"settings,omitempty"`
	Secret    Secret            `json:"secret"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type PublicMedium struct {
	ID            string            `json:"id"`
	Kind          Kind              `json:"kind"`
	Name          string            `json:"name"`
	Enabled       bool              `json:"enabled"`
	Settings      map[string]string `json:"settings,omitempty"`
	HasCredential bool              `json:"has_credential"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

func (m Medium) Public() PublicMedium {
	return PublicMedium{
		ID:            m.ID,
		Kind:          m.Kind,
		Name:          m.Name,
		Enabled:       m.Enabled,
		Settings:      cloneSettings(m.Settings),
		HasCredential: m.Secret.Ciphertext != "",
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

func (m Medium) String() string {
	return "Medium{id=" + m.ID + " kind=" + string(m.Kind) + " name=" + m.Name + "}"
}

type Source struct {
	EntryIDs  []string  `json:"entry_ids,omitempty"`
	Dates     []string  `json:"dates,omitempty"`
	Revision  int       `json:"revision,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type Attachment struct {
	MediaType string `json:"media_type,omitempty"`
	Size      int64  `json:"size,omitempty"`
	EntryID   string `json:"entry_id,omitempty"`
}

type Document struct {
	Kind        DocumentKind
	Path        string
	Body        []byte
	ContentType string
	Source      Source
	Attachment  *Attachment
}

type DocumentRef struct {
	Kind DocumentKind
	Path string
}

type Result struct {
	Path     string
	RemoteID string
	SyncedAt time.Time
}

type RemoteStatus struct {
	Path      string
	Exists    bool
	RemoteID  string
	UpdatedAt time.Time
}

// VerifyCheck is one step of a connection probe.
type VerifyCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// VerifyResult is the structured outcome of probing a storage medium.
type VerifyResult struct {
	OK     bool          `json:"ok"`
	Checks []VerifyCheck `json:"checks"`
}

type DocumentState struct {
	Path     string   `json:"path"`
	RemoteID string   `json:"remote_id,omitempty"`
	EntryIDs []string `json:"entry_ids,omitempty"`
	// Dates are the diary dates this medium last wrote. Days are only ever
	// replaced or removed while they are listed here; anything else in the
	// remote file is treated as pre-existing and preserved.
	Dates        []string  `json:"dates,omitempty"`
	Revision     int       `json:"revision,omitempty"`
	LastSyncedAt time.Time `json:"last_synced_at,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
}

type SyncState struct {
	MediumID     string                   `json:"medium_id"`
	Status       Status                   `json:"status"`
	LastError    string                   `json:"last_error,omitempty"`
	LastSyncedAt *time.Time               `json:"last_synced_at,omitempty"`
	Documents    map[string]DocumentState `json:"documents,omitempty"`
}

type MediumView struct {
	PublicMedium
	State SyncState `json:"state"`
}

func cloneSettings(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneMedium(src *Medium) *Medium {
	if src == nil {
		return nil
	}
	cp := *src
	cp.Settings = cloneSettings(src.Settings)
	return &cp
}

func cloneState(src *SyncState) *SyncState {
	if src == nil {
		return nil
	}
	cp := *src
	if src.LastSyncedAt != nil {
		t := *src.LastSyncedAt
		cp.LastSyncedAt = &t
	}
	if len(src.Documents) == 0 {
		cp.Documents = map[string]DocumentState{}
		return &cp
	}
	cp.Documents = make(map[string]DocumentState, len(src.Documents))
	for k, v := range src.Documents {
		v.EntryIDs = cloneStrings(v.EntryIDs)
		v.Dates = cloneStrings(v.Dates)
		cp.Documents[k] = v
	}
	return &cp
}
