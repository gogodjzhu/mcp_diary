package diary

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"

	_ "github.com/gen2brain/heic"
)

const (
	codeOK         = 0
	codeBadRequest = 400
	codeNotFound   = 404
	codeConflict   = 409
	codeTooLarge   = 413
	codeInternal   = 500
)

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

type ChangeHook func(fs *filesystem.Service, dates ...string)

// MetaEnricher attaches derived metadata (lunar date, weekday, weather) to a
// newly committed entry. It must be best-effort: an empty or partial result is
// always acceptable.
type MetaEnricher interface {
	Enrich(ctx context.Context, fs *filesystem.Service, date string) EntryMeta
}

type Service struct {
	now  func() time.Time
	loc  *time.Location
	hook ChangeHook
	meta MetaEnricher

	mu     sync.Mutex
	stores map[string]*Store
}

func New(now func() time.Time, loc *time.Location) *Service {
	if now == nil {
		now = func() time.Time { return time.Now() }
	}
	if loc == nil {
		loc = time.Local
	}
	return &Service{
		now:    now,
		loc:    loc,
		stores: map[string]*Store{},
	}
}

func (s *Service) SetChangeHook(h ChangeHook) {
	s.hook = h
}

// SetMetaEnricher installs the metadata resolver used when committing entries.
func (s *Service) SetMetaEnricher(m MetaEnricher) {
	s.meta = m
}

func (s *Service) emitChange(fs *filesystem.Service, dates ...string) {
	if s.hook == nil || fs == nil {
		return
	}
	s.hook(fs, dates...)
}

// storeFor returns the persisted store for the workspace behind fs, loading it
// from the sandbox on first use. Stores are cached per workspace root.
func (s *Service) storeFor(ctx context.Context, fs *filesystem.Service) (*Store, error) {
	if fs == nil {
		return nil, errf(codeInternal, "no filesystem bound to the request")
	}
	root := fs.Root()
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.stores[root]; ok {
		return st, nil
	}
	st, err := loadStore(ctx, fs, s.now)
	if err != nil {
		return nil, err
	}
	s.stores[root] = st
	return st, nil
}

type CreateSessionIn struct {
	RequestID string
	DiaryDate string
}

func (s *Service) CreateSession(ctx context.Context, fs *filesystem.Service, in CreateSessionIn) (*Session, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	date, err := s.resolveDate(in.DiaryDate)
	if err != nil {
		return nil, err
	}
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	var cached *Session
	if ok, err := replayInto(st, in.RequestID, &cached); err != nil {
		return nil, err
	} else if ok {
		return cached, nil
	}
	if entryID, ok := st.data.DateEntries[date]; ok {
		return nil, errf(codeConflict, "diary for %s already committed as %s", date, entryID)
	}
	if sessionID, ok := st.data.DateSessions[date]; ok {
		sess := cloneSession(st.data.Sessions[sessionID])
		if sess == nil || sess.Status != StatusDraft {
			return nil, errf(codeConflict, "diary session for %s is not a draft", date)
		}
		if err := remember(ctx, st, in.RequestID, sess); err != nil {
			return nil, err
		}
		return sess, nil
	}

	now := st.now().In(s.loc)
	sess := &Session{
		SessionID: newID("ds"),
		DiaryDate: date,
		Content:   "",
		Status:    StatusDraft,
		Revision:  1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	st.data.Sessions[sess.SessionID] = sess
	st.data.DateSessions[date] = sess.SessionID
	if err := remember(ctx, st, in.RequestID, sess); err != nil {
		return nil, err
	}
	return cloneSession(sess), nil
}

type AppendSessionIn struct {
	RequestID        string
	SessionID        string
	ExpectedRevision int
	Content          string
}

func (s *Service) AppendSession(ctx context.Context, fs *filesystem.Service, in AppendSessionIn) (SessionContent, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return SessionContent{}, err
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return SessionContent{}, errf(codeBadRequest, "session_id is required")
	}
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return SessionContent{}, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	var cached SessionContent
	if ok, err := replayInto(st, in.RequestID, &cached); err != nil {
		return SessionContent{}, err
	} else if ok {
		return cached, nil
	}
	sess, err := requireDraft(st, in.SessionID, in.ExpectedRevision)
	if err != nil {
		return SessionContent{}, err
	}
	sess.Content = joinContent(sess.Content, in.Content)
	sess.Revision++
	sess.UpdatedAt = st.now().In(s.loc)
	out := SessionContent{
		SessionID: sess.SessionID,
		Revision:  sess.Revision,
		Content:   sess.Content,
		UpdatedAt: sess.UpdatedAt,
	}
	if err := remember(ctx, st, in.RequestID, out); err != nil {
		return SessionContent{}, err
	}
	return out, nil
}

