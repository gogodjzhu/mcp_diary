package diarysync

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/diary"
	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
)

func testEngine(t *testing.T) (*Engine, *Service, *diary.Service, *filesystem.Service, *MemoryProvider) {
	t.Helper()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	codec, err := NewCodec(testKey)
	if err != nil {
		t.Fatal(err)
	}
	mem := NewMemoryProvider(KindGitHub, func() time.Time { return now })
	reg := NewRegistry()
	reg.Register(KindGitHub, func(Medium, string) (Provider, error) { return mem, nil })
	syncSvc := New(codec, reg, func() time.Time { return now })
	diarySvc := diary.New(func() time.Time { return now }, time.UTC)
	engine := NewEngine(syncSvc, diarySvc, 20*time.Millisecond, func() time.Time { return now }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(engine.Stop)
	fs := testFS(t)
	return engine, syncSvc, diarySvc, fs, mem
}

func upsertGitHub(t *testing.T, svc *Service, fs *filesystem.Service) *PublicMedium {
	t.Helper()
	got, err := svc.Upsert(context.Background(), fs, UpsertIn{
		Kind:       KindGitHub,
		Name:       "github",
		Credential: "ghp_xxx",
		Settings:   map[string]string{"owner": "alice", "repo": "diary"},
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	return got
}

func commitDate(t *testing.T, svc *diary.Service, fs *filesystem.Service, date, content, req string) {
	t.Helper()
	created, err := svc.CreateSession(context.Background(), fs, diary.CreateSessionIn{RequestID: req + "_c", DiaryDate: date})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess := created
	if _, err := svc.AppendSession(context.Background(), fs, diary.AppendSessionIn{
		RequestID: req + "_a", SessionID: sess.SessionID, ExpectedRevision: 1, Content: content,
	}); err != nil {
		t.Fatalf("AppendSession: %v", err)
	}
	if _, err := svc.CommitSession(context.Background(), fs, diary.CommitSessionIn{
		RequestID: req + "_m", SessionID: sess.SessionID, ExpectedRevision: 2,
	}); err != nil {
		t.Fatalf("CommitSession: %v", err)
	}
}

func TestEnginePushesMonthlyMarkdownAndDeletesEmptyMonth(t *testing.T) {
	engine, syncSvc, diarySvc, fs, mem := testEngine(t)
	medium := upsertGitHub(t, syncSvc, fs)
	ctx := context.Background()

	commitDate(t, diarySvc, fs, "2026-10-04", "later day", "r1")
	commitDate(t, diarySvc, fs, "2026-10-01", "first day", "r2")
	if err := engine.TriggerFull(ctx, fs); err != nil {
		t.Fatalf("TriggerFull: %v", err)
	}
	body := string(mem.Body("2026/2026-10.md"))
	if body == "" {
		t.Fatal("expected monthly file")
	}
	if !strings.Contains(body, "2026-10-04") || !strings.Contains(body, "2026-10-01") {
		t.Fatalf("missing days:\n%s", body)
	}
	if !strings.Contains(body, daySeparator) {
		t.Fatalf("missing day separator:\n%s", body)
	}
	if strings.Index(body, "2026-10-04") > strings.Index(body, "2026-10-01") {
		t.Fatalf("days not descending:\n%s", body)
	}

	st, err := syncSvc.State(ctx, fs, medium.ID)
	if err != nil {
		t.Fatal(err)
	}
	ds := st.Documents["2026/2026-10.md"]
	if ds.Revision == 0 || len(ds.EntryIDs) != 2 {
		t.Fatalf("document state = %+v", ds)
	}

	listed, err := diarySvc.ListCommitted(ctx, fs)
	if err != nil {
		t.Fatal(err)
	}
	var oct1 diary.Entry
	for _, e := range listed {
		if e.DiaryDate == "2026-10-01" {
			oct1 = e
		}
	}
	if _, err := diarySvc.DeleteEntry(ctx, fs, diary.DeleteEntryIn{RequestID: "del1", EntryID: oct1.EntryID, ExpectedRevision: oct1.Revision}); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}
	if err := engine.SyncNow(ctx, fs, "2026-10-01"); err != nil {
		t.Fatalf("SyncNow after one delete: %v", err)
	}
	body = string(mem.Body("2026/2026-10.md"))
	if strings.Contains(body, "first day") {
		t.Fatalf("deleted day still present:\n%s", body)
	}

	listed, _ = diarySvc.ListCommitted(ctx, fs)
	for _, e := range listed {
		if _, err := diarySvc.DeleteEntry(ctx, fs, diary.DeleteEntryIn{RequestID: "del_" + e.EntryID, EntryID: e.EntryID, ExpectedRevision: e.Revision}); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.SyncNow(ctx, fs, "2026-10-04"); err != nil {
		t.Fatalf("SyncNow empty month: %v", err)
	}
	if mem.Body("2026/2026-10.md") != nil {
		t.Fatal("empty month should delete remote file")
	}
}

func TestEngineSyncAllTargetsSingleMedium(t *testing.T) {
	engine, syncSvc, diarySvc, fs, mem := testEngine(t)
	ctx := context.Background()
	first := upsertGitHub(t, syncSvc, fs)
	if _, err := syncSvc.Upsert(ctx, fs, UpsertIn{
		Kind:       KindGitHub,
		Name:       "second",
		Credential: "ghp_yyy",
		Settings:   map[string]string{"owner": "alice", "repo": "other"},
	}); err != nil {
		t.Fatalf("Upsert second: %v", err)
	}
	commitDate(t, diarySvc, fs, "2026-10-04", "single target", "sa")

	if err := engine.SyncAll(ctx, fs, first.ID); err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if mem.Body("2026/2026-10.md") == nil {
		t.Fatal("expected push for the targeted medium")
	}
	if err := engine.SyncAll(ctx, fs, "sm_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SyncAll unknown medium err = %v, want ErrNotFound", err)
	}
}

func TestEngineDebouncesMultipleCommits(t *testing.T) {
	_, syncSvc, diarySvc, fs, mem := testEngine(t)
	upsertGitHub(t, syncSvc, fs)
	commitDate(t, diarySvc, fs, "2026-10-04", "one", "d1")
	commitDate(t, diarySvc, fs, "2026-10-05", "two", "d2")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if mem.Body("2026/2026-10.md") != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if mem.Body("2026/2026-10.md") == nil {
		t.Fatal("debounced push never landed")
	}
}

func TestEngineRetriesTransientPush(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	codec, _ := NewCodec(testKey)
	flaky := &flakyProvider{failTimes: 2, inner: NewMemoryProvider(KindGitHub, func() time.Time { return now })}
	reg := NewRegistry()
	reg.Register(KindGitHub, func(Medium, string) (Provider, error) { return flaky, nil })
	syncSvc := New(codec, reg, func() time.Time { return now })
	diarySvc := diary.New(func() time.Time { return now }, time.UTC)
	engine := NewEngine(syncSvc, diarySvc, time.Millisecond, func() time.Time { return now }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	engine.sleep = func(time.Duration) {}
	t.Cleanup(engine.Stop)
	fs := testFS(t)
	upsertGitHub(t, syncSvc, fs)
	commitDate(t, diarySvc, fs, "2026-10-04", "retry me", "k1")
	if err := engine.TriggerFull(context.Background(), fs); err != nil {
		t.Fatalf("TriggerFull: %v", err)
	}
	if flaky.calls < 3 {
		t.Fatalf("calls = %d, want retries", flaky.calls)
	}
	if flaky.inner.Body("2026/2026-10.md") == nil {
		t.Fatal("expected eventual push")
	}
}

func TestEngineSkipsUnchangedMonth(t *testing.T) {
	engine, syncSvc, diarySvc, fs, mem := testEngine(t)
	if _, err := syncSvc.Upsert(context.Background(), fs, UpsertIn{
		Kind:       KindGitHub,
		Name:       "github",
		Credential: "ghp_xxx",
		Settings:   map[string]string{"owner": "alice", "repo": "diary", "preserve_existing": "false"},
	}); err != nil {
		t.Fatal(err)
	}
	commitDate(t, diarySvc, fs, "2026-10-04", "same", "s1")
	if err := engine.TriggerFull(context.Background(), fs); err != nil {
		t.Fatal(err)
	}
	counting := &countingProvider{inner: mem}
	syncSvc.registry.Register(KindGitHub, func(Medium, string) (Provider, error) { return counting, nil })
	if err := engine.SyncNow(context.Background(), fs, "2026-10-04"); err != nil {
		t.Fatal(err)
	}
	if counting.pushes != 0 {
		t.Fatalf("incremental unchanged month pushed %d times", counting.pushes)
	}
	if err := engine.TriggerFull(context.Background(), fs); err != nil {
		t.Fatal(err)
	}
	if counting.pushes != 1 {
		t.Fatalf("full resync should push once, got %d", counting.pushes)
	}
}

func TestEnginePreservesPreexistingRemoteOnFirstSync(t *testing.T) {
	engine, syncSvc, diarySvc, fs, mem := testEngine(t)
	upsertGitHub(t, syncSvc, fs)
	ctx := context.Background()

	// A day already present remotely but not in the local store.
	remote := []byte(daySeparator + "\n\n2026-10-01，四\n\nold remote day\n")
	if _, err := mem.Push(ctx, Document{Kind: DocumentText, Path: "2026/2026-10.md", Body: remote}); err != nil {
		t.Fatal(err)
	}

	commitDate(t, diarySvc, fs, "2026-10-04", "new local day", "ps1")
	if err := engine.TriggerFull(ctx, fs); err != nil {
		t.Fatalf("TriggerFull: %v", err)
	}
	body := string(mem.Body("2026/2026-10.md"))
	if !strings.Contains(body, "old remote day") {
		t.Fatalf("pre-existing remote day was overwritten:\n%s", body)
	}
	if !strings.Contains(body, "new local day") {
		t.Fatalf("new local day not synced:\n%s", body)
	}

	// A later local change to the same month must still preserve the
	// pre-existing remote day.
	commitDate(t, diarySvc, fs, "2026-10-05", "another local day", "ps2")
	if err := engine.SyncNow(ctx, fs, "2026-10-05"); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	body = string(mem.Body("2026/2026-10.md"))
	if !strings.Contains(body, "old remote day") {
		t.Fatalf("pre-existing remote day lost on later sync:\n%s", body)
	}
	if !strings.Contains(body, "new local day") || !strings.Contains(body, "another local day") {
		t.Fatalf("local days missing after later sync:\n%s", body)
	}
}

func TestEngineOverwritesWhenPreserveDisabled(t *testing.T) {
	engine, syncSvc, diarySvc, fs, mem := testEngine(t)
	if _, err := syncSvc.Upsert(context.Background(), fs, UpsertIn{
		Kind:       KindGitHub,
		Name:       "github",
		Credential: "ghp_xxx",
		Settings:   map[string]string{"owner": "alice", "repo": "diary", "preserve_existing": "false"},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	ctx := context.Background()

	remote := []byte(daySeparator + "\n\n2026-10-01，四\n\nold remote day\n")
	if _, err := mem.Push(ctx, Document{Kind: DocumentText, Path: "2026/2026-10.md", Body: remote}); err != nil {
		t.Fatal(err)
	}
	commitDate(t, diarySvc, fs, "2026-10-04", "new local day", "pd1")
	if err := engine.TriggerFull(ctx, fs); err != nil {
		t.Fatalf("TriggerFull: %v", err)
	}
	body := string(mem.Body("2026/2026-10.md"))
	if strings.Contains(body, "old remote day") {
		t.Fatalf("preserve_existing=false should overwrite:\n%s", body)
	}
	if !strings.Contains(body, "new local day") {
		t.Fatalf("new local day not synced:\n%s", body)
	}
}

type flakyProvider struct {
	failTimes int
	calls     int
	inner     *MemoryProvider
}

func (p *flakyProvider) Kind() Kind { return KindGitHub }
func (p *flakyProvider) Push(ctx context.Context, doc Document) (Result, error) {
	p.calls++
	if p.calls <= p.failTimes {
		return Result{}, errors.New("temporary")
	}
	return p.inner.Push(ctx, doc)
}
func (p *flakyProvider) Delete(ctx context.Context, ref DocumentRef) (Result, error) {
	return p.inner.Delete(ctx, ref)
}
func (p *flakyProvider) Status(ctx context.Context, ref DocumentRef) (RemoteStatus, error) {
	return p.inner.Status(ctx, ref)
}
func (p *flakyProvider) Get(ctx context.Context, ref DocumentRef) ([]byte, bool, error) {
	return p.inner.Get(ctx, ref)
}

type countingProvider struct {
	inner  *MemoryProvider
	pushes int
}

func (p *countingProvider) Kind() Kind { return KindGitHub }
func (p *countingProvider) Push(ctx context.Context, doc Document) (Result, error) {
	p.pushes++
	return p.inner.Push(ctx, doc)
}
func (p *countingProvider) Delete(ctx context.Context, ref DocumentRef) (Result, error) {
	return p.inner.Delete(ctx, ref)
}
func (p *countingProvider) Status(ctx context.Context, ref DocumentRef) (RemoteStatus, error) {
	return p.inner.Status(ctx, ref)
}
func (p *countingProvider) Get(ctx context.Context, ref DocumentRef) ([]byte, bool, error) {
	return p.inner.Get(ctx, ref)
}
