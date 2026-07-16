<script setup>
import { onActivated, onDeactivated, onUnmounted, ref } from 'vue'
import { closeAllConnections, closeConnection, getConnections } from '../api'

defineOptions({ name: 'Connections' })

const loading = ref(true)
const busy = ref(false)
const items = ref([])
const count = ref(0)
const upTotal = ref(0)
const downTotal = ref(0)

let pollTimer

function formatBytes(n) {
  const v = Number(n) || 0
  if (v < 1024) return `${v} B`
  if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)} KB`
  if (v < 1024 * 1024 * 1024) return `${(v / 1024 / 1024).toFixed(2)} MB`
  return `${(v / 1024 / 1024 / 1024).toFixed(2)} GB`
}

async function refresh() {
  try {
    const data = await getConnections()
    items.value = data.items || []
    count.value = data.count ?? items.value.length
    upTotal.value = data.uploadTotal || 0
    downTotal.value = data.downloadTotal || 0
  } catch (e) {
    if (!items.value.length) window.$toast?.(e.message || '无法获取连接')
  } finally {
    loading.value = false
  }
}

async function closeOne(id) {
  if (!id || busy.value) return
  busy.value = true
  try {
    await closeConnection(id)
    await refresh()
  } catch (e) {
    window.$toast?.(e.message)
  } finally {
    busy.value = false
  }
}

async function closeAll() {
  if (busy.value) return
  busy.value = true
  try {
    await closeAllConnections()
    await refresh()
    window.$toast?.('已关闭全部连接')
  } catch (e) {
    window.$toast?.(e.message)
  } finally {
    busy.value = false
  }
}

function startLive() {
  if (!items.value.length) loading.value = true
  refresh()
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = setInterval(refresh, 2000)
}

function stopLive() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

onActivated(startLive)
onDeactivated(stopLive)
onUnmounted(stopLive)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1 class="page-title">连接</h1>
      <div class="page-actions">
        <button class="btn btn-ghost" :disabled="busy" @click="refresh">刷新</button>
        <button class="btn btn-danger" :disabled="busy || !count" @click="closeAll">全部关闭</button>
      </div>
    </div>

    <div class="card" style="padding: 12px 14px">
      <div class="conn-summary">
        <span>当前 {{ count }}</span>
        <span>↑ {{ formatBytes(upTotal) }}</span>
        <span>↓ {{ formatBytes(downTotal) }}</span>
      </div>
    </div>

    <div v-if="loading" class="card empty">加载中…</div>
    <div v-else-if="!items.length" class="card empty">暂无连接</div>

    <div v-else class="conn-list">
      <div v-for="c in items" :key="c.id" class="card conn-item">
        <div class="conn-top">
          <div class="conn-host">{{ c.host || c.destination || '—' }}</div>
          <div class="conn-bytes">
            ↑ {{ formatBytes(c.upload) }} · ↓ {{ formatBytes(c.download) }}
          </div>
          <button
            class="conn-close"
            title="关闭连接"
            :disabled="busy"
            @click="closeOne(c.id)"
          >
            ×
          </button>
        </div>
        <div class="conn-bottom">
          <span v-if="c.chain" class="badge on">{{ c.chain }}</span>
          <span v-if="c.network" class="badge">{{ c.network }}</span>
          <span v-if="c.type" class="badge">{{ c.type }}</span>
          <span v-if="c.rule" class="conn-meta">{{ c.rule }}{{ c.rulePayload ? ` · ${c.rulePayload}` : '' }}</span>
          <span v-if="c.process" class="conn-meta">{{ c.process }}</span>
        </div>
      </div>
    </div>
  </div>
</template>