type GetSessionIn struct {
	RequestID string
	SessionID string
}

func (s *Service) GetSession(ctx context.Context, fs *filesystem.Service, in GetSessionIn) (*Session, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return nil, errf(codeBadRequest, "session_id is required")
	}
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	var cached *Session
	if ok, err := replayInto(st, in.RequestID, &cached); err != nil {
		return nil, err
	} else if ok {
		return cached, nil
	}
	sess := cloneSession(st.data.Sessions[in.SessionID])
	if sess == nil {
		return nil, errf(codeNotFound, "session %s not found", in.SessionID)
	}
	if err := remember(ctx, st, in.RequestID, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

type UpdateSessionIn struct {
	RequestID        string
	SessionID        string
	ExpectedRevision int
	Content          string
}

func (s *Service) UpdateSession(ctx context.Context, fs *filesystem.Service, in UpdateSessionIn) (SessionContent, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return SessionContent{}, err
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return SessionContent{}, errf(codeBadRequest, "session_id is required")
	}
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return SessionContent{}, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	var cached SessionContent
	if ok, err := replayInto(st, in.RequestID, &cached); err != nil {
		return SessionContent{}, err
	} else if ok {
		return cached, nil
	}
	sess, err := requireDraft(st, in.SessionID, in.ExpectedRevision)
	if err != nil {
		return SessionContent{}, err
	}
	sess.Content = in.Content
	sess.Revision++
	sess.UpdatedAt = st.now().In(s.loc)
	out := SessionContent{
		SessionID: sess.SessionID,
		Revision:  sess.Revision,
		Content:   sess.Content,
		UpdatedAt: sess.UpdatedAt,
	}
	if err := remember(ctx, st, in.RequestID, out); err != nil {
		return SessionContent{}, err
	}
	return out, nil
}

type CommitSessionIn struct {
	RequestID        string
	SessionID        string
	ExpectedRevision int
}

func (s *Service) CommitSession(ctx context.Context, fs *filesystem.Service, in CommitSessionIn) (CommittedEntry, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return CommittedEntry{}, err
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return CommittedEntry{}, errf(codeBadRequest, "session_id is required")
	}
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return CommittedEntry{}, wrapInternal(err)
	}
	st.mu.Lock()

	var cached CommittedEntry
	if ok, err := replayInto(st, in.RequestID, &cached); err != nil {
		st.mu.Unlock()
		return CommittedEntry{}, err
	} else if ok {
		st.mu.Unlock()
		return cached, nil
	}
	sess, err := requireDraft(st, in.SessionID, in.ExpectedRevision)
	if err != nil {
		st.mu.Unlock()
		return CommittedEntry{}, err
	}
	now := st.now().In(s.loc)
	entry := &Entry{
		EntryID:   newID("de"),
		DiaryDate: sess.DiaryDate,
		Content:   sess.Content,
		Status:    StatusCommitted,
		Revision:  1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if s.meta != nil {
		meta := s.meta.Enrich(ctx, fs, entry.DiaryDate)
		entry.Meta = &meta
	}
	st.data.Entries[entry.EntryID] = entry
	st.data.DateEntries[entry.DiaryDate] = entry.EntryID
	delete(st.data.Sessions, sess.SessionID)
	delete(st.data.DateSessions, sess.DiaryDate)

	out := CommittedEntry{
		EntryID:   entry.EntryID,
		SessionID: sess.SessionID,
		DiaryDate: entry.DiaryDate,
		Content:   entry.Content,
		Status:    StatusCommitted,
		Meta:      entry.Meta,
		CreatedAt: entry.CreatedAt,
		UpdatedAt: entry.UpdatedAt,
	}
	if err := remember(ctx, st, in.RequestID, out); err != nil {
		st.mu.Unlock()
		return CommittedEntry{}, err
	}
	date := entry.DiaryDate
	st.mu.Unlock()
	s.emitChange(fs, date)
	return out, nil
}

