import { defineStore } from 'pinia'
import { setAdminPassword } from '../services/api'
import { useConfigStore } from './config'
import type { ServerConfig } from '../services/types'

// SPEC-13 §3.3: settings center store. The form edits a draft copy;
// save diffs it against the loaded config (PATCH semantics per SPEC §3.3.1)
// and posts only the changed fields. 400 responses map to the section that
// owns the offending field (SPEC §3.3.2).

export type SettingsSection =
  | 'server'
  | 'cache'
  | 'proxy'
  | 'flaresolverr'
  | 'omdb'
  | 'update'

export const NUMBER_FIELDS = [
  'port',
  'cache_ttl',
  'cache_max_results_per_indexer',
  'flaresolverr_maxtimeout',
  'proxy_port',
] as const

export const BOOLEAN_FIELDS = [
  'external',
  'cors',
  'updatedisabled',
  'prerelease',
  'logging',
  'cache_enabled',
] as const

/** Section ownership of config fields (error mapping + touched-section set). */
export const SECTION_FIELDS: Record<SettingsSection, string[]> = {
  server: ['port', 'local_bind_address', 'external', 'cors', 'basepathoverride', 'baseurloverride', 'blackholedir'],
  cache: ['cache_enabled', 'cache_ttl', 'cache_max_results_per_indexer'],
  proxy: ['proxy_type', 'proxy_url', 'proxy_port', 'proxy_username', 'proxy_password'],
  flaresolverr: ['flaresolverrurl', 'flaresolverr_maxtimeout'],
  omdb: ['omdbkey', 'omdburl'],
  update: ['updatedisabled', 'prerelease'],
}

/** Keywords (lowercase) that tie a server error text to a section. */
export const SECTION_ERROR_KEYWORDS: Record<SettingsSection, string[]> = {
  server: ['port', 'bind', 'basepath', 'baseurl', 'blackhole', 'external', 'cors', 'address'],
  cache: ['cache'],
  proxy: ['proxy'],
  flaresolverr: ['flaresolverr'],
  omdb: ['omdb'],
  update: ['update', 'prerelease'],
}

/** Deep copy of the loaded config as the editable draft. JSON round-trip:
 *  the config crossed the wire as JSON, and structuredClone cannot handle
 *  the Pinia reactive proxy. */
export function draftFromConfig(cfg: ServerConfig): ServerConfig {
  return JSON.parse(JSON.stringify(cfg)) as ServerConfig
}

function normalizeNumber(value: unknown): number | null {
  if (value === null || value === undefined || value === '') return null
  const n = Number(value)
  return isNaN(n) ? null : n
}

/** Fields that differ between base and draft, normalized to the base types.
 *  Numbers parse from form strings ('' → null for nullable proxy_port);
 *  booleans coerce so PrimeVue toggle/checkbox state round-trips. */
export function diffConfig(base: ServerConfig, draft: ServerConfig): Partial<ServerConfig> {
  const out: Partial<ServerConfig> = {}
  for (const key of Object.keys(base) as (keyof ServerConfig)[]) {
    if (key === 'api_key' || key === 'app_version' || key === 'can_run_netcore' || key === 'notices' || key === 'password') continue
    let d = draft[key]
    const b = base[key]
    if ((NUMBER_FIELDS as readonly string[]).includes(key)) {
      const n = normalizeNumber(d)
      if (key === 'proxy_port') {
        if (n !== (b as number | null)) out[key] = n as never
      } else if (n !== null && n !== b) {
        out[key] = n as never
      }
      continue
    }
    if ((BOOLEAN_FIELDS as readonly string[]).includes(key)) {
      d = d === true || d === 'true' || d === 'on'
      if (d !== b) out[key] = d as never
      continue
    }
    if (String(d ?? '') !== String(b ?? '')) out[key] = d as never
  }
  return out
}

/** Sections touched by a config diff. */
export function sectionsOfDiff(diff: Partial<ServerConfig>): SettingsSection[] {
  const keys = new Set(Object.keys(diff))
  const touched: SettingsSection[] = []
  for (const [section, fields] of Object.entries(SECTION_FIELDS) as [SettingsSection, string[]][]) {
    if (fields.some((f) => keys.has(f))) touched.push(section)
  }
  return touched
}

/** Maps a 400 error text to one of the touched sections; falls back to all
 *  touched sections when no keyword matches (SPEC §3.3.2). */
export function sectionForError(
  message: string,
  touched: SettingsSection[],
): SettingsSection[] {
  const lower = message.toLowerCase()
  for (const section of touched) {
    if (SECTION_ERROR_KEYWORDS[section].some((k) => lower.includes(k))) return [section]
  }
  return touched
}

/** SPEC §3.3: proxyWarning shows when the proxy type is not disabled and no
 *  proxy URL is set (upstream shows it for any enabled type — see the
 *  SPEC-13 澄清记录). */
export function isProxyWarningVisible(
  draft: Pick<ServerConfig, 'proxy_type' | 'proxy_url'>,
): boolean {
  return (
    draft.proxy_type !== null &&
    String(draft.proxy_type).trim() !== '-1' &&
    (draft.proxy_url ?? '').trim() === ''
  )
}

export const useSettingsStore = defineStore('settings', {
  state: () => ({
    draft: null as ServerConfig | null,
    saving: false,
    sectionErrors: {} as Partial<Record<SettingsSection, string>>,
    restartRequired: false,
  }),
  actions: {
    /** Initializes the draft from the config store (upstream loads the modal
     *  fields from loadJackettSettings, custom.js:93-163). */
    open(): void {
      const config = useConfigStore()
      this.draft = config.serverConfig ? draftFromConfig(config.serverConfig) : null
      this.sectionErrors = {}
      this.restartRequired = false
    },
    clearSectionError(section: SettingsSection): void {
      if (this.sectionErrors[section]) {
        const next = { ...this.sectionErrors }
        delete next[section]
        this.sectionErrors = next
      }
    },
    /** PATCH save: diff → POST; 400 lands in the owning section's Alert. */
    async save(): Promise<boolean> {
      const config = useConfigStore()
      if (!this.draft || !config.serverConfig) return false
      const diff = diffConfig(config.serverConfig, this.draft)
      this.sectionErrors = {}
      if (Object.keys(diff).length === 0) return true
      this.saving = true
      try {
        const res = await config.save(diff)
        if (res.restart_required) this.restartRequired = true
        return true
      } catch (err) {
        const e = err as { status?: number; message?: string }
        const text = e.message ?? String(err)
        const touched = sectionsOfDiff(diff)
        if (e.status === 400) {
          for (const section of sectionForError(text, touched)) {
            this.sectionErrors[section] = text
          }
        } else {
          for (const section of touched) {
            this.sectionErrors[section] = text
          }
        }
        return false
      } finally {
        this.saving = false
      }
    },
    /** Admin password change (upstream #change-jackett-password,
     *  custom.js:1705-1722): the backend invalidates sessions; the page
     *  reload forces the re-login flow. */
    async changePassword(password: string): Promise<void> {
      await setAdminPassword(JSON.stringify(password))
    },
  },
})
