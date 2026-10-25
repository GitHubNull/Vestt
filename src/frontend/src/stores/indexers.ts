import { defineStore } from 'pinia'
import { i18n } from '../i18n'
import { notify } from '../components/common/notify'
import {
  deleteIndexer,
  getIndexerConfig,
  getIndexers,
  postIndexerConfig,
  testIndexer,
} from '../services/api'
import type { ApiError } from '../services/api'
import type { ConfigFieldDef, ConfigItem, IndexerDTO } from '../services/types'

// Indexer management store (SPEC-12 §3.1/3.2/3.5): filter wiring with AND
// semantics, test-state machine (idle/running/ok/fail), delete and batch add.

export type IndexerTypeFilter = 'public' | 'semi-private' | 'private' | 'all'

export interface IndexerFilters {
  /** 'all' = no filtering (PrimeVue Select treats '' as empty/placeholder). */
  type: IndexerTypeFilter
  /** tag value, case-insensitively matched; 'all' = no filtering. */
  tag: string
  /** healthy | failing | untested | 'all' */
  state: string
}

/** Runtime test-button state (SPEC-12 §3.1.6: idle/running/ok/fail). */
export type TestRunState = 'idle' | 'running' | 'ok' | 'fail'

/** Status badge shown in the configured table. */
export type IndexerBadge = 'healthy' | 'failing' | 'untested'

/** Mask sentinels: '********' (SPEC/backend-api.md §2.2 备注) and the
 * upstream PasswordReplacement actually emitted by the backend display
 * form (configdata/items.go PasswordReplacement). */
export const PASSWORD_SENTINELS = ['********', '|||%%PREVJACKPASSWD%%|||']

/** Badge derivation: a stored last_error means failing; a successful test
 * run this session means healthy; anything else is untested. */
export function badgeOf(indexer: IndexerDTO, run: TestRunState | undefined): IndexerBadge {
  if (indexer.last_error) return 'failing'
  if (run === 'ok') return 'healthy'
  return 'untested'
}

export function errMessage(err: unknown): string {
  const e = err as Partial<ApiError> | null
  return e?.message ?? String(err)
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

// --- filter (de)serialization for the route-query deep link -----------------
// SPEC-12 §3.2: "#indexers=1&filter=<json>". The SPA runs on a vue-router
// hash history (SPEC-11 澄清记录), so the dashboard route carries
// ?indexers=1&filter=<json> inside its hash instead of a bare fragment.

export function serializeFilters(f: IndexerFilters): string | null {
  if (f.type === 'all' && f.tag === 'all' && f.state === 'all') return null
  return JSON.stringify({ type: f.type, tag: f.tag, state: f.state })
}

export function parseFilters(raw: unknown): IndexerFilters | null {
  if (typeof raw !== 'string' || raw === '') return null
  try {
    const j = JSON.parse(raw) as Partial<IndexerFilters>
    const f: IndexerFilters = { type: 'all', tag: 'all', state: 'all' }
    if (j.type === 'public' || j.type === 'private' || j.type === 'semi-private') {
      f.type = j.type
    }
    if (typeof j.tag === 'string' && j.tag !== '') f.tag = j.tag
    if (j.state === 'healthy' || j.state === 'failing' || j.state === 'untested') {
      f.state = j.state
    }
    return f
  } catch {
    return null
  }
}

// --- config payload assembly (shared by the dialog and batch add) -----------

/** Builds the POST Config payload from field defs + current values
 * (frontend.md §5): {id,value} / {id,values} / {id,cookie}; masked
 * passwords are skipped; display items and hiddendata pass through per
 * upstream getConfigModalJson (hiddendata keeps its value, display items
 * ride along as bare ids). */
export function buildConfigItems(
  fields: ConfigFieldDef[],
  values: Record<string, unknown>,
): ConfigItem[] {
  const items: ConfigItem[] = []
  for (const f of fields) {
    const v = values[f.id]
    switch (f.type) {
      case 'hiddendata':
        items.push({ id: f.id, value: String(v ?? f.value ?? '') })
        break
      case 'displayinfo':
      case 'displayimage':
        // upstream posts a bare {id}; the backend ignores value-less
        // display items. Skipping entirely is equivalent and safer.
        break
      case 'password':
        if (PASSWORD_SENTINELS.includes(String(v ?? ''))) break
        items.push({ id: f.id, value: String(v ?? '') })
        break
      case 'inputbool':
        items.push({ id: f.id, value: v ? 'true' : 'false' })
        break
      case 'inputcheckbox':
        items.push({ id: f.id, values: Array.isArray(v) ? (v as string[]) : [] })
        break
      case 'inputselect':
        items.push({ id: f.id, value: String(v ?? '') })
        break
      case 'inputtags':
        // Tags field: upstream tagify serializes to the separator-joined
        // lowercase string (originalInputValueFormat).
        items.push({ id: f.id, value: (Array.isArray(v) ? v : []).join(',') })
        break
      case 'inputstring':
        if (f.id === 'cookie') {
          // cookie alternative login channel (backend-api.md §2.3):
          // submitted as {id, cookie}; ApplyItems maps it onto the item value.
          items.push({ id: f.id, cookie: String(v ?? '') })
        } else {
          items.push({ id: f.id, value: String(v ?? '') })
        }
        break
    }
  }
  return items
}

/** Initial form values from field defs (DynamicForm default state). */
export function initialValues(fields: ConfigFieldDef[]): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const f of fields) {
    switch (f.type) {
      case 'inputbool':
        out[f.id] = f.value === true || f.value === 'true'
        break
      case 'inputcheckbox':
        out[f.id] = f.values ? [...f.values] : []
        break
      case 'inputtags':
        out[f.id] = String(f.value ?? '')
          .split(',')
          .map((t) => t.trim())
          .filter(Boolean)
        break
      default:
        out[f.id] = f.value ?? ''
    }
  }
  return out
}

