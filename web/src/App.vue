<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { accessToken, beginLogin, completeLogin, signOut as clearSession } from './auth'
import {
  attachmentURL,
  getMe,
  listDiaryEntries,
  listDiarySessions,
  listFiles,
  uploadDiaryAttachment,
  type Attachment,
  type DiaryRecord,
  type Entry,
  type Me,
} from './api'

const token = ref('')
const me = ref<Me | null>(null)
const entries = ref<Entry[]>([])
const diaryEntries = ref<DiaryRecord[]>([])
const diarySessions = ref<DiaryRecord[]>([])
const error = ref('')
const loading = ref(false)
const uploading = ref('')

async function refresh(): Promise<void> {
  if (!token.value) {
    return
  }
  loading.value = true
  error.value = ''
  try {
    me.value = await getMe(token.value)
    const listing = await listFiles(token.value)
    entries.value = listing.entries
    const [committed, drafts] = await Promise.all([
      listDiaryEntries(token.value),
      listDiarySessions(token.value),
    ])
    diaryEntries.value = committed.entries
    diarySessions.value = drafts.sessions
    await loadDiaryMedia()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
    clearSession()
    token.value = ''
    me.value = null
    entries.value = []
    diaryEntries.value = []
    diarySessions.value = []
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  try {
    await completeLogin()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
  token.value = await accessToken()
  if (token.value) {
    await refresh()
  }
})

function signOut(): void {
  clearSession()
  token.value = ''
  me.value = null
  entries.value = []
  diaryEntries.value = []
  diarySessions.value = []
  error.value = ''
}

async function onUpload(event: Event, target: { session_id?: string; entry_id?: string }): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || !token.value) {
    return
  }
  const key = target.session_id ?? target.entry_id ?? ''
  uploading.value = key
  error.value = ''
  try {
    await uploadDiaryAttachment(token.value, file, target)
    await refresh()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    uploading.value = ''
  }
}

function authFetch(url: string): Promise<Response> {
  return fetch(url, { headers: { Authorization: `Bearer ${token.value}` } })
}

const blobURLs = new Map<string, string>()

async function authorizedSrc(att: Attachment): Promise<string> {
  const key = att.attachment_id
  const cached = blobURLs.get(key)
  if (cached) {
    return cached
  }
  const resp = await authFetch(attachmentURL(att))
  if (!resp.ok) {
    throw new Error('failed to load attachment')
  }
  const blob = await resp.blob()
  const url = URL.createObjectURL(blob)
  blobURLs.set(key, url)
  return url
}

const mediaBlobs = ref<Record<string, string>>({})

async function ensureMedia(atts: Attachment[] | undefined): Promise<void> {
  if (!atts?.length || !token.value) {
    return
  }
  for (const att of atts) {
    if (mediaBlobs.value[att.attachment_id]) {
      continue
    }
    try {
      const url = await authorizedSrc(att)
      mediaBlobs.value = { ...mediaBlobs.value, [att.attachment_id]: url }
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
    }
  }
}

async function loadDiaryMedia(): Promise<void> {
  for (const rec of [...diaryEntries.value, ...diarySessions.value]) {
    await ensureMedia(rec.attachments)
  }
}
</script>

