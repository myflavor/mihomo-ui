<script setup>
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import {
  activateConfig,
  addConfig,
  deleteConfig,
  getConfigRaw,
  listConfigs,
  refreshConfig,
  refreshConfigs,
  saveConfigRaw,
  updateConfig,
  uploadConfig,
} from '../api'

const items = ref([])
const loading = ref(true)
const busy = ref(false)
const menuId = ref('')

const showForm = ref(false)
const editing = ref(null)
const form = reactive({
  name: '',
  url: '',
  interval: '',
  source: 'url',
  file: null,
  fileName: '',
})

const showConfig = ref(false)
const cfgLoading = ref(false)
const cfgBusy = ref(false)
const cfgId = ref('')
const cfgName = ref('')
const cfgActive = ref(false)
const cfgContent = ref('')
const cfgOriginal = ref('')
const cfgDirty = computed(() => cfgContent.value !== cfgOriginal.value)

const activeId = computed(() => items.value.find((i) => i.active)?.id || '')

function applyNote(res, okMsg) {
  const apply = res?.apply
  if (apply && apply.ok === '0') {
    window.$toast?.(apply.error || '已保存，但写入内核失败')
    return
  }
  if (apply?.detail?.Failed?.length) {
    window.$toast?.(`${okMsg}（失败：${apply.detail.Failed.join('；')}）`)
    return
  }
  if (apply?.detail?.Warnings?.length) {
    window.$toast?.(`${okMsg}（${apply.detail.Warnings[0]}）`)
    return
  }
  window.$toast?.(okMsg)
}

async function refresh() {
  try {
    const data = await listConfigs()
    items.value = data.configs || []
  } catch (e) {
    window.$toast?.(e.message)
  } finally {
    loading.value = false
  }
}

function openAdd() {
  menuId.value = ''
  editing.value = null
  form.name = ''
  form.url = ''
  form.interval = ''
  form.source = 'url'
  form.file = null
  form.fileName = ''
  showForm.value = true
}

function toggleMenu(id, e) {
  e?.stopPropagation?.()
  e?.preventDefault?.()
  menuId.value = menuId.value === id ? '' : id
}

function onGlobalPointerDown(e) {
  if (!menuId.value) return
  const el = e.target
  if (el?.closest?.('.cfg-menu') || el?.closest?.('.cfg-more')) return
  menuId.value = ''
}

function openEdit(item, e) {
  e?.stopPropagation?.()
  menuId.value = ''
  editing.value = item
  form.name = item.name
  form.url = item.url || ''
  form.interval = item.interval > 0 ? item.interval : ''
  form.source = item.source || (item.url ? 'url' : 'file')
  form.file = null
  form.fileName = ''
  requestAnimationFrame(() => {
    showForm.value = true
  })
}

async function remove(id, e) {
  e?.stopPropagation?.()
  menuId.value = ''
  if (!confirm('删除该配置？')) return
  busy.value = true
  try {
    const res = await deleteConfig(id)
    await refresh()
    applyNote(res, '已删除')
  } catch (e2) {
    window.$toast?.(e2.message)
  } finally {
    busy.value = false
  }
}

async function openConfig(item, e) {
  e?.stopPropagation?.()
  menuId.value = ''
  cfgId.value = item.id
  cfgName.value = item.name
  cfgActive.value = !!item.active
  showConfig.value = true
  cfgLoading.value = true
  cfgContent.value = ''
  cfgOriginal.value = ''
  try {
    const data = await getConfigRaw(item.id)
    cfgContent.value = data.content || ''
    cfgOriginal.value = cfgContent.value
    cfgActive.value = !!data.active
  } catch (err) {
    window.$toast?.(err.message || '尚未缓存原始配置，请先点更新或重新添加')
    showConfig.value = false
  } finally {
    cfgLoading.value = false
  }
}

function setSource(src) {
  form.source = src
  if (src === 'url') {
    form.file = null
    form.fileName = ''
  } else {
    form.url = ''
    form.interval = ''
  }
}

function onFile(e) {
  const f = e.target.files?.[0]
  form.file = f || null
  form.fileName = f?.name || ''
  if (f && !form.name.trim()) {
    form.name = f.name.replace(/\.(ya?ml|txt)$/i, '')
  }
}

function parsedInterval() {
  if (form.source === 'file') return 0
  const n = Number(form.interval)
  if (!form.interval || Number.isNaN(n) || n <= 0) return 0
  return n
}

