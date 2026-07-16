<script setup>
import { onMounted, ref } from 'vue'
import { getRuntime, saveRuntime } from '../api'

const loading = ref(true)
const busy = ref(false)
const path = ref('')
const content = ref('')
const original = ref('')

const dirty = ref(false)

async function refresh() {
  loading.value = true
  try {
    const data = await getRuntime()
    path.value = data.path || ''
    content.value = data.content || ''
    original.value = content.value
    dirty.value = false
  } catch (e) {
    window.$toast?.(e.message)
  } finally {
    loading.value = false
  }
}

function onInput() {
  dirty.value = content.value !== original.value
}

async function save() {
  if (busy.value) return
  busy.value = true
  try {
    const res = await saveRuntime(content.value, true)
    if (res.ok === '0') {
      window.$toast?.(res.error || '已写入文件，但内核重载失败')
    } else {
      window.$toast?.('已保存并重载内核')
    }
    original.value = content.value
    dirty.value = false
  } catch (e) {
    window.$toast?.(e.message)
  } finally {
    busy.value = false
  }
}

function reset() {
  content.value = original.value
  dirty.value = false
}

onMounted(refresh)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1 class="page-title">配置</h1>
      <div class="page-actions">
        <button class="btn btn-ghost" :disabled="busy || loading" @click="refresh">重新加载</button>
        <button class="btn btn-ghost" :disabled="busy || !dirty" @click="reset">还原</button>
        <button class="btn btn-primary" :disabled="busy || loading || !dirty" @click="save">
          保存并重载
        </button>
      </div>
    </div>

    <div class="card tip-card" style="padding: 12px 16px">
      <div class="sub mono" style="line-height: 1.5">
        {{ path || '…' }}
      </div>
      <div class="sub" style="margin-top: 4px">编辑的是内核实际加载的原始文件。订阅「更新」会覆盖此文件中的订阅相关段。</div>
    </div>

    <div v-if="loading" class="card empty">加载中…</div>
    <div v-else class="card config-card">
      <textarea
        v-model="content"
        class="config-editor"
        spellcheck="false"
        @input="onInput"
      />
    </div>
  </div>
</template>
