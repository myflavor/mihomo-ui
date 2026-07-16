<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { checkAuth, getToken, setToken } from './api'

const route = useRoute()
const router = useRouter()
const toast = ref('')
let toastTimer

function showToast(msg) {
  toast.value = msg
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (toast.value = ''), 2200)
}

if (typeof window !== 'undefined') {
  window.$toast = showToast
}

const tabs = [
  {
    to: '/',
    label: '首页',
    paths: [
      'M4.5 10.8 12 4.3l7.5 6.5',
      'M6.2 9.8V19a1.2 1.2 0 0 0 1.2 1.2h3.1v-4.6a1.2 1.2 0 0 1 1.2-1.2h1.6a1.2 1.2 0 0 1 1.2 1.2v4.6h3.1A1.2 1.2 0 0 0 18.8 19V9.8',
    ],
  },
  {
    to: '/proxies',
    label: '节点',
    paths: [
      'M12 4.2a2.8 2.8 0 1 1 0 5.6 2.8 2.8 0 0 1 0-5.6Z',
      'M12 9.8v3.2',
      'M8.2 16.2h7.6',
      'M8.2 19.2h7.6',
      'M12 13v3.2',
    ],
  },
  {
    to: '/configs',
    label: '配置',
    paths: [
      'M5 7.2h10.5',
      'M5 12h6.5',
      'M5 16.8h10.5',
      'M17.8 7.2a1.4 1.4 0 1 1 0.01 0Z',
      'M14.2 12a1.4 1.4 0 1 1 0.01 0Z',
      'M17.8 16.8a1.4 1.4 0 1 1 0.01 0Z',
    ],
  },
  {
    to: '/connections',
    label: '连接',
    paths: [
      // network: three nodes (slightly inset so visual weight matches other tabs)
      'M17.2 8.2a2.2 2.2 0 1 0 0-4.4 2.2 2.2 0 0 0 0 4.4z',
      'M6.8 14.2a2.2 2.2 0 1 0 0-4.4 2.2 2.2 0 0 0 0 4.4z',
      'M17.2 20.2a2.2 2.2 0 1 0 0-4.4 2.2 2.2 0 0 0 0 4.4z',
      'M8.9 13.1 15.1 16.9',
      'M15.1 7.1 8.9 10.9',
    ],
  },
  {
    to: '/logs',
    label: '日志',
    paths: [
      'M7.2 4.5h6.2L17 8.1v11.4a1.3 1.3 0 0 1-1.3 1.3H7.2a1.3 1.3 0 0 1-1.3-1.3V5.8A1.3 1.3 0 0 1 7.2 4.5Z',
      'M13.3 4.7v3.5H16.8',
      'M8.8 12.2h6.4',
      'M8.8 15.6h4.6',
    ],
  },
]

const active = computed(() => route.path)
const isLogin = computed(() => route.name === 'login' || route.path === '/login')

function go(path) {
  router.push(path)
}

const authReady = ref(false)
const authed = ref(false)

async function probeAuth() {
  try {
    const res = await checkAuth()
    if (!res.required) {
      authed.value = true
      return
    }
    authed.value = !!(res.ok && getToken())
  } catch {
    authed.value = !!getToken()
  } finally {
    authReady.value = true
    await guardRoute()
  }
}

async function guardRoute() {
  if (!authReady.value) return
  if (authed.value) {
    if (isLogin.value) await router.replace('/')
    return
  }
  if (!isLogin.value) await router.replace('/login')
}

function onAuthRequired() {
  setToken('')
  authed.value = false
  if (!isLogin.value) router.replace('/login')
}

function onAuthOk() {
  authed.value = true
}

onMounted(() => {
  probeAuth()
  window.addEventListener('ui-auth-required', onAuthRequired)
  window.addEventListener('ui-auth-ok', onAuthOk)
})
onUnmounted(() => {
  window.removeEventListener('ui-auth-required', onAuthRequired)
  window.removeEventListener('ui-auth-ok', onAuthOk)
})

watch(
  () => route.fullPath,
  () => {
    guardRoute()
  },
)

watch(
  () => route.meta?.title,
  (t) => {
    document.title = t ? `${t} · Mihomo` : 'Mihomo'
  },
  { immediate: true },
)
</script>

<template>
  <div class="app-shell" :class="{ 'app-shell-login': isLogin }">
    <div v-if="!authReady" class="page empty">加载中…</div>

    <template v-else-if="isLogin || !authed">
      <router-view />
    </template>

    <template v-else>
      <div class="desktop-nav page" style="padding-bottom: 0">
        <button
          v-for="t in tabs"
          :key="t.to"
          class="nav-item"
          :class="{ active: active === t.to }"
          @click="go(t.to)"
        >
          <span class="nav-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none">
              <path
                v-for="(d, i) in t.paths"
                :key="i"
                :d="d"
                stroke="currentColor"
                stroke-width="1.75"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
          </span>
          {{ t.label }}
        </button>
      </div>
      <router-view />
      <nav class="bottom-nav">
        <button
          v-for="t in tabs"
          :key="t.to"
          class="nav-item"
          :class="{ active: active === t.to }"
          @click="go(t.to)"
        >
          <span class="nav-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none">
              <path
                v-for="(d, i) in t.paths"
                :key="i"
                :d="d"
                stroke="currentColor"
                stroke-width="1.75"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
          </span>
          {{ t.label }}
        </button>
      </nav>
      <div v-if="toast" class="toast">{{ toast }}</div>
    </template>
  </div>
</template>