type DiscardSessionIn struct {
	RequestID        string
	SessionID        string
	ExpectedRevision int
}

func (s *Service) DiscardSession(ctx context.Context, fs *filesystem.Service, in DiscardSessionIn) (DiscardedSession, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return DiscardedSession{}, err
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return DiscardedSession{}, errf(codeBadRequest, "session_id is required")
	}
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return DiscardedSession{}, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	var cached DiscardedSession
	if ok, err := replayInto(st, in.RequestID, &cached); err != nil {
		return DiscardedSession{}, err
	} else if ok {
		return cached, nil
	}
	sess, err := requireDraft(st, in.SessionID, in.ExpectedRevision)
	if err != nil {
		return DiscardedSession{}, err
	}
	now := st.now().In(s.loc)
	delete(st.data.Sessions, sess.SessionID)
	delete(st.data.DateSessions, sess.DiaryDate)
	out := DiscardedSession{
		SessionID: sess.SessionID,
		Status:    StatusDiscarded,
		UpdatedAt: now,
	}
	if err := remember(ctx, st, in.RequestID, out); err != nil {
		return DiscardedSession{}, err
	}
	return out, nil
}

type GetEntryIn struct {
	RequestID string
	EntryID   string
}

func (s *Service) GetEntry(ctx context.Context, fs *filesystem.Service, in GetEntryIn) (*Entry, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.EntryID) == "" {
		return nil, errf(codeBadRequest, "entry_id is required")
	}
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	var cached *Entry
	if ok, err := replayInto(st, in.RequestID, &cached); err != nil {
		return nil, err
	} else if ok {
		return cached, nil
	}
	entry := cloneEntry(st.data.Entries[in.EntryID])
	if entry == nil {
		return nil, errf(codeNotFound, "entry %s not found", in.EntryID)
	}
	if err := remember(ctx, st, in.RequestID, entry); err != nil {
		return nil, err
	}
	return entry, nil
}

type UpdateEntryIn struct {
	RequestID        string
	EntryID          string
	ExpectedRevision int
	Content          string
}

func (s *Service) UpdateEntry(ctx context.Context, fs *filesystem.Service, in UpdateEntryIn) (EntryRevision, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return EntryRevision{}, err
	}
	if strings.TrimSpace(in.EntryID) == "" {
		return EntryRevision{}, errf(codeBadRequest, "entry_id is required")
	}
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return EntryRevision{}, wrapInternal(err)
	}
	st.mu.Lock()

	var cached EntryRevision
	if ok, err := replayInto(st, in.RequestID, &cached); err != nil {
		st.mu.Unlock()
		return EntryRevision{}, err
	} else if ok {
		st.mu.Unlock()
		return cached, nil
	}
	entry := st.data.Entries[in.EntryID]
	if entry == nil {
		st.mu.Unlock()
		return EntryRevision{}, errf(codeNotFound, "entry %s not found", in.EntryID)
	}
	if entry.Revision != in.ExpectedRevision {
		st.mu.Unlock()
		return EntryRevision{}, errf(codeConflict, "revision conflict")
	}
	entry.Content = in.Content
	entry.Revision++
	entry.UpdatedAt = st.now().In(s.loc)
	out := EntryRevision{
		EntryID:   entry.EntryID,
		Revision:  entry.Revision,
		UpdatedAt: entry.UpdatedAt,
	}
	if err := remember(ctx, st, in.RequestID, out); err != nil {
		st.mu.Unlock()
		return EntryRevision{}, err
	}
	date := entry.DiaryDate
	st.mu.Unlock()
	s.emitChange(fs, date)
	return out, nil
}

type ListEntriesIn struct {
	RequestID     string
	DiaryDateFrom string
	DiaryDateTo   string
	Page          int
	PageSize      int
}

