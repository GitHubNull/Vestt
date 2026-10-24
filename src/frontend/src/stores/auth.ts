import { defineStore } from 'pinia'
import { getServerConfig, loginForm, logout } from '../services/api'
import type { ApiError } from '../services/api'

export const PASSWORD_MASK = '********'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    /** admin password configured on the server (password field is masked). */
    passwordSet: false,
    /** management session valid (or no password required). */
    authenticated: false,
    /** probe() has completed at least once. */
    resolved: false,
    probing: false,
  }),
  actions: {
    /** Probes the management session via GET server/config (SPEC-11 §3.4). */
    async probe(): Promise<boolean> {
      if (this.probing) return this.authenticated
      this.probing = true
      try {
        const cfg = await getServerConfig()
        this.passwordSet = cfg.password === PASSWORD_MASK
        this.authenticated = true
        this.resolved = true
        return true
      } catch (err) {
        if (err instanceof Object && (err as ApiError).status === 401) {
          this.passwordSet = true
          this.authenticated = false
          this.resolved = true
          return false
        }
        throw err
      } finally {
        this.probing = false
      }
    },
    /** Submits the admin password form; true when the session was established. */
    async login(password: string): Promise<boolean> {
      const ok = await loginForm(password)
      this.authenticated = ok
      this.passwordSet = true
      this.resolved = true
      return ok
    },
    /** Clears the server session and local state. */
    async logout(): Promise<void> {
      try {
        await logout()
      } finally {
        this.authenticated = false
        this.resolved = true
      }
    },
  },
})
