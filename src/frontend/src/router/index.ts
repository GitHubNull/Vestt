import { createRouter, createWebHashHistory } from 'vue-router'
import { consumePendingIndexersHash } from '../services/searchHash'
import { useAuthStore } from '../stores/auth'

// Hash history: the backend only serves the SPA at {base}/UI/Dashboard
// (upstream parity — Jackett does in-page hash navigation too), so in-app
// routes live in the fragment (#/, #/indexers, #/settings/server, …).
// UI 改版 (deliberate upstream deviation): the search form is the homepage
// and indexer/server management lives on dedicated routes (frontend.md v1.1).
export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', name: 'home', component: () => import('../views/HomeView.vue'), meta: { requiresAuth: true } },
    { path: '/login', name: 'login', component: () => import('../views/LoginView.vue') },
    { path: '/indexers', name: 'indexers', component: () => import('../views/IndexersView.vue'), meta: { requiresAuth: true } },
    { path: '/settings/server', name: 'settings-server', component: () => import('../views/SettingsServerView.vue'), meta: { requiresAuth: true } },
    { path: '/settings/cache', name: 'settings-cache', component: () => import('../views/SettingsCacheView.vue'), meta: { requiresAuth: true } },
    { path: '/settings/logs', name: 'settings-logs', component: () => import('../views/SettingsLogsView.vue'), meta: { requiresAuth: true } },
    // legacy bookmarks from the single-dashboard era
    { path: '/dashboard', redirect: '/' },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

// Session probe (SPEC-11 §3.4): every route except /login requires a
// management session; /login skips straight through when no admin password
// is set.
router.beforeEach(async (to) => {
  // Upstream-style bare `#indexers=1&filter=<json>` deep link: navigate to
  // the indexers route with the filter in the route query (main.ts stashed
  // the fragment before the router could rewrite it).
  const indexersLink = consumePendingIndexersHash()
  if (indexersLink && to.name !== 'indexers') {
    const query: Record<string, string> = { indexers: '1' }
    if (indexersLink.filter) query.filter = indexersLink.filter
    return { name: 'indexers', query }
  }

  const auth = useAuthStore()
  if (to.name === 'login') {
    if (auth.authenticated) return { name: 'home' }
    try {
      if (await auth.probe()) return { name: 'home' }
    } catch {
      // server unreachable — stay on the login page
      return true
    }
    return true
  }
  if (to.meta.requiresAuth) {
    if (auth.authenticated) return true
    try {
      if (await auth.probe()) return true
    } catch {
      return true // network error: let the view show its error state
    }
    return { name: 'login' }
  }
  return true
})
