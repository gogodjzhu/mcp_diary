// Client-side OAuth 2.1 (authorization code + PKCE) for the mcp-diary UI.
//
// The browser is a public client of mcp-diary's own authorization server; no
// secret is involved. The access token is held in sessionStorage so a reload
// keeps the session but closing the tab clears it.

const CLIENT_ID = 'mcp-diary-web'
const REDIRECT_PATH = '/auth/callback'
const SCOPES = 'mcp'
const VERIFIER_KEY = 'diary.pkce.verifier'
const STATE_KEY = 'diary.pkce.state'
const TOKEN_KEY = 'diary.token'

export interface TokenResponse {
  access_token: string
  token_type?: string
  expires_in?: number
  refresh_token?: string
  obtained_at?: number
}

interface StoredToken extends TokenResponse {
  obtained_at: number
}

function base64url(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) {
    binary += String.fromCharCode(byte)
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function randomString(byteLength = 48): string {
  const bytes = new Uint8Array(byteLength)
  crypto.getRandomValues(bytes)
  return base64url(bytes)
}

async function codeChallenge(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier))
  return base64url(new Uint8Array(digest))
}

export function redirectUri(): string {
  return window.location.origin + REDIRECT_PATH
}

// resource is the RFC 8707 audience. It must be identical on the authorization
// request and on every token request, or the server rejects the grant.
function resource(): string {
  return window.location.origin + '/mcp'
}

/** Start the authorization code flow by navigating to the authorization server. */
export async function beginLogin(): Promise<void> {
  const verifier = randomString()
  const state = randomString(24)
  sessionStorage.setItem(VERIFIER_KEY, verifier)
  sessionStorage.setItem(STATE_KEY, state)

  const params = new URLSearchParams({
    response_type: 'code',
    client_id: CLIENT_ID,
    redirect_uri: redirectUri(),
    scope: SCOPES,
    state,
    code_challenge: await codeChallenge(verifier),
    code_challenge_method: 'S256',
    resource: resource(),
  })
  window.location.assign('/oauth/authorize?' + params.toString())
}

/**
 * If the current URL is the OAuth redirect target, exchange the code for a
 * token. Returns true when a token was obtained.
 */
export async function completeLogin(): Promise<boolean> {
  const url = new URL(window.location.href)
  if (url.pathname !== REDIRECT_PATH) {
    return false
  }

  const code = url.searchParams.get('code')
  const state = url.searchParams.get('state')
  const oauthError = url.searchParams.get('error')
  const expectedState = sessionStorage.getItem(STATE_KEY)
  const verifier = sessionStorage.getItem(VERIFIER_KEY)

  // Drop the query string so a reload does not replay the code.
  window.history.replaceState({}, '', '/')

  if (oauthError) {
    throw new Error(oauthError)
  }
  if (!code || !state || !verifier || !expectedState || state !== expectedState) {
    return false
  }

  const body = new URLSearchParams({
    grant_type: 'authorization_code',
    code,
    redirect_uri: redirectUri(),
    client_id: CLIENT_ID,
    code_verifier: verifier,
    resource: resource(),
  })
  const resp = await fetch('/oauth/token', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body,
  })
  if (!resp.ok) {
    throw new Error('token exchange failed: ' + resp.status)
  }
  const token = (await resp.json()) as TokenResponse
  saveToken(token)
  sessionStorage.removeItem(VERIFIER_KEY)
  sessionStorage.removeItem(STATE_KEY)
  return true
}

function loadToken(): StoredToken | null {
  const raw = sessionStorage.getItem(TOKEN_KEY)
  if (!raw) {
    return null
  }
  try {
    const token = JSON.parse(raw) as StoredToken
    return token.access_token ? token : null
  } catch {
    return null
  }
}

function saveToken(token: TokenResponse): void {
  const current = loadToken()
  const stored: StoredToken = {
    ...token,
    refresh_token: token.refresh_token ?? current?.refresh_token,
    obtained_at: Date.now(),
  }
  sessionStorage.setItem(TOKEN_KEY, JSON.stringify(stored))
}

/** accessToken returns a usable access token, refreshing it when it is near expiry. */
export async function accessToken(): Promise<string> {
  const token = loadToken()
  if (!token) {
    return ''
  }
  if (!isExpiring(token)) {
    return token.access_token
  }
  if (!token.refresh_token) {
    signOut()
    return ''
  }
  try {
    return await refresh(token.refresh_token)
  } catch {
    signOut()
    return ''
  }
}

function isExpiring(token: StoredToken): boolean {
  if (!token.expires_in || !token.obtained_at) {
    return false
  }
  const expiresAt = token.obtained_at + token.expires_in * 1000
  return Date.now() > expiresAt - 60_000
}

async function refresh(refreshToken: string): Promise<string> {
  const body = new URLSearchParams({
    grant_type: 'refresh_token',
    refresh_token: refreshToken,
    client_id: CLIENT_ID,
    resource: resource(),
  })
  const resp = await fetch('/oauth/token', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body,
  })
  if (!resp.ok) {
    throw new Error('refresh failed: ' + resp.status)
  }
  const token = (await resp.json()) as TokenResponse
  saveToken(token)
  return token.access_token
}

export function signOut(): void {
  sessionStorage.removeItem(TOKEN_KEY)
  sessionStorage.removeItem(VERIFIER_KEY)
  sessionStorage.removeItem(STATE_KEY)
}
