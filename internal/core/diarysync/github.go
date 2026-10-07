package diarysync

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const githubAPI = "https://api.github.com"

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type GitHubSettings struct {
	Owner  string
	Repo   string
	Branch string
}

// ParseGitHubSettings validates and normalizes GitHub medium settings.
// Pasted values such as "https://github.com/owner/repo.git" or "owner/repo"
// are split into owner and repo; an empty branch becomes "main".
func ParseGitHubSettings(settings map[string]string) (GitHubSettings, error) {
	out, err := NormalizeGitHubSettings(settings)
	if err != nil {
		return GitHubSettings{}, err
	}
	if out.Owner == "" || out.Repo == "" {
		return GitHubSettings{}, ErrGitHubRepoRequired
	}
	return out, nil
}

// NormalizeGitHubSettings extracts owner/repo from pasted URLs, defaults an
// empty branch to "main", and checks character sets. owner and repo are required.
func NormalizeGitHubSettings(settings map[string]string) (GitHubSettings, error) {
	owner := strings.TrimSpace(settings["owner"])
	repo := strings.TrimSpace(settings["repo"])
	branch := strings.TrimSpace(settings["branch"])

	ownerFromURL := false
	if parsedOwner, parsedRepo, ok := splitGitHubRef(owner); ok {
		owner = parsedOwner
		repo = parsedRepo
		ownerFromURL = true
	}
	if !ownerFromURL {
		if parsedOwner, parsedRepo, ok := splitGitHubRef(repo); ok {
			if owner == "" {
				owner = parsedOwner
			}
			repo = parsedRepo
		}
	}
	repo = strings.TrimSuffix(repo, ".git")
	owner = strings.TrimSuffix(owner, ".git")

	if owner == "" || repo == "" {
		return GitHubSettings{}, fieldErrorf("owner", "owner 和 repo 不能为空。示例：owner 填 alice，repo 填 diary（不要粘贴完整 URL，系统会自动拆分 https://github.com/alice/diary）")
	}
	if err := validateGitHubName("owner", owner, "alice"); err != nil {
		return GitHubSettings{}, err
	}
	if err := validateGitHubName("repo", repo, "diary"); err != nil {
		return GitHubSettings{}, err
	}
	if branch == "" {
		branch = "main"
	}
	if strings.ContainsAny(branch, " \t\r\n") {
		return GitHubSettings{}, fieldErrorf("branch", "branch 不能包含空白字符。示例：main")
	}
	if strings.HasPrefix(branch, "/") || strings.Contains(branch, "..") {
		return GitHubSettings{}, fieldErrorf("branch", "branch 不能以 / 开头，也不能包含 ..。示例：main")
	}
	return GitHubSettings{Owner: owner, Repo: repo, Branch: branch}, nil
}

// splitGitHubRef accepts "https://github.com/owner/repo(.git)",
// "git@github.com:owner/repo.git", or "owner/repo" and returns the two names.
func splitGitHubRef(value string) (owner, repo string, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	value = strings.TrimSuffix(value, "/")
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "https://github.com/"), strings.HasPrefix(lower, "http://github.com/"):
		u, err := url.Parse(value)
		if err != nil {
			return "", "", false
		}
		parts := splitPath(u.Path)
		if len(parts) < 2 {
			return "", "", false
		}
		return parts[0], strings.TrimSuffix(parts[1], ".git"), true
	case strings.HasPrefix(lower, "git@github.com:"):
		rest := value[strings.Index(value, ":")+1:]
		parts := splitPath(rest)
		if len(parts) < 2 {
			return "", "", false
		}
		return parts[0], strings.TrimSuffix(parts[1], ".git"), true
	case strings.HasPrefix(lower, "github.com/"):
		parts := splitPath(strings.TrimPrefix(value, value[:strings.Index(lower, "github.com/")+len("github.com/")]))
		if len(parts) < 2 {
			return "", "", false
		}
		return parts[0], strings.TrimSuffix(parts[1], ".git"), true
	default:
		if strings.Count(value, "/") != 1 {
			return "", "", false
		}
		owner, repo, _ = strings.Cut(value, "/")
		owner = strings.TrimSpace(owner)
		repo = strings.TrimSuffix(strings.TrimSpace(repo), ".git")
		if owner == "" || repo == "" || strings.Contains(owner, " ") || strings.Contains(repo, " ") {
			return "", "", false
		}
		return owner, repo, true
	}
}

