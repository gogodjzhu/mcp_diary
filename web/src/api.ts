export interface Me {
  subject: string
  email: string
  name: string
  username: string
  scopes: string[]
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