async function submit() {
  if (!form.name.trim()) {
    window.$toast?.('请填写名称')
    return
  }
  if (form.source === 'url' && !form.url.trim()) {
    window.$toast?.('请填写订阅地址')
    return
  }
  if (form.source === 'file' && !form.file && !editing.value) {
    window.$toast?.('请选择配置文件')
    return
  }
  busy.value = true
  try {
    const interval = parsedInterval()
    let res
    const isEdit = !!editing.value
    if (form.source === 'file') {
      if (form.file) {
        window.$toast?.(isEdit ? '正在重新加载…' : '正在保存…')
        res = await uploadConfig({
          id: editing.value?.id,
          name: form.name.trim(),
          url: '',
          interval: 0,
          file: form.file,
          activate: !editing.value || editing.value.active,
        })
      } else {
        window.$toast?.('正在重新加载…')
        res = await updateConfig(editing.value.id, {
          name: form.name.trim(),
          source: 'file',
          interval: 0,
        })
      }
    } else if (editing.value) {
      // always full refresh pipeline — remote content / providers may have changed
      window.$toast?.('正在重新加载…')
      res = await updateConfig(editing.value.id, {
        name: form.name.trim(),
        url: form.url.trim(),
        source: 'url',
        interval,
      })
    } else {
      window.$toast?.('正在添加…')
      res = await addConfig({
        name: form.name.trim(),
        url: form.url.trim(),
        source: 'url',
        interval,
        activate: true,
      })
    }
    showForm.value = false
    await refresh()
    applyNote(res, isEdit ? '已重新加载' : '已保存')
  } catch (e) {
    window.$toast?.(e.message)
  } finally {
    busy.value = false
  }
}

async function onCardClick(item) {
  if (menuId.value) {
    menuId.value = ''
    return
  }
  if (item.active || busy.value) return
  busy.value = true
  try {
    const res = await activateConfig(item.id)
    await refresh()
    applyNote(res, `已切换到 ${item.name}`)
  } catch (e) {
    window.$toast?.(e.message)
  } finally {
    busy.value = false
  }
}

async function doRefresh() {
  menuId.value = ''
  if (!items.value.length) {
    window.$toast?.('还没有配置')
    return
  }
  const urlItems = items.value.filter((i) => i.source !== 'file' && i.url)
  if (!urlItems.length) {
    window.$toast?.('没有可更新的 URL 配置')
    return
  }
  busy.value = true
  window.$toast?.(`正在更新 ${urlItems.length} 个配置…`)
  try {
    const res = await refreshConfigs()
    const n = res?.refreshed ?? urlItems.length
    const fails = res?.errors?.length ? `（${res.errors[0]}）` : ''
    if (res?.ok === false) {
      window.$toast?.(`更新完成但有错误${fails}`)
    } else {
      window.$toast?.(`已更新 ${n} 个配置${fails}`)
    }
    await refresh()
  } catch (err) {
    window.$toast?.(err.message)
  } finally {
    busy.value = false
  }
}

async function refreshOne(item, e) {
  e?.stopPropagation?.()
  menuId.value = ''
  if (!item || item.source === 'file' || !item.url) {
    window.$toast?.('本地文件无需更新')
    return
  }
  busy.value = true
  window.$toast?.(`正在更新 ${item.name}…`)
  try {
    const res = await refreshConfig(item.id)
    const fails = res?.errors?.length ? `（${res.errors[0]}）` : ''
    if (res?.ok === false || res?.error) {
      window.$toast?.(res.error || `更新失败${fails}`)
    } else {
      window.$toast?.(`已更新 ${item.name}${fails}`)
    }
    await refresh()
  } catch (err) {
    window.$toast?.(err.message)
  } finally {
    busy.value = false
  }
}

async function saveCfg() {
  if (cfgBusy.value || !cfgDirty.value || !cfgId.value) return
  cfgBusy.value = true
  try {
    const res = await saveConfigRaw(cfgId.value, cfgContent.value)
    if (res.ok === '0') {
      window.$toast?.(res.error || '保存失败')
    } else {
      window.$toast?.('已保存')
    }
    cfgOriginal.value = cfgContent.value
  } catch (e) {
    window.$toast?.(e.message)
  } finally {
    cfgBusy.value = false
  }
}

function resetCfg() {
  cfgContent.value = cfgOriginal.value
}

function sourceLabel(item) {
  return item.source === 'file' ? '本地文件' : '订阅'
}

function timeText(item) {
  if (!item.updatedAt) return ''
  try {
    return new Date(item.updatedAt).toLocaleString()
  } catch {
    return ''
  }
}

