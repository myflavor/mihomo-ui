export function getToken() {
  try {
    return localStorage.getItem('ui_token') || ''
  } catch {
    return ''
  }
}

export function setToken(token) {
  try {
    if (token) localStorage.setItem('ui_token', token)
    else localStorage.removeItem('ui_token')
  } catch {
    // ignore
  }
}

export async function api(path, options = {}) {
  const opts = {
    headers: {
      ...(options.headers || {}),
    },
    ...options,
  }
  const token = getToken()
  if (token && !opts.headers.Authorization) {
    opts.headers.Authorization = `Bearer ${token}`
  }
  if (opts.body && typeof opts.body === 'object' && !(opts.body instanceof FormData)) {
    opts.headers['Content-Type'] = opts.headers['Content-Type'] || 'application/json'
    opts.body = JSON.stringify(opts.body)
  }
  const res = await fetch(path, opts)
  const text = await res.text()
  let data = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = { raw: text }
  }
  if (res.status === 401) {
    setToken('')
    if (typeof window !== 'undefined') {
      window.dispatchEvent(new CustomEvent('ui-auth-required'))
    }
    throw new Error(data?.error || '需要登录')
  }
  if (!res.ok) {
    throw new Error(data?.error || res.statusText || 'request failed')
  }
  return data
}

export const checkAuth = () => api('/api/auth/check')
export const login = (password) =>
  api('/api/login', { method: 'POST', body: { password } })

export const getOverview = () => api('/api/overview')
export const setMode = (mode) => api('/api/mode', { method: 'POST', body: { mode } })
export const setTun = (enable) => api('/api/tun', { method: 'POST', body: { enable } })
export const setLogLevel = (level) => api('/api/log-level', { method: 'POST', body: { level } })
export const getProxies = () => api('/api/proxies')
export const selectProxy = (group, name) =>
  api('/api/proxies/select', { method: 'POST', body: { group, name } })
export const testDelay = (name) => api(`/api/proxies/delay?name=${encodeURIComponent(name)}`)
export const testGroupDelay = (group) =>
  api(`/api/group/delay?group=${encodeURIComponent(group)}`)

export const listConfigs = () => api('/api/config/list')
export const addConfig = (body) => api('/api/config', { method: 'POST', body })
export const updateConfig = (id, body) =>
  api(`/api/config/${id}`, { method: 'PUT', body })
export const deleteConfig = (id) => api(`/api/config/${id}`, { method: 'DELETE' })
export const activateConfig = (id) =>
  api(`/api/config/${id}/activate`, { method: 'POST' })
export const refreshConfig = (id) =>
  api(`/api/config/${id}/refresh`, { method: 'POST' })
export const refreshConfigs = () => api('/api/config/refresh', { method: 'POST' })
export const applyConfigs = () => api('/api/config/apply', { method: 'POST' })

export async function uploadConfig({ id, name, url, interval, file, content, activate }) {
  const fd = new FormData()
  if (name != null) fd.append('name', name)
  if (url != null) fd.append('url', url)
  if (interval != null) fd.append('interval', String(interval))
  if (file) fd.append('file', file)
  if (content) fd.append('content', content)
  if (activate) fd.append('activate', '1')
  if (file || content) fd.append('source', 'file')
  else if (url) fd.append('source', 'url')
  const path = id ? `/api/config/${id}/upload` : '/api/config'
  return api(path, { method: 'POST', body: fd })
}

export const getConfigRaw = (id) => api(`/api/config/${id}/raw`)
export const saveConfigRaw = (id, content) =>
  api(`/api/config/${id}/raw`, { method: 'PUT', body: { content } })

export const getRuntime = () => api('/api/runtime')
export const saveRuntime = (content, reload = true) =>
  api('/api/runtime', { method: 'PUT', body: { content, reload } })

export const getConnections = () => api('/api/connections')
export const closeAllConnections = () => api('/api/connections', { method: 'DELETE' })
export const closeConnection = (id) =>
  api(`/api/connections?id=${encodeURIComponent(id)}`, { method: 'DELETE' })

export function authHeaders(extra = {}) {
  const h = { ...extra }
  const token = getToken()
  if (token) h.Authorization = `Bearer ${token}`
  return h
}
