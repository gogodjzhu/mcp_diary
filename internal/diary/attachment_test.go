package diary

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pngBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
		0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x05, 0xFE,
		0xD4, 0xEF, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
		0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
	}
}

func jpegBytes() []byte {
	return []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46,
		0x49, 0x46, 0x00, 0x01, 0x01, 0x00, 0x00, 0x01,
		0x00, 0x01, 0x00, 0x00, 0xFF, 0xD9,
	}
}

func mp4Bytes() []byte {
	buf := make([]byte, 24)
	buf[3] = 24
	copy(buf[4:8], "ftyp")
	copy(buf[8:12], "isom")
	copy(buf[16:20], "isom")
	copy(buf[20:24], "mp41")
	return buf
}

func mustAttach(t *testing.T, v any) Attachment {
	t.Helper()
	out, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("got %T", v)
	}
	att, ok := out["attachment"].(Attachment)
	if !ok {
		t.Fatalf("attachment type %T", out["attachment"])
	}
	return att
}

func TestAttachImageToSessionAndCommit(t *testing.T) {
	svc, root, _ := testService(t)
	created, err := svc.CreateSession(root, CreateSessionIn{RequestID: "a1", DiaryDate: "2026-10-01"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess := mustSession(t, created)

	attached, err := svc.AttachMedia(root, AttachMediaIn{
		RequestID: "a2",
		SessionID: sess.SessionID,
		Filename:  "photo.png",
		MIMEType:  "image/png",
		Data:      pngBytes(),
	})
	if err != nil {
		t.Fatalf("AttachMedia: %v", err)
	}
	att := mustAttach(t, attached)
	if att.MediaType != MediaImage || att.MIMEType != "image/png" {
		t.Fatalf("attachment = %+v", att)
	}
	if att.OwnerID != sess.SessionID {
		t.Fatalf("owner = %s", att.OwnerID)
	}
	stored := filepath.Join(root, ".mcp-diary", att.RelPath)
	if _, err := os.Stat(stored); err != nil {
		t.Fatalf("stored file missing: %v", err)
	}

	committed, err := svc.CommitSession(root, CommitSessionIn{
		RequestID:        "a3",
		SessionID:        sess.SessionID,
		ExpectedRevision: 1,
	})
	if err != nil {
		t.Fatalf("CommitSession: %v", err)
	}
	out := committed.(map[string]any)
	entryID := out["entry_id"].(string)
	atts := out["attachments"].([]Attachment)
	if len(atts) != 1 {
		t.Fatalf("committed attachments = %+v", atts)
	}
	if atts[0].OwnerID != entryID {
		t.Fatalf("owner after commit = %s want %s", atts[0].OwnerID, entryID)
	}
	if _, err := os.Stat(filepath.Join(root, ".mcp-diary", atts[0].RelPath)); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Fatalf("old session dir still present")
	}

	got, err := svc.GetEntry(root, GetEntryIn{RequestID: "a4", EntryID: entryID})
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if len(got.(*Entry).Attachments) != 1 {
		t.Fatalf("entry attachments = %+v", got.(*Entry).Attachments)
	}

	opened, err := svc.OpenAttachment(root, OpenAttachmentIn{OwnerID: entryID, AttachmentID: atts[0].AttachmentID})
	if err != nil {
		t.Fatalf("OpenAttachment: %v", err)
	}
	if opened.Attachment.MIMEType != "image/png" {
		t.Fatalf("opened mime = %s", opened.Attachment.MIMEType)
	}
}

func TestAttachFromWorkspacePathAndRejectEscape(t *testing.T) {
	svc, root, _ := testService(t)
	created, err := svc.CreateSession(root, CreateSessionIn{RequestID: "p1", DiaryDate: "2026-10-01"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess := mustSession(t, created)

	src := filepath.Join(root, "inbox", "shot.jpg")
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, jpegBytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	attached, err := svc.AttachMedia(root, AttachMediaIn{
		RequestID:  "p2",
		SessionID:  sess.SessionID,
		SourcePath: "inbox/shot.jpg",
	})
	if err != nil {
		t.Fatalf("AttachMedia from path: %v", err)
	}
	if mustAttach(t, attached).MIMEType != "image/jpeg" {
		t.Fatalf("want jpeg, got %+v", attached)
	}

	if _, err := svc.AttachMedia(root, AttachMediaIn{
		RequestID:  "p3",
		SessionID:  sess.SessionID,
		SourcePath: "../shot.jpg",
	}); err == nil {
		t.Fatal("expected path escape rejection")
	} else if de, ok := IsError(err); !ok || de.Code != 400 {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectBadExtensionMIMEAndSize(t *testing.T) {
	svc, root, _ := testService(t)
	created, err := svc.CreateSession(root, CreateSessionIn{RequestID: "b1", DiaryDate: "2026-10-01"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess := mustSession(t, created)

	if _, err := svc.AttachMedia(root, AttachMediaIn{
		RequestID: "b2", SessionID: sess.SessionID, Filename: "note.txt", Data: []byte("hello world!!!!"),
	}); err == nil {
		t.Fatal("expected extension rejection")
	}

	if _, err := svc.AttachMedia(root, AttachMediaIn{
		RequestID: "b3", SessionID: sess.SessionID, Filename: "photo.png", MIMEType: "image/jpeg", Data: pngBytes(),
	}); err == nil {
		t.Fatal("expected mime mismatch")
	}

	if _, err := svc.AttachMedia(root, AttachMediaIn{
		RequestID: "b4", SessionID: sess.SessionID, Filename: "photo.png", Data: []byte("not a png at all"),
	}); err == nil {
		t.Fatal("expected content sniff rejection")
	}

	huge := bytes.Repeat(pngBytes(), (MaxImageBytes/len(pngBytes()))+2)
	if _, err := svc.AttachMedia(root, AttachMediaIn{
		RequestID: "b5", SessionID: sess.SessionID, Filename: "photo.png", Data: huge,
	}); err == nil {
		t.Fatal("expected size rejection")
	} else if de, ok := IsError(err); !ok || de.Code != 413 {
		t.Fatalf("err = %v", err)
	}
}

func TestAttachVideoAndRemove(t *testing.T) {
	svc, root, _ := testService(t)
	created, err := svc.CreateSession(root, CreateSessionIn{RequestID: "v1", DiaryDate: "2026-10-01"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess := mustSession(t, created)

	attached, err := svc.AttachMedia(root, AttachMediaIn{
		RequestID: "v2",
		SessionID: sess.SessionID,
		Filename:  "clip.mp4",
		Data:      mp4Bytes(),
	})
	if err != nil {
		t.Fatalf("AttachMedia video: %v", err)
	}
	att := mustAttach(t, attached)
	if att.MediaType != MediaVideo {
		t.Fatalf("media = %s", att.MediaType)
	}

	removed, err := svc.RemoveAttachment(root, RemoveAttachmentIn{
		RequestID:    "v3",
		SessionID:    sess.SessionID,
		AttachmentID: att.AttachmentID,
	})
	if err != nil {
		t.Fatalf("RemoveAttachment: %v", err)
	}
	out := removed.(map[string]any)
	if out["deleted"] != true {
		t.Fatalf("deleted = %v", out["deleted"])
	}
	if _, err := os.Stat(filepath.Join(root, ".mcp-diary", att.RelPath)); !os.IsNotExist(err) {
		t.Fatalf("file should be gone")
	}
}

func TestDiscardAndDeleteRemoveFiles(t *testing.T) {
	svc, root, _ := testService(t)
	created, err := svc.CreateSession(root, CreateSessionIn{RequestID: "d1", DiaryDate: "2026-10-01"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess := mustSession(t, created)
	attached, err := svc.AttachMedia(root, AttachMediaIn{
		RequestID: "d2", SessionID: sess.SessionID, Filename: "a.png", Data: pngBytes(),
	})
	if err != nil {
		t.Fatal(err)
	}
	att := mustAttach(t, attached)
	if _, err := svc.DiscardSession(root, DiscardSessionIn{RequestID: "d3", SessionID: sess.SessionID, ExpectedRevision: 1}); err != nil {
		t.Fatalf("DiscardSession: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".mcp-diary", "attachments", sess.SessionID)); !os.IsNotExist(err) {
		t.Fatalf("session attachment dir should be gone")
	}

	created, err = svc.CreateSession(root, CreateSessionIn{RequestID: "d4", DiaryDate: "2026-10-02"})
	if err != nil {
		t.Fatal(err)
	}
	sess = mustSession(t, created)
	if _, err := svc.AttachMedia(root, AttachMediaIn{
		RequestID: "d5", SessionID: sess.SessionID, Filename: "b.png", Data: pngBytes(),
	}); err != nil {
		t.Fatal(err)
	}
	committed, err := svc.CommitSession(root, CommitSessionIn{RequestID: "d6", SessionID: sess.SessionID, ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	entryID := committed.(map[string]any)["entry_id"].(string)
	if _, err := svc.DeleteEntry(root, DeleteEntryIn{RequestID: "d7", EntryID: entryID, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".mcp-diary", "attachments", entryID)); !os.IsNotExist(err) {
		t.Fatalf("entry attachment dir should be gone")
	}
	_ = att
}

func TestSanitizeAndPathTraversalName(t *testing.T) {
	svc, root, _ := testService(t)
	created, err := svc.CreateSession(root, CreateSessionIn{RequestID: "s1", DiaryDate: "2026-10-01"})
	if err != nil {
		t.Fatal(err)
	}
	sess := mustSession(t, created)
	attached, err := svc.AttachMedia(root, AttachMediaIn{
		RequestID: "s2",
		SessionID: sess.SessionID,
		Filename:  "../../etc/passwd.png",
		Data:      pngBytes(),
	})
	if err != nil {
		t.Fatalf("AttachMedia: %v", err)
	}
	att := mustAttach(t, attached)
	if strings.Contains(att.Filename, "..") || strings.Contains(att.RelPath, "..") {
		t.Fatalf("unsafe name: %+v", att)
	}
	if !strings.HasPrefix(att.RelPath, "attachments/"+sess.SessionID+"/") {
		t.Fatalf("rel_path = %s", att.RelPath)
	}
}

func TestOpenAttachmentRejectsUnknownOwner(t *testing.T) {
	svc, root, _ := testService(t)
	if _, err := svc.OpenAttachment(root, OpenAttachmentIn{OwnerID: "de_nope", AttachmentID: "da_nope"}); err == nil {
		t.Fatal("expected not found")
	} else if de, ok := IsError(err); !ok || de.Code != 404 {
		t.Fatalf("err = %v", err)
	}
	if _, err := svc.OpenAttachment(root, OpenAttachmentIn{OwnerID: "../etc", AttachmentID: "da_x"}); err == nil {
		t.Fatal("expected bad owner")
	}
}
