import { createRouter, createWebHistory } from 'vue-router'
import Home from './views/Home.vue'
import Proxies from './views/Proxies.vue'
import Subs from './views/Subs.vue'
import Logs from './views/Logs.vue'
import Login from './views/Login.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: Login, meta: { title: '登录' } },
    { path: '/', name: 'home', component: Home, meta: { title: '首页' } },
    { path: '/proxies', name: 'proxies', component: Proxies, meta: { title: '节点' } },
    { path: '/subs', name: 'subs', component: Subs, meta: { title: '配置' } },
    { path: '/logs', name: 'logs', component: Logs, meta: { title: '日志' } },
    { path: '/config', redirect: '/subs' },
  ],
})
