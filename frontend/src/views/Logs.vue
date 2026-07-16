<script setup>
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { authHeaders } from '../api'

const levels = [
  { key: 'info', label: 'Info' },
  { key: 'debug', label: 'Debug' },
  { key: 'warning', label: 'Warning' },
  { key: 'error', label: 'Error' },
]

const level = ref('info')
const lines = ref([])
const paused = ref(false)
// idle | connecting | live | error
const status = ref('idle')
const autoScroll = ref(true)
const box = ref(null)
const maxLines = 500

let es = null
let buf = ''
let reconnectTimer = null
let backoffMs = 1000
let stopped = false

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
    const res = await fetch(`/api/logs?level=${encodeURIComponent(level.value)}`, {
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
  try {
    const j = JSON.parse(raw)
    type = j.type || j.level || ''
    if (type === 'ping') return
    const p = j.payload || j.message || j.msg || raw
    if (type === 'connected') {
      payload = p || '已连接'
    } else {
      payload = type ? `[${type}] ${p}` : String(p)
    }
  } catch {
    // plain text
  }
  const ts = new Date().toLocaleTimeString()
  lines.value.push({ ts, text: payload })
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

watch(level, () => {
  lines.value = []
  backoffMs = 1000
  start()
})

onMounted(start)
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
      <div class="row" style="flex-wrap: wrap; gap: 10px">
        <div class="pill-group">
          <button
            v-for="l in levels"
            :key="l.key"
            class="pill"
            :class="{ active: level === l.key }"
            @click="level = l.key"
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
      <div v-if="!lines.length" class="empty" style="padding: 28px">等待日志…</div>
      <div v-for="(line, i) in lines" :key="i" class="log-line">
        <span class="log-ts">{{ line.ts }}</span>
        <span class="log-text">{{ line.text }}</span>
      </div>
    </div>
  </div>
</template>
