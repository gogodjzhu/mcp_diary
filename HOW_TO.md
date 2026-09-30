# HOW TO：用 Google 账号接入并验证 mcp-diary

面向本仓库的本地验证流程（devcontainer），走真实的 Google OAuth。覆盖三种入口：
**MCP Inspector v2**、**opencode 远程 MCP** 与 **浏览器 Web 界面**。

`mcp-diary` 自己是 OAuth 2.1 授权服务器，Google 只是上游身份。客户端只需 MCP URL，
通过动态注册（DCR）拿到 client id，**不要**再把 Google client id / secret 配到客户端。

## 1. Google Cloud 配置

1. 新建项目并配置 **OAuth consent screen**（External），添加 scope：`openid`、`email`、`profile`。
2. 创建 **OAuth client ID → Web application**（只需要这一个）。
3. 添加 **Authorized redirect URIs**（只有这一条，指向 mcp-diary 自己）：
   - 本地：`http://localhost:8080/oauth/callback`
   - 必须与服务端 `--auth-public-url` + `/oauth/callback` 完全一致。
4. 应用处于 Testing 时，把你自己的 Google 账号加入 **Test users**。
5. 记下 **Client ID** 与 **Client secret**。secret 只放服务端，不要写进客户端配置。

不需要 **Authorized JavaScript origins**：浏览器不直接对接 Google。
也不要把 Inspector / opencode 的本地回调登记到 Google；那些回调由 mcp-diary 自己接受。

## 2. 启动服务端

```bash
make build

./bin/mcp-diary serve \
  --root /data \
  --auth-enabled \
  --auth-public-url "http://localhost:8080" \
  --google-client-id "<你的 Google Client ID>" \
  --google-client-secret "<你的 Google Client secret>" \
  --auth-encryption-key "$(openssl rand -base64 32)"
```

- `--auth-public-url` 必须与客户端访问的地址一致。`localhost` 与 `127.0.0.1` 是两个不同 host，全程只用一个。
- 健康检查：`curl localhost:8080/healthz`（`auth_enabled`、`per_user` 应为 `true`）。
- OAuth 状态落在 `$ROOT/auth/oauth.json`。

## 3. 启动 MCP Inspector（v2）

```bash
npx @modelcontextprotocol/inspector
```

浏览器打开它输出的 `http://127.0.0.1:6274`。

## 4. Inspector 添加并配置 server

1. 在 Servers 中新增：Transport Type = `Streamable HTTP`，URL = `http://localhost:8080/mcp`。
2. **不要**填 Google Client ID / Secret。留空让 Inspector 走动态注册。
3. 点击 **Connect**，在 Google 页面登录并同意。
4. 连接成功后状态为 connected。

若客户端不支持动态注册，可手工 `POST /oauth/register` 预注册一个公共客户端，
`redirect_uris` 用它自己的本地回调（`http://127.0.0.1:<port>/callback` 会被接受）。

## 5. 通过 opencode 接入（V2）

opencode 省略 `client_id` 时会动态注册。仓库里的 `opencode.json` 已经是这种配置：

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "mcp-diary": {
        "type": "remote",
        "url": "http://localhost:8080/mcp"
      }
    }
  }
}
```

认证与查看：

```bash
opencode mcp list                # 应显示 mcp-diary  needs authentication
# 然后在 OpenCode 里运行 /connect（或 /mcps），选中 mcp-diary 完成 Google 登录
opencode mcp logout mcp-diary    # 清除已存凭据
```

说明：

- **`url` 必须与服务端 `--auth-public-url` + `/mcp` 完全一致**。`localhost` 与 `127.0.0.1` 混用会报
  `Protected resource ... does not cover ...`。
- 修改 `opencode.json` 后需运行 `opencode service restart` 才生效。
- V2 用 `disabled: true` 禁用 server，没有 `enabled` 字段。
- devcontainer 中需把 `8080`（服务端）转发到宿主。opencode 的本地回调由 mcp-diary 接受，不用登记到 Google。

## 6. 调用工具

启用认证后，每个用户拥有独立工作区：`$ROOT/users/<邮箱>/`（例如 `/data/users/you@gmail.com/`）。
工具路径请用**相对路径**（相对工作区根）：

```json
{ "name": "write_file", "arguments": { "path": "myfile.txt", "content": "hello" } }
```

实际写入 `/data/users/<邮箱>/myfile.txt`，与其他用户及默认 root 隔离。

## 7. 浏览器界面（Web）

服务端启用认证后，Web 界面与 REST API 会一并启用（`--web-enabled`，默认 true）。

```bash
make build
./bin/mcp-diary serve \
  --root /data \
  --auth-enabled \
  --auth-public-url "http://localhost:8080" \
  --google-client-id "<你的 Google Client ID>" \
  --google-client-secret "<你的 Google Client secret>"
```

浏览器打开 `http://localhost:8080/` → 点击「使用 Google 登录」→ 同意后即可看到
`/data/users/<邮箱>/` 下的文件列表。前端是预注册的公共客户端 `mcp-diary-web`，
走 Authorization Code + PKCE，token 放在 `sessionStorage`。

也可用 curl 直接验证 API：

```bash
# 未带 token -> 401
curl -i http://localhost:8080/api/me

# 带本服务签发的 token -> 200
curl -H "Authorization: Bearer <access-token>" http://localhost:8080/api/files?path=.
```

前端开发（热更新）：

```bash
make dev   # 启动 Vite (:5173)，/api、/mcp、/oauth 代理到 localhost:8080
```

前端不再需要 `VITE_GOOGLE_CLIENT_ID`。Vite 开发时登录回调是
`http://localhost:5173/auth/callback`，与服务端预注册的
`<auth-public-url>/auth/callback` 不是同一个来源，所以浏览器登录请直接打开服务端端口。

## 8. 快速自检

```bash
curl -s localhost:8080/healthz
curl -s localhost:8080/.well-known/oauth-protected-resource/mcp
curl -s localhost:8080/.well-known/oauth-authorization-server
ls -la "/data/users/<邮箱>/"
```

`/healthz` 中 `auth_enabled` 应为 `true`，`per_user` 应为 `true`。
受保护资源元数据的 `authorization_servers` 应指向 `--auth-public-url`，而不是 Google。
