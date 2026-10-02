package diary

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const (
	codePayloadTooLarge = 413

	MaxImageBytes = 10 << 20
	MaxVideoBytes = 30 << 20

	MediaImage = "image"
	MediaVideo = "video"
)

type Attachment struct {
	AttachmentID string `json:"attachment_id"`
	OwnerID      string `json:"owner_id"`
	Filename     string `json:"filename"`
	OriginalName string `json:"original_name,omitempty"`
	RelPath      string `json:"rel_path"`
	MediaType    string `json:"media_type"`
	MIMEType     string `json:"mime_type"`
	Size         int64  `json:"size"`
}

type mediaSpec struct {
	media string
	mime  string
}

var allowedExt = map[string]mediaSpec{
	".jpg":  {media: MediaImage, mime: "image/jpeg"},
	".jpeg": {media: MediaImage, mime: "image/jpeg"},
	".png":  {media: MediaImage, mime: "image/png"},
	".gif":  {media: MediaImage, mime: "image/gif"},
	".webp": {media: MediaImage, mime: "image/webp"},
	".mp4":  {media: MediaVideo, mime: "video/mp4"},
	".webm": {media: MediaVideo, mime: "video/webm"},
	".mov":  {media: MediaVideo, mime: "video/quicktime"},
}

var mimeAliases = map[string]string{
	"image/jpg":       "image/jpeg",
	"image/pjpeg":     "image/jpeg",
	"video/quicktime": "video/quicktime",
}

var ownerIDRE = regexp.MustCompile(`^[a-z]{2}_[A-Za-z0-9_]+$`)
var storedNameRE = regexp.MustCompile(`^[a-z]{2}_[A-Za-z0-9_]+\.(jpg|jpeg|png|gif|webp|mp4|webm|mov)$`)

func cloneAttachments(in []Attachment) []Attachment {
	if len(in) == 0 {
		return []Attachment{}
	}
	out := make([]Attachment, len(in))
	copy(out, in)
	return out
}

func attachmentsRoot(storePath string) string {
	return filepath.Join(filepath.Dir(storePath), "attachments")
}

func attachmentRelPath(ownerID, storedName string) string {
	return filepath.ToSlash(filepath.Join("attachments", ownerID, storedName))
}

func attachmentAbsPath(storePath, ownerID, storedName string) (string, error) {
	if !ownerIDRE.MatchString(ownerID) || !storedNameRE.MatchString(storedName) {
		return "", errf(codeBadRequest, "invalid attachment path")
	}
	root := attachmentsRoot(storePath)
	abs := filepath.Join(root, ownerID, storedName)
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errf(codeBadRequest, "invalid attachment path")
	}
	return abs, nil
}

func (st *Store) removeAttachmentDir(ownerID string) error {
	if !ownerIDRE.MatchString(ownerID) {
		return nil
	}
	return os.RemoveAll(filepath.Join(attachmentsRoot(st.path), ownerID))
}

func (st *Store) transferAttachmentDir(fromID, toID string, atts []Attachment) ([]Attachment, error) {
	out := cloneAttachments(atts)
	for i := range out {
		out[i].OwnerID = toID
		out[i].RelPath = attachmentRelPath(toID, out[i].Filename)
	}
	src := filepath.Join(attachmentsRoot(st.path), fromID)
	dst := filepath.Join(attachmentsRoot(st.path), toID)
	_, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			if len(out) == 0 {
				return out, nil
			}
			return nil, errf(codeInternal, "attachments directory missing")
		}
		return nil, err
	}
	if len(out) == 0 {
		_ = os.RemoveAll(src)
		return out, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(dst); err == nil {
		return nil, errf(codeConflict, "attachment directory already exists")
	}
	if err := os.Rename(src, dst); err != nil {
		return nil, err
	}
	return out, nil
}

func sanitizeOriginalName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." {
		return "attachment"
	}
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

func normalizeMIME(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	if i := strings.IndexByte(raw, ';'); i >= 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	if aliased, ok := mimeAliases[raw]; ok {
		return aliased
	}
	return raw
}

func validateMedia(filename, declaredMIME string, data []byte) (ext string, spec mediaSpec, err error) {
	if len(data) == 0 {
		return "", spec, errf(codeBadRequest, "attachment is empty")
	}
	original := sanitizeOriginalName(filename)
	ext = strings.ToLower(filepath.Ext(original))
	spec, ok := allowedExt[ext]
	if !ok {
		return "", spec, errf(codeBadRequest, "unsupported file extension %q", ext)
	}
	limit := int64(MaxImageBytes)
	if spec.media == MediaVideo {
		limit = MaxVideoBytes
	}
	if int64(len(data)) > limit {
		return "", spec, errf(codePayloadTooLarge, "%s exceeds %d byte limit", spec.media, limit)
	}
	declared := normalizeMIME(declaredMIME)
	if declared == "application/octet-stream" || declared == "binary/octet-stream" {
		declared = ""
	}
	if declared != "" && declared != spec.mime {
		return "", spec, errf(codeBadRequest, "mime type %q does not match extension %s", declaredMIME, ext)
	}
	detected := sniffMIME(data)
	if detected != "" && detected != spec.mime {
		return "", spec, errf(codeBadRequest, "file content is %s, not %s", detected, spec.mime)
	}
	if detected == "" {
		return "", spec, errf(codeBadRequest, "could not detect a supported media type")
	}
	return ext, spec, nil
}

func sniffMIME(data []byte) string {
	if len(data) < 8 {
		return ""
	}
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif"
	}
	if len(data) < 12 {
		return ""
	}
	switch {
	case bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	case bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBM")):
		return "video/webm"
	case bytes.HasPrefix(data, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return "video/webm"
	case isFtyp(data, "qt"):
		return "video/quicktime"
	case isISOMedia(data):
		return "video/mp4"
	}
	return ""
}

func isFtyp(data []byte, brand string) bool {
	if len(data) < 12 || string(data[4:8]) != "ftyp" {
		return false
	}
	return strings.Contains(string(data[8:12]), brand)
}

func isISOMedia(data []byte) bool {
	if len(data) < 12 || string(data[4:8]) != "ftyp" {
		return false
	}
	size := binary.BigEndian.Uint32(data[:4])
	if size < 8 || int(size) > len(data) {
		size = 24
		if int(size) > len(data) {
			size = uint32(len(data))
		}
	}
	box := string(data[8:size])
	for _, brand := range []string{"isom", "iso2", "iso4", "iso5", "iso6", "mp41", "mp42", "avc1", "mmp4", "msnv", "ndas", "ndsc", "ndsh", "ndsm", "ndsp", "ndss", "ndxc", "ndxh", "ndxm", "ndxp", "ndxs", "M4V ", "M4A "} {
		if strings.Contains(box, brand) {
			return true
		}
	}
	return strings.HasPrefix(string(data[8:12]), "mp4") || strings.HasPrefix(string(data[8:12]), "iso")
}

func writeExclusive(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return errf(codeConflict, "attachment file already exists")
		}
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func readWorkspaceFile(workspaceRoot, sourcePath string) ([]byte, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return nil, errf(codeBadRequest, "source_path is empty")
	}
	if filepath.IsAbs(sourcePath) {
		return nil, errf(codeBadRequest, "source_path must be relative to the workspace")
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	clean := filepath.Clean(sourcePath)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return nil, errf(codeBadRequest, "source_path is outside the workspace")
	}
	abs := filepath.Join(root, clean)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, errf(codeBadRequest, "source_path is outside the workspace")
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errf(codeNotFound, "source file not found")
		}
		return nil, wrapInternal(err)
	}
	if info.IsDir() {
		return nil, errf(codeBadRequest, "source_path is a directory")
	}
	if info.Size() > MaxVideoBytes {
		return nil, errf(codePayloadTooLarge, "source file exceeds %d byte limit", MaxVideoBytes)
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, wrapInternal(err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxVideoBytes+1))
	if err != nil {
		return nil, wrapInternal(err)
	}
	if int64(len(data)) > MaxVideoBytes {
		return nil, errf(codePayloadTooLarge, "source file exceeds %d byte limit", MaxVideoBytes)
	}
	return data, nil
}

type AttachMediaIn struct {
	RequestID  string
	SessionID  string
	EntryID    string
	Filename   string
	MIMEType   string
	Data       []byte
	SourcePath string
}

func (s *Service) AttachMedia(workspaceRoot string, in AttachMediaIn) (any, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(in.SessionID)
	entryID := strings.TrimSpace(in.EntryID)
	if (sessionID == "") == (entryID == "") {
		return nil, errf(codeBadRequest, "exactly one of session_id or entry_id is required")
	}
	data := in.Data
	if len(data) == 0 && strings.TrimSpace(in.SourcePath) != "" {
		var err error
		data, err = readWorkspaceFile(workspaceRoot, in.SourcePath)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Filename) == "" {
			in.Filename = filepath.Base(in.SourcePath)
		}
	}
	ext, spec, err := validateMedia(in.Filename, in.MIMEType, data)
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

	var sess *Session
	var entry *Entry
	var ownerID string
	if sessionID != "" {
		sess = st.data.Sessions[sessionID]
		if sess == nil {
			return nil, errf(codeNotFound, "session %s not found", sessionID)
		}
		if sess.Status != StatusDraft {
			return nil, errf(codeConflict, "session is %s, not draft", sess.Status)
		}
		ownerID = sess.SessionID
	} else {
		entry = st.data.Entries[entryID]
		if entry == nil {
			return nil, errf(codeNotFound, "entry %s not found", entryID)
		}
		ownerID = entry.EntryID
	}

	attID := newID("da")
	stored := attID + ext
	abs, err := attachmentAbsPath(st.path, ownerID, stored)
	if err != nil {
		return nil, err
	}
	if err := writeExclusive(abs, data); err != nil {
		return nil, wrapInternal(err)
	}
	att := Attachment{
		AttachmentID: attID,
		OwnerID:      ownerID,
		Filename:     stored,
		OriginalName: sanitizeOriginalName(in.Filename),
		RelPath:      attachmentRelPath(ownerID, stored),
		MediaType:    spec.media,
		MIMEType:     spec.mime,
		Size:         int64(len(data)),
	}
	if sess != nil {
		sess.Attachments = append(sess.Attachments, att)
		sess.UpdatedAt = st.now().In(s.loc)
	} else {
		entry.Attachments = append(entry.Attachments, att)
		entry.UpdatedAt = st.now().In(s.loc)
	}
	out := map[string]any{
		"attachment":  att,
		"attachments": attachmentsOf(sess, entry),
	}
	if err := remember(st, in.RequestID, out); err != nil {
		_ = os.Remove(abs)
		if sess != nil && len(sess.Attachments) > 0 {
			sess.Attachments = sess.Attachments[:len(sess.Attachments)-1]
		}
		if entry != nil && len(entry.Attachments) > 0 {
			entry.Attachments = entry.Attachments[:len(entry.Attachments)-1]
		}
		return nil, err
	}
	return out, nil
}