func (s *Service) ListEntries(ctx context.Context, fs *filesystem.Service, in ListEntriesIn) (EntryList, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return EntryList{}, err
	}
	if in.DiaryDateFrom != "" {
		if _, err := parseDate(in.DiaryDateFrom); err != nil {
			return EntryList{}, err
		}
	}
	if in.DiaryDateTo != "" {
		if _, err := parseDate(in.DiaryDateTo); err != nil {
			return EntryList{}, err
		}
	}
	page := in.Page
	if page <= 0 {
		page = 1
	}
	pageSize := in.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return EntryList{}, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	var cached EntryList
	if ok, err := replayInto(st, in.RequestID, &cached); err != nil {
		return EntryList{}, err
	} else if ok {
		return cached, nil
	}

	all := st.listEntries(in.DiaryDateFrom, in.DiaryDateTo)
	total := len(all)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	items := all[start:end]
	summaries := make([]EntrySummary, 0, len(items))
	for _, e := range items {
		summaries = append(summaries, EntrySummary{
			EntryID:     e.EntryID,
			DiaryDate:   e.DiaryDate,
			Content:     e.Content,
			Revision:    e.Revision,
			Meta:        e.Meta,
			Attachments: append([]Attachment(nil), e.Attachments...),
			CreatedAt:   e.CreatedAt,
			UpdatedAt:   e.UpdatedAt,
		})
	}
	out := EntryList{
		Entries:  summaries,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}
	if err := remember(ctx, st, in.RequestID, out); err != nil {
		return EntryList{}, err
	}
	return out, nil
}

type DeleteEntryIn struct {
	RequestID        string
	EntryID          string
	ExpectedRevision int
}

func (s *Service) DeleteEntry(ctx context.Context, fs *filesystem.Service, in DeleteEntryIn) (DeletedEntry, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return DeletedEntry{}, err
	}
	if strings.TrimSpace(in.EntryID) == "" {
		return DeletedEntry{}, errf(codeBadRequest, "entry_id is required")
	}
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return DeletedEntry{}, wrapInternal(err)
	}
	st.mu.Lock()

	var cached DeletedEntry
	if ok, err := replayInto(st, in.RequestID, &cached); err != nil {
		st.mu.Unlock()
		return DeletedEntry{}, err
	} else if ok {
		st.mu.Unlock()
		return cached, nil
	}
	entry := st.data.Entries[in.EntryID]
	if entry == nil {
		st.mu.Unlock()
		return DeletedEntry{}, errf(codeNotFound, "entry %s not found", in.EntryID)
	}
	if entry.Revision != in.ExpectedRevision {
		st.mu.Unlock()
		return DeletedEntry{}, errf(codeConflict, "revision conflict")
	}
	date := entry.DiaryDate
	files := attachmentPaths(entry)
	delete(st.data.Entries, in.EntryID)
	if st.data.DateEntries[entry.DiaryDate] == in.EntryID {
		delete(st.data.DateEntries, entry.DiaryDate)
	}
	out := DeletedEntry{
		EntryID: in.EntryID,
		Deleted: true,
	}
	if err := remember(ctx, st, in.RequestID, out); err != nil {
		st.mu.Unlock()
		return DeletedEntry{}, err
	}
	st.mu.Unlock()
	for _, rel := range files {
		_ = fs.Delete(ctx, rel, false)
	}
	s.emitChange(fs, date)
	return out, nil
}

const (
	// filesDir is the workspace directory that holds uploaded file bytes.
	// Metadata stays in diary.json; the bytes never do.
	filesDir = ".mcp-diary/files"

	// maxAttachmentBytes is the largest single upload. MCP tool arguments are
	// JSON, so the practical ceiling is the encoded argument, not this number.
	maxAttachmentBytes = 8 << 20

	// maxAttachmentsPerEntry keeps one diary entry from accumulating an
	// unbounded file list.
	maxAttachmentsPerEntry = 20

	// maxExtractBytes is how much of a text-like upload is copied into the
	// metadata so the model can read it without a second round trip.
	maxExtractBytes = 32 << 10

	// jpegLimit is the largest JPEG the server keeps for an image upload.
	// Every image is re-encoded, even one the widget already compressed.
	jpegLimit = 5 << 20
)

// AddAttachmentIn is one file uploaded against a committed diary entry.
type AddAttachmentIn struct {
	RequestID        string
	EntryID          string
	ExpectedRevision int
	Name             string
	MIME             string
	ContentBase64    string
}

