<script setup>
import { computed, onActivated, onDeactivated, onUnmounted, ref } from 'vue'
import { authFailed, authHeaders, getOverview, logout, setMode, setTun } from '../api'
import ConfirmDialog from '../components/ConfirmDialog.vue'

defineOptions({ name: 'Home' })

const overview = ref(null)
const busy = ref(false)
const up = ref(0)
const down = ref(0)
const upTotal = ref(0)
const downTotal = ref(0)

let pollTimer
let trafficCtrl
let trafficBuf = ''
const leaving = ref(false)
const askLogout = ref(false)
let trafficRetryTimer = null
let trafficBackoffMs = 1000

const modes = [
  { key: 'rule', label: '规则' },
  { key: 'global', label: '全局' },
  { key: 'direct', label: '直连' },
]

const tunOn = computed(() => !!overview.value?.tun?.enable)
const kernelVer = computed(() => overview.value?.version?.version || '—')
const memText = computed(() => formatBytes(overview.value?.memory ?? 0))
const connCount = computed(() => overview.value?.connections ?? 0)
const activeName = computed(() => overview.value?.active?.name || '—')
const mixedPort = computed(() => overview.value?.['mixed-port'] ?? '—')
const logLevel = computed(() => overview.value?.['log-level'] || '—')

function formatBytes(n) {
  const v = Number(n) || 0
  if (v < 1024) return `${v} B`
  if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)} KB`
  if (v < 1024 * 1024 * 1024) return `${(v / 1024 / 1024).toFixed(2)} MB`
  return `${(v / 1024 / 1024 / 1024).toFixed(2)} GB`
}

function formatRate(n) {
  const v = Number(n) || 0
  if (v < 1024) return `${Math.round(v)} B/s`
  if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)} KB/s`
  return `${(v / 1024 / 1024).toFixed(2)} MB/s`
}

async function refresh() {
  try {
    const data = await getOverview()
    overview.value = data
    if (data.uploadTotal != null) upTotal.value = data.uploadTotal
    if (data.downloadTotal != null) downTotal.value = data.downloadTotal
  } catch (e) {
    if (!overview.value) window.$toast?.(e.message || '无法连接内核')
  }
}

async function changeMode(mode) {
  if (busy.value) return
  busy.value = true
  try {
    await setMode(mode)
    await refresh()
    window.$toast?.(`已切换到${modes.find((m) => m.key === mode)?.label || mode}`)
  } catch (e) {
    window.$toast?.(e.message)
  } finally {
    busy.value = false
  }
}

async function toggleTun() {
  if (busy.value || !overview.value) return
  const cur = tunOn.value
  busy.value = true
  try {
    await setTun(!cur)
    await refresh()
    window.$toast?.(cur ? '已关闭 TUN' : '已开启 TUN')
  } catch (e) {
    window.$toast?.(e.message)
  } finally {
    busy.value = false
  }
}

// Without this the rates freeze forever once a proxy drops the idle stream.
// Note this restores the data, not the feedback: unlike Logs there is still no
// status badge here, so during a retry the numbers just sit at their last value.
function scheduleTrafficReconnect() {
  if (trafficRetryTimer) clearTimeout(trafficRetryTimer)
  const wait = trafficBackoffMs
  trafficBackoffMs = Math.min(trafficBackoffMs * 1.8, 15000)
  trafficRetryTimer = setTimeout(() => {
    trafficRetryTimer = null
    startTraffic()
  }, wait)
}

function startTraffic() {
  stopTraffic()
  const ctrl = new AbortController()
  trafficCtrl = ctrl
  trafficBuf = ''
  ;(async () => {
    try {
      const res = await fetch('/api/traffic', {
        signal: ctrl.signal,
        headers: authHeaders({ Accept: 'application/x-ndjson' }),
      })
      if (res.status === 401) {
        // Retrying with a dead token would loop forever; hand off to login.
        // Guarded like every other exit here: a newer stream may already have
        // replaced this one, and tearing that down would kill a healthy stream.
        if (trafficCtrl !== ctrl) return
        stopTraffic()
        authFailed()
        return
      }
      if (!res.ok || !res.body) {
        if (trafficCtrl === ctrl) scheduleTrafficReconnect()
        return
      }
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      while (true) {
        const { value, done } = await reader.read()
        if (done) break
        trafficBuf += decoder.decode(value, { stream: true })
        let idx
        while ((idx = trafficBuf.indexOf('\n')) >= 0) {
          const line = trafficBuf.slice(0, idx).trim()
          trafficBuf = trafficBuf.slice(idx + 1)
          if (!line) continue
          try {
            const j = JSON.parse(line)
            // Only a real data line proves the stream works: /api/traffic sends
            // 200 before it dials mihomo, so res.ok alone would reset the
            // backoff on every failed attempt and busy-loop the reconnect.
            trafficBackoffMs = 1000
            if (j.up != null) up.value = j.up
            if (j.down != null) down.value = j.down
            if (j.upTotal != null) upTotal.value = j.upTotal
            if (j.downTotal != null) downTotal.value = j.downTotal
          } catch {
            // ignore
          }
        }
      }
      // upstream closed the stream
      if (trafficCtrl === ctrl) scheduleTrafficReconnect()
    } catch (e) {
      if (e.name !== 'AbortError' && trafficCtrl === ctrl) scheduleTrafficReconnect()
    }
  })()
}