func validateGitHubName(field, value, example string) error {
	if strings.Contains(value, "/") || !namePattern.MatchString(value) {
		return fieldErrorf(field, "%s 只能包含字母、数字、下划线、点和连字符，且不能包含 /。示例：%s", field, example)
	}
	return nil
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

// ApplyGitHubSettings validates settings for a GitHub medium and writes the
// normalized owner, repo, and branch back. Other keys (such as
// preserve_existing) are kept.
func ApplyGitHubSettings(settings map[string]string) (map[string]string, error) {
	parsed, err := NormalizeGitHubSettings(settings)
	if err != nil {
		return nil, err
	}
	out := cloneSettings(settings)
	out["owner"] = parsed.Owner
	out["repo"] = parsed.Repo
	out["branch"] = parsed.Branch
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

type githubFactory struct {
	apiBase string
	now     func() time.Time
	client  HTTPDoer
}

func (f githubFactory) Probe() bool { return true }

func (f githubFactory) Open(medium Medium, credential string) (Provider, error) {
	p, err := newGitHubProvider(medium, credential, f.client, f.now)
	if err != nil {
		return nil, err
	}
	if f.apiBase != "" {
		p.apiBase = strings.TrimRight(f.apiBase, "/")
	}
	return p, nil
}

func GitHubFactory(now func() time.Time, client HTTPDoer) Factory {
	return GitHubFactoryAt(githubAPI, now, client)
}

// GitHubFactoryAt is GitHubFactory pointed at a specific API origin. Tests use
// it so create and update probes never leave the process.
func GitHubFactoryAt(apiBase string, now func() time.Time, client HTTPDoer) Factory {
	return githubFactory{apiBase: apiBase, now: now, client: client}
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
		return Result{}, p.explainWrite(ctx, err)
	}
	remoteID := out.Content.SHA
	if remoteID == "" {
		remoteID = out.Commit.SHA
	}
	return Result{Path: path, RemoteID: remoteID, SyncedAt: p.now()}, nil
}

func (p *GitHubProvider) Verify(ctx context.Context) (VerifyResult, error) {
	if err := ctx.Err(); err != nil {
		return VerifyResult{}, err
	}
	steps := []func(context.Context) VerifyCheck{p.checkToken, p.checkRepo, p.checkBranch}
	checks := make([]VerifyCheck, 0, len(steps))
	for _, step := range steps {
		check := step(ctx)
		checks = append(checks, check)
		if !check.OK {
			return VerifyResult{OK: false, Checks: checks}, nil
		}
	}
	return VerifyResult{OK: true, Checks: checks}, nil
}

func (p *GitHubProvider) checkToken(ctx context.Context) VerifyCheck {
	status, detail, err := p.probe(ctx, p.apiBase+"/user")
	if err != nil {
		return VerifyCheck{Name: "token", OK: false, Message: friendlyProbeFailure("token")}
	}
	if status == http.StatusOK {
		return VerifyCheck{Name: "token", OK: true, Message: "token 有效"}
	}
	return VerifyCheck{Name: "token", OK: false, Message: classifyGitHubStatus(status, detail, p.settings, false).Error()}
}

func (p *GitHubProvider) checkRepo(ctx context.Context) VerifyCheck {
	status, detail, err := p.probe(ctx, p.repoURL())
	if err != nil {
		return VerifyCheck{Name: "repository", OK: false, Message: friendlyProbeFailure("repository")}
	}
	if status == http.StatusOK {
		return VerifyCheck{Name: "repository", OK: true, Message: fmt.Sprintf("仓库 %s/%s 可访问", p.settings.Owner, p.settings.Repo)}
	}
	return VerifyCheck{Name: "repository", OK: false, Message: classifyGitHubStatus(status, detail, p.settings, true).Error()}
}

func (p *GitHubProvider) checkBranch(ctx context.Context) VerifyCheck {
	status, detail, err := p.probe(ctx, p.branchURL())
	if err != nil {
		return VerifyCheck{Name: "branch", OK: false, Message: friendlyProbeFailure("branch")}
	}
	if status == http.StatusOK {
		return VerifyCheck{Name: "branch", OK: true, Message: fmt.Sprintf("分支 %s 存在", p.settings.Branch)}
	}
	if status == http.StatusNotFound {
		return VerifyCheck{Name: "branch", OK: false, Message: branchMissingMessage(p.settings, detail)}
	}
	return VerifyCheck{Name: "branch", OK: false, Message: classifyGitHubStatus(status, detail, p.settings, false).Error()}
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
		return Result{}, p.explainWrite(ctx, err)
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

func (p *GitHubProvider) repoURL() string {
	return fmt.Sprintf("%s/repos/%s/%s", p.apiBase, url.PathEscape(p.settings.Owner), url.PathEscape(p.settings.Repo))
}

func (p *GitHubProvider) branchURL() string {
	return fmt.Sprintf("%s/repos/%s/%s/branches/%s", p.apiBase, url.PathEscape(p.settings.Owner), url.PathEscape(p.settings.Repo), url.PathEscape(p.settings.Branch))
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
	detail := githubDetail(resp.StatusCode, b)
	return classifyGitHubStatus(resp.StatusCode, detail, GitHubSettings{}, false)
}

func githubDetail(status int, body []byte) string {
	msg := githubMessage(body)
	if msg == "" {
		msg = http.StatusText(status)
	}
	msg = strings.Join(strings.Fields(msg), " ")
	const max = 180
	if len(msg) > max {
		msg = msg[:max] + "…"
	}
	return fmt.Sprintf("github api %d: %s", status, msg)
}

func githubMessage(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.Message) != "" {
		return strings.TrimSpace(payload.Message)
	}
	return strings.TrimSpace(string(body))
}

func classifyGitHubStatus(status int, detail string, settings GitHubSettings, repoScope bool) *HTTPStatusError {
	switch status {
	case http.StatusUnauthorized:
		msg := "GitHub token 无效或已过期，请在 GitHub 重新生成"
		return httpStatusError(status, msg, msg, detail)
	case http.StatusForbidden:
		msg := "token 权限不足：需要目标仓库 Contents: Read and write 权限"
		lower := strings.ToLower(detail)
		if strings.Contains(lower, "rate limit") || strings.Contains(lower, "secondary rate") {
			msg = "GitHub API 触发了速率限制，请稍后再试。若频繁出现，请检查 token 权限是否为 Contents: Read and write"
		}
		return httpStatusError(status, msg, msg, detail)
	case http.StatusNotFound:
		if repoScope {
			msg := repoMissingMessage(settings)
			return httpStatusError(status, msg, msg, detail)
		}
		msg := "GitHub 返回 404：目标不存在，或当前 token 无权访问"
		return httpStatusError(status, msg, msg, detail)
	case http.StatusTooManyRequests:
		msg := "GitHub API 触发了速率限制，请稍后再试"
		return httpStatusError(status, msg, msg, detail)
	default:
		if status >= 500 {
			msg := "GitHub 服务暂时不可用，请稍后重试"
			return httpStatusError(status, msg, msg, detail)
		}
		msg := fmt.Sprintf("GitHub 请求失败（HTTP %d）", status)
		return httpStatusError(status, msg, msg, detail)
	}
}

func repoMissingMessage(settings GitHubSettings) string {
	return fmt.Sprintf("仓库 %s/%s 不存在，或当前 token 无权访问（私有仓库需在 PAT 中勾选该仓库）", settings.Owner, settings.Repo)
}

func branchMissingMessage(settings GitHubSettings, detail string) string {
	msg := fmt.Sprintf("分支 %s 不存在，请核对分支名", settings.Branch)
	if detail == "" {
		return msg
	}
	return msg + "（" + detail + "）"
}

// explainWrite attributes a failed contents write. A 404 is probed with at
// most two authenticated GETs (repository, then branch). Probe failures never
// hide the original error.
func (p *GitHubProvider) explainWrite(ctx context.Context, err error) error {
	var hs *HTTPStatusError
	if !errors.As(err, &hs) || hs.Status != http.StatusNotFound {
		return err
	}
	repoStatus, _, repoErr := p.probe(ctx, p.repoURL())
	if repoErr != nil {
		return err
	}
	if repoStatus == http.StatusNotFound {
		msg := repoMissingMessage(p.settings)
		return httpStatusError(http.StatusNotFound, msg, msg, hs.Detail)
	}
	if repoStatus != http.StatusOK {
		return err
	}
	branchStatus, _, branchErr := p.probe(ctx, p.branchURL())
	if branchErr != nil {
		return err
	}
	if branchStatus == http.StatusNotFound {
		msg := fmt.Sprintf("分支 %s 不存在，请核对分支名", p.settings.Branch)
		return httpStatusError(http.StatusNotFound, msg, msg, hs.Detail)
	}
	return err
}

// probe performs a single authenticated GET and does not retry.
func (p *GitHubProvider) probe(ctx context.Context, rawURL string) (status int, detail string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, "", err
	}
	p.auth(req)
	resp, err := p.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, githubDetail(resp.StatusCode, b), nil
}

func friendlyProbeFailure(name string) string {
	label := map[string]string{"token": "token", "repository": "仓库", "branch": "分支"}[name]
	if label == "" {
		label = name
	}
	return fmt.Sprintf("无法完成%s检查：GitHub 暂时无法连接，请稍后重试", label)
}
