import { createRouter, createWebHistory } from 'vue-router'
import Home from './views/Home.vue'
import Proxies from './views/Proxies.vue'
import Configs from './views/Configs.vue'
import Connections from './views/Connections.vue'
import Logs from './views/Logs.vue'
import Login from './views/Login.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: Login, meta: { title: '登录' } },
    { path: '/', name: 'home', component: Home, meta: { title: '首页' } },
    { path: '/proxies', name: 'proxies', component: Proxies, meta: { title: '节点' } },
    { path: '/configs', name: 'configs', component: Configs, meta: { title: '配置' } },
    { path: '/connections', name: 'connections', component: Connections, meta: { title: '连接' } },
    { path: '/logs', name: 'logs', component: Logs, meta: { title: '日志' } },
    { path: '/subs', redirect: '/configs' },
    { path: '/config', redirect: '/configs' },
  ],
})
