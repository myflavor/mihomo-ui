<script setup>
import { onMounted, onUnmounted, ref } from 'vue'
import { getOverview, setMode, setTun } from '../api'

const loading = ref(true)
const overview = ref(null)
const busy = ref(false)
let timer

const modes = [
  { key: 'rule', label: '规则' },
  { key: 'global', label: '全局' },
  { key: 'direct', label: '直连' },
]

async function refresh() {
  try {
    overview.value = await getOverview()
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
  const cur = !!overview.value.tun?.enable
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

onMounted(() => {
  refresh()
  timer = setInterval(refresh, 5000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1 class="page-title">首页</h1>
    </div>

    <div v-if="loading" class="card empty">加载中…</div>

    <template v-else>
      <div class="card hero-card">
        <div class="hero-row">
          <div>
            <div class="hero-k">代理模式</div>
            <div class="hero-v">
              {{ modes.find((m) => m.key === overview?.mode)?.label || overview?.mode || '—' }}
            </div>
          </div>
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
          <div>
            <div class="label">TUN 模式</div>
            <div class="sub">接管系统流量（NET_ADMIN + /dev/net/tun）</div>
          </div>
          <button
            class="switch"
            :class="{ on: !!overview?.tun?.enable }"
            :disabled="busy"
            @click="toggleTun"
            aria-label="toggle tun"
          />
        </div>
      </div>

      <div class="card">
        <div class="card-title">运行状态</div>
        <div class="stat-grid">
          <div class="stat">
            <div class="k">内核</div>
            <div class="v" style="font-size: 15px">
              {{ overview?.version?.version || '—' }}
            </div>
          </div>
          <div class="stat">
            <div class="k">混合端口</div>
            <div class="v">{{ overview?.['mixed-port'] ?? '—' }}</div>
          </div>
          <div class="stat">
            <div class="k">订阅数</div>
            <div class="v">{{ overview?.subscriptions ?? '—' }}</div>
          </div>
          <div class="stat">
            <div class="k">TUN</div>
            <div class="v" style="font-size: 16px">
              {{ overview?.tun?.enable ? '开启' : '关闭' }}
            </div>
          </div>
        </div>
      </div>

          </template>
  </div>
</template>
