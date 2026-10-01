export interface Me {
  subject: string
  email: string
  name: string
  username: string
  scopes: string[]
}

export interface Entry {
  name: string
  path: string
  is_dir: boolean
  size: number
  mode: string
  mod_time: string
}

export interface Listing {
  path: string
  count: number
  entries: Entry[]
}

export interface Attachment {
  attachment_id: string
  owner_id: string
  filename: string
  original_name?: string
  rel_path: string
  media_type: 'image' | 'video' | string
  mime_type: string
  size: number
}

export interface DiaryRecord {
  session_id?: string
  entry_id?: string
  diary_date: string
  content: string
  status: string
  attachments?: Attachment[]
}

export interface DiaryEntries {
  entries: DiaryRecord[]
  count: number
}

export interface DiarySessions {
  sessions: DiaryRecord[]
  count: number
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

async function apiGet<T>(token: string, path: string): Promise<T> {
  const resp = await fetch(path, {
    headers: { Authorization: `Bearer ${token}` },
  })

  const data = (await resp.json().catch(() => null)) as
    | { error?: { message?: string }; error_description?: string }
    | null

  if (!resp.ok) {
    const message =
      data?.error?.message ?? data?.error_description ?? resp.statusText
    throw new ApiError(resp.status, message)
  }

  return data as T
}

export const getMe = (token: string): Promise<Me> => apiGet<Me>(token, '/api/me')

export const listFiles = (token: string, path = '.'): Promise<Listing> =>
  apiGet<Listing>(token, `/api/files?path=${encodeURIComponent(path)}`)

export const listDiaryEntries = (token: string): Promise<DiaryEntries> =>
  apiGet<DiaryEntries>(token, '/api/diary/entries')

export const listDiarySessions = (token: string): Promise<DiarySessions> =>
  apiGet<DiarySessions>(token, '/api/diary/sessions')

export function attachmentURL(att: Attachment): string {
  return `/api/diary/attachments/${encodeURIComponent(att.owner_id)}/${encodeURIComponent(att.attachment_id)}`
}

export async function uploadDiaryAttachment(
  token: string,
  file: File,
  target: { session_id?: string; entry_id?: string },
): Promise<{ attachment: Attachment }> {
  const body = new FormData()
  body.append('file', file, file.name)
  if (target.session_id) {
    body.append('session_id', target.session_id)
  }
  if (target.entry_id) {
    body.append('entry_id', target.entry_id)
  }
  const resp = await fetch('/api/diary/attachments', {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}` },
    body,
  })
  const data = (await resp.json().catch(() => null)) as
    | { error?: { message?: string }; attachment?: Attachment }
    | null
  if (!resp.ok) {
    throw new ApiError(resp.status, data?.error?.message ?? resp.statusText)
  }
  return data as { attachment: Attachment }
}
