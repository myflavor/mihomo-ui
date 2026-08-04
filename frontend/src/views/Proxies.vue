<script setup>
import { computed, onActivated, ref } from 'vue'
import { getProxies, selectProxy, testGroupDelay } from '../api'
import { useMaskClose } from '../composables/useMaskClose'

defineOptions({ name: 'Proxies' })

const loading = ref(true)
const groups = ref([])
const activeGroup = ref('')
const delays = ref({})
const busy = ref(false)
const filter = ref('')
const pickerOpen = ref(false)
const pickerMask = useMaskClose(() => {
  pickerOpen.value = false
})

const current = computed(() => groups.value.find((g) => g.name === activeGroup.value))

const hiddenNodes = new Set(['COMPATIBLE', 'Pass', 'REJECT'])

const filteredNodes = computed(() => {
  const all = (current.value?.all || []).filter((n) => !hiddenNodes.has(String(n)))
  const q = filter.value.trim().toLowerCase()
  if (!q) return all
  return all.filter((n) => String(n).toLowerCase().includes(q))
})

async function refresh() {
  try {
    const data = await getProxies()
    // keep empty groups (show empty state); only drop pure noise
    const list = (data.groups || []).filter((g) => g.name !== 'COMPATIBLE' && g.name !== 'Pass')
    groups.value = list
    if (!activeGroup.value && list.length) {
      activeGroup.value = list[0].name
    } else if (activeGroup.value && !list.find((g) => g.name === activeGroup.value) && list.length) {
      activeGroup.value = list[0].name
    }
  } catch (e) {
    if (!groups.value.length) window.$toast?.(e.message)
  } finally {
    loading.value = false
  }
}

async function pick(name) {
  if (!current.value || busy.value) return
  if (hiddenNodes.has(name)) return
  busy.value = true
  try {
    await selectProxy(current.value.name, name)
    await refresh()
    window.$toast?.(`已选择 ${name}`)
  } catch (e) {
    window.$toast?.(e.message)
  } finally {
    busy.value = false
  }
}

function chooseGroup(name) {
  activeGroup.value = name
  filter.value = ''
  pickerOpen.value = false
}

async function speedtest() {
  if (!current.value || busy.value) return
  busy.value = true
  window.$toast?.('测速中…')
  try {
    const res = await testGroupDelay(current.value.name)
    delays.value = { ...delays.value, ...res }
    window.$toast?.('测速完成')
  } catch (e) {
    window.$toast?.(e.message)
  } finally {
    busy.value = false
  }
}

function delayClass(ms) {
  if (ms == null || ms === 0) return ''
  if (ms < 200) return 'good'
  if (ms < 500) return 'mid'
  return 'bad'
}

function delayText(name) {
  const v = delays.value[name]
  if (v == null) return '—'
  if (v === 0) return '超时'
  return `${v}ms`
}

function typeLabel(t) {
  const map = {
    Selector: '手动',
    URLTest: '自动',
    Fallback: '故障转移',
    LoadBalance: '负载均衡',
    Relay: '中继',
  }
  return map[t] || t || ''
}

onActivated(() => {
  // 已有数据时静默刷新，避免 keep-alive 回来再闪「加载中…」
  if (groups.value.length) {
    refresh()
  } else {
    loading.value = true
    refresh()
  }
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1 class="page-title">节点</h1>
      <div class="page-actions">
        <button class="btn btn-ghost" :disabled="busy || loading" @click="refresh">刷新</button>
        <button class="btn btn-primary" :disabled="busy || !current" @click="speedtest">测速</button>
      </div>
    </div>

    <div v-if="loading" class="card empty">加载中…</div>
    <div v-else-if="!groups.length" class="card empty">暂无策略组。请先在「配置」添加订阅。</div>

    <template v-else>
      <div
        class="card group-pick-card"
        role="button"
        tabindex="0"
        @click="pickerOpen = true"
        @keydown.enter.space.prevent="pickerOpen = true"
      >
        <div class="row">
          <div class="label">分组</div>
          <div class="group-pick-value">
            <span class="group-pick-name">{{ activeGroup || '未选择' }}</span>
            <svg class="chev" viewBox="0 0 12 12" width="12" height="12" aria-hidden="true">
              <path d="M2 4.5 L6 8.5 L10 4.5" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </div>
        </div>
      </div>

      <div class="card group-card">
        <div class="group-head">
          <div>
            <div class="label">{{ current?.name }}</div>
            <div class="sub">
              {{ typeLabel(current?.type) }} · 当前
              <span class="accent-text">{{ current?.now || '—' }}</span>
              · {{ (current?.all || []).length }} 个节点
            </div>
          </div>
        </div>

        <input
          v-if="(current?.all || []).length > 8"
          v-model="filter"
          class="field search-field"
          placeholder="筛选节点…"
        />

        <div class="list node-list">
          <button
            v-for="name in filteredNodes"
            :key="name"
            class="node-item"
            :class="{ active: name === current?.now }"
            :disabled="busy"
            @click="pick(name)"
          >
            <span class="node-radio" :class="{ on: name === current?.now }" />
            <span class="node-name">{{ name }}</span>
            <span class="delay" :class="delayClass(delays[name])">{{ delayText(name) }}</span>
          </button>
          <div v-if="!filteredNodes.length" class="empty" style="padding: 16px">
            暂无节点（provider 未拉取到内容）
          </div>
        </div>
      </div>

      <Transition name="modal-fade">
        <div
          v-if="pickerOpen"
          class="modal-mask"
          @mousedown="pickerMask.onMousedown"
          @click="pickerMask.onClick"
        >
          <div class="modal group-modal">
            <h3>选择分组</h3>
            <div class="group-modal-list">
              <button
                v-for="g in groups"
                :key="g.name"
                type="button"
                class="group-opt"
                :class="{ active: g.name === activeGroup }"
                @click="chooseGroup(g.name)"
              >
                <span>{{ g.name }}</span>
                <span v-if="g.name === activeGroup" class="check">✓</span>
              </button>
            </div>
          </div>
        </div>
      </Transition>
    </template>
  </div>
</template>
