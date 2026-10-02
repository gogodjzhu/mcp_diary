# mcp-diary

基于 [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) 与 [cobra](https://github.com/spf13/cobra)
构建的 **Streamable HTTP** MCP 服务端，核心能力是**对话式日记存取**。

服务端将某个目录作为“工作区（workspace）”作为日记数据的存储根，所有数据都写入该目录内，
彻底避免客户端读写宿主机上的任意文件。

> **近期变更**：服务已聚焦日记场景。原文件系统 MCP 工具（`read_file` 等 7 个）与原始文件
> REST 接口（`/api/files`、`/api/file`）已移除；文件操作仅作为日记存储的内部封装存在。

## 特性

- **Streamable HTTP 传输**（默认），同时支持 stdio，适配不同 MCP 客户端。
- **Web 接入与浏览器界面**：同一进程同时提供 REST API（`/api/*`）与内嵌的 Vue 单页应用，用于确认登录身份。
- **OAuth 2.1 授权服务器**：自身签发 token，Google 只做上游身份；客户端只需 MCP URL，动态注册，无需分发 client id / secret。
- **按用户隔离工作区**：认证后每个用户拥有独立沙箱目录，日记数据互相不可见，也与默认 Root 隔离。
- **封装的沙箱存储**：日记数据通过沙箱化文件服务落盘（原子写入、路径校验、防 `../` 与符号链接逃逸），不直接对外暴露文件接口。
- **只读模式**：一键禁用所有写操作，适合只读检索场景。
- **结构化输出**：每个工具同时返回结构化内容与可读的 JSON 文本。
- **可扩展架构**：新增工具只需实现 `tools.Tool` 接口并注册即可。
- **优雅退出**：监听 `SIGINT` / `SIGTERM`，HTTP 服务平滑关闭。

## 架构

```
cmd/mcp-diary/                程序入口
internal/
  core/                       领域与应用服务（不依赖任何接入层）
    diary/                    纯文本日记存取（草稿会话 + 正式条目）
    filesystem/               沙箱化文件服务（日记存储的封装底层）
    workspace/                按用户解析隔离工作区
  auth/                       安全域
    identity/                 请求身份（从 token 解析出的用户）
    oauth/                    OAuth 2.1 授权服务器（Google 联邦、动态注册、文件存储）
  transport/                  接入层：所有对外协议与 UI
    mcp/                      MCP Server 组装与 Streamable HTTP 传输
      tools/                  Tool 接口 + 注册表 + 结果辅助函数
        diary/                日记 MCP 工具
    httpapi/                  REST API（/api/*），复用同一套 OAuth 校验
    webui/                    go:embed 的前端产物与静态文件服务
  platform/                   基础设施
    config/                   运行配置、默认值与校验
    logging/                  基于 slog 的日志构建
  app/                        组装根：共享依赖、路由与 HTTP 服务生命周期
  cli/                        cobra 命令（serve / tools / version）
web/                          Vite + Vue 3 + TypeScript 前端源码
```

分层原则（由 `internal/arch_test.go` 固化，违规依赖会让测试失败）：

- `platform` 是无业务语义的基础设施（配置、日志），不依赖其他 internal 组。
- `auth/identity` 只描述请求身份；`auth/oauth` 是授权服务器（动态注册、Google 联邦、自签 token）。
- `core` 是业务层：`filesystem` 是与协议解耦的沙箱文件能力，`diary` 在其上实现按工作区隔离的日记存取，`workspace` 根据请求身份解析出对应沙箱。
- `transport` 是接入层：`mcp` 把业务能力适配成 MCP 工具（运行时从上下文取沙箱），`httpapi` 提供 REST API，`webui` 服务浏览器 UI；只允许依赖 `core` / `auth/identity` / `platform`。
- `app` 是唯一允许 import 所有 internal 包的组合根；`cli` 只通过 `app` 与 `platform` 交互。

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
- `GET  /.well-known/oauth-authorization-server` — 授权服务器元数据（RFC 8414）。
- `POST /oauth/register`、`/oauth/authorize`、`/oauth/token`、`/oauth/callback` — 授权流程。
- `GET  /` — 浏览器界面（内嵌的 Vue 前端）。
- `GET  /api/me` — Web REST API（需 `Authorization: Bearer <token>`）。
  仅在启用认证时注册，未启用认证时不会暴露。

### 启用 OAuth 2.1（Google 联邦）

`mcp-diary` 自己是授权服务器，Google 只是上游身份。完整配置见 [`docs/auth.md`](docs/auth.md)。

```bash
./bin/mcp-diary serve \
  --root /data \
  --auth-enabled \
  --auth-public-url "https://mcp.example.com" \
  --google-client-id "<google-client-id>" \
  --google-client-secret "<google-client-secret>" \
  --auth-encryption-key "$(openssl rand -base64 32)"
```

只需要一个 Google **Web application** 客户端，重定向 URI 固定为
`<auth-public-url>/oauth/callback`。secret 留在服务端，不发给任何客户端。

认证开启后，服务端会：

1. 在 `/.well-known/oauth-protected-resource/mcp` 公布受保护资源元数据，授权服务器指向自己。
2. 对未携带 Token 的请求返回 `401` 与 `WWW-Authenticate` 挑战，引导客户端动态注册并走 PKCE。
3. 校验自己签发的 Bearer Token（audience = `<public-url>/mcp`），再向 Google 确认用户身份。
4. 以用户邮箱生成独立工作目录：`$ROOT/users/<email>/`，与其他用户及默认 Root 完全隔离。

客户端只需 MCP URL，不需要 client id 或 secret：

```json
{ "mcpServers": { "mcp-diary": { "url": "https://mcp.example.com/mcp" } } }
```

### Web 界面

启用认证后，浏览器访问 `http://<host>:8080/` 即可打开 Web 界面：点击「使用 Google 登录」，
前端作为本服务的公共客户端走 Authorization Code + PKCE（client id `mcp-diary-web`，服务端预注册），
token 放在 `sessionStorage`，再带 `Authorization: Bearer` 调用 `/api/me` 确认身份。
日记数据请通过 MCP 客户端读写。

- 前端产物通过 `go:embed` 打进二进制；`make build` 会先构建前端。
- 只需 `go build`（如 `make build-go`）时使用仓库中已提交的 `internal/transport/webui/dist`。
- 开发时 `make dev` 启动 Vite（`http://localhost:5173`），并把 `/api`、`/mcp`、`/oauth` 代理到本地 `:8080`。
- 不需要在 Google Console 登记 JavaScript origins：浏览器不直接对接 Google。
- 对外发布前，把 OAuth consent screen 从 **Testing** 切到 **In production**，否则只有 Test users 能登录。

Docker 部署：

```bash
cp .env.example .env   # 填写 GOOGLE_CLIENT_ID、GOOGLE_CLIENT_SECRET、MCP_PUBLIC_URL、AUTH_ENCRYPTION_KEY
docker compose up --build -d
```

## 日记工具（第一版）

MCP 只负责纯文本草稿和正式日记的存取；追问、整理、成稿由宿主 Agent 完成。用户身份由 OAuth 决定，工具参数不接受 `user_id`。

| 工具 | 说明 | 只读 |
| --- | --- | --- |
| `createDiarySession` | 创建或恢复某业务日期的草稿 | |
| `appendDiarySession` | 向草稿追加文本 | |
| `getDiarySession` | 读取草稿 | ✅ |
| `updateDiarySession` | 整体替换草稿正文 | |
| `commitDiarySession` | 用户确认后提交为正式日记并删除草稿 | |
| `discardDiarySession` | 放弃未提交草稿 | |
| `getDiaryEntry` | 读取正式日记 | ✅ |
| `updateDiaryEntry` | 整体替换正式日记正文 | |
| `listDiaryEntries` | 按业务日期分页列出正式日记 | ✅ |
| `deleteDiaryEntry` | 永久删除正式日记（调用前需用户确认） | |

状态机：`draft -> committed` 或 `draft -> discarded`。终态不可回到草稿。同一用户同一 `diary_date` 只能有一篇正式日记。

日记数据以 JSON 形式持久化在每个工作区的 `.mcp-diary/diary.json`，写入走沙箱文件服务的原子替换，用户之间完全隔离。

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
  --auth-enabled                          启用 OAuth 2.1（本服务作为授权服务器）
  --auth-public-url string                对外可访问的基础 URL（即 issuer）
  --google-client-id string               服务端使用的 Google OAuth client id
  --google-client-secret string           Google OAuth client secret（只留在服务端）
  --auth-encryption-key string            加密落盘 OAuth 状态的 32 字节密钥（base64 或 hex）
  --auth-access-token-ttl duration        签发的 access token 有效期（默认 1h）
  --auth-refresh-token-ttl duration       签发的 refresh token 有效期（默认 2160h）
  --auth-max-clients-per-ip int           每个 IP 的动态注册上限（默认 10）
  --auth-store-dir string                 OAuth 状态目录（默认 "auth"）
  --auth-users-dir string                 按用户工作区目录（默认 "users"）

Web 参数：
  --web-enabled                           提供 Web 界面与 REST API（需配合 --auth-enabled，默认 true）
  --web-static-dir string                 从磁盘目录提供前端资源（开发用，默认使用内嵌产物）

全局参数：
  --log-level string        日志级别：debug/info/warn/error（默认 "info"）
  --log-format string       日志格式：text/json（默认 "text"）
```

## 安全模型

- 日记数据全部通过**沙箱化文件服务**落盘：每个路径在解析前后都做工作区边界与符号链接校验，
  落盘使用临时文件 + 原子替换，读者永远不会看到半写状态。
- `--read-only` 可让服务端完全不可写，日记工具会返回 403。
- Streamable HTTP 默认开启 DNS rebinding 防护；仅在受控环境（如反向代理）下才使用 `--allow-remote`。
- 启用认证后：每个请求必须携带本服务签发的 Bearer Token（audience = `<public-url>/mcp`），
  用户目录名由邮箱安全化生成并限制在 `users/` 内，用户之间以及与默认 Root 相互隔离。
- Google client secret 只留在服务端；客户端通过动态注册拿到自己的 client id，不接触 secret。
- Web REST API（`/api/*`）复用同一套校验，且只在启用认证时注册；服务不提供任何原始文件读写接口。
- 生产环境务必使用 HTTPS、设置 `--auth-public-url`，并用 `--auth-encryption-key` 加密落盘的 OAuth 状态。

## 开发

```bash
make build       # 构建前端 + 编译服务端到 bin/
make build-go    # 仅编译服务端（使用已提交的前端产物）
make dev         # 启动 Vite 开发服务器（:5173，代理 /api、/mcp、/oauth 到 :8080）
make test        # 单元测试 + HTTP 端到端集成测试（含认证与按用户隔离）
make test-race   # 竞态检测
make vet         # 静态检查
make fmt         # 格式化
```

集成测试会启动真实的 Streamable HTTP 服务端，并使用 mcp-go 官方客户端完成
`initialize → tools/list → 日记工具全流程（创建/追加/提交/查询/删除）` 全链路验证；
认证测试验证 `401 挑战 → 受保护资源元数据指向本服务 → 授权服务器元数据 → 动态客户端注册`。
Web 接入层有独立的 `httptest` 用例，覆盖 `/api/me` 与错误码映射；
`internal/arch_test.go` 固化 internal 分层依赖规则。

## 扩展新工具

1. 在 `internal/transport/mcp/tools/<group>/` 下新建工具，实现 `tools.Tool`：

```go
type myTool struct{ /* 依赖注入 */ }

func (t myTool) Name() string             { return "my_tool" }
func (t myTool) Definition() mcp.Tool     { return mcp.NewTool(t.Name(), /* ... */) }
func (t myTool) Handle(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    return tools.Result(payload), nil
}
```

2. 在该 group 的 `All(...)` 中返回新工具，`app.New` 会自动完成注册。