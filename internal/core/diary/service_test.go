package diary

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
)

func testFS(t *testing.T, readOnly bool) *filesystem.Service {
	t.Helper()
	fs, err := filesystem.New(filesystem.Options{
		Root:     t.TempDir(),
		ReadOnly: readOnly,
	})
	if err != nil {
		t.Fatalf("filesystem.New: %v", err)
	}
	return fs
}

func testService(t *testing.T) (*Service, *filesystem.Service, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 1, 13, 0, 0, 0, time.FixedZone("CST", 8*3600))
	clock := now
	svc := New(func() time.Time { return clock }, now.Location())
	return svc, testFS(t, false), now
}

func mustSession(t *testing.T, v any) *Session {
	t.Helper()
	s, ok := v.(*Session)
	if !ok {
		t.Fatalf("got %T, want *Session", v)
	}
	return s
}

func TestCreateAppendCommitFlow(t *testing.T) {
	svc, fs, now := testService(t)
	ctx := context.Background()

	created, err := svc.CreateSession(ctx, fs, CreateSessionIn{RequestID: "req_001", DiaryDate: "2026-10-01"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess := mustSession(t, created)
	if sess.Status != StatusDraft || sess.Revision != 1 || sess.Content != "" {
		t.Fatalf("unexpected session: %+v", sess)
	}
	if !sess.CreatedAt.Equal(now) {
		t.Fatalf("created_at = %v, want %v", sess.CreatedAt, now)
	}

	appended, err := svc.AppendSession(ctx, fs, AppendSessionIn{
		RequestID:        "req_002",
		SessionID:        sess.SessionID,
		ExpectedRevision: 1,
		Content:          "今天开会时需求反复调整，我感到有些沮丧。",
	})
	if err != nil {
		t.Fatalf("AppendSession: %v", err)
	}
	data := appended.(map[string]any)
	if data["revision"] != 2 {
		t.Fatalf("revision = %v, want 2", data["revision"])
	}

	got, err := svc.GetSession(ctx, fs, GetSessionIn{RequestID: "req_003", SessionID: sess.SessionID})
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if mustSession(t, got).Content != "今天开会时需求反复调整，我感到有些沮丧。" {
		t.Fatalf("content = %q", mustSession(t, got).Content)
	}

	updated, err := svc.UpdateSession(ctx, fs, UpdateSessionIn{
		RequestID:        "req_004",
		SessionID:        sess.SessionID,
		ExpectedRevision: 2,
		Content:          "今天开会时需求反复调整，我感到沮丧。晚上和小王去跑步后情绪好转。",
	})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if updated.(map[string]any)["revision"] != 3 {
		t.Fatalf("update revision = %v", updated.(map[string]any)["revision"])
	}

	committed, err := svc.CommitSession(ctx, fs, CommitSessionIn{
		RequestID:        "req_005",
		SessionID:        sess.SessionID,
		ExpectedRevision: 3,
	})
	if err != nil {
		t.Fatalf("CommitSession: %v", err)
	}
	out := committed.(map[string]any)
	entryID := out["entry_id"].(string)
	if out["status"] != StatusCommitted {
		t.Fatalf("status = %v", out["status"])
	}

	if _, err := svc.GetSession(ctx, fs, GetSessionIn{RequestID: "req_006", SessionID: sess.SessionID}); err == nil {
		t.Fatal("draft should be deleted after commit")
	} else if de, ok := IsError(err); !ok || de.Code != 404 {
		t.Fatalf("GetSession after commit err = %v", err)
	}

	entry, err := svc.GetEntry(ctx, fs, GetEntryIn{RequestID: "req_007", EntryID: entryID})
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry.(*Entry).Content == "" || entry.(*Entry).Revision != 1 {
		t.Fatalf("entry = %+v", entry)
	}

	if _, err := svc.CreateSession(ctx, fs, CreateSessionIn{RequestID: "req_008", DiaryDate: "2026-10-01"}); err == nil {
		t.Fatal("expected conflict creating draft for committed date")
	} else if de, ok := IsError(err); !ok || de.Code != 409 {
		t.Fatalf("CreateSession after commit err = %v", err)
	}
}

func TestResumeDraftAndIdempotency(t *testing.T) {
	svc, fs, _ := testService(t)
	ctx := context.Background()

	first, err := svc.CreateSession(ctx, fs, CreateSessionIn{RequestID: "req_a", DiaryDate: "2026-10-01"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess := mustSession(t, first)

	replay, err := svc.CreateSession(ctx, fs, CreateSessionIn{RequestID: "req_a", DiaryDate: "2026-10-01"})
	if err != nil {
		t.Fatalf("idempotent CreateSession: %v", err)
	}
	if mustSession(t, replay).SessionID != sess.SessionID {
		t.Fatalf("idempotent session id mismatch")
	}

	second, err := svc.CreateSession(ctx, fs, CreateSessionIn{RequestID: "req_b", DiaryDate: "2026-10-01"})
	if err != nil {
		t.Fatalf("resume CreateSession: %v", err)
	}
	if mustSession(t, second).SessionID != sess.SessionID {
		t.Fatalf("resume created a new session")
	}
}

func TestRevisionConflict(t *testing.T) {
	svc, fs, _ := testService(t)
	ctx := context.Background()
	created, err := svc.CreateSession(ctx, fs, CreateSessionIn{RequestID: "req_1", DiaryDate: "2026-10-01"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess := mustSession(t, created)
	_, err = svc.AppendSession(ctx, fs, AppendSessionIn{
		RequestID:        "req_2",
		SessionID:        sess.SessionID,
		ExpectedRevision: 99,
		Content:          "x",
	})
	if err == nil {
		t.Fatal("expected revision conflict")
	}
	de, ok := IsError(err)
	if !ok || de.Code != 409 {
		t.Fatalf("err = %v", err)
	}
}

func TestDiscardAndUpdateDeleteEntry(t *testing.T) {
	svc, fs, _ := testService(t)
	ctx := context.Background()

	created, err := svc.CreateSession(ctx, fs, CreateSessionIn{RequestID: "d1", DiaryDate: "2026-10-02"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess := mustSession(t, created)
	if _, err := svc.DiscardSession(ctx, fs, DiscardSessionIn{RequestID: "d2", SessionID: sess.SessionID, ExpectedRevision: 1}); err != nil {
		t.Fatalf("DiscardSession: %v", err)
	}
	if _, err := svc.GetSession(ctx, fs, GetSessionIn{RequestID: "d3", SessionID: sess.SessionID}); err == nil {
		t.Fatal("discarded session should be gone")
	}

	created, err = svc.CreateSession(ctx, fs, CreateSessionIn{RequestID: "e1", DiaryDate: "2026-10-02"})
	if err != nil {
		t.Fatalf("recreate after discard: %v", err)
	}
	sess = mustSession(t, created)
	if _, err := svc.AppendSession(ctx, fs, AppendSessionIn{RequestID: "e2", SessionID: sess.SessionID, ExpectedRevision: 1, Content: "hello"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	committed, err := svc.CommitSession(ctx, fs, CommitSessionIn{RequestID: "e3", SessionID: sess.SessionID, ExpectedRevision: 2})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	entryID := committed.(map[string]any)["entry_id"].(string)

	updated, err := svc.UpdateEntry(ctx, fs, UpdateEntryIn{RequestID: "e4", EntryID: entryID, ExpectedRevision: 1, Content: "hello world"})
	if err != nil {
		t.Fatalf("UpdateEntry: %v", err)
	}
	if updated.(map[string]any)["revision"] != 2 {
		t.Fatalf("entry revision = %v", updated.(map[string]any)["revision"])
	}

	listed, err := svc.ListEntries(ctx, fs, ListEntriesIn{RequestID: "e5", DiaryDateFrom: "2026-10-01", DiaryDateTo: "2026-10-07"})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	list := listed.(map[string]any)
	if list["total"] != 1 {
		t.Fatalf("total = %v", list["total"])
	}

	if _, err := svc.DeleteEntry(ctx, fs, DeleteEntryIn{RequestID: "e6", EntryID: entryID, ExpectedRevision: 2}); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}
	if _, err := svc.GetEntry(ctx, fs, GetEntryIn{RequestID: "e7", EntryID: entryID}); err == nil {
		t.Fatal("deleted entry should be gone")
	}
}

func TestWorkspaceIsolationAndPersist(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	svc := New(func() time.Time { return now }, time.UTC)
	alice := testFS(t, false)
	bob := testFS(t, false)

	a, err := svc.CreateSession(ctx, alice, CreateSessionIn{RequestID: "a1", DiaryDate: "2026-10-01"})
	if err != nil {
		t.Fatalf("alice create: %v", err)
	}
	aliceID := mustSession(t, a).SessionID
	if _, err := svc.GetSession(ctx, bob, GetSessionIn{RequestID: "b1", SessionID: aliceID}); err == nil {
		t.Fatal("bob must not see alice session")
	}

	if _, err := os.Stat(filepath.Join(alice.Root(), ".mcp-diary", "diary.json")); err != nil {
		t.Fatalf("store not written: %v", err)
	}

	// A fresh service over the same workspace must reload persisted state.
	reloaded := New(func() time.Time { return now }, time.UTC)
	aliceReopened, err := filesystem.New(filesystem.Options{Root: alice.Root()})
	if err != nil {
		t.Fatalf("reopen workspace: %v", err)
	}
	got, err := reloaded.GetSession(ctx, aliceReopened, GetSessionIn{RequestID: "a2", SessionID: aliceID})
	if err != nil {
		t.Fatalf("reload GetSession: %v", err)
	}
	if mustSession(t, got).SessionID != aliceID {
		t.Fatalf("persisted session mismatch")
	}
}

func TestReadOnlyWorkspaceBlocksWrites(t *testing.T) {
	svc, _, _ := testService(t)
	ctx := context.Background()
	fs := testFS(t, true)

	if _, err := svc.CreateSession(ctx, fs, CreateSessionIn{RequestID: "ro1", DiaryDate: "2026-10-01"}); err == nil {
		t.Fatal("expected read-only workspace to block CreateSession")
	} else if de, ok := IsError(err); !ok || de.Code != 500 {
		t.Fatalf("err = %v, want internal error", err)
	}
}

func TestDefaultDiaryDate(t *testing.T) {
	svc, fs, _ := testService(t)
	created, err := svc.CreateSession(context.Background(), fs, CreateSessionIn{RequestID: "n1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if mustSession(t, created).DiaryDate != "2026-10-01" {
		t.Fatalf("diary_date = %s", mustSession(t, created).DiaryDate)
	}
}

func TestInvalidDate(t *testing.T) {
	svc, fs, _ := testService(t)
	if _, err := svc.CreateSession(context.Background(), fs, CreateSessionIn{RequestID: "x", DiaryDate: "10/01"}); err == nil {
		t.Fatal("expected invalid date")
	} else if de, ok := IsError(err); !ok || de.Code != 400 {
		t.Fatalf("err = %v", err)
	}
}
