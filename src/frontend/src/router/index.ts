import { createRouter, createWebHashHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'

// Hash history: the backend only serves the SPA at {base}/UI/Dashboard
// (upstream parity — Jackett does in-page hash navigation too), so in-app
// routes live in the fragment (#/login, #/dashboard).
export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/dashboard' },
    { path: '/login', name: 'login', component: () => import('../views/LoginView.vue') },
    { path: '/dashboard', name: 'dashboard', component: () => import('../views/DashboardView.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/dashboard' },
  ],
})

// Session probe (SPEC-11 §3.4): dashboard requires a management session;
// /login skips straight through when no admin password is set.
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (to.name === 'login') {
    if (auth.authenticated) return { name: 'dashboard' }
    try {
      if (await auth.probe()) return { name: 'dashboard' }
    } catch {
      // server unreachable — stay on the login page
      return true
    }
    return true
  }
  if (to.name === 'dashboard') {
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