export const useIndexersStore = defineStore('indexers', {
  state: () => ({
    all: [] as IndexerDTO[],
    loading: false,
    loaded: false,
    filters: { type: 'all', tag: 'all', state: 'all' } as IndexerFilters,
    testStates: {} as Record<string, TestRunState>,
  }),
  getters: {
    configured: (s): IndexerDTO[] => s.all.filter((i) => i.configured),
    unconfigured: (s): IndexerDTO[] => s.all.filter((i) => !i.configured),
    /** distinct tags across all indexers, case-insensitively merged
     * (upstream tag_filter semantics). */
    tags: (s): string[] => {
      const seen = new Map<string, string>()
      for (const i of s.all) {
        for (const t of i.tags) {
          const k = t.toLowerCase()
          if (!seen.has(k)) seen.set(k, t)
        }
      }
      return [...seen.values()].sort((a, b) => a.localeCompare(b))
    },
    /** configured list with the three orthogonal (AND) filters applied. */
    filteredConfigured(): IndexerDTO[] {
      return this.configured.filter((i) => {
        if (this.filters.type !== 'all' && i.type !== this.filters.type) return false
        if (
          this.filters.tag !== 'all' &&
          !i.tags.some((t) => t.toLowerCase() === this.filters.tag.toLowerCase())
        ) {
          return false
        }
        if (
          this.filters.state !== 'all' &&
          badgeOf(i, this.testStates[i.id]) !== this.filters.state
        ) {
          return false
        }
        return true
      })
    },
  },
  actions: {
    async load(force = false): Promise<IndexerDTO[]> {
      if (this.loaded && !force) return this.all
      this.loading = true
      try {
        this.all = await getIndexers()
        this.loaded = true
        // drop test-state entries for indexers that no longer exist
        const ids = new Set(this.all.map((i) => i.id))
        for (const id of Object.keys(this.testStates)) {
          if (!ids.has(id)) delete this.testStates[id]
        }
        return this.all
      } finally {
        this.loading = false
      }
    },

    setFilters(patch: Partial<IndexerFilters>): void {
      this.filters = { ...this.filters, ...patch }
    },

    /** Applies a parsed filter deep-link (route query on the dashboard). */
    applyFilterDeepLink(raw: unknown): void {
      const f = parseFilters(raw)
      if (f) this.filters = f
    },

    /** Test one indexer (upstream testIndexer, custom.js:709-729). */
    async test(id: string, opts: { notifyResult?: boolean } = {}): Promise<void> {
      const { notifyResult = true } = opts
      if (this.testStates[id] === 'running') return
      this.testStates[id] = 'running'
      if (notifyResult) notify.info(`Test started for ${id}`)
      try {
        await testIndexer(id)
        this.testStates[id] = 'ok'
        this.patchLastError(id, null)
        if (notifyResult) notify.success(`Test successful for ${id}`)
      } catch (err) {
        const msg = errMessage(err)
        this.testStates[id] = 'fail'
        this.patchLastError(id, msg)
        if (notifyResult) notify.danger(`Test failed for ${id}: \n${msg}`)
      }
    },

    /** Test all configured indexers sequentially, 200 ms apart (SPEC-12
     * §3.1.6 防 UI 卡死; upstream fires them all at once without toasts —
     * see SPEC-12 澄清记录). */
    async testAll(): Promise<void> {
      for (const i of [...this.configured]) {
        await this.test(i.id, { notifyResult: false })
        await sleep(200)
      }
    },

    /** Delete an indexer (upstream prepareDeleteButtons: notify, reload). */
    async remove(id: string): Promise<void> {
      try {
        await deleteIndexer(id)
        notify.success(i18n.global.t('indexersView.deleted', { name: id }))
      } catch (err) {
        notify.danger(i18n.global.t('indexersView.deleteError', { name: id, message: errMessage(err) }))
      } finally {
        await this.load(true)
      }
    },

    /** Batch-add unconfigured indexers with their default (fetched) config
     * (upstream addIndexers/addIndexer, custom.js:512-574). */
    async addIndexers(ids: string[]): Promise<void> {
      notify.info(i18n.global.t('indexersView.addingSelected'), true)
      for (const id of ids) {
        try {
          const fields = await getIndexerConfig(id)
          await postIndexerConfig(id, buildConfigItems(fields, initialValues(fields)))
          notify.success(i18n.global.t('configDialog.configured', { name: id }))
        } catch (err) {
          notify.danger(i18n.global.t('configDialog.configFailed', { message: errMessage(err) }))
        }
      }
      notify.success(i18n.global.t('indexersView.batchAdded'))
      await this.load(true)
    },

    patchLastError(id: string, message: string | null): void {
      const row = this.all.find((i) => i.id === id)
      if (row) row.last_error = message
    },
  },
})
