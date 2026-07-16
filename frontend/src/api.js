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
  // don't force JSON content-type for FormData
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
    const msg = data?.error || '需要登录'
    throw new Error(msg)
  }
  if (!res.ok) {
    const msg = data?.error || res.statusText || 'request failed'
    throw new Error(msg)
  }
  return data
}

export const checkAuth = () => api('/api/auth/check')
export const login = (password) =>
  api('/api/login', { method: 'POST', body: { password } })

export const getOverview = () => api('/api/overview')
export const setMode = (mode) => api('/api/mode', { method: 'POST', body: { mode } })
export const setTun = (enable) => api('/api/tun', { method: 'POST', body: { enable } })
export const getProxies = () => api('/api/proxies')
export const selectProxy = (group, name) =>
  api('/api/proxies/select', { method: 'POST', body: { group, name } })
export const testDelay = (name) => api(`/api/proxies/delay?name=${encodeURIComponent(name)}`)
export const testGroupDelay = (group) =>
  api(`/api/group/delay?group=${encodeURIComponent(group)}`)

export const listSubs = () => api('/api/subscriptions')
export const addSub = (body) => api('/api/subscriptions', { method: 'POST', body })
export const updateSub = (id, body) =>
  api(`/api/subscriptions/${id}`, { method: 'PATCH', body })
export const deleteSub = (id) => api(`/api/subscriptions/${id}`, { method: 'DELETE' })
export const activateSub = (id) =>
  api(`/api/subscriptions/${id}/activate`, { method: 'POST' })
export const refreshSubs = () => api('/api/subscriptions/refresh', { method: 'POST' })
export const applySubs = () => api('/api/subscriptions/apply', { method: 'POST' })

export async function uploadSub({ id, name, url, interval, file, content, activate }) {
  const fd = new FormData()
  if (name != null) fd.append('name', name)
  if (url != null) fd.append('url', url)
  if (interval != null) fd.append('interval', String(interval))
  if (file) fd.append('file', file)
  if (content) fd.append('content', content)
  if (activate) fd.append('activate', '1')
  if (file || content) fd.append('source', 'file')
  else if (url) fd.append('source', 'url')
  const path = id ? `/api/subscriptions/${id}/upload` : '/api/subscriptions'
  return api(path, { method: 'POST', body: fd })
}

// Per-subscription original YAML (not the merged kernel config)
export const getSubRaw = (id) => api(`/api/subscriptions/${id}/raw`)
export const saveSubRaw = (id, content) =>
  api(`/api/subscriptions/${id}/raw`, { method: 'PUT', body: { content } })

// Advanced: merged kernel config (debug)
export const getConfig = () => api('/api/config')
export const saveConfig = (content, reload = true) =>
  api('/api/config', { method: 'PUT', body: { content, reload } })

/** Auth headers for raw fetch (logs stream) */
export function authHeaders(extra = {}) {
  const h = { ...extra }
  const token = getToken()
  if (token) h.Authorization = `Bearer ${token}`
  return h
}
