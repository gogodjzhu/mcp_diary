/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<Record<string, never>, Record<string, never>, unknown>
  export default component
}

interface ImportMetaEnv {
  readonly VITE_GOOGLE_CLIENT_ID?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}

// Minimal typing for the Google Identity Services script we load at runtime.
interface GoogleTokenResponse {
  access_token?: string
  error?: string
  expires_in?: number
}

interface GoogleTokenClient {
  requestAccessToken(): void
}

interface GoogleOAuth2 {
  initTokenClient(config: {
    client_id: string
    scope: string
    callback: (response: GoogleTokenResponse) => void
  }): GoogleTokenClient
  revoke(token: string): void
}

interface Window {
  google?: {
    accounts?: {
      oauth2?: GoogleOAuth2
    }
  }
}
