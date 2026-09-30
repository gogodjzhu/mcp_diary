<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { accessToken, beginLogin, completeLogin, signOut as clearSession } from './auth'
import { getMe, listFiles, type Entry, type Me } from './api'

const token = ref('')
const me = ref<Me | null>(null)
const entries = ref<Entry[]>([])
const error = ref('')
const loading = ref(false)

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
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
    clearSession()
    token.value = ''
    me.value = null
    entries.value = []
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
  error.value = ''
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
</style>
