# Authentication

`mcp-diary` acts as its **own OAuth 2.1 authorization server**. MCP clients and
the browser UI obtain tokens from `mcp-diary` itself, registering dynamically
(RFC 7591), and `mcp-diary` federates the actual user login to Google behind the
scenes. The Google client secret never leaves the server, and **nothing but the
server URL needs to be distributed** to users.

This follows the [MCP authorization spec](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization)
(RFC 9728 protected-resource metadata, RFC 8414 authorization-server metadata,
RFC 7591 dynamic client registration, PKCE).

## How it works

```
MCP client / browser
        │  1. GET /mcp  →  401 + WWW-Authenticate (resource_metadata=…)
        │  2. GET /.well-known/oauth-protected-resource/mcp   →  authorization_servers = mcp-diary
        │  3. GET /.well-known/oauth-authorization-server     →  endpoints + registration_endpoint
        │  4. POST /oauth/register                            →  client_id (no secret, PKCE)
        │  5. /oauth/authorize + PKCE  →  mcp-diary  →  Google login  →  /oauth/callback
        │  6. POST /oauth/token (code + code_verifier)        →  mcp-diary issues its own token
        ▼
mcp-diary  ──  validates its own token, extracts identity, resolves /data/users/<email>/
```

1. `mcp-diary` publishes **protected resource metadata** at
   `/.well-known/oauth-protected-resource/mcp`, pointing `authorization_servers`
   at itself.
2. Unauthenticated requests get `401` with a `WWW-Authenticate` challenge.
3. The client registers dynamically (`/oauth/register`) and runs the
   authorization-code + PKCE flow against `mcp-diary`.
4. `mcp-diary` redirects to Google, receives the user identity, and issues its
   **own** access/refresh tokens (audience bound to `<public-url>/mcp`). The
   Google token is never handed to the client (no token passthrough).
5. Every `/mcp` and `/api/*` request is validated against those tokens and the
   user's workspace is resolved.

## Google Cloud setup

You need a **single** Google OAuth client:

1. Create (or select) a project in the [Google Cloud Console](https://console.cloud.google.com/).
2. Configure the **OAuth consent screen** (External). Scopes: `openid`,
   `.../auth/userinfo.email`, `.../auth/userinfo.profile`.
3. Create an **OAuth client ID** of type **Web application**.
4. Add one redirect URI: `https://<your-public-url>/oauth/callback`
   (exactly the value of `--auth-public-url` plus `/oauth/callback`).
5. Copy the **Client ID** and **Client secret** (both stay server-side).
6. **Publish** the consent screen (Testing → In production) before opening the
   server to other users, otherwise only Test users can sign in.

No *Authorized JavaScript origins* are needed: the browser talks to `mcp-diary`,
not to Google directly.

## Running with authentication

```bash
./bin/mcp-diary serve \
  --root /data \
  --auth-enabled \
  --auth-public-url "https://mcp.example.com" \
  --google-client-id "<google-client-id>" \
  --google-client-secret "<google-client-secret>" \
  --auth-encryption-key "$(openssl rand -base64 32)"
```

`--auth-public-url` is the OAuth issuer identifier; it must match how clients
reach the server (scheme, host, port). Set it explicitly behind any proxy or
tunnel.

### Auth flags

| Flag | Description |
| --- | --- |
| `--auth-enabled` | Turn on OAuth 2.1 authentication |
| `--auth-public-url` | Externally reachable base URL (the OAuth issuer), e.g. `https://mcp.example.com` |
| `--google-client-id` | Google OAuth client id used server-side |
| `--google-client-secret` | Google OAuth client secret (kept on the server) |
| `--auth-encryption-key` | 32-byte key (base64 or hex) encrypting OAuth state at rest; empty stores it unencrypted |
| `--auth-access-token-ttl` | Issued access token lifetime (default `1h`) |
| `--auth-refresh-token-ttl` | Issued refresh token lifetime (default `2160h`) |
| `--auth-max-clients-per-ip` | Cap on dynamic client registrations per IP (default `10`) |
| `--auth-store-dir` | Directory for the OAuth state file, relative to `--root` (default `auth`) |
| `--auth-users-dir` | Per-user workspace directory, relative to `--root` (default `users`) |

## Connecting a client

Because `mcp-diary` supports dynamic client registration, clients need **only
the URL** — no client id, no secret:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "mcp-diary": { "type": "remote", "url": "https://mcp.example.com/mcp" }
    }
  }
}
```

Run `/connect` in opencode, or the client's "authenticate" action, and complete
the browser sign-in. Clients that do not support DCR yet can pre-register a
public client; a loopback redirect URI (`http://127.0.0.1:<port>/callback`) is
allowed.

## Web UI sign-in

The browser UI is a first-party **public client** named `mcp-diary-web`,
pre-registered on startup with redirect URI `<public-url>/auth/callback`. It uses
authorization code + PKCE against the same authorization server and holds the
access token in `sessionStorage`. No Google client id is compiled into the
frontend. The web routes are only registered when `--auth-enabled` is set.

## Per-user workspaces

When authentication is enabled, every user gets an isolated directory:

```
$ROOT/                     # base root, only contains the users directory and the auth state
├── auth/                  # OAuth client registrations and tokens (0600)
└── users/
    ├── alice@example.com/ # workspace for alice@example.com (mode 0700)
    └── bob@example.com/   # workspace for bob@example.com
```

- The directory name is derived from the verified Google email (falling back to
  the `sub`) and sanitized to a filesystem-safe slug.
- All filesystem tools resolve paths inside the caller's directory only, so one
  user can never read or write another user's files.
- The base root itself is not exposed to authenticated requests.
- Without `--auth-enabled`, the server serves `--root` directly (no auth).

## Docker deployment

```bash
cp .env.example .env
# edit .env: set GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, MCP_PUBLIC_URL, AUTH_ENCRYPTION_KEY
docker compose up --build -d
```

The named volume `mcp-diary-data` is mounted at `/data`; per-user directories are
created under `/data/users` and OAuth state under `/data/auth`.

## Security notes

- The Google client secret is only used server-side; clients never receive it.
- The server issues its own tokens and validates the audience
  (`<public-url>/mcp`), so the RFC 8707 resource-binding gap of Google access
  tokens no longer applies.
- Dynamic client registration is open (bounded by `--auth-max-clients-per-ip`)
  so clients can connect with zero configuration; registration alone grants no
  data access — every user still authenticates with Google.
- Set `--auth-encryption-key` in production so OAuth state is encrypted at rest
  (AES-256-GCM).
- Always run behind HTTPS in production and set `--auth-public-url` so that
  metadata, redirect and challenge URLs are correct.
- Use `--read-only` to expose an authenticated but non-mutating workspace.
