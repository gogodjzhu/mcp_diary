package sync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGitHubProviderPushDeleteStatus(t *testing.T) {
	var putBody map[string]any
	var deleted bool
	files := map[string]string{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ghp_") {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/contents/"):
			path := r.URL.Path[strings.Index(r.URL.Path, "/contents/")+len("/contents/"):]
			if _, ok := files[path]; !ok {
				http.Error(w, "missing", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": "abc123", "path": path})
		case r.Method == http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &putBody)
			path := r.URL.Path[strings.Index(r.URL.Path, "/contents/")+len("/contents/"):]
			files[path] = "ok"
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": map[string]any{"sha": "newsha"},
				"commit":  map[string]any{"sha": "commitsha"},
			})
		case r.Method == http.MethodDelete:
			deleted = true
			path := r.URL.Path[strings.Index(r.URL.Path, "/contents/")+len("/contents/"):]
			delete(files, path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, r.Method+" "+r.URL.Path, http.StatusTeapot)
		}
	}))
	defer ts.Close()

	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	p, err := newGitHubProvider(Medium{
		Kind:     KindGitHub,
		Settings: map[string]string{"owner": "alice", "repo": "diary"},
	}, "ghp_xxx", ts.Client(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	p.apiBase = ts.URL

	doc := Document{Kind: DocumentText, Path: "diary/2026/2026-10.md", Body: []byte("# 2026-10\n")}
	got, err := p.Push(context.Background(), doc)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got.RemoteID != "newsha" {
		t.Fatalf("remote id = %s", got.RemoteID)
	}
	decoded, _ := base64.StdEncoding.DecodeString(putBody["content"].(string))
	if string(decoded) != string(doc.Body) {
		t.Fatalf("pushed body = %q", decoded)
	}
	if putBody["branch"] != "main" {
		t.Fatalf("branch = %v", putBody["branch"])
	}

	st, err := p.Status(context.Background(), DocumentRef{Kind: DocumentText, Path: doc.Path})
	if err != nil || !st.Exists {
		t.Fatalf("status = %+v err=%v", st, err)
	}
	if _, err := p.Delete(context.Background(), DocumentRef{Kind: DocumentText, Path: doc.Path}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !deleted {
		t.Fatal("expected delete request")
	}
	st, err = p.Status(context.Background(), DocumentRef{Kind: DocumentText, Path: doc.Path})
	if err != nil || st.Exists {
		t.Fatalf("after delete status = %+v err=%v", st, err)
	}
}

func TestGitHubProviderRequiresRepoAndToken(t *testing.T) {
	_, err := NewGitHubProvider(Medium{Settings: map[string]string{"owner": "a"}}, "tok")
	if err != ErrGitHubRepoRequired {
		t.Fatalf("err = %v", err)
	}
	_, err = NewGitHubProvider(Medium{Settings: map[string]string{"owner": "a", "repo": "b"}}, "")
	if err != ErrGitHubCredential {
		t.Fatalf("err = %v", err)
	}
}
