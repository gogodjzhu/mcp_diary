package diarysync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sha":     "abc123",
				"path":    path,
				"content": base64.StdEncoding.EncodeToString([]byte("remote body")),
			})
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

	body, exists, err := p.Get(context.Background(), DocumentRef{Kind: DocumentText, Path: doc.Path})
	if err != nil || !exists || string(body) != "remote body" {
		t.Fatalf("Get = %q exists=%v err=%v", body, exists, err)
	}
	_, exists, err = p.Get(context.Background(), DocumentRef{Kind: DocumentText, Path: "2026/2026-11.md"})
	if err != nil || exists {
		t.Fatalf("Get missing = exists=%v err=%v", exists, err)
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
	if !errors.Is(err, ErrGitHubRepoRequired) && !errors.Is(err, ErrGitHubSettings) {
		t.Fatalf("err = %v", err)
	}
	_, err = NewGitHubProvider(Medium{Settings: map[string]string{"owner": "a", "repo": "b"}}, "")
	if err != ErrGitHubCredential {
		t.Fatalf("err = %v", err)
	}
}

func TestNormalizeGitHubSettings(t *testing.T) {
	cases := []struct {
		name   string
		in     map[string]string
		owner  string
		repo   string
		branch string
	}{
		{name: "plain", in: map[string]string{"owner": "Alice", "repo": "Diary"}, owner: "Alice", repo: "Diary", branch: "main"},
		{name: "url", in: map[string]string{"owner": "https://github.com/alice/diary.git"}, owner: "alice", repo: "diary", branch: "main"},
		{name: "split url", in: map[string]string{"repo": "https://github.com/bob/notes.git", "branch": "dev"}, owner: "bob", repo: "notes", branch: "dev"},
		{name: "owner/repo", in: map[string]string{"repo": "carol/journal.git"}, owner: "carol", repo: "journal", branch: "main"},
		{name: "ssh", in: map[string]string{"owner": "git@github.com:dave/diary.git"}, owner: "dave", repo: "diary", branch: "main"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeGitHubSettings(tc.in)
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if got.Owner != tc.owner || got.Repo != tc.repo || got.Branch != tc.branch {
				t.Fatalf("got %+v", got)
			}
		})
	}

	bad := []map[string]string{
		{"owner": "al ice", "repo": "diary"},
		{"owner": "alice", "repo": "has space"},
		{"owner": "alice", "repo": "diary", "branch": "feature/../main"},
		{"owner": "alice", "repo": "diary", "branch": "/main"},
		{"owner": "alice", "repo": "diary", "branch": "my branch"},
		{"owner": "", "repo": ""},
	}
	for i, settings := range bad {
		if _, err := NormalizeGitHubSettings(settings); err == nil {
			t.Fatalf("case %d should fail: %+v", i, settings)
		} else if !errors.Is(err, ErrGitHubSettings) {
			t.Fatalf("case %d err = %v", i, err)
		}
	}
}