// AddAttachment stores an uploaded file in the caller's workspace and records
// it on the entry. The same request_id returns the stored attachment without
// writing the bytes again.
func (s *Service) AddAttachment(ctx context.Context, fs *filesystem.Service, in AddAttachmentIn) (StoredAttachment, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return StoredAttachment{}, err
	}
	if strings.TrimSpace(in.EntryID) == "" {
		return StoredAttachment{}, errf(codeBadRequest, "entry_id is required")
	}
	raw, err := decodeUpload(in.ContentBase64)
	if err != nil {
		return StoredAttachment{}, err
	}
	mime := sniffMIME(in.MIME, raw)
	if stored, storedMIME, ok := normalizeImage(mime, raw); ok {
		raw, mime = stored, storedMIME
	}

	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return StoredAttachment{}, wrapInternal(err)
	}
	st.mu.Lock()

	var cached StoredAttachment
	if ok, err := replayInto(st, in.RequestID, &cached); err != nil {
		st.mu.Unlock()
		return StoredAttachment{}, err
	} else if ok {
		st.mu.Unlock()
		return cached, nil
	}
	entry := st.data.Entries[in.EntryID]
	if entry == nil {
		st.mu.Unlock()
		return StoredAttachment{}, errf(codeNotFound, "entry %s not found", in.EntryID)
	}
	if entry.Revision != in.ExpectedRevision {
		st.mu.Unlock()
		return StoredAttachment{}, errf(codeConflict, "revision conflict")
	}
	if len(entry.Attachments) >= maxAttachmentsPerEntry {
		st.mu.Unlock()
		return StoredAttachment{}, errf(codeBadRequest, "entry already has %d attachments", maxAttachmentsPerEntry)
	}

	fileID := newID("df")
	rel := path.Join(filesDir, fileID)
	sum := sha256.Sum256(raw)
	now := st.now().In(s.loc)
	name := attachmentName(entry, now, mime)
	att := Attachment{
		FileID:    fileID,
		Name:      name,
		MIME:      mime,
		Size:      int64(len(raw)),
		Path:      rel,
		SHA256:    hex.EncodeToString(sum[:]),
		Content:   extractText(mime, raw),
		Preview:   imagePreview(mime, raw),
		CreatedAt: now,
	}

	// Write the bytes before the metadata. A crash between the two leaves an
	// unreferenced file, which is recoverable; the reverse would advertise a
	// file that does not exist.
	if _, err := fs.WriteAtomic(ctx, rel, string(raw), filesystem.WriteOptions{CreateDirs: true, Mode: 0o600}); err != nil {
		st.mu.Unlock()
		return StoredAttachment{}, wrapInternal(err)
	}
	entry.Attachments = append(entry.Attachments, att)
	entry.Revision++
	entry.UpdatedAt = now
	out := StoredAttachment{EntryID: entry.EntryID, Revision: entry.Revision, Attachment: att}
	if err := remember(ctx, st, in.RequestID, out); err != nil {
		st.mu.Unlock()
		return StoredAttachment{}, err
	}
	date := entry.DiaryDate
	st.mu.Unlock()
	s.emitChange(fs, date)
	return out, nil
}

// RemoveAttachmentIn detaches one uploaded file from a committed entry.
type RemoveAttachmentIn struct {
	RequestID        string
	EntryID          string
	FileID           string
	ExpectedRevision int
}

