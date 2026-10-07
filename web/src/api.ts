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

export interface DiarySettings {
  lunar_enabled: boolean
  weather_enabled: boolean
  weather_location?: string
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly code?: string,
    readonly state?: SyncState,
    readonly hint?: string,
    readonly detail?: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

type ErrorBody = {
  error?: { message?: string; code?: string; hint?: string; detail?: string }
  error_description?: string
  state?: SyncState
}

const codeMessages: Record<string, string> = {
  invalid_request: '请求无效，请检查填写内容',
  not_found: '找不到对应的存储介质',
  unknown_kind: '不支持的存储类型',
  sync_failed: '同步失败',
  medium_disabled: '存储介质已停用',
  engine_unavailable: '同步服务未就绪，请稍后重试',
  encryption_unavailable: '无法保存凭证：服务端未配置加密密钥',
  read_only: '当前工作区只读，无法保存',
  internal_error: '服务内部错误，请稍后重试',
}

const githubDetailPattern = /^(.*?)（(github api [\s\S]*)）$/

export function splitGitHubDetail(message?: string): { summary: string; detail: string } {
  if (!message) {
    return { summary: '', detail: '' }
  }
  const split = message.match(githubDetailPattern)
  if (!split) {
    return { summary: message, detail: '' }
  }
  return { summary: split[1].trim(), detail: split[2].trim() }
}

export function friendlyError(err: unknown): { summary: string; detail: string } {
  if (!(err instanceof ApiError)) {
    const text = err instanceof Error ? err.message : String(err)
    return { summary: text, detail: '' }
  }
  const split = splitGitHubDetail(err.message)
  const summary = split.summary
  const detail = err.detail || split.detail
  const mapped = err.code ? codeMessages[err.code] : ''
  const hint = err.hint && err.hint !== summary ? err.hint : ''
  const friendly =
    err.code === 'sync_failed' || err.code === 'invalid_request' ? summary : mapped || summary
  return {
    summary: hint ? `${friendly} ${hint}` : friendly,
    detail,
  }
}

async function parseResponse<T>(resp: Response): Promise<T> {
  const data = (await resp.json().catch(() => null)) as (T & ErrorBody) | null
  if (!resp.ok) {
    const message =
      data?.error?.message ?? data?.error_description ?? resp.statusText
    throw new ApiError(
      resp.status,
      message,
      data?.error?.code,
      data?.state,
      data?.error?.hint,
      data?.error?.detail,
    )
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

export const getSettings = (token: string): Promise<DiarySettings> =>
  apiGet<DiarySettings>(token, '/api/settings')

export const updateSettings = (token: string, input: DiarySettings): Promise<DiarySettings> =>
  apiSend<DiarySettings>(token, 'PUT', '/api/settings', input)
