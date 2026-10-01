package diary

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	codeOK         = 0
	codeBadRequest = 400
	codeNotFound   = 404
	codeConflict   = 409
	codeInternal   = 500
)

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

type Service struct {
	now func() time.Time
	loc *time.Location

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

func (s *Service) storeFor(workspaceRoot string) (*Store, error) {
	if workspaceRoot == "" {
		return nil, errf(codeInternal, "workspace root is empty")
	}
	path := filepath.Join(workspaceRoot, ".mcp-diary", "diary.json")
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.stores[path]; ok {
		return st, nil
	}
	st, err := loadStore(path, s.now)
	if err != nil {
		return nil, err
	}
	s.stores[path] = st
	return st, nil
}

type CreateSessionIn struct {
	RequestID string
	DiaryDate string
}

func (s *Service) CreateSession(workspaceRoot string, in CreateSessionIn) (any, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	date, err := s.resolveDate(in.DiaryDate)
	if err != nil {
		return nil, err
	}
	st, err := s.storeFor(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	if cached, ok := replay(st, in.RequestID); ok {
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
		if err := remember(st, in.RequestID, sess); err != nil {
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
	if err := remember(st, in.RequestID, cloneSession(sess)); err != nil {
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

func (s *Service) AppendSession(workspaceRoot string, in AppendSessionIn) (any, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return nil, errf(codeBadRequest, "session_id is required")
	}
	st, err := s.storeFor(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	if cached, ok := replay(st, in.RequestID); ok {
		return cached, nil
	}
	sess, err := requireDraft(st, in.SessionID, in.ExpectedRevision)
	if err != nil {
		return nil, err
	}
	sess.Content = joinContent(sess.Content, in.Content)
	sess.Revision++
	sess.UpdatedAt = st.now().In(s.loc)
	out := map[string]any{
		"session_id": sess.SessionID,
		"revision":   sess.Revision,
		"content":    sess.Content,
		"updated_at": sess.UpdatedAt,
	}
	if err := remember(st, in.RequestID, out); err != nil {
		return nil, err
	}
	return out, nil
}

type GetSessionIn struct {
	RequestID string
	SessionID string
}

func (s *Service) GetSession(workspaceRoot string, in GetSessionIn) (any, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return nil, errf(codeBadRequest, "session_id is required")
	}
	st, err := s.storeFor(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	if cached, ok := replay(st, in.RequestID); ok {
		return cached, nil
	}
	sess := cloneSession(st.data.Sessions[in.SessionID])
	if sess == nil {
		return nil, errf(codeNotFound, "session %s not found", in.SessionID)
	}
	if err := remember(st, in.RequestID, sess); err != nil {
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

func (s *Service) UpdateSession(workspaceRoot string, in UpdateSessionIn) (any, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return nil, errf(codeBadRequest, "session_id is required")
	}
	st, err := s.storeFor(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	if cached, ok := replay(st, in.RequestID); ok {
		return cached, nil
	}
	sess, err := requireDraft(st, in.SessionID, in.ExpectedRevision)
	if err != nil {
		return nil, err
	}
	sess.Content = in.Content
	sess.Revision++
	sess.UpdatedAt = st.now().In(s.loc)
	out := map[string]any{
		"session_id": sess.SessionID,
		"revision":   sess.Revision,
		"content":    sess.Content,
		"updated_at": sess.UpdatedAt,
	}
	if err := remember(st, in.RequestID, out); err != nil {
		return nil, err
	}
	return out, nil
}

type CommitSessionIn struct {
	RequestID        string
	SessionID        string
	ExpectedRevision int
}

func (s *Service) CommitSession(workspaceRoot string, in CommitSessionIn) (any, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return nil, errf(codeBadRequest, "session_id is required")
	}
	st, err := s.storeFor(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	if cached, ok := replay(st, in.RequestID); ok {
		return cached, nil
	}
	sess, err := requireDraft(st, in.SessionID, in.ExpectedRevision)
	if err != nil {
		return nil, err
	}
	now := st.now().In(s.loc)
	entry := &Entry{
		EntryID:     newID("de"),
		DiaryDate:   sess.DiaryDate,
		Content:     sess.Content,
		Status:      StatusCommitted,
		Revision:    1,
		Attachments: cloneAttachments(sess.Attachments),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	moved, err := st.transferAttachmentDir(sess.SessionID, entry.EntryID, entry.Attachments)
	if err != nil {
		return nil, wrapInternal(err)
	}
	entry.Attachments = moved
	st.data.Entries[entry.EntryID] = entry
	st.data.DateEntries[entry.DiaryDate] = entry.EntryID
	delete(st.data.Sessions, sess.SessionID)
	delete(st.data.DateSessions, sess.DiaryDate)

	out := map[string]any{
		"entry_id":    entry.EntryID,
		"session_id":  sess.SessionID,
		"diary_date":  entry.DiaryDate,
		"content":     entry.Content,
		"status":      StatusCommitted,
		"attachments": cloneAttachments(entry.Attachments),
		"created_at":  entry.CreatedAt,
		"updated_at":  entry.UpdatedAt,
	}
	if err := remember(st, in.RequestID, out); err != nil {
		return nil, err
	}
	return out, nil
}

type DiscardSessionIn struct {
	RequestID        string
	SessionID        string
	ExpectedRevision int
}

func (s *Service) DiscardSession(workspaceRoot string, in DiscardSessionIn) (any, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return nil, errf(codeBadRequest, "session_id is required")
	}
	st, err := s.storeFor(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	if cached, ok := replay(st, in.RequestID); ok {
		return cached, nil
	}
	sess, err := requireDraft(st, in.SessionID, in.ExpectedRevision)
	if err != nil {
		return nil, err
	}
	now := st.now().In(s.loc)
	sessionID := sess.SessionID
	delete(st.data.Sessions, sessionID)
	delete(st.data.DateSessions, sess.DiaryDate)
	out := map[string]any{
		"session_id": sessionID,
		"status":     StatusDiscarded,
		"updated_at": now,
	}
	if err := remember(st, in.RequestID, out); err != nil {
		return nil, err
	}
	_ = st.removeAttachmentDir(sessionID)
	return out, nil
}

type GetEntryIn struct {
	RequestID string
	EntryID   string
}

func (s *Service) GetEntry(workspaceRoot string, in GetEntryIn) (any, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.EntryID) == "" {
		return nil, errf(codeBadRequest, "entry_id is required")
	}
	st, err := s.storeFor(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	if cached, ok := replay(st, in.RequestID); ok {
		return cached, nil
	}
	entry := cloneEntry(st.data.Entries[in.EntryID])
	if entry == nil {
		return nil, errf(codeNotFound, "entry %s not found", in.EntryID)
	}
	if err := remember(st, in.RequestID, entry); err != nil {
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

func (s *Service) UpdateEntry(workspaceRoot string, in UpdateEntryIn) (any, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.EntryID) == "" {
		return nil, errf(codeBadRequest, "entry_id is required")
	}
	st, err := s.storeFor(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	if cached, ok := replay(st, in.RequestID); ok {
		return cached, nil
	}
	entry := st.data.Entries[in.EntryID]
	if entry == nil {
		return nil, errf(codeNotFound, "entry %s not found", in.EntryID)
	}
	if entry.Revision != in.ExpectedRevision {
		return nil, errf(codeConflict, "revision conflict")
	}
	entry.Content = in.Content
	entry.Revision++
	entry.UpdatedAt = st.now().In(s.loc)
	out := map[string]any{
		"entry_id":   entry.EntryID,
		"revision":   entry.Revision,
		"updated_at": entry.UpdatedAt,
	}
	if err := remember(st, in.RequestID, out); err != nil {
		return nil, err
	}
	return out, nil
}

type ListEntriesIn struct {
	RequestID     string
	DiaryDateFrom string
	DiaryDateTo   string
	Page          int
	PageSize      int
}

func (s *Service) ListEntries(workspaceRoot string, in ListEntriesIn) (any, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	if in.DiaryDateFrom != "" {
		if _, err := parseDate(in.DiaryDateFrom); err != nil {
			return nil, err
		}
	}
	if in.DiaryDateTo != "" {
		if _, err := parseDate(in.DiaryDateTo); err != nil {
			return nil, err
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

	st, err := s.storeFor(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	if cached, ok := replay(st, in.RequestID); ok {
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
	summaries := make([]map[string]any, 0, len(items))
	for _, e := range items {
		summaries = append(summaries, map[string]any{
			"entry_id":    e.EntryID,
			"diary_date":  e.DiaryDate,
			"content":     e.Content,
			"attachments": cloneAttachments(e.Attachments),
			"created_at":  e.CreatedAt,
			"updated_at":  e.UpdatedAt,
		})
	}
	out := map[string]any{
		"entries":   summaries,
		"page":      page,
		"page_size": pageSize,
		"total":     total,
	}
	if err := remember(st, in.RequestID, out); err != nil {
		return nil, err
	}
	return out, nil
}

type DeleteEntryIn struct {
	RequestID        string
	EntryID          string
	ExpectedRevision int
}

func (s *Service) DeleteEntry(workspaceRoot string, in DeleteEntryIn) (any, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.EntryID) == "" {
		return nil, errf(codeBadRequest, "entry_id is required")
	}
	st, err := s.storeFor(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	if cached, ok := replay(st, in.RequestID); ok {
		return cached, nil
	}
	entry := st.data.Entries[in.EntryID]
	if entry == nil {
		return nil, errf(codeNotFound, "entry %s not found", in.EntryID)
	}
	if entry.Revision != in.ExpectedRevision {
		return nil, errf(codeConflict, "revision conflict")
	}
	delete(st.data.Entries, in.EntryID)
	if st.data.DateEntries[entry.DiaryDate] == in.EntryID {
		delete(st.data.DateEntries, entry.DiaryDate)
	}
	out := map[string]any{
		"entry_id": in.EntryID,
		"deleted":  true,
	}
	if err := remember(st, in.RequestID, out); err != nil {
		return nil, err
	}
	_ = st.removeAttachmentDir(in.EntryID)
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

func replay(st *Store, requestID string) (any, bool) {
	v, ok := st.data.Idempotency[requestID]
	return v, ok
}

func remember(st *Store, requestID string, value any) error {
	st.data.Idempotency[requestID] = value
	if err := st.persistLocked(); err != nil {
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