// RemoveAttachment drops the file from the entry and deletes its bytes.
func (s *Service) RemoveAttachment(ctx context.Context, fs *filesystem.Service, in RemoveAttachmentIn) (RemovedAttachment, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return RemovedAttachment{}, err
	}
	if strings.TrimSpace(in.EntryID) == "" {
		return RemovedAttachment{}, errf(codeBadRequest, "entry_id is required")
	}
	if strings.TrimSpace(in.FileID) == "" {
		return RemovedAttachment{}, errf(codeBadRequest, "file_id is required")
	}
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return RemovedAttachment{}, wrapInternal(err)
	}
	st.mu.Lock()

	var cached RemovedAttachment
	if ok, err := replayInto(st, in.RequestID, &cached); err != nil {
		st.mu.Unlock()
		return RemovedAttachment{}, err
	} else if ok {
		st.mu.Unlock()
		return cached, nil
	}
	entry := st.data.Entries[in.EntryID]
	if entry == nil {
		st.mu.Unlock()
		return RemovedAttachment{}, errf(codeNotFound, "entry %s not found", in.EntryID)
	}
	if entry.Revision != in.ExpectedRevision {
		st.mu.Unlock()
		return RemovedAttachment{}, errf(codeConflict, "revision conflict")
	}
	kept := make([]Attachment, 0, len(entry.Attachments))
	var removed *Attachment
	for i := range entry.Attachments {
		if entry.Attachments[i].FileID == in.FileID && removed == nil {
			cp := entry.Attachments[i]
			removed = &cp
			continue
		}
		kept = append(kept, entry.Attachments[i])
	}
	if removed == nil {
		st.mu.Unlock()
		return RemovedAttachment{}, errf(codeNotFound, "attachment %s not found", in.FileID)
	}
	entry.Attachments = kept
	entry.Revision++
	entry.UpdatedAt = st.now().In(s.loc)
	out := RemovedAttachment{EntryID: entry.EntryID, FileID: in.FileID, Revision: entry.Revision, Removed: true}
	if err := remember(ctx, st, in.RequestID, out); err != nil {
		st.mu.Unlock()
		return RemovedAttachment{}, err
	}
	rel := removed.Path
	date := entry.DiaryDate
	st.mu.Unlock()
	if rel != "" {
		_ = fs.Delete(ctx, rel, false)
	}
	s.emitChange(fs, date)
	return out, nil
}

func attachmentPaths(entry *Entry) []string {
	if entry == nil || len(entry.Attachments) == 0 {
		return nil
	}
	out := make([]string, 0, len(entry.Attachments))
	for _, att := range entry.Attachments {
		if att.Path != "" {
			out = append(out, att.Path)
		}
	}
	return out
}

// decodeUpload accepts standard or raw base64 and rejects empty or oversized
// payloads before they touch the workspace.
func decodeUpload(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, errf(codeBadRequest, "content_base64 is required")
	}
	if strings.HasPrefix(encoded, "data:") {
		if i := strings.Index(encoded, ","); i >= 0 {
			encoded = encoded[i+1:]
		}
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		return nil, errf(codeBadRequest, "content_base64 is not valid base64")
	}
	if len(raw) == 0 {
		return nil, errf(codeBadRequest, "file is empty")
	}
	if len(raw) > maxAttachmentBytes {
		return nil, errf(codeTooLarge, "file exceeds %d bytes", maxAttachmentBytes)
	}
	return raw, nil
}

// cleanFileName keeps a display name only. The stored path never uses it, so a
// hostile name cannot escape the files directory.
func cleanFileName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	if utf8.RuneCountInString(name) > 120 {
		runes := []rune(name)
		name = string(runes[len(runes)-120:])
	}
	return name
}

// sniffMIME trusts a declared type only when it looks like one. Otherwise it
// sniffs the bytes, so a renamed executable is not stored as an image.
func sniffMIME(declared string, raw []byte) string {
	declared = strings.ToLower(strings.TrimSpace(declared))
	if i := strings.Index(declared, ";"); i >= 0 {
		declared = strings.TrimSpace(declared[:i])
	}
	if strings.Count(declared, "/") == 1 && !strings.ContainsAny(declared, " \t") {
		return declared
	}
	return http.DetectContentType(raw)
}

// extractText copies a short prefix of text-like uploads into the metadata so
// the model can quote the file. Binary uploads stay bytes-only.
func extractText(mime string, raw []byte) string {
	if !isTextMIME(mime) || !utf8.Valid(raw) {
		return ""
	}
	if len(raw) > maxExtractBytes {
		raw = raw[:maxExtractBytes]
	}
	return string(raw)
}

// previewMaxEdge is the longest side of the thumbnail stored next to an
// image attachment. The card renders this; the original file stays untouched.
const previewMaxEdge = 480

// imagePreview makes a small JPEG thumbnail so the diary card can show the
// picture without a second request. Decoding failure leaves the card with the
// file name only; the original bytes are already stored.

