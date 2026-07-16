<script setup>
import { nextTick, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { login, setToken } from '../api'

const router = useRouter()

const password = ref('')
const busy = ref(false)
const error = ref('')
const inputEl = ref(null)

async function doLogin() {
  if (!password.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    const res = await login(password.value)
    setToken(res.token || password.value)
    await router.replace('/')
    window.dispatchEvent(new CustomEvent('ui-auth-ok'))
  } catch (e) {
    error.value = e.message || '密码错误'
    setToken('')
    password.value = ''
    await nextTick()
    inputEl.value?.focus?.()
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  nextTick(() => inputEl.value?.focus?.())
})
</script>

<template>
  <div class="login-page">
    <div class="login-card">
      <div class="login-brand">
        <div class="login-logo">M</div>
        <h1 class="login-title">Mihomo</h1>
      </div>

      <form class="login-form" @submit.prevent="doLogin">
        <input
          id="ui-password"
          ref="inputEl"
          v-model="password"
          class="field login-input"
          type="password"
          autocomplete="current-password"
          placeholder="请输入密码"
          :disabled="busy"
        />
        <p v-if="error" class="login-error">{{ error }}</p>
        <button class="btn btn-primary login-btn" type="submit" :disabled="busy || !password">
          {{ busy ? '登录中…' : '登录' }}
        </button>
      </form>
    </div>
  </div>
</template>