onMounted(() => {
  refresh()
  document.addEventListener('pointerdown', onGlobalPointerDown, true)
})
onUnmounted(() => {
  document.removeEventListener('pointerdown', onGlobalPointerDown, true)
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1 class="page-title">配置</h1>
      <div class="page-actions">
        <button class="btn btn-ghost" :disabled="busy || !items.length" @click="doRefresh">更新</button>
        <button class="btn btn-primary" :disabled="busy" @click="openAdd">添加</button>
      </div>
    </div>

    <div v-if="loading" class="card empty">加载中…</div>
    <div v-else-if="!items.length" class="card empty">还没有配置。添加订阅链接或上传 YAML。</div>

    <div v-else class="cfg-list">
      <div
        v-for="item in items"
        :key="item.id"
        class="cfg-card"
        :class="{ active: item.active, open: menuId === item.id }"
        @click="onCardClick(item)"
      >
        <div class="cfg-card-main">
          <div class="cfg-card-top">
            <div class="cfg-card-title">
              <span class="cfg-name">{{ item.name }}</span>
              <span v-if="item.active" class="badge on">当前</span>
            </div>
            <button
              type="button"
              class="cfg-more"
              :disabled="busy"
              aria-label="更多"
              @click="toggleMenu(item.id, $event)"
            >
              ⋯
            </button>
          </div>
          <div class="cfg-card-meta">
            <span class="badge">{{ sourceLabel(item) }}</span>
            <span v-if="timeText(item)" class="cfg-time">{{ timeText(item) }}</span>
          </div>
        </div>

        <div v-if="menuId === item.id" class="cfg-menu" @click.stop>
          <button
            v-if="item.source !== 'file' && item.url"
            type="button"
            class="cfg-menu-item"
            :disabled="busy"
            @click="refreshOne(item, $event)"
          >
            更新
          </button>
          <button type="button" class="cfg-menu-item" @click="openEdit(item, $event)">编辑</button>
          <button type="button" class="cfg-menu-item" @click="openConfig(item, $event)">
            配置
          </button>
          <button type="button" class="cfg-menu-item danger" @click="remove(item.id, $event)">
            删除
          </button>
        </div>
      </div>
    </div>

    <!-- add / edit config meta -->
    <div v-if="showForm" class="modal-mask" @click.self="showForm = false">
      <div class="modal">
        <h3>{{ editing ? '编辑' : '添加配置' }}</h3>

        <div class="field-block">
          <div class="field-label">名称</div>
          <input v-model="form.name" class="field" placeholder="请输入名称" />
        </div>

        <div class="field-block">
          <div class="field-label">来源</div>
          <div class="pill-group" style="width: 100%">
            <button
              class="pill"
              style="flex: 1"
              :class="{ active: form.source === 'url' }"
              type="button"
              @click="setSource('url')"
            >
              订阅 URL
            </button>
            <button
              class="pill"
              style="flex: 1"
              :class="{ active: form.source === 'file' }"
              type="button"
              @click="setSource('file')"
            >
              本地文件
            </button>
          </div>
        </div>

        <template v-if="form.source === 'url'">
          <div class="field-block">
            <div class="field-label">订阅地址</div>
            <textarea v-model="form.url" class="field" rows="3" placeholder="请输入订阅地址" />
          </div>
          <div class="field-block">
            <div class="field-label">更新间隔（秒）</div>
            <input
              v-model="form.interval"
              class="field"
              type="number"
              min="0"
              placeholder="留空不更新"
            />
          </div>
        </template>

        <template v-else>
          <div class="field-block">
            <div class="field-label">上传文件</div>
            <label class="file-pick">
              <input type="file" accept=".yaml,.yml,.txt,text/yaml,text/plain" @change="onFile" />
              <span>{{ form.fileName || (editing ? '选择新文件覆盖…' : '点击选择文件') }}</span>
            </label>
          </div>
        </template>

        <div class="modal-actions">
          <button class="btn btn-ghost" @click="showForm = false">取消</button>
          <button class="btn btn-primary" :disabled="busy" @click="submit">
            保存
          </button>
        </div>
      </div>
    </div>

    <!-- per-config original YAML editor -->
    <div v-if="showConfig" class="modal-mask modal-mask-full" @click.self="showConfig = false">
      <div class="modal modal-editor">
        <div class="modal-editor-head">
          <h3 class="modal-editor-title">编辑配置 · {{ cfgName }}</h3>
          <button
            type="button"
            class="modal-close"
            title="关闭"
            aria-label="关闭"
            @click="showConfig = false"
          >
            ×
          </button>
        </div>
        <div v-if="cfgLoading" class="empty" style="padding: 24px">加载中…</div>
        <textarea
          v-else
          v-model="cfgContent"
          class="config-editor config-editor-full"
          spellcheck="false"
        />
        <div class="modal-actions">
          <button class="btn btn-ghost" :disabled="cfgBusy || !cfgDirty" @click="resetCfg">还原</button>
          <button class="btn btn-primary" :disabled="cfgBusy || cfgLoading || !cfgDirty" @click="saveCfg">
            保存
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