// normalizeImage re-encodes every decodable image as a JPEG of at most
// jpegLimit. A picture the widget could not compress is handled here; a
// picture it already compressed is still re-encoded so the stored format is
// always JPEG. Undecodable bytes return ok=false and are stored unchanged.
func normalizeImage(mime string, raw []byte) ([]byte, string, bool) {
	if !isRasterImage(mime) {
		return nil, "", false
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", false
	}
	edge := max(img.Bounds().Dx(), img.Bounds().Dy())
	if edge <= 0 {
		return nil, "", false
	}
	quality := 85
	for {
		scaled := scaleImage(img, edge)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: quality}); err != nil {
			return nil, "", false
		}
		if buf.Len() <= jpegLimit || (edge <= 1024 && quality <= 50) {
			return buf.Bytes(), "image/jpeg", true
		}
		if quality > 55 {
			quality -= 15
			continue
		}
		edge = max(1024, edge*3/4)
		quality = 75
	}
}

func isRasterImage(mime string) bool {
	return strings.HasPrefix(mime, "image/") && mime != "image/svg+xml"
}

func attachmentName(entry *Entry, now time.Time, mime string) string {
	seq := 1
	prefix := now.Format("20060102") + "-"
	for _, att := range entry.Attachments {
		if !strings.HasPrefix(att.Name, prefix) {
			continue
		}
		rest := strings.TrimSuffix(strings.TrimPrefix(att.Name, prefix), path.Ext(att.Name))
		n := 0
		for _, r := range rest {
			if r < '0' || r > '9' {
				n = 0
				break
			}
			n = n*10 + int(r-'0')
		}
		if n >= seq {
			seq = n + 1
		}
	}
	return fmt.Sprintf("%s%02d%s", prefix, seq, extForMIME(mime))
}

func extForMIME(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/heic", "image/heif":
		return ".heic"
	case "application/pdf":
		return ".pdf"
	case "text/plain":
		return ".txt"
	default:
		return ""
	}
}

func scaleImage(src image.Image, edge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	nw, nh := fitEdge(w, h, edge)
	if nw == w && nh == h {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*h/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*w/nw
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

func imagePreview(mime string, raw []byte) string {
	if !strings.HasPrefix(mime, "image/") || mime == "image/svg+xml" {
		return ""
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return ""
	}
	nw, nh := fitEdge(w, h, previewMaxEdge)
	thumb := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*h/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*w/nw
			thumb.Set(x, y, img.At(sx, sy))
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 70}); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func fitEdge(w, h, edge int) (int, int) {
	if w <= edge && h <= edge {
		return w, h
	}
	if w >= h {
		return edge, max(1, h*edge/w)
	}
	return max(1, w*edge/h), edge
}

func isTextMIME(mime string) bool {
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	switch mime {
	case "application/json", "application/xml", "application/javascript", "image/svg+xml":
		return true
	}
	return strings.HasSuffix(mime, "+json") || strings.HasSuffix(mime, "+xml")
}

// BackfillPreviews fills in a thumbnail for image attachments stored before
// previews were generated. It does not bump the entry revision: the diary text
// did not change, and a revision bump would make an open card fail its next edit.
// GetAttachmentIn identifies one uploaded file on a committed entry.
type GetAttachmentIn struct {
	RequestID string
	EntryID   string
	FileID    string
}

// GetAttachment returns the original bytes of one attachment. List results
// carry only a thumbnail, so viewing or downloading goes through this.
func (s *Service) GetAttachment(ctx context.Context, fs *filesystem.Service, in GetAttachmentIn) (AttachmentFile, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return AttachmentFile{}, err
	}
	if strings.TrimSpace(in.EntryID) == "" {
		return AttachmentFile{}, errf(codeBadRequest, "entry_id is required")
	}
	if strings.TrimSpace(in.FileID) == "" {
		return AttachmentFile{}, errf(codeBadRequest, "file_id is required")
	}
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return AttachmentFile{}, wrapInternal(err)
	}
	st.mu.Lock()
	entry := st.data.Entries[in.EntryID]
	var att *Attachment
	if entry != nil {
		for i := range entry.Attachments {
			if entry.Attachments[i].FileID == in.FileID {
				cp := entry.Attachments[i]
				att = &cp
				break
			}
		}
	}
	st.mu.Unlock()
	if entry == nil {
		return AttachmentFile{}, errf(codeNotFound, "entry %s not found", in.EntryID)
	}
	if att == nil {
		return AttachmentFile{}, errf(codeNotFound, "attachment %s not found", in.FileID)
	}
	raw, err := fs.ReadFile(ctx, att.Path)
	if err != nil {
		return AttachmentFile{}, wrapInternal(err)
	}
	return AttachmentFile{
		FileID:        att.FileID,
		Name:          att.Name,
		MIME:          att.MIME,
		Size:          att.Size,
		ContentBase64: base64.StdEncoding.EncodeToString(raw),
	}, nil
}

