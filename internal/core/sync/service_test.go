package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
)

const testKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" // 32 bytes, base64

func testFS(t *testing.T) *filesystem.Service {
	t.Helper()
	fs, err := filesystem.New(filesystem.Options{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("filesystem.New: %v", err)
	}
	return fs
}

func testService(t *testing.T) (*Service, *filesystem.Service, *MemoryProvider, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	codec, err := NewCodec(testKey)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	provider := NewMemoryProvider(KindGitHub, func() time.Time { return now })
	reg := NewRegistry()
	reg.Register(KindGitHub, func(Medium, string) (Provider, error) { return provider, nil })
	svc := New(codec, reg, func() time.Time { return now })
	return svc, testFS(t), provider, now
}

func TestUpsertEncryptsCredentialAndHidesIt(t *testing.T) {
	svc, fs, _, _ := testService(t)
	ctx := context.Background()
	token := "ghp_super_secret_token"

	enabled := true
	got, err := svc.Upsert(ctx, fs, UpsertIn{
		Kind:       KindGitHub,
		Name:       "personal github",
		Enabled:    &enabled,
		Settings:   map[string]string{"owner": "alice", "repo": "diary"},
		Credential: token,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got.ID == "" || !got.HasCredential || got.Kind != KindGitHub {
		t.Fatalf("public medium = %+v", got)
	}

	raw, _ := json.Marshal(got)
	if bytes.Contains(raw, []byte(token)) {
		t.Fatal("public JSON leaked the credential")
	}
	if bytes.Contains(raw, []byte("ciphertext")) {
		t.Fatal("public JSON exposed ciphertext")
	}

	onDisk, err := os.ReadFile(filepath.Join(fs.Root(), ".mcp-diary", "sync-media.json"))
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	if bytes.Contains(onDisk, []byte(token)) {
		t.Fatal("plaintext credential written to disk")
	}
	if !bytes.Contains(onDisk, []byte(ciphertextPrefix)) {
		t.Fatal("expected encrypted ciphertext on disk")
	}

	st, err := svc.storeFor(ctx, fs)
	if err != nil {
		t.Fatalf("storeFor: %v", err)
	}
	plain, err := st.credential(ctx, got.ID)
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if plain != token {
		t.Fatalf("decrypted = %q", plain)
	}
}

func TestRejectCredentialInSettings(t *testing.T) {
	svc, fs, _, _ := testService(t)
	_, err := svc.Upsert(context.Background(), fs, UpsertIn{
		Kind:     KindGitHub,
		Name:     "bad",
		Settings: map[string]string{"token": "ghp_xxx"},
	})
	if !errors.Is(err, ErrSecretKey) {
		t.Fatalf("err = %v, want ErrSecretKey", err)
	}
}

func TestCredentialRequiresEncryptionKey(t *testing.T) {
	reg := NewRegistry()
	svc := New(&Codec{}, reg, nil)
	fs := testFS(t)
	_, err := svc.Upsert(context.Background(), fs, UpsertIn{
		Kind:       KindGitHub,
		Name:       "github",
		Credential: "ghp_xxx",
	})
	if !errors.Is(err, ErrNoEncryptionKey) {
		t.Fatalf("err = %v, want ErrNoEncryptionKey", err)
	}
}

func TestFakeProviderPushDeleteStatus(t *testing.T) {
	svc, fs, mem, now := testService(t)
	ctx := context.Background()

	medium, err := svc.Upsert(ctx, fs, UpsertIn{
		Kind:       KindGitHub,
		Name:       "github",
		Credential: "ghp_xxx",
		Settings:   map[string]string{"owner": "alice", "repo": "diary"},
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	doc := Document{
		Kind:        DocumentText,
		Path:        "diary/2026/2026-10.md",
		Body:        []byte("# 2026-10-04\nhello"),
		ContentType: "text/markdown",
		Source:      Source{EntryIDs: []string{"de_1"}, Revision: 3, UpdatedAt: now},
	}
	pushed, err := svc.Push(ctx, fs, medium.ID, doc)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if pushed.Path != doc.Path || pushed.RemoteID == "" {
		t.Fatalf("result = %+v", pushed)
	}
	if string(mem.Body(doc.Path)) != string(doc.Body) {
		t.Fatalf("remote body = %q", mem.Body(doc.Path))
	}

	st, err := svc.State(ctx, fs, medium.ID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if st.Status != StatusSucceeded || st.LastSyncedAt == nil {
		t.Fatalf("state = %+v", st)
	}
	ds := st.Documents[doc.Path]
	if ds.Revision != 3 || len(ds.EntryIDs) != 1 || ds.EntryIDs[0] != "de_1" {
		t.Fatalf("document state = %+v", ds)
	}

	remote, err := svc.RemoteStatus(ctx, fs, medium.ID, DocumentRef{Kind: DocumentText, Path: doc.Path})
	if err != nil {
		t.Fatalf("RemoteStatus: %v", err)
	}
	if !remote.Exists || remote.RemoteID != pushed.RemoteID {
		t.Fatalf("remote = %+v", remote)
	}

	if _, err := svc.Remove(ctx, fs, medium.ID, DocumentRef{Kind: DocumentText, Path: doc.Path}); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if mem.Body(doc.Path) != nil {
		t.Fatal("expected remote file deleted")
	}
	st, err = svc.State(ctx, fs, medium.ID)
	if err != nil {
		t.Fatalf("State after delete: %v", err)
	}
	if _, ok := st.Documents[doc.Path]; ok {
		t.Fatal("document state should be cleared")
	}
	remote, err = svc.RemoteStatus(ctx, fs, medium.ID, DocumentRef{Kind: DocumentText, Path: doc.Path})
	if err != nil {
		t.Fatalf("RemoteStatus after delete: %v", err)
	}
	if remote.Exists {
		t.Fatal("remote should not exist")
	}
}

func TestAttachmentChannelReserved(t *testing.T) {
	svc, fs, _, _ := testService(t)
	ctx := context.Background()
	medium, err := svc.Upsert(ctx, fs, UpsertIn{Kind: KindGitHub, Name: "github", Credential: "x"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	_, err = svc.Push(ctx, fs, medium.ID, Document{Kind: DocumentAttachment, Path: "attachments/a.bin"})
	if !errors.Is(err, ErrAttachmentNotSupported) {
		t.Fatalf("Push err = %v", err)
	}
	_, err = svc.Remove(ctx, fs, medium.ID, DocumentRef{Kind: DocumentAttachment, Path: "attachments/a.bin"})
	if !errors.Is(err, ErrAttachmentNotSupported) {
		t.Fatalf("Remove err = %v", err)
	}
}

func TestPersistReloadAndIsolation(t *testing.T) {
	svc, fs, _, now := testService(t)
	ctx := context.Background()
	medium, err := svc.Upsert(ctx, fs, UpsertIn{Kind: KindLocalDisk, Name: "disk", Settings: map[string]string{"path": "/tmp/diary"}})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	codec, err := NewCodec(testKey)
	if err != nil {
		t.Fatal(err)
	}
	reloaded := New(codec, NewRegistry(), func() time.Time { return now })
	reopened, err := filesystem.New(filesystem.Options{Root: fs.Root()})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Get(ctx, reopened, medium.ID)
	if err != nil {
		t.Fatalf("reload Get: %v", err)
	}
	if got.Name != "disk" || got.Kind != KindLocalDisk {
		t.Fatalf("reloaded = %+v", got)
	}

	other := testFS(t)
	if _, err := svc.Get(ctx, other, medium.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other workspace err = %v, want not found", err)
	}
}

func TestHexEncryptionKey(t *testing.T) {
	hexKey := "3031323334353637383961626364656630313233343536373839616263646566"
	codec, err := NewCodec(hexKey)
	if err != nil {
		t.Fatalf("NewCodec hex: %v", err)
	}
	enc, err := codec.Encrypt("ghp_xxx")
	if err != nil {
		t.Fatal(err)
	}
	got, err := codec.Decrypt(enc)
	if err != nil || got != "ghp_xxx" {
		t.Fatalf("roundtrip = %q, %v", got, err)
	}
}

func TestTriggerUsesEngineAndRecordsFailureWithoutOne(t *testing.T) {
	svc, fs, _, _ := testService(t)
	ctx := context.Background()
	medium, err := svc.Upsert(ctx, fs, UpsertIn{Kind: KindGitHub, Name: "github", Credential: "x"})
	if err != nil {
		t.Fatal(err)
	}

	st, err := svc.Trigger(ctx, fs, medium.ID)
	if !errors.Is(err, ErrEngineNotConfigured) {
		t.Fatalf("err = %v, want ErrEngineNotConfigured", err)
	}
	if st == nil || st.Status != StatusFailed || st.LastError == "" {
		t.Fatalf("state = %+v", st)
	}

	svc.SetRunner(funcRunner(func(context.Context, *filesystem.Service, string) error { return nil }))
	st, err = svc.Trigger(ctx, fs, medium.ID)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if st.Status != StatusSucceeded {
		t.Fatalf("state after engine = %+v", st)
	}

	disabled := false
	if _, err := svc.Upsert(ctx, fs, UpsertIn{ID: medium.ID, Name: "github", Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Trigger(ctx, fs, medium.ID); !errors.Is(err, ErrMediumDisabled) {
		t.Fatalf("err = %v, want ErrMediumDisabled", err)
	}
}

func TestListViewsIncludesState(t *testing.T) {
	svc, fs, _, _ := testService(t)
	ctx := context.Background()
	if _, err := svc.Upsert(ctx, fs, UpsertIn{Kind: KindGitHub, Name: "github", Credential: "x"}); err != nil {
		t.Fatal(err)
	}
	views, err := svc.ListViews(ctx, fs)
	if err != nil {
		t.Fatalf("ListViews: %v", err)
	}
	if len(views) != 1 || views[0].State.Status != StatusIdle {
		t.Fatalf("views = %+v", views)
	}
}

type funcRunner func(context.Context, *filesystem.Service, string) error

func (f funcRunner) SyncAll(ctx context.Context, fs *filesystem.Service, mediumID string) error {
	return f(ctx, fs, mediumID)
}

func TestUnknownKindAndInvalidKey(t *testing.T) {
	if _, err := NewCodec("too-short"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("NewCodec err = %v", err)
	}
	svc, fs, _, _ := testService(t)
	_, err := svc.Upsert(context.Background(), fs, UpsertIn{Kind: Kind("dropbox"), Name: "x"})
	if !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("err = %v, want ErrUnknownKind", err)
	}
}

func TestMediumLogAndStringHideSecret(t *testing.T) {
	m := Medium{ID: "sm_1", Kind: KindGitHub, Name: "github", Secret: Secret{Ciphertext: "s1:AAAA"}}
	if strings.Contains(m.String(), "s1:") || strings.Contains(m.String(), "AAAA") {
		t.Fatalf("String leaked secret: %s", m.String())
	}
	raw, _ := json.Marshal(m.Public())
	if bytes.Contains(raw, []byte("s1:")) {
		t.Fatal("Public JSON leaked ciphertext")
	}
}

func TestPushFailureRecorded(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	codec, _ := NewCodec(testKey)
	reg := NewRegistry()
	reg.Register(KindGitHub, func(Medium, string) (Provider, error) {
		return failingProvider{}, nil
	})
	svc := New(codec, reg, func() time.Time { return now })
	fs := testFS(t)
	ctx := context.Background()
	medium, err := svc.Upsert(ctx, fs, UpsertIn{Kind: KindGitHub, Name: "github", Credential: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Push(ctx, fs, medium.ID, Document{Kind: DocumentText, Path: "a.md", Body: []byte("x")})
	if err == nil {
		t.Fatal("expected push failure")
	}
	st, err := svc.State(ctx, fs, medium.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != StatusFailed || st.LastError == "" {
		t.Fatalf("state = %+v", st)
	}
}

type failingProvider struct{}

func (failingProvider) Kind() Kind { return KindGitHub }
func (failingProvider) Push(context.Context, Document) (Result, error) {
	return Result{}, errors.New("remote unavailable")
}
func (failingProvider) Delete(context.Context, DocumentRef) (Result, error) {
	return Result{}, errors.New("remote unavailable")
}
func (failingProvider) Status(context.Context, DocumentRef) (RemoteStatus, error) {
	return RemoteStatus{}, errors.New("remote unavailable")
}
