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
