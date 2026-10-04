export interface Me {
  subject: string
  email: string
  name: string
  username: string
  scopes: string[]
}

export interface DocumentState {
  path: string
  remote_id?: string
  entry_ids?: string[]
  revision?: number
  last_synced_at?: string
  last_error?: string
}

export interface SyncState {
  medium_id: string
  status: string
  last_error?: string
  last_synced_at?: string | null
  documents?: Record<string, DocumentState>
}

export interface Medium {
  id: string
  kind: string
  name: string
  enabled: boolean
  settings?: Record<string, string>
  has_credential: boolean
  created_at: string
  updated_at: string
  state: SyncState
}

export interface MediumInput {
  kind?: string
  name: string
  enabled?: boolean
  settings?: Record<string, string>
  credential?: string
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly code?: string,
    readonly state?: SyncState,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

type ErrorBody = {
  error?: { message?: string; code?: string }
  error_description?: string
  state?: SyncState
}

async function parseResponse<T>(resp: Response): Promise<T> {
  const data = (await resp.json().catch(() => null)) as (T & ErrorBody) | null
  if (!resp.ok) {
    const message =
      data?.error?.message ?? data?.error_description ?? resp.statusText
    throw new ApiError(resp.status, message, data?.error?.code, data?.state)
  }
  return data as T
}

async function apiGet<T>(token: string, path: string): Promise<T> {
  const resp = await fetch(path, {
    headers: { Authorization: `Bearer ${token}` },
  })
  return parseResponse<T>(resp)
}

async function apiSend<T>(
  token: string,
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const resp = await fetch(path, {
    method,
    headers: {
      Authorization: `Bearer ${token}`,
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (resp.status === 204) {
    return undefined as T
  }
  return parseResponse<T>(resp)
}

export const getMe = (token: string): Promise<Me> => apiGet<Me>(token, '/api/me')

export const listMedia = (token: string): Promise<{ media: Medium[] }> =>
  apiGet<{ media: Medium[] }>(token, '/api/sync/media')

export const createMedium = (token: string, input: MediumInput): Promise<Medium> =>
  apiSend<Medium>(token, 'POST', '/api/sync/media', input)

export const updateMedium = (
  token: string,
  id: string,
  input: MediumInput,
): Promise<Medium> => apiSend<Medium>(token, 'PUT', `/api/sync/media/${id}`, input)

export const deleteMedium = (token: string, id: string): Promise<void> =>
  apiSend<void>(token, 'DELETE', `/api/sync/media/${id}`)

export const triggerSync = (token: string, id: string): Promise<SyncState> =>
  apiSend<SyncState>(token, 'POST', `/api/sync/media/${id}/sync`)
