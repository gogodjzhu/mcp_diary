package diary

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
)

const (
	codeOK         = 0
	codeBadRequest = 400
	codeNotFound   = 404
	codeConflict   = 409
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
			EntryID:   e.EntryID,
			DiaryDate: e.DiaryDate,
			Content:   e.Content,
			Meta:      e.Meta,
			CreatedAt: e.CreatedAt,
			UpdatedAt: e.UpdatedAt,
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
	s.emitChange(fs, date)
	return out, nil
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