func (s *Service) BackfillPreviews(ctx context.Context, fs *filesystem.Service) (int, error) {
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return 0, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	filled := 0
	for _, entry := range st.data.Entries {
		changed := false
		for i := range entry.Attachments {
			att := &entry.Attachments[i]
			if att.Preview != "" || !strings.HasPrefix(att.MIME, "image/") || att.MIME == "image/svg+xml" || att.Path == "" {
				continue
			}
			raw, err := fs.ReadFile(ctx, att.Path)
			if err != nil {
				return filled, wrapInternal(err)
			}
			preview := imagePreview(att.MIME, raw)
			if preview == "" {
				continue
			}
			att.Preview = preview
			changed = true
			filled++
		}
		if changed {
			entry.UpdatedAt = st.now().In(s.loc)
		}
	}
	if filled == 0 {
		return 0, nil
	}
	if err := st.persistLocked(ctx); err != nil {
		return 0, wrapInternal(err)
	}
	return filled, nil
}

func (s *Service) ListCommitted(ctx context.Context, fs *filesystem.Service) ([]Entry, error) {
	st, err := s.storeFor(ctx, fs)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	all := st.listEntries("", "")
	out := make([]Entry, 0, len(all))
	for _, e := range all {
		if e == nil {
			continue
		}
		out = append(out, *e)
	}
	return out, nil
}

func (s *Service) resolveDate(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return s.now().In(s.loc).Format("2006-01-02"), nil
	}
	return parseDate(raw)
}

func parseDate(raw string) (string, error) {
	if !dateRE.MatchString(raw) {
		return "", errf(codeBadRequest, "diary_date must be YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", raw); err != nil {
		return "", errf(codeBadRequest, "diary_date is not a valid date")
	}
	return raw, nil
}

func requireRequestID(id string) error {
	if strings.TrimSpace(id) == "" {
		return errf(codeBadRequest, "request_id is required")
	}
	return nil
}

func requireDraft(st *Store, sessionID string, expectedRevision int) (*Session, error) {
	sess := st.data.Sessions[sessionID]
	if sess == nil {
		return nil, errf(codeNotFound, "session %s not found", sessionID)
	}
	if sess.Status != StatusDraft {
		return nil, errf(codeConflict, "session is %s, not draft", sess.Status)
	}
	if sess.Revision != expectedRevision {
		return nil, errf(codeConflict, "revision conflict")
	}
	return sess, nil
}

// replayInto decodes a previously persisted idempotent response into dest. It
// returns false when requestID has not been seen. Storing responses as typed
// JSON keeps replay consistent before and after a restart.
func replayInto(st *Store, requestID string, dest any) (bool, error) {
	raw, ok := st.data.Idempotency[requestID]
	if !ok || len(raw) == 0 {
		return false, nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, wrapInternal(err)
	}
	return true, nil
}

func remember(ctx context.Context, st *Store, requestID string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return wrapInternal(err)
	}
	st.data.Idempotency[requestID] = json.RawMessage(raw)
	if err := st.persistLocked(ctx); err != nil {
		return wrapInternal(err)
	}
	return nil
}

func joinContent(existing, extra string) string {
	existing = strings.TrimRight(existing, "\n")
	extra = strings.TrimRight(extra, "\n")
	if existing == "" {
		return extra
	}
	if extra == "" {
		return existing
	}
	return existing + "\n" + extra
}

func wrapInternal(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := IsError(err); ok {
		return err
	}
	return errf(codeInternal, "%s", err.Error())
}
