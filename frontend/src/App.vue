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
  { to: '/', label: '首页', icon: '⌂' },
  { to: '/proxies', label: '节点', icon: '◎' },
  { to: '/subs', label: '配置', icon: '☰' },
  { to: '/logs', label: '日志', icon: '≡' },
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
          <span class="nav-icon">{{ t.icon }}</span>
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
          <span class="nav-icon">{{ t.icon }}</span>
          {{ t.label }}
        </button>
      </nav>
      <div v-if="toast" class="toast">{{ toast }}</div>
    </template>
  </div>
</template>
