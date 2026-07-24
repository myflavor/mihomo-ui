<script setup>
import { computed, nextTick, onActivated, onDeactivated, onMounted, onUnmounted, ref } from 'vue'
import { authHeaders, getOverview, setLogLevel } from '../api'

defineOptions({ name: 'Logs' })

const levels = [
  { key: 'debug', label: 'Debug' },
  { key: 'info', label: 'Info' },
  { key: 'warning', label: 'Warning' },
  { key: 'error', label: 'Error' },
]

// mihomo ranks: lower number = more verbose (DEBUG=0 … SILENT=4)
const levelRank = {
  debug: 0,
  info: 1,
  warning: 2,
  warn: 2,
  error: 3,
  silent: 4,
}

const level = ref('info')
const lines = ref([])
const paused = ref(false)
const busy = ref(false)
// idle | connecting | live | error
const status = ref('idle')
const autoScroll = ref(true)
const box = ref(null)
/** Ring buffer cap for retained history (all levels; debug can flood). */
const maxLines = 3000

let es = null
let buf = ''
let reconnectTimer = null
let backoffMs = 1000
let stopped = false
let lineSeq = 0

const visibleLines = computed(() => {
  const floor = levelRank[level.value] ?? levelRank.info
  return lines.value.filter((row) => {
    if (row.control) return true
    const r = levelRank[row.level]
    if (r == null) return level.value === 'debug'
    return r >= floor
  })
})

function statusLabel() {
  if (paused.value && status.value === 'live') return '已暂停'
  switch (status.value) {
    case 'connecting':
      return '连接中…'
    case 'live':
      return '实时'
    case 'error':
      return '未连接'
    default:
      return '未连接'
  }
}

function normalizeLevel(raw) {
  const l = String(raw || '').toLowerCase()
  if (l === 'warn') return 'warning'
  if (levelRank[l] != null) return l
  return ''
}

function stop() {
  stopped = true
  if (reconnectTimer) {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
  if (es) {
    es.abort()
    es = null
  }
  if (status.value !== 'error') status.value = 'idle'
}

function scheduleReconnect() {
  if (stopped) return
  if (reconnectTimer) clearTimeout(reconnectTimer)
  status.value = 'error'
  const wait = backoffMs
  backoffMs = Math.min(backoffMs * 1.8, 15000)
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null
    start()
  }, wait)
}

async function start() {
  if (es) {
    es.abort()
    es = null
  }
  if (reconnectTimer) {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
  stopped = false
  buf = ''
  status.value = 'connecting'
  const ctrl = new AbortController()
  es = ctrl
  try {
    // Always subscribe at debug so level pills only refilter history client-side;
    // stream stays open across switches (no history loss).
    const res = await fetch('/api/logs?level=debug', {
      signal: ctrl.signal,
      headers: authHeaders({ Accept: 'application/x-ndjson' }),
    })
    if (!res.ok || !res.body) {
      throw new Error(res.statusText || '无法连接日志流')
    }
    status.value = 'live'
    backoffMs = 1000
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    while (true) {
      const { value, done } = await reader.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      let idx
      while ((idx = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, idx).trim()
        buf = buf.slice(idx + 1)
        if (!line) continue
        pushLine(line)
      }
    }
    if (!stopped && es === ctrl) {
      scheduleReconnect()
    }
  } catch (e) {
    if (e.name === 'AbortError') return
    if (!stopped) {
      scheduleReconnect()
    }
  } finally {
    if (es === ctrl) {
      es = null
    }
  }
}

function pushLine(raw) {
  if (paused.value) return
  let payload = raw
  let type = ''
  let control = false
  try {
    const j = JSON.parse(raw)
    type = normalizeLevel(j.type || j.level) || String(j.type || j.level || '').toLowerCase()
    if (type === 'ping' || type === 'connected') return
    // stream control errors from our proxy always keep
    if (type === 'error' && typeof j.payload === 'string' && /upstream|log stream/i.test(j.payload)) {
      control = true
    }
    const p = j.payload || j.message || j.msg || raw
    payload = type ? `[${type}] ${p}` : String(p)
  } catch {
    // plain text — treat as debug-tier noise
    type = 'debug'
  }
  const ts = new Date().toLocaleTimeString()
  lines.value.push({
    id: ++lineSeq,
    ts,
    text: payload,
    level: type || '',
    control,
  })
  if (lines.value.length > maxLines) {
    lines.value.splice(0, lines.value.length - maxLines)
  }
  if (autoScroll.value) {
    nextTick(() => {
      if (box.value) box.value.scrollTop = box.value.scrollHeight
    })
  }
}

function clear() {
  lines.value = []
}

function togglePause() {
  paused.value = !paused.value
}

async function changeLevel(l) {
  if (busy.value || l === level.value) return
  busy.value = true
  const prev = level.value
  level.value = l
  try {
    // Kernel stdout follows pill; stream stays on debug (history retained).
    await setLogLevel(l)
    if (autoScroll.value) {
      nextTick(() => {
        if (box.value) box.value.scrollTop = box.value.scrollHeight
      })
    }
  } catch (e) {
    level.value = prev
    window.$toast?.(e.message || '设置日志级别失败')
  } finally {
    busy.value = false
  }
}

async function loadInitialLevel() {
  try {
    const ov = await getOverview()
    const l = (ov?.['log-level'] || '').toLowerCase()
    if (l && levels.some((x) => x.key === l)) {
      level.value = l
    }
  } catch {
    // keep default
  }
}

onMounted(loadInitialLevel)
onActivated(start)
onDeactivated(stop)
onUnmounted(stop)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1 class="page-title">日志</h1>
      <div class="page-actions">
        <button class="btn btn-ghost" @click="clear">清空</button>
        <button class="btn btn-ghost" @click="togglePause">{{ paused ? '继续' : '暂停' }}</button>
      </div>
    </div>

    <div class="card" style="padding: 12px 14px; margin-bottom: 10px">
      <div class="row" style="flex-wrap: wrap; gap: 10px; align-items: center">
        <div class="pill-group">
          <button
            v-for="l in levels"
            :key="l.key"
            class="pill"
            :class="{ active: level === l.key }"
            :disabled="busy"
            @click="changeLevel(l.key)"
          >
            {{ l.label }}
          </button>
        </div>
        <span
          class="badge"
          :class="{
            on: status === 'live',
            off: status !== 'live',
          }"
        >
          {{ statusLabel() }}
        </span>
      </div>
    </div>

    <div ref="box" class="card log-box">
      <div v-if="!visibleLines.length" class="empty" style="padding: 28px">等待日志…</div>
      <div v-for="line in visibleLines" :key="line.id" class="log-line">
        <span class="log-ts">{{ line.ts }}</span>
        <span class="log-text">{{ line.text }}</span>
      </div>
    </div>
  </div>
</template>
