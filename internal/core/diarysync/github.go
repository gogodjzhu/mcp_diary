package diarysync

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const githubAPI = "https://api.github.com"

type GitHubSettings struct {
	Owner  string
	Repo   string
	Branch string
}

func ParseGitHubSettings(settings map[string]string) (GitHubSettings, error) {
	out := GitHubSettings{
		Owner:  strings.TrimSpace(settings["owner"]),
		Repo:   strings.TrimSpace(settings["repo"]),
		Branch: strings.TrimSpace(settings["branch"]),
	}
	if out.Owner == "" || out.Repo == "" {
		return GitHubSettings{}, ErrGitHubRepoRequired
	}
	if out.Branch == "" {
		out.Branch = "main"
	}
	return out, nil
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type GitHubProvider struct {
	settings GitHubSettings
	token    string
	http     HTTPDoer
	now      func() time.Time
	apiBase  string
}

func NewGitHubProvider(medium Medium, credential string) (*GitHubProvider, error) {
	return newGitHubProvider(medium, credential, nil, nil)
}

func GitHubFactory(now func() time.Time, client HTTPDoer) Factory {
	return func(medium Medium, credential string) (Provider, error) {
		return newGitHubProvider(medium, credential, client, now)
	}
}

func newGitHubProvider(medium Medium, credential string, client HTTPDoer, now func() time.Time) (*GitHubProvider, error) {
	settings, err := ParseGitHubSettings(medium.Settings)
	if err != nil {
		return nil, err
	}
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return nil, ErrGitHubCredential
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if now == nil {
		now = func() time.Time { return time.Now() }
	}
	return &GitHubProvider{
		settings: settings,
		token:    credential,
		http:     client,
		now:      now,
		apiBase:  githubAPI,
	}, nil
}

func (p *GitHubProvider) Kind() Kind { return KindGitHub }

func (p *GitHubProvider) Push(ctx context.Context, doc Document) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if doc.Kind == DocumentAttachment {
		return Result{}, ErrAttachmentNotSupported
	}
	path := strings.TrimPrefix(doc.Path, "/")
	existing, err := p.getFile(ctx, path)
	if err != nil {
		return Result{}, err
	}
	message := fmt.Sprintf("sync diary %s", path)
	sha := ""
	if existing != nil {
		sha = existing.SHA
	}
	body := map[string]any{
		"message": message,
		"content": base64.StdEncoding.EncodeToString(doc.Body),
		"branch":  p.settings.Branch,
	}
	if sha != "" {
		body["sha"] = sha
	}
	var out githubContentResponse
	if err := p.doJSON(ctx, http.MethodPut, p.contentsURL(path), body, http.StatusOK, http.StatusCreated, &out); err != nil {
		return Result{}, err
	}
	remoteID := out.Content.SHA
	if remoteID == "" {
		remoteID = out.Commit.SHA
	}
	return Result{Path: path, RemoteID: remoteID, SyncedAt: p.now()}, nil
}

func (p *GitHubProvider) Delete(ctx context.Context, ref DocumentRef) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if ref.Kind == DocumentAttachment {
		return Result{}, ErrAttachmentNotSupported
	}
	path := strings.TrimPrefix(ref.Path, "/")
	existing, err := p.getFile(ctx, path)
	if err != nil {
		return Result{}, err
	}
	if existing == nil {
		return Result{Path: path, SyncedAt: p.now()}, nil
	}
	body := map[string]any{
		"message": fmt.Sprintf("delete diary %s", path),
		"sha":     existing.SHA,
		"branch":  p.settings.Branch,
	}
	if err := p.doJSON(ctx, http.MethodDelete, p.contentsURL(path), body, http.StatusOK, http.StatusOK, nil); err != nil {
		return Result{}, err
	}
	return Result{Path: path, RemoteID: existing.SHA, SyncedAt: p.now()}, nil
}

func (p *GitHubProvider) Status(ctx context.Context, ref DocumentRef) (RemoteStatus, error) {
	if err := ctx.Err(); err != nil {
		return RemoteStatus{}, err
	}
	path := strings.TrimPrefix(ref.Path, "/")
	existing, err := p.getFile(ctx, path)
	if err != nil {
		return RemoteStatus{}, err
	}
	if existing == nil {
		return RemoteStatus{Path: path, Exists: false}, nil
	}
	return RemoteStatus{Path: path, Exists: true, RemoteID: existing.SHA, UpdatedAt: p.now()}, nil
}

// Get returns the decoded body of the remote file. The GitHub Contents API
// inlines the body as base64 in "content"; for files larger than 1 MiB it is
// omitted and we fall back to "download_url".
func (p *GitHubProvider) Get(ctx context.Context, ref DocumentRef) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if ref.Kind == DocumentAttachment {
		return nil, false, ErrAttachmentNotSupported
	}
	path := strings.TrimPrefix(ref.Path, "/")
	file, err := p.getFile(ctx, path)
	if err != nil {
		return nil, false, err
	}
	if file == nil {
		return nil, false, nil
	}
	if file.Content != "" {
		raw := strings.ReplaceAll(file.Content, "\n", "")
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, false, err
		}
		return decoded, true, nil
	}
	if file.DownloadURL != "" {
		body, err := p.getRaw(ctx, file.DownloadURL)
		if err != nil {
			return nil, false, err
		}
		return body, true, nil
	}
	return []byte{}, true, nil
}

// getRaw downloads a raw file body (used for over-sized contents responses).
func (p *GitHubProvider) getRaw(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	p.auth(req)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, githubAPIError(resp)
	}
	return io.ReadAll(resp.Body)
}

type githubFile struct {
	SHA         string `json:"sha"`
	Content     string `json:"content"`
	DownloadURL string `json:"download_url"`
	Name        string `json:"name"`
	Path        string `json:"path"`
}

type githubContentResponse struct {
	Content githubFile `json:"content"`
	Commit  struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

func (p *GitHubProvider) getFile(ctx context.Context, path string) (*githubFile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.contentsURL(path), nil)
	if err != nil {
		return nil, err
	}
	p.auth(req)
	q := req.URL.Query()
	q.Set("ref", p.settings.Branch)
	req.URL.RawQuery = q.Encode()
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, githubAPIError(resp)
	}
	var file githubFile
	if err := json.NewDecoder(resp.Body).Decode(&file); err != nil {
		return nil, err
	}
	return &file, nil
}

func (p *GitHubProvider) contentsURL(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return fmt.Sprintf("%s/repos/%s/%s/contents/%s", p.apiBase, url.PathEscape(p.settings.Owner), url.PathEscape(p.settings.Repo), strings.Join(parts, "/"))
}

func (p *GitHubProvider) auth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "mcp-diary")
}

func (p *GitHubProvider) doJSON(ctx context.Context, method, rawURL string, body any, ok1, ok2 int, dest any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, r)
	if err != nil {
		return err
	}
	p.auth(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != ok1 && resp.StatusCode != ok2 {
		return githubAPIError(resp)
	}
	if dest == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

func githubAPIError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	msg := strings.TrimSpace(string(b))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("github api %s: %s", resp.Status, msg)
}
