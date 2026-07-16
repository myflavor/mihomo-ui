<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { authHeaders, getOverview, setMode, setTun } from '../api'

const loading = ref(true)
const overview = ref(null)
const busy = ref(false)
const up = ref(0)
const down = ref(0)
const upTotal = ref(0)
const downTotal = ref(0)

let pollTimer
let trafficCtrl
let trafficBuf = ''

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
    window.$toast?.(e.message || '无法连接内核')
  } finally {
    loading.value = false
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
      if (!res.ok || !res.body) return
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
            if (j.up != null) up.value = j.up
            if (j.down != null) down.value = j.down
            if (j.upTotal != null) upTotal.value = j.upTotal
            if (j.downTotal != null) downTotal.value = j.downTotal
          } catch {
            // ignore
          }
        }
      }
    } catch (e) {
      if (e.name !== 'AbortError') {
        // silent
      }
    }
  })()
}

function stopTraffic() {
  if (trafficCtrl) {
    trafficCtrl.abort()
    trafficCtrl = null
  }
}

onMounted(() => {
  refresh()
  startTraffic()
  pollTimer = setInterval(refresh, 4000)
})
onUnmounted(() => {
  clearInterval(pollTimer)
  stopTraffic()
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1 class="page-title">首页</h1>
      <div class="page-actions">
        <button class="btn btn-ghost" :disabled="busy" @click="refresh">刷新</button>
      </div>
    </div>

    <div v-if="loading" class="card empty">加载中…</div>

    <template v-else>
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
            :disabled="busy"
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
    </template>
  </div>
</template>