func attachmentsOf(sess *Session, entry *Entry) []Attachment {
	if sess != nil {
		return cloneAttachments(sess.Attachments)
	}
	if entry != nil {
		return cloneAttachments(entry.Attachments)
	}
	return nil
}

type RemoveAttachmentIn struct {
	RequestID    string
	SessionID    string
	EntryID      string
	AttachmentID string
}

func (s *Service) RemoveAttachment(workspaceRoot string, in RemoveAttachmentIn) (any, error) {
	if err := requireRequestID(in.RequestID); err != nil {
		return nil, err
	}
	attID := strings.TrimSpace(in.AttachmentID)
	if attID == "" {
		return nil, errf(codeBadRequest, "attachment_id is required")
	}
	sessionID := strings.TrimSpace(in.SessionID)
	entryID := strings.TrimSpace(in.EntryID)
	if (sessionID == "") == (entryID == "") {
		return nil, errf(codeBadRequest, "exactly one of session_id or entry_id is required")
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

	var atts *[]Attachment
	ownerID := sessionID
	if sessionID != "" {
		sess := st.data.Sessions[sessionID]
		if sess == nil {
			return nil, errf(codeNotFound, "session %s not found", sessionID)
		}
		atts = &sess.Attachments
		ownerID = sess.SessionID
	} else {
		entry := st.data.Entries[entryID]
		if entry == nil {
			return nil, errf(codeNotFound, "entry %s not found", entryID)
		}
		atts = &entry.Attachments
		ownerID = entry.EntryID
	}

	idx := -1
	var removed Attachment
	for i, a := range *atts {
		if a.AttachmentID == attID {
			idx = i
			removed = a
			break
		}
	}
	if idx < 0 {
		return nil, errf(codeNotFound, "attachment %s not found", attID)
	}
	abs, err := attachmentAbsPath(st.path, ownerID, removed.Filename)
	if err != nil {
		return nil, err
	}
	*atts = append((*atts)[:idx], (*atts)[idx+1:]...)
	_ = os.Remove(abs)

	out := map[string]any{
		"attachment_id": attID,
		"deleted":       true,
		"attachments":   cloneAttachments(*atts),
	}
	if err := remember(st, in.RequestID, out); err != nil {
		return nil, err
	}
	return out, nil
}

type OpenAttachmentIn struct {
	OwnerID      string
	AttachmentID string
}

type OpenedAttachment struct {
	Attachment Attachment
	Path       string
}

func (s *Service) OpenAttachment(workspaceRoot string, in OpenAttachmentIn) (*OpenedAttachment, error) {
	ownerID := strings.TrimSpace(in.OwnerID)
	attID := strings.TrimSpace(in.AttachmentID)
	if !ownerIDRE.MatchString(ownerID) {
		return nil, errf(codeBadRequest, "invalid owner id")
	}
	if !ownerIDRE.MatchString(attID) {
		return nil, errf(codeBadRequest, "invalid attachment id")
	}

	st, err := s.storeFor(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	var atts []Attachment
	if sess := st.data.Sessions[ownerID]; sess != nil {
		atts = sess.Attachments
	} else if entry := st.data.Entries[ownerID]; entry != nil {
		atts = entry.Attachments
	} else {
		return nil, errf(codeNotFound, "diary %s not found", ownerID)
	}
	var att Attachment
	found := false
	for _, a := range atts {
		if a.AttachmentID == attID {
			att = a
			found = true
			break
		}
	}
	if !found {
		return nil, errf(codeNotFound, "attachment %s not found", attID)
	}
	abs, err := attachmentAbsPath(st.path, ownerID, att.Filename)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return nil, errf(codeNotFound, "attachment file missing")
		}
		return nil, wrapInternal(err)
	}
	return &OpenedAttachment{Attachment: att, Path: abs}, nil
}

func (s *Service) ListDrafts(workspaceRoot string) ([]*Session, error) {
	st, err := s.storeFor(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.listSessions(), nil
}

func (s *Service) ListCommitted(workspaceRoot string) ([]*Entry, error) {
	st, err := s.storeFor(workspaceRoot)
	if err != nil {
		return nil, wrapInternal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.listEntries("", ""), nil
}

func NewRequestID() string {
	return newID("rq")
}