<template>
  <main>
    <h1>mcp-diary</h1>
    <p class="hint">使用 Google 账号登录，查看你自己的工作区。</p>

    <div v-if="!token">
      <button @click="beginLogin">使用 Google 登录</button>
    </div>
    <div v-else class="session">
      <span v-if="me">已登录：{{ me.email || me.username }}</span>
      <button class="secondary" @click="signOut">退出</button>
    </div>

    <p v-if="error" class="error">{{ error }}</p>
    <p v-if="loading">加载中…</p>

    <section v-if="me">
      <h2>日记</h2>
      <p v-if="diarySessions.length === 0 && diaryEntries.length === 0" class="hint">还没有日记。</p>

      <article v-for="sess in diarySessions" :key="sess.session_id" class="card">
        <header>
          <strong>{{ sess.diary_date }}</strong>
          <span class="badge">草稿</span>
        </header>
        <p class="body">{{ sess.content || '（空）' }}</p>
        <div class="media">
          <template v-for="att in sess.attachments || []" :key="att.attachment_id">
            <img
              v-if="att.media_type === 'image' && mediaBlobs[att.attachment_id]"
              :src="mediaBlobs[att.attachment_id]"
              :alt="att.original_name || att.filename"
            />
            <video
              v-else-if="att.media_type === 'video' && mediaBlobs[att.attachment_id]"
              :src="mediaBlobs[att.attachment_id]"
              controls
              preload="metadata"
            />
          </template>
        </div>
        <label class="upload">
          添加图片/视频
          <input
            type="file"
            accept="image/jpeg,image/png,image/gif,image/webp,video/mp4,video/webm,video/quicktime,.jpg,.jpeg,.png,.gif,.webp,.mp4,.webm,.mov"
            :disabled="uploading === sess.session_id"
            @change="onUpload($event, { session_id: sess.session_id })"
          />
        </label>
      </article>

      <article v-for="entry in diaryEntries" :key="entry.entry_id" class="card">
        <header>
          <strong>{{ entry.diary_date }}</strong>
        </header>
        <p class="body">{{ entry.content || '（空）' }}</p>
        <div class="media">
          <template v-for="att in entry.attachments || []" :key="att.attachment_id">
            <img
              v-if="att.media_type === 'image' && mediaBlobs[att.attachment_id]"
              :src="mediaBlobs[att.attachment_id]"
              :alt="att.original_name || att.filename"
            />
            <video
              v-else-if="att.media_type === 'video' && mediaBlobs[att.attachment_id]"
              :src="mediaBlobs[att.attachment_id]"
              controls
              preload="metadata"
            />
          </template>
        </div>
        <label class="upload">
          添加图片/视频
          <input
            type="file"
            accept="image/jpeg,image/png,image/gif,image/webp,video/mp4,video/webm,video/quicktime,.jpg,.jpeg,.png,.gif,.webp,.mp4,.webm,.mov"
            :disabled="uploading === entry.entry_id"
            @change="onUpload($event, { entry_id: entry.entry_id })"
          />
        </label>
      </article>
    </section>

    <section v-if="me">
      <h2>工作区文件</h2>
      <ul>
        <li v-for="entry in entries" :key="entry.path">
          {{ entry.is_dir ? '📁' : '📄' }} {{ entry.name }}
        </li>
      </ul>
      <p v-if="entries.length === 0" class="hint">（空）</p>
    </section>
  </main>
</template>

<style>
:root {
  color-scheme: light dark;
  font-family: system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif;
}

body {
  margin: 0;
  display: flex;
  justify-content: center;
}

main {
  width: min(680px, 92vw);
  padding: 3rem 0;
}

h1 {
  margin-bottom: 0.25rem;
}

.hint {
  color: #888;
}

.error {
  color: #d33;
}

.session {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

button {
  padding: 0.5rem 1rem;
  border: none;
  border-radius: 6px;
  background: #4285f4;
  color: #fff;
  font-size: 0.95rem;
  cursor: pointer;
}

button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

button.secondary {
  background: #eee;
  color: #333;
}

ul {
  list-style: none;
  padding: 0;
}

li {
  padding: 0.35rem 0;
  border-bottom: 1px solid rgba(128, 128, 128, 0.2);
}

.card {
  border: 1px solid rgba(128, 128, 128, 0.25);
  border-radius: 8px;
  padding: 0.9rem 1rem;
  margin: 0.75rem 0;
}

.card header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.badge {
  font-size: 0.75rem;
  background: #eee;
  color: #333;
  border-radius: 999px;
  padding: 0.1rem 0.5rem;
}

.body {
  white-space: pre-wrap;
}

.media {
  display: grid;
  gap: 0.75rem;
}

.media img,
.media video {
  max-width: 100%;
  border-radius: 6px;
  background: #111;
}

.upload {
  display: inline-flex;
  margin-top: 0.5rem;
  font-size: 0.9rem;
  color: #4285f4;
  cursor: pointer;
}

.upload input {
  display: none;
}
</style>
