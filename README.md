# mcp-diary

基于 [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) 与 [cobra](https://github.com/spf13/cobra)
构建的 **Streamable HTTP** MCP 服务端，核心能力是**安全地读写文件**。

服务端将某个目录作为“工作区（workspace）”暴露给 MCP 客户端，所有文件操作都被限制在该目录内，
彻底避免客户端读写宿主机上的任意文件。

## 特性

- **Streamable HTTP 传输**（默认），同时支持 stdio，适配不同 MCP 客户端。
- **Web 接入与浏览器界面**：同一进程同时提供 REST API（`/api/*`）与内嵌的 Vue 单页应用，可直接在浏览器里浏览工作区文件。
- **Google OAuth 2.0 / OIDC 认证**：作为资源服务器校验 Bearer Token，遵循 MCP 授权规范与 RFC 9728。
- **按用户隔离工作区**：认证后每个用户拥有独立沙箱目录，互相不可见，也与默认 Root 隔离。
- **沙箱化文件系统**：基于工作区根目录解析路径，防止 `../` 与符号链接逃逸。
- **只读模式**：一键禁用所有写操作，适合只读检索场景。
- **结构化输出**：每个工具同时返回结构化内容与可读的 JSON 文本。
- **可扩展架构**：新增工具只需实现 `tools.Tool` 接口并注册即可。
- **优雅退出**：监听 `SIGINT` / `SIGTERM`，HTTP 服务平滑关闭。

## 架构

```
cmd/mcp-diary/            程序入口
internal/
  cli/                    cobra 命令（serve / tools / version）
  config/                 运行配置、默认值与校验
  logging/                基于 slog 的日志构建
  auth/                   OAuth 2.0 / OIDC 资源服务器（校验、中间件、元数据）
  filesystem/             沙箱化文件服务（与协议解耦，可独立测试）
  workspace/              按用户解析隔离工作区
  tools/                  Tool 接口 + 注册表 + 结果辅助函数
    fstools/              文件系统相关工具实现
  access/                 接入层
    mcp/                  MCP Server 组装、Streamable HTTP 传输与元数据
    web/                  REST API（/api/*），复用 Google Bearer 认证
  app/                    组装根：共享依赖、路由与 HTTP 服务生命周期
  webui/                  go:embed 的前端产物与静态文件服务
web/                      Vite + Vue 3 + TypeScript 前端源码
```

分层原则：

- `filesystem` 不依赖任何协议，纯业务能力，便于测试与复用。
- `auth` 只负责令牌校验与请求身份注入，不感知工具。
- `workspace` 根据请求身份解析出对应的沙箱文件系统。
- `tools` 只负责把业务能力适配成 MCP 工具，运行时从上下文取工作区。
- `access/mcp` 与 `access/web` 是两个并列的接入层，共享 `auth` / `workspace` / `filesystem`。
- `app` 负责组装与生命周期，`cli` 只负责参数解析。

## 快速开始

```bash
# 编译
make build

# 以当前目录为工作区启动 Streamable HTTP 服务，默认监听 :8080，端点 /mcp
./bin/mcp-diary serve --root .

# 查看所有可用工具
./bin/mcp-diary tools --root .
```

健康检查与 HTTP 端点：

- `GET  /healthz` — 健康探测，返回服务与工具信息。
- `POST /mcp` — MCP Streamable HTTP 端点（启用认证时需 `Authorization: Bearer <token>`）。
- `GET  /.well-known/oauth-protected-resource/mcp` — OAuth 受保护资源元数据（RFC 9728）。
- `GET  /` — 浏览器界面（内嵌的 Vue 前端）。
- `GET  /api/me`、`/api/files`、`/api/file` — Web REST API（需 `Authorization: Bearer <token>`）。
  仅在启用认证时注册，未启用认证时不会暴露。

### 启用 Google OAuth2 / OIDC

参考 [OpenAI 插件认证文档](https://developers.openai.com/plugins/build/auth) 与
MCP 授权规范实现。完整配置见 [`docs/auth.md`](docs/auth.md)。

```bash
./bin/mcp-diary serve \
  --root /data \
  --auth-enabled \
  --auth-client-id "<google-client-id>" \
  --auth-public-url "https://mcp.example.com"
```

认证开启后，服务端会：

1. 在 `/.well-known/oauth-protected-resource/mcp` 公布受保护资源元数据，声明 Google 为授权服务器。
2. 对未携带 Token 的请求返回 `401` 与 `WWW-Authenticate` 挑战，引导客户端发起 OAuth 流程。
3. 校验每个请求的 Bearer Token：Google tokeninfo、`aud`（= client id）、有效期、scope 与已验证邮箱。
4. 以用户邮箱生成独立工作目录：`$ROOT/users/<email>/`，与其他用户及默认 Root 完全隔离。

### Web 界面

启用认证后，浏览器访问 `http://<host>:8080/` 即可打开 Web 界面：点击「使用 Google 登录」，
前端通过 Google Identity Services 获取 access token，再带 `Authorization: Bearer` 调用
`/api/*`（与 MCP 共用同一套 Google 校验），页面展示当前用户的工作区文件列表。

- 前端产物通过 `go:embed` 打进二进制；`make build` 会先构建前端。
- 只需 `go build`（如 `make build-go`）时使用仓库中已提交的 `internal/webui/dist`。
- 开发时 `make dev` 启动 Vite（`http://localhost:5173`），并把 `/api`、`/mcp` 代理到本地 `:8080`。
- 需要在 Google Cloud Console 的 **Authorized JavaScript origins** 中登记前端来源
  （如 `http://localhost:5173` 与生产域名）；scope 保持 `openid email profile`。

Docker 部署：

```bash
cp .env.example .env   # 填写 GOOGLE_CLIENT_ID 与 MCP_PUBLIC_URL
docker compose up --build -d
```

## 工具列表

| 工具 | 说明 | 只读 |
| --- | --- | --- |
| `read_file` | 读取文件内容，支持 `offset` / `limit` 分段读取 | ✅ |
| `write_file` | 创建或覆盖文件，可选择自动创建父目录 | |
| `append_file` | 追加内容，文件不存在时创建 | |
| `list_directory` | 列出目录内容，目录在前、文件在后 | ✅ |
| `file_info` | 查看路径的元信息（大小、权限、修改时间） | ✅ |
| `create_directory` | 创建目录 | |
| `delete_path` | 删除文件或目录（删除目录需 `recursive`） | |

## 客户端接入

支持 Streamable HTTP 的客户端可直接配置 URL：

```json
{
  "mcpServers": {
    "mcp-diary": {
      "url": "http://localhost:8080/mcp"
    }
  }
}
```

仅支持 stdio 的客户端（如 Claude Desktop），可通过 `mcp-remote` 代理：

```json
{
  "mcpServers": {
    "mcp-diary": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "http://localhost:8080/mcp"]
    }
  }
}
```

也可以直接使用 stdio 传输：

```bash
./bin/mcp-diary serve --transport stdio --root .
```

## CLI 参数

```
mcp-diary serve [flags]

  --transport string        传输方式：streamable-http 或 stdio（默认 "streamable-http"）
  --addr string             HTTP 监听地址（默认 ":8080"）
  --endpoint string         HTTP 端点路径（默认 "/mcp"）
  --root string             工作区根目录（默认 "."）
  --read-only               禁用所有写操作
  --allow-remote            关闭 DNS rebinding 防护（当通过非 localhost Host 访问时需要）
  --disable-streaming       禁用 SSE 流式响应，GET 返回 405
  --max-read-bytes int      单次读取返回的最大字节数（默认 1048576）
  --max-request-bytes int   HTTP 请求体上限（默认 33554432）

认证参数（详见 docs/auth.md）：
  --auth-enabled                          启用 Bearer Token 认证
  --auth-issuer string                    授权服务器 issuer（默认 Google）
  --auth-client-id string                 OAuth 客户端 id（Token audience）
  --auth-audience string                  覆盖期望 audience（默认等于 client id）
  --auth-scopes strings                   必需 scope（默认 openid,email,profile）
  --auth-public-url string                对外可访问的基础 URL
  --auth-tokeninfo-url string             Token 校验端点（默认 Google tokeninfo）
  --auth-userinfo-url string              可选的 userinfo 端点
  --auth-require-verified-email           要求邮箱已验证（默认 true）
  --auth-users-dir string                 按用户工作区目录（默认 "users"）

Web 参数：
  --web-enabled                           提供 Web 界面与 REST API（需配合 --auth-enabled，默认 true）
  --web-static-dir string                 从磁盘目录提供前端资源（开发用，默认使用内嵌产物）

全局参数：
  --log-level string        日志级别：debug/info/warn/error（默认 "info"）
  --log-format string       日志格式：text/json（默认 "text"）
```

## 安全模型

- 每个请求路径都会在**解析前**做词法校验，并在**解析后**对最长已存在前缀做符号链接解析，
  两者都必须落在工作区根目录内，否则返回 `path escapes workspace root`。
- `delete_path` 拒绝删除工作区根目录本身。
- 通过 `--read-only` 可让服务端完全不可写。
- Streamable HTTP 默认开启 DNS rebinding 防护；仅在受控环境（如反向代理）下才使用 `--allow-remote`。
- 启用认证后：每个请求必须携带有效 Bearer Token（校验 issuer/audience/有效期/scope/邮箱），
  用户目录名由邮箱安全化生成并限制在 `users/` 内，用户之间以及与默认 Root 相互隔离。
- Web REST API（`/api/*`）复用同一套 Bearer 校验，且只在启用认证时注册；未启用认证时不会暴露文件接口。
- 生产环境务必使用 HTTPS，并设置 `--auth-public-url` 以生成正确的元数据与挑战地址。

## 开发

```bash
make build       # 构建前端 + 编译服务端到 bin/
make build-go    # 仅编译服务端（使用已提交的前端产物）
make dev         # 启动 Vite 开发服务器（:5173，代理 /api 与 /mcp 到 :8080）
make test        # 单元测试 + HTTP 端到端集成测试（含认证与按用户隔离）
make test-race   # 竞态检测
make vet         # 静态检查
make fmt         # 格式化
```

集成测试会启动真实的 Streamable HTTP 服务端，并使用 mcp-go 官方客户端完成
`initialize → tools/list → write_file → append_file → read_file` 全链路验证；
认证测试使用模拟 IdP，验证 `401 挑战 → 元数据 → Bearer 通过 → 写入 $ROOT/users/<email>/`。
Web 接入层有独立的 `httptest` 用例，覆盖 `/api/me`、`/api/files`、`/api/file` 与错误码映射。

## 扩展新工具

1. 在 `internal/tools/<group>/` 下新建工具，实现 `tools.Tool`：

```go
type myTool struct{ /* 依赖注入 */ }

func (t myTool) Name() string             { return "my_tool" }
func (t myTool) Definition() mcp.Tool     { return mcp.NewTool(t.Name(), /* ... */) }
func (t myTool) Handle(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    return tools.Result(payload), nil
}
```

2. 在该 group 的 `All(...)` 中返回新工具，`app.New` 会自动完成注册。