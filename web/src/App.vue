<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { accessToken, beginLogin, completeLogin, signOut as clearSession } from './auth'
import {
  ApiError,
  createMedium,
  deleteMedium,
  getMe,
  getSettings,
  listMedia,
  triggerSync,
  updateMedium,
  updateSettings,
  type DiarySettings,
  type Me,
  type Medium,
  type SyncState,
} from './api'

const token = ref('')
const me = ref<Me | null>(null)
const error = ref('')
const loading = ref(false)
const media = ref<Medium[]>([])
const saving = ref(false)
const syncingID = ref('')
const editingID = ref('')

const settings = ref<DiarySettings>({
  lunar_enabled: false,
  weather_enabled: false,
  weather_location: '',
})
const settingsSaving = ref(false)

const form = ref({
  name: 'GitHub',
  owner: '',
  repo: '',
  branch: 'main',
  credential: '',
  enabled: true,
  preserveExisting: true,
})

const formTitle = computed(() => (editingID.value ? '编辑存储介质' : '添加 GitHub 存储'))

function resetForm(): void {
  editingID.value = ''
  form.value = {
    name: 'GitHub',
    owner: '',
    repo: '',
    branch: 'main',
    credential: '',
    enabled: true,
    preserveExisting: true,
  }
}

function startEdit(item: Medium): void {
  editingID.value = item.id
  form.value = {
    name: item.name,
    owner: item.settings?.owner ?? '',
    repo: item.settings?.repo ?? '',
    branch: item.settings?.branch ?? 'main',
    credential: '',
    enabled: item.enabled,
    preserveExisting: item.settings?.preserve_existing !== 'false',
  }
}

async function loadMedia(): Promise<void> {
  if (!token.value) {
    return
  }
  const data = await listMedia(token.value)
  media.value = data.media ?? []
}

