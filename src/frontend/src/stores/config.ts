import { defineStore } from 'pinia'
import { getServerConfig, lockBasePath, postServerConfig } from '../services/api'
import type { ServerConfig, ServerConfigResponse } from '../services/types'

export const useConfigStore = defineStore('config', {
  state: () => ({
    serverConfig: null as ServerConfig | null,
    loading: false,
    loaded: false,
  }),
  getters: {
    appVersion: (s): string => s.serverConfig?.app_version ?? '',
    apiKey: (s): string => s.serverConfig?.api_key ?? '',
    external: (s): boolean => s.serverConfig?.external ?? false,
    /** true while the server has no admin password configured. */
    passwordUnset: (s): boolean => (s.serverConfig?.password ?? '') === '',
  },
  actions: {
    /** Loads server config once (upstream loadJackettSettings equivalent). */
    async load(force = false): Promise<ServerConfig> {
      if (this.loaded && !force && this.serverConfig) return this.serverConfig
      this.loading = true
      try {
        const cfg = await getServerConfig()
        this.serverConfig = cfg
        this.loaded = true
        lockBasePath(cfg.basepathoverride)
        return cfg
      } finally {
        this.loading = false
      }
    },
    /** Applies a partial config update; resolves restart_required. */
    async save(patch: Partial<ServerConfig>): Promise<ServerConfigResponse> {
      const res = await postServerConfig(patch)
      this.serverConfig = res
      lockBasePath(res.basepathoverride)
      return res
    },
  },
})
