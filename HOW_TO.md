# HOW TO：用 Google 账号接入并验证 mcp-diary

面向本仓库的本地验证流程（devcontainer），走真实的 Google OAuth。覆盖三种入口：
**MCP Inspector v2**、**opencode 远程 MCP** 与 **浏览器 Web 界面**。

## 1. Google Cloud 配置

1. 新建项目并配置 **OAuth consent screen**（External），添加 scope：`openid`、`email`、`profile`。
2. 创建 **OAuth client ID → Web application**。
3. 添加 **Authorized redirect URIs**（OAuth 客户端的回调）：
   - Inspector：`http://localhost:6274/oauth/callback`
   - Inspector：`http://127.0.0.1:6274/oauth/callback`
   - opencode（V2）：`http://127.0.0.1:19876/callback`
   - opencode（旧）：`http://127.0.0.1:19876/mcp/oauth/callback`
   > 若登录报 `redirect_uri_mismatch`，以报错页「错误详情」里的准确 URI 为准。
4. 添加 **Authorized JavaScript origins**（浏览器 Web 界面用，不带路径/结尾斜杠）：
   - `http://localhost:8080`
   - `http://localhost:5173`（Vite 开发）
5. 应用处于 Testing 时，把你自己的 Google 账号加入 **Test users**。
6. 记下 **Client ID** 与 **Client secret**。

> Google 不支持动态客户端注册（DCR），所以必须预注册并使用这个 client。

## 2. 启动服务端

```bash
make build

./bin/mcp-diary serve \
  --root /data \
  --auth-enabled \
  --auth-client-id "<你的 Google Client ID>" \
  --auth-public-url "http://localhost:8080"
```

- 服务端只校验 token，不需要 client secret。
- 健康检查：`curl localhost:8080/healthz`。

## 3. 启动 MCP Inspector（v2）

```bash
npx @modelcontextprotocol/inspector
```

浏览器打开它输出的 `http://127.0.0.1:6274`。v2 没有 "Via Proxy/Direct" 选项，连接统一由后端代理。

## 4. Inspector 添加并配置 server

1. 在 Servers 中新增：Transport Type = `Streamable HTTP`，URL = `http://localhost:8080/mcp`。
2. 打开该 server 的 **Server Settings → OAuth Settings**：
   - **Client ID**：填 Google Client ID
   - **Client Secret**：填 Google Client secret
   - **Scopes**：`openid email profile`
3. 其余保持默认，点击 **Connect**，在 Google 页面登录并同意。
4. 连接成功后状态为 connected。

## 5. 通过 opencode 接入（V2）

opencode V2 的 MCP 配置放在 `mcp.servers` 下，OAuth 字段用 **snake_case**。Google 不支持动态
客户端注册（DCR），所以需要预注册 `client_id` / `client_secret`（用同一个 Google OAuth client）。

`opencode.json`：

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "mcp-diary": {
        "type": "remote",
        "url": "http://localhost:8080/mcp",
        "oauth": {
          "client_id": "<你的 Google Client ID>",
          "client_secret": "{env:GOOGLE_CLIENT_SECRET}",
          "scope": "openid email profile",
          "callback_port": 19876
        }
      }
    }
  }
}
```

设置环境变量后重启 opencode：

```bash
export GOOGLE_CLIENT_SECRET="<你的 Google Client secret>"
opencode service restart
```

认证与查看：

```bash
opencode mcp list                # 应显示 mcp-diary  needs authentication
# 然后在 OpenCode 里运行 /mcps，选中 mcp-diary 完成 Google 登录
opencode mcp logout mcp-diary    # 清除已存凭据
```

说明（重要）：

- V2 用 `disabled: true` 禁用 server，没有 `enabled` 字段。
- **`url` 必须与服务端 `--auth-public-url` + `/mcp` 完全一致**。`localhost` 与 `127.0.0.1` 是两个不同
  host，混用会报 `Protected resource ... does not cover ...`；二者选一个全程统一。
- opencode 的本地回调（`http://127.0.0.1:19876/...`）要登记在 Google 的 **Authorized redirect URIs**；
  若报 `redirect_uri_mismatch`，点报错页的「错误详情」复制准确的 `redirect_uri` 再登记。
- devcontainer 中需把 `8080`（服务端）与 `19876`（OAuth 回调）转发到宿主。
- 修改 `opencode.json` 后需运行 `opencode service restart` 才生效。

## 6. 调用工具

启用认证后，每个用户拥有独立工作区：`$ROOT/users/<邮箱>/`（例如 `/data/users/you@gmail.com/`）。
工具路径请用**相对路径**（相对工作区根）：

```json
{ "name": "write_file", "arguments": { "path": "myfile.txt", "content": "hello" } }
```

实际写入 `/data/users/<邮箱>/myfile.txt`，与其他用户及默认 root 隔离。

## 7. 浏览器界面（Web）

服务端启用认证后，Web 界面与 REST API 会一并启用（`--web-enabled`，默认 true）。

前提：在 Google OAuth client 的 **Authorized JavaScript origins** 中加入 `http://localhost:8080`
（以及 Vite 开发时的 `http://localhost:5173`）。scope 保持 `openid email profile`。

生产/单端口验证：

```bash
make build
./bin/mcp-diary serve \
  --root /data \
  --auth-enabled \
  --auth-client-id "<你的 Google Client ID>" \
  --auth-public-url "http://localhost:8080"
```

浏览器打开 `http://localhost:8080/` → 点击「使用 Google 登录」→ 同意后即可看到
`/data/users/<邮箱>/` 下的文件列表。也可用 curl 直接验证 API：

```bash
# 未带 token -> 401
curl -i http://localhost:8080/api/me

# 带 token -> 200，返回当前用户身份
curl -H "Authorization: Bearer <access-token>" http://localhost:8080/api/files?path=.
```

前端开发（热更新）：

```bash
make dev   # 启动 Vite (:5173)，/api 与 /mcp 代理到 localhost:8080
```

此时在 Google Console 中需一并加入 `http://localhost:5173`。
前端构建需要 `VITE_GOOGLE_CLIENT_ID`（见 `web/.env.example`）。

## 8. 快速自检

```bash
curl -s localhost:8080/healthz
ls -la "/data/users/<邮箱>/"
```

`/healthz` 中 `auth_enabled` 应为 `true`，`per_user` 应为 `true`。