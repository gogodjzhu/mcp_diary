# Google OAuth 2.0 / OIDC authentication

`mcp-diary` can act as an MCP **resource server**: it requires a bearer access
token on every MCP request, validates that token with the configured identity
provider (Google by default), and gives each authenticated user a private,
sandboxed workspace.

This follows the [MCP authorization spec](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization)
and [RFC 9728](https://datatracker.ietf.org/doc/html/rfc9728), the same contract
described in the [OpenAI plugin authentication guide](https://developers.openai.com/plugins/build/auth).

## How it works

```
MCP client (ChatGPT / Inspector / mcp-go)
        │  1. GET /.well-known/oauth-protected-resource/mcp
        ▼
mcp-diary  ──────────────────────────────►  returns resource metadata
        │                                   (authorization_servers = Google)
        │  2. OAuth 2.1 authorization-code + PKCE with Google
        ▼
Google accounts  ────────────────────────►  issues access token
        │
        │  3. Authorization: Bearer <token> on every /mcp request
        ▼
mcp-diary  ──────────────────────────────►  verifies token, extracts identity,
                                             resolves /data/users/<email>/
```

1. The server publishes **protected resource metadata** at
   `/.well-known/oauth-protected-resource/mcp` and advertises Google as the
   authorization server.
2. Unauthenticated requests get `401` with a `WWW-Authenticate` challenge that
   points clients at the metadata document.
3. Clients run the OAuth flow with Google and attach the access token.
4. The server verifies the token on every request (signature/validity via
   Google's tokeninfo endpoint, `aud` = your client id, expiry, required
   scopes, verified email) and resolves the user's workspace.

## Google Cloud setup

1. Create (or select) a project in the [Google Cloud Console](https://console.cloud.google.com/).
2. Configure the **OAuth consent screen** (External). Add the scopes
   `openid`, `.../auth/userinfo.email`, `.../auth/userinfo.profile`.
3. Create an **OAuth client ID** of type **Web application** under
   *APIs & Services → Credentials*.
4. Add the redirect URI(s) of the MCP client(s) you use, for example:
   - MCP Inspector: `http://localhost:6274/oauth/callback`
   - ChatGPT connector: the redirect URI shown on the connector page
     (`https://chatgpt.com/connector_platform_oauth_redirect` or
     `https://chatgpt.com/connector/oauth/<callback_id>`).
5. Copy the **Client ID** — it is the value for `--auth-client-id` (used as the
   expected token audience). No client secret is needed to validate tokens.

Google does not support dynamic client registration, so each MCP client must be
configured with this client id.

## Running with authentication

```bash
./bin/mcp-diary serve \
  --root /data \
  --addr :8080 \
  --auth-enabled \
  --auth-client-id "<google-client-id>" \
  --auth-public-url "https://mcp.example.com"
```

When `--auth-public-url` is omitted the server derives it from the request
(`Host` and `X-Forwarded-Proto`), which is only correct when clients reach the
server directly. Behind a proxy or tunnel, always set it explicitly.

### Auth flags

| Flag | Description |
| --- | --- |
| `--auth-enabled` | Require a valid bearer token on every MCP request |
| `--auth-issuer` | Authorization-server issuer advertised in metadata (default `https://accounts.google.com`) |
| `--auth-client-id` | OAuth client id; tokens must be minted for it (audience) |
| `--auth-client-secret` | Only for providers that require confidential-client auth |
| `--auth-audience` | Override the expected audience (defaults to the client id) |
| `--auth-scopes` | Required scopes, comma separated (default `openid,email,profile`) |
| `--auth-public-url` | Externally reachable base URL |
| `--auth-tokeninfo-url` | Token validation endpoint (default Google tokeninfo) |
| `--auth-userinfo-url` | Optional userinfo endpoint to enrich the identity |
| `--auth-require-verified-email` | Reject unverified emails (default true) |
| `--auth-cache-ttl` | How long successful verifications are cached |
| `--auth-http-timeout` | Timeout for authorization-server calls |
| `--auth-users-dir` | Per-user workspace directory, relative to `--root` (default `users`) |

## Per-user workspaces

When authentication is enabled, every user gets an isolated directory:

```
$ROOT/                     # base root, only contains the users directory
└── users/
    ├── alice@example.com/ # workspace for alice@example.com (mode 0700)
    └── bob@example.com/   # workspace for bob@example.com
```

- The directory name is derived from the verified email (falling back to the
  OIDC `sub`) and sanitized to a filesystem-safe slug.
- All filesystem tools resolve paths inside the caller's directory only, so one
  user can never read or write another user's files.
- The base root itself is not exposed to authenticated requests.
- Without `--auth-enabled`, the server keeps its previous behaviour and serves
  `--root` directly.

## Local testing without Google

Point the server at any tokeninfo endpoint that returns Google-style claims:

```bash
# fake tokeninfo server (see internal/auth/verifier_test.go for the shape)
# then:
./bin/mcp-diary serve \
  --root /tmp/workspace \
  --addr 127.0.0.1:8080 \
  --auth-enabled \
  --auth-client-id test-client \
  --auth-tokeninfo-url http://127.0.0.1:19090/tokeninfo
```

```bash
# No token -> 401 with a challenge
curl -i -X POST http://127.0.0.1:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'

# With a valid token -> the call is served in the user's workspace
curl -s -X POST http://127.0.0.1:8080/mcp \
  -H 'Authorization: Bearer good-token' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"write_file","arguments":{"path":"diary.txt","content":"hello"}}}'
```

## Web UI sign-in (browser)

The same OAuth client powers the browser UI. The single-page app uses
[Google Identity Services](https://developers.google.com/identity/oauth2/web/guides/use-token-model)
to obtain an access token and then calls the REST API with
`Authorization: Bearer <token>`. The server validates that token with the very
same verifier used for MCP, so no additional auth code is involved.

Setup:

1. Reuse the existing OAuth client, but make sure its type is **Web application**
   (only web clients have the *Authorized JavaScript origins* field).
2. Add every origin the UI is served from under **Authorized JavaScript
   origins** — scheme + host + port, no trailing slash, no path, no wildcards,
   for example:
   - `http://localhost:5173` (Vite dev server)
   - `http://localhost:8080` (server-hosted UI)
   - `https://mcp.example.com` (production)
3. Keep the scopes `openid`, `email`, `profile` — they must match
   `--auth-scopes`.
4. The frontend build needs the client id in `VITE_GOOGLE_CLIENT_ID` (see
   `web/.env.example`). Docker builds pass it as a build arg.

The browser flow's tokens carry the same `aud`, so one `--auth-client-id`
configures both MCP and the web UI. The web routes are only registered when
`--auth-enabled` is set.

## Docker deployment

```bash
cp .env.example .env
# edit .env: set GOOGLE_CLIENT_ID and MCP_PUBLIC_URL
docker compose up --build -d
```

The named volume `mcp-diary-data` is mounted at `/data`; per-user directories
are created under `/data/users`.

## Security notes

- Google access tokens are opaque, so the server validates them through
  Google's tokeninfo endpoint. This is the standard approach for opaque tokens;
  the response is cached for a short TTL and bounded by the token's own expiry.
  If you need fully offline verification, terminate OIDC at a proxy that issues
  JWTs and switch `--auth-tokeninfo-url`/`--auth-audience` accordingly.
- Tokens must match the configured audience (`--auth-client-id`), be unexpired,
  carry the required scopes and (by default) have a verified email.
- Always run behind HTTPS in production and set `--auth-public-url` so the
  metadata and challenge URLs are correct.
- Use `--read-only` to expose an authenticated but non-mutating workspace.