// Logging out is rare but costs a password re-entry to undo, and the button
// sits next to 刷新 — worth one tap of confirmation.
async function doLogout() {
  if (leaving.value) return
  leaving.value = true
  // Stop the polls and the stream first, so nothing fires a request against a
  // token we are about to revoke and bounces the user through a spurious 401.
  stopLive()
  try {
    await logout()
    window.$toast?.('已退出登录')
  } finally {
    leaving.value = false
    askLogout.value = false
  }
}

function stopTraffic() {
  if (trafficRetryTimer) {
    clearTimeout(trafficRetryTimer)
    trafficRetryTimer = null
  }
  if (trafficCtrl) {
    trafficCtrl.abort()
    trafficCtrl = null
  }
}

function startLive() {
  refresh()
  startTraffic()
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = setInterval(refresh, 4000)
}

function stopLive() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
  stopTraffic()
}

onActivated(startLive)
onDeactivated(stopLive)
onUnmounted(stopLive)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1 class="page-title">首页</h1>
      <div class="page-actions">
        <button class="btn btn-ghost" :disabled="busy" @click="refresh">刷新</button>
        <button class="btn btn-ghost" :disabled="leaving" @click="askLogout = true">退出登录</button>
      </div>
    </div>

    <!-- 首屏直接画布局，避免「加载中…」一闪 -->
    <div class="card traffic-card">
      <div class="traffic-grid">
        <div class="traffic-cell">
          <div class="traffic-label up">↑ 上传</div>
          <div class="traffic-rate">{{ formatRate(up) }}</div>
          <div class="traffic-total">累计 {{ formatBytes(upTotal) }}</div>
        </div>
        <div class="traffic-divider" />
        <div class="traffic-cell">
          <div class="traffic-label down">↓ 下载</div>
          <div class="traffic-rate">{{ formatRate(down) }}</div>
          <div class="traffic-total">累计 {{ formatBytes(downTotal) }}</div>
        </div>
      </div>
      <div class="traffic-meta">
        <span>连接 {{ connCount }}</span>
        <span>内存 {{ memText }}</span>
      </div>
    </div>

    <div class="card">
      <div class="row">
        <div class="label">代理模式</div>
        <div class="pill-group">
          <button
            v-for="m in modes"
            :key="m.key"
            class="pill"
            :class="{ active: overview?.mode === m.key }"
            :disabled="busy"
            @click="changeMode(m.key)"
          >
            {{ m.label }}
          </button>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="row">
        <div class="label">TUN 模式</div>
        <button
          class="switch"
          :class="{ on: tunOn }"
          :disabled="busy || !overview"
          aria-label="toggle tun"
          @click="toggleTun"
        />
      </div>
    </div>

    <div class="card">
      <div class="card-title">运行状态</div>
      <div class="stat-grid">
        <div class="stat">
          <div class="k">当前配置</div>
          <div class="v" style="font-size: 15px">{{ activeName }}</div>
        </div>
        <div class="stat">
          <div class="k">日志级别</div>
          <div class="v" style="font-size: 15px; text-transform: uppercase">{{ logLevel }}</div>
        </div>
        <div class="stat">
          <div class="k">内核版本</div>
          <div class="v" style="font-size: 15px">{{ kernelVer }}</div>
        </div>
        <div class="stat">
          <div class="k">混合端口</div>
          <div class="v">{{ mixedPort }}</div>
        </div>
      </div>
    </div>


    <ConfirmDialog
      :open="askLogout"
      :busy="leaving"
      @confirm="doLogout"
      @cancel="askLogout = false"
    >
      确认退出登录？
    </ConfirmDialog>
  </div>
</template>