async function refresh(): Promise<void> {
  if (!token.value) {
    return
  }
  loading.value = true
  error.value = ''
  try {
    me.value = await getMe(token.value)
    await loadMedia()
    settings.value = await getSettings(token.value)
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
    clearSession()
    token.value = ''
    me.value = null
    media.value = []
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
  media.value = []
  error.value = ''
  settings.value = { lunar_enabled: false, weather_enabled: false, weather_location: '' }
  resetForm()
}

async function saveSettings(): Promise<void> {
  if (!token.value) {
    return
  }
  settingsSaving.value = true
  error.value = ''
  try {
    settings.value = await updateSettings(token.value, {
      lunar_enabled: settings.value.lunar_enabled,
      weather_enabled: settings.value.weather_enabled,
      weather_location: settings.value.weather_location?.trim() || undefined,
    })
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    settingsSaving.value = false
  }
}

async function saveMedium(): Promise<void> {
  if (!token.value) {
    return
  }
  saving.value = true
  error.value = ''
  try {
    const input = {
      name: form.value.name.trim(),
      enabled: form.value.enabled,
      settings: {
        owner: form.value.owner.trim(),
        repo: form.value.repo.trim(),
        branch: form.value.branch.trim() || 'main',
        preserve_existing: form.value.preserveExisting ? 'true' : 'false',
      },
      credential: form.value.credential.trim() || undefined,
    }
    if (editingID.value) {
      await updateMedium(token.value, editingID.value, input)
    } else {
      await createMedium(token.value, { ...input, kind: 'github' })
    }
    resetForm()
    await loadMedia()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    saving.value = false
  }
}

async function removeMedium(item: Medium): Promise<void> {
  if (!token.value) {
    return
  }
  if (!window.confirm(`删除存储介质「${item.name}」？`)) {
    return
  }
  error.value = ''
  try {
    await deleteMedium(token.value, item.id)
    if (editingID.value === item.id) {
      resetForm()
    }
    await loadMedia()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}

function applyState(id: string, state: SyncState): void {
  media.value = media.value.map((item) => (item.id === id ? { ...item, state } : item))
}

async function runSync(item: Medium): Promise<void> {
  if (!token.value) {
    return
  }
  syncingID.value = item.id
  error.value = ''
  try {
    const state = await triggerSync(token.value, item.id)
    applyState(item.id, state)
  } catch (err) {
    if (err instanceof ApiError && err.state) {
      applyState(item.id, err.state)
    }
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    syncingID.value = ''
  }
}

function statusLabel(status: string): string {
  switch (status) {
    case 'idle':
      return '空闲'
    case 'syncing':
      return '同步中'
    case 'succeeded':
      return '已同步'
    case 'failed':
      return '失败'
    default:
      return status || '未知'
  }
}

function formatTime(value?: string | null): string {
  if (!value) {
    return '尚未同步'
  }
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) {
    return value
  }
  return d.toLocaleString()
}

function repoLabel(item: Medium): string {
  const owner = item.settings?.owner
  const repo = item.settings?.repo
  if (owner && repo) {
    return `${owner}/${repo}`
  }
  return '未填写仓库'
}
</script>

<template>
  <main>
    <h1>mcp-diary</h1>
    <p class="hint">日记通过 MCP 客户端记录；此页面用于确认登录身份，并配置同步到 GitHub 的存储介质。</p>

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
      <h2>身份信息</h2>
      <dl>
        <dt>用户</dt>
        <dd>{{ me.username }}</dd>
        <dt>Subject</dt>
        <dd>{{ me.subject }}</dd>
        <dt>Scopes</dt>
        <dd>{{ me.scopes.join(', ') }}</dd>
      </dl>
    </section>

    <section v-if="me">
      <h2>日记元数据</h2>
      <p class="hint">
        农历与天气作为元数据随日记保存，并在导出到 GitHub 时写入每天的标题行。天气需要填写城市名。
      </p>
      <form class="card" @submit.prevent="saveSettings">
        <label class="check">
          <input v-model="settings.lunar_enabled" type="checkbox" />
          写入农历（按日期推算）
        </label>
        <label class="check">
          <input v-model="settings.weather_enabled" type="checkbox" />
          写入天气（Open-Meteo，免 key）
        </label>
        <label v-if="settings.weather_enabled">
          城市名
          <input v-model="settings.weather_location" placeholder="Beijing" />
        </label>
        <div class="actions">
          <button type="submit" :disabled="settingsSaving">
            {{ settingsSaving ? '保存中…' : '保存元数据设置' }}
          </button>
        </div>
      </form>
    </section>

    <section v-if="me">
      <h2>存储介质</h2>
      <p class="hint">配置仅能在此页面完成。Token 只在提交时通过已登录会话传输，接口与页面均不回显明文。</p>

      <form class="card" @submit.prevent="saveMedium">
        <h3>{{ formTitle }}</h3>
        <label>
          名称
          <input v-model="form.name" required />
        </label>
        <label>
          GitHub owner
          <input v-model="form.owner" required placeholder="your-name" />
        </label>
        <label>
          仓库
          <input v-model="form.repo" required placeholder="diary" />
        </label>
        <label>
          分支
          <input v-model="form.branch" placeholder="main" />
        </label>
        <label>
          Personal Access Token
          <input
            v-model="form.credential"
            type="password"
            autocomplete="off"
            :placeholder="editingID ? '留空则保留已保存的 token' : 'ghp_… 细粒度 PAT，仅授予目标仓库 Contents 读写'"
            :required="!editingID"
          />
        </label>
        <label class="check">
          <input v-model="form.enabled" type="checkbox" />
          启用同步
        </label>
        <label class="check">
          <input v-model="form.preserveExisting" type="checkbox" />
          保留远端已有日记（按日期合并，不覆盖旧日期）
        </label>
        <div class="actions">
          <button type="submit" :disabled="saving">{{ editingID ? '保存修改' : '添加' }}</button>
          <button v-if="editingID" type="button" class="secondary" @click="resetForm">取消</button>
        </div>
      </form>

      <p v-if="!media.length" class="hint">还没有配置存储介质。</p>
      <ul v-else class="media-list">
        <li v-for="item in media" :key="item.id" class="card">
          <div class="media-head">
            <strong>{{ item.name }}</strong>
            <span class="badge" :data-status="item.state?.status">{{ statusLabel(item.state?.status) }}</span>
          </div>
          <p>{{ repoLabel(item) }} · {{ item.settings?.branch || 'main' }}</p>
          <p class="hint">
            {{ item.enabled ? '已启用' : '已停用' }} ·
            {{ item.has_credential ? '已保存凭证（已脱敏）' : '未保存凭证' }} ·
            最近同步：{{ formatTime(item.state?.last_synced_at) }}
          </p>
          <p v-if="item.state?.last_error" class="error">{{ item.state.last_error }}</p>
          <ul v-if="item.state?.documents && Object.keys(item.state.documents).length" class="docs">
            <li v-for="doc in Object.values(item.state.documents)" :key="doc.path">
              {{ doc.path }}
              <span v-if="doc.last_synced_at"> · {{ formatTime(doc.last_synced_at) }}</span>
            </li>
          </ul>
          <div class="actions">
            <button
              type="button"
              :disabled="syncingID === item.id || !item.enabled"
              @click="runSync(item)"
            >
              {{ syncingID === item.id ? '同步中…' : '立即同步' }}
            </button>
            <button type="button" class="secondary" @click="startEdit(item)">编辑</button>
            <button type="button" class="secondary" @click="removeMedium(item)">删除</button>
          </div>
        </li>
      </ul>
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
  width: min(720px, 92vw);
  padding: 3rem 0;
}

h1 {
  margin-bottom: 0.25rem;
}

h2 {
  margin-top: 2rem;
}

h3 {
  margin: 0 0 0.75rem;
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

dl {
  margin: 0;
}

dt {
  margin-top: 0.75rem;
  font-size: 0.8rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: #888;
}

dd {
  margin: 0.15rem 0 0;
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

.card {
  margin: 1rem 0;
  padding: 1rem;
  border: 1px solid #ddd;
  border-radius: 8px;
}

form.card {
  display: grid;
  gap: 0.75rem;
}

label {
  display: grid;
  gap: 0.3rem;
  font-size: 0.9rem;
}

label.check {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

input[type='text'],
input[type='password'],
input:not([type]) {
  padding: 0.45rem 0.6rem;
  border: 1px solid #ccc;
  border-radius: 6px;
  font: inherit;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}

.media-list {
  list-style: none;
  padding: 0;
  margin: 0;
}

.media-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}

.badge {
  font-size: 0.8rem;
  padding: 0.15rem 0.5rem;
  border-radius: 999px;
  background: #eee;
}

.badge[data-status='succeeded'] {
  background: #d7f5d7;
}

.badge[data-status='failed'] {
  background: #f8d4d4;
}

.badge[data-status='syncing'] {
  background: #dce8ff;
}

.docs {
  margin: 0.5rem 0 0;
  padding-left: 1.1rem;
  color: #666;
  font-size: 0.9rem;
}
</style>