func TestGitHubAPIErrorClassification(t *testing.T) {
	type want struct {
		status  int
		contain string
	}
	cases := []struct {
		name   string
		status int
		body   string
		repo   int
		branch int
		want   want
		probes int
	}{
		{name: "401", status: 401, body: `{"message":"Bad credentials"}`, want: want{401, "token 无效或已过期"}},
		{name: "403", status: 403, body: `{"message":"Resource not accessible by integration"}`, want: want{403, "Contents: Read and write"}},
		{name: "403 rate", status: 403, body: `{"message":"API rate limit exceeded"}`, want: want{403, "速率限制"}},
		{name: "404 repo", status: 404, body: `{"message":"Not Found"}`, repo: 404, probes: 1, want: want{404, "仓库 alice/diary 不存在"}},
		{name: "404 branch", status: 404, body: `{"message":"Not Found"}`, repo: 200, branch: 404, probes: 2, want: want{404, "分支 dev 不存在"}},
		{name: "404 file", status: 404, body: `{"message":"Not Found"}`, repo: 200, branch: 200, probes: 2, want: want{404, "github api 404: Not Found"}},
		{name: "500", status: 500, body: `{"message":"Server Error"}`, want: want{500, "暂时不可用"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var probes int
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer ghp_test" {
					t.Errorf("auth = %q", got)
				}
				switch {
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/contents/"):
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"Not Found"}`))
				case r.Method == http.MethodPut:
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/branches/"):
					probes++
					code := tc.branch
					if code == 0 {
						code = http.StatusOK
					}
					w.WriteHeader(code)
					_, _ = w.Write([]byte(`{"message":"Branch not found"}`))
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/repos/"):
					probes++
					code := tc.repo
					if code == 0 {
						code = http.StatusOK
					}
					w.WriteHeader(code)
					_, _ = w.Write([]byte(`{"message":"Not Found"}`))
				default:
					http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusTeapot)
				}
			}))
			defer ts.Close()

			p := githubProviderFor(t, ts, "dev")
			_, err := p.Push(context.Background(), Document{Kind: DocumentText, Path: "2026/2026-10.md", Body: []byte("hi")})
			var hs *HTTPStatusError
			if !errors.As(err, &hs) {
				t.Fatalf("err = %v, want HTTPStatusError", err)
			}
			if hs.Status != tc.want.status || !strings.Contains(err.Error(), tc.want.contain) {
				t.Fatalf("err = %v", err)
			}
			if !strings.Contains(err.Error(), "github api") {
				t.Fatalf("missing raw detail: %v", err)
			}
			if probes != tc.probes {
				t.Fatalf("attribution GETs = %d, want %d", probes, tc.probes)
			}
		})
	}
}

func TestGitHub404AttributionDoesNotHideProbeError(t *testing.T) {
	var puts int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut || strings.Contains(r.URL.Path, "/contents/") {
			if r.Method == http.MethodPut {
				puts++
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("no hijacker")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}))
	defer ts.Close()

	p := githubProviderFor(t, ts, "main")
	_, err := p.Push(context.Background(), Document{Kind: DocumentText, Path: "a.md", Body: []byte("x")})
	var hs *HTTPStatusError
	if !errors.As(err, &hs) || hs.Status != http.StatusNotFound {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "仓库") {
		t.Fatalf("probe failure should keep original 404, got %v", err)
	}
	if puts != 1 {
		t.Fatalf("puts = %d", puts)
	}
}

func TestGitHubVerifySteps(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Fatal("missing auth")
		}
		switch r.URL.Path {
		case "/user":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		case "/repos/alice/diary":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"full_name":"alice/diary"}`))
		case "/repos/alice/diary/branches/main":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Branch not found"}`))
		default:
			http.Error(w, r.URL.Path, http.StatusTeapot)
		}
	}))
	defer ts.Close()

	p := githubProviderFor(t, ts, "main")
	got, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.OK || len(got.Checks) != 3 || !got.Checks[0].OK || !got.Checks[1].OK || got.Checks[2].OK {
		t.Fatalf("result = %+v", got)
	}
	if !strings.Contains(got.Checks[2].Message, "分支 main 不存在") {
		t.Fatalf("branch message = %q", got.Checks[2].Message)
	}
}

func TestGitHubVerifyStopsAtFirstFailure(t *testing.T) {
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/user" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
			return
		}
		t.Errorf("should not probe %s after token failure", r.URL.Path)
		http.Error(w, "unexpected", http.StatusTeapot)
	}))
	defer ts.Close()

	p := githubProviderFor(t, ts, "main")
	got, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.OK || len(got.Checks) != 1 || got.Checks[0].Name != "token" || got.Checks[0].OK {
		t.Fatalf("result = %+v", got)
	}
	if !strings.Contains(got.Checks[0].Message, "token 无效或已过期") {
		t.Fatalf("message = %q", got.Checks[0].Message)
	}
	if len(paths) != 1 || paths[0] != "/user" {
		t.Fatalf("paths = %v", paths)
	}
}

func githubProviderFor(t *testing.T, ts *httptest.Server, branch string) *GitHubProvider {
	t.Helper()
	p, err := newGitHubProvider(Medium{
		Kind: KindGitHub,
		Settings: map[string]string{
			"owner":  "alice",
			"repo":   "diary",
			"branch": branch,
		},
	}, "ghp_test", ts.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	p.apiBase = ts.URL
	return p
}
