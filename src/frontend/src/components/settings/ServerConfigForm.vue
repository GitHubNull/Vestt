<script setup lang="ts">
import Button from 'primevue/button'
import Checkbox from 'primevue/checkbox'
import InputNumber from 'primevue/inputnumber'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Password from 'primevue/password'
import Select from 'primevue/select'
import { computed, ref } from 'vue'
import { notify } from '../common/notify'
import { checkForUpdates } from '../../services/api'
import { errMessage } from '../../stores/indexers'
import { isProxyWarningVisible, useSettingsStore, type SettingsSection } from '../../stores/settings'
import type { ServerConfig } from '../../services/types'

// Server configuration form (SPEC-13 §3.3, upstream "Jackett Configuration"
// index.html:117-243). Sections and field labels follow the upstream modal;
// saving diffs the draft (PATCH semantics per SPEC §3.3.1) and a 400 maps
// to the owning section's Alert (SPEC §3.3.2).

const settings = useSettingsStore()

const newPassword = ref('')
const confirmPassword = ref('')
const passwordError = ref<string | null>(null)
const changingPassword = ref(false)
const checkingUpdates = ref(false)

/** Never-null binding target: the form renders only once the config store
 *  has loaded (v-if below), but arrow callbacks in template bindings don't
 *  inherit v-if narrowing, so the computed falls back to a blank config. */
const blankDraft: ServerConfig = {
  notices: [],
  port: 9117,
  external: false,
  local_bind_address: '127.0.0.1',
  cors: false,
  api_key: '',
  blackholedir: '',
  updatedisabled: false,
  prerelease: false,
  password: '',
  logging: false,
  basepathoverride: '',
  baseurloverride: '',
  cache_enabled: true,
  cache_ttl: 2100,
  cache_max_results_per_indexer: 1000,
  flaresolverrurl: '',
  flaresolverr_maxtimeout: 60000,
  omdbkey: '',
  omdburl: '',
  app_version: '',
  can_run_netcore: true,
  proxy_type: '-1',
  proxy_url: '',
  proxy_port: null,
  proxy_username: '',
  proxy_password: '',
}

const draft = computed<ServerConfig>(() => settings.draft ?? blankDraft)

const proxyTypeOptions = [
  { label: 'Disabled', value: '-1' },
  { label: 'HTTP', value: '0' },
  { label: 'SOCKS4', value: '1' },
  { label: 'SOCKS5', value: '2' },
]

function sectionError(section: SettingsSection): string | null {
  return settings.sectionErrors[section] ?? null
}

async function save(): Promise<void> {
  const ok = await settings.save()
  if (ok) {
    notify.success('Server settings applied')
  }
}

async function changePassword(): Promise<void> {
  passwordError.value = null
  if (newPassword.value === '') {
    passwordError.value = 'Enter a password first'
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    passwordError.value = 'Passwords do not match'
    return
  }
  changingPassword.value = true
  try {
    await settings.changePassword(newPassword.value)
    notify.success('Admin password has been set.')
    newPassword.value = ''
    confirmPassword.value = ''
    // The backend invalidates sessions; upstream reloads the page after 1 s
    // (custom.js:1712-1714). The reload re-runs the auth gate → re-login.
    window.setTimeout(() => window.location.reload(), 1000)
  } catch (err) {
    passwordError.value = errMessage(err)
  } finally {
    changingPassword.value = false
  }
}

async function checkUpdates(): Promise<void> {
  checkingUpdates.value = true
  try {
    await checkForUpdates()
    notify.success('Updater triggered see log for details..')
  } catch (err) {
    notify.danger(`Error: ${errMessage(err)}`)
  } finally {
    checkingUpdates.value = false
  }
}

type NumberKey =
  | 'port'
  | 'cache_ttl'
  | 'cache_max_results_per_indexer'
  | 'flaresolverr_maxtimeout'
  | 'proxy_port'

function setNumber(key: NumberKey, value: number | null | undefined): void {
  if (!settings.draft) return
  if (key === 'proxy_port') {
    settings.draft.proxy_port = value ?? null
  } else {
    settings.draft[key] = value ?? 0
  }
}
</script>

<template>
  <div v-if="settings.draft" class="server-config">
    <Message v-if="settings.restartRequired" severity="warn" :closable="false" class="restart-banner">
      Configuration saved. A server restart is required for the changes to take effect.
    </Message>

    <fieldset class="section">
      <legend>Server base</legend>
      <Message v-if="sectionError('server')" severity="error" :closable="false" class="section-alert">
        {{ sectionError('server') }}
      </Message>

      <div class="field">
        <label for="jackett-port">Server port:</label>
        <InputNumber id="jackett-port" :model-value="draft.port" :use-grouping="false" class="field-input" @update:model-value="(v) => setNumber('port', v as number | null)" />
      </div>
      <div class="field">
        <label for="jackett-local-bind-address">Local bind address:</label>
        <InputText id="jackett-local-bind-address" v-model="draft.local_bind_address" placeholder="127.0.0.1" class="field-input" />
      </div>
      <div class="field">
        <label for="jackett-allowext">External access:</label>
        <Checkbox id="jackett-allowext" v-model="draft.external" binary />
      </div>
      <div class="field">
        <label for="jackett-allowcors">Allow CORS:</label>
        <Checkbox id="jackett-allowcors" v-model="draft.cors" binary />
      </div>
      <div class="field">
        <label for="jackett-basepathoverride">Base path override:</label>
        <InputText id="jackett-basepathoverride" v-model="draft.basepathoverride" placeholder="/jackett" class="field-input" />
      </div>
      <div class="field">
        <label for="jackett-baseurloverride">Base URL override:</label>
        <InputText id="jackett-baseurloverride" v-model="draft.baseurloverride" placeholder="http://jackett:9117" class="field-input" />
      </div>
      <div class="field">
        <label for="jackett-savedir">Blackhole directory:</label>
        <InputText id="jackett-savedir" v-model="draft.blackholedir" placeholder="c:\torrents\" class="field-input" />
      </div>

      <div class="field">
        <label for="jackett-adminpwd">Admin password:</label>
        <Password
          id="jackett-adminpwd"
          v-model="newPassword"
          placeholder="Blank to disable"
          :feedback="false"
          toggle-mask
          class="field-input"
          input-class="w-full"
        />
        <Password
          v-model="confirmPassword"
          placeholder="Confirm password"
          :feedback="false"
          toggle-mask
          class="field-input"
          input-class="w-full"
        />
        <Button
          label="Set Password"
          icon="pi pi-user"
          size="small"
          :loading="changingPassword"
          @click="changePassword"
        />
      </div>
      <Message v-if="passwordError" severity="error" :closable="false" class="section-alert">{{ passwordError }}</Message>
    </fieldset>

    <fieldset class="section">
      <legend>Cache</legend>
      <Message v-if="sectionError('cache')" severity="error" :closable="false" class="section-alert">
        {{ sectionError('cache') }}
      </Message>
      <div class="field">
        <label for="jackett-cache-enabled">Cache enabled (recommended):</label>
        <Checkbox id="jackett-cache-enabled" v-model="draft.cache_enabled" binary />
      </div>
      <div class="field">
        <label for="jackett-cache-ttl">Cache TTL (seconds):</label>
        <InputNumber id="jackett-cache-ttl" :model-value="draft.cache_ttl" :use-grouping="false" class="field-input" @update:model-value="(v) => setNumber('cache_ttl', v as number | null)" />
      </div>
      <div class="field">
        <label for="jackett-cache-max-results-per-indexer">Cache max results per indexer:</label>
        <InputNumber id="jackett-cache-max-results-per-indexer" :model-value="draft.cache_max_results_per_indexer" :use-grouping="false" class="field-input" @update:model-value="(v) => setNumber('cache_max_results_per_indexer', v as number | null)" />
      </div>
    </fieldset>

    <fieldset class="section">
      <legend>Proxy</legend>
      <Message v-if="sectionError('proxy')" severity="error" :closable="false" class="section-alert">
        {{ sectionError('proxy') }}
      </Message>
      <div class="field">
        <label for="jackett-proxy-type">Proxy type:</label>
        <Select id="jackett-proxy-type" v-model="draft.proxy_type" :options="proxyTypeOptions" option-label="label" option-value="value" class="field-input" />
      </div>
      <div v-if="isProxyWarningVisible(draft)" class="proxy-warning" role="alert">
        <span>WARNING: The proxy option potentially leaks requests. Recommendation is to use a VPN.</span>
      </div>
      <div class="field">
        <label for="jackett-proxy-url">Proxy URL:</label>
        <InputText id="jackett-proxy-url" v-model="draft.proxy_url" class="field-input" />
      </div>
      <div class="field">
        <label for="jackett-proxy-port">Proxy port:</label>
        <InputNumber id="jackett-proxy-port" :model-value="draft.proxy_port" :use-grouping="false" class="field-input" @update:model-value="(v) => setNumber('proxy_port', v as number | null)" />
      </div>
      <div class="field">
        <label for="jackett-proxy-username">Proxy username:</label>
        <InputText id="jackett-proxy-username" v-model="draft.proxy_username" class="field-input" />
      </div>
      <div class="field">
        <label for="jackett-proxy-password">Proxy password:</label>
        <Password id="jackett-proxy-password" v-model="draft.proxy_password" :feedback="false" toggle-mask class="field-input" input-class="w-full" />
      </div>
    </fieldset>

    <fieldset class="section">
      <legend>FlareSolverr</legend>
      <Message v-if="sectionError('flaresolverr')" severity="error" :closable="false" class="section-alert">
        {{ sectionError('flaresolverr') }}
      </Message>
      <div class="field">
        <label for="jackett-flaresolverrurl">FlareSolverr API URL:</label>
        <InputText id="jackett-flaresolverrurl" v-model="draft.flaresolverrurl" placeholder="Blank for default" class="field-input" />
      </div>
      <div class="field">
        <label for="jackett-flaresolverr-maxtimeout">FlareSolverr Max Timeout (ms):</label>
        <InputNumber id="jackett-flaresolverr-maxtimeout" :model-value="draft.flaresolverr_maxtimeout" :use-grouping="false" class="field-input" @update:model-value="(v) => setNumber('flaresolverr_maxtimeout', v as number | null)" />
      </div>
    </fieldset>

    <fieldset class="section">
      <legend>OMDb</legend>
      <Message v-if="sectionError('omdb')" severity="error" :closable="false" class="section-alert">
        {{ sectionError('omdb') }}
      </Message>
      <div class="field">
        <label for="jackett-omdbkey">OMDB API key:</label>
        <InputText id="jackett-omdbkey" v-model="draft.omdbkey" class="field-input" />
      </div>
      <div class="field">
        <label for="jackett-omdburl">OMDB API Url:</label>
        <InputText id="jackett-omdburl" v-model="draft.omdburl" placeholder="Blank for default" class="field-input" />
      </div>
    </fieldset>

    <fieldset class="section">
      <legend>Update</legend>
      <Message v-if="sectionError('update')" severity="error" :closable="false" class="section-alert">
        {{ sectionError('update') }}
      </Message>
      <div class="field">
        <label for="jackett-allowupdate">Disable auto update:</label>
        <Checkbox id="jackett-allowupdate" v-model="draft.updatedisabled" binary />
      </div>
      <div class="field">
        <label for="jackett-prerelease">Update to pre-release:</label>
        <Checkbox id="jackett-prerelease" v-model="draft.prerelease" binary />
      </div>
      <div class="field">
        <span />
        <Button
          label="Check for updates"
          icon="pi pi-refresh"
          severity="warning"
          size="small"
          :loading="checkingUpdates"
          @click="checkUpdates"
        />
      </div>
    </fieldset>

    <div class="form-actions">
      <Button
        label="Apply server settings"
        icon="pi pi-wrench"
        :loading="settings.saving"
        @click="save"
      />
    </div>
  </div>
  <p v-else class="config-loading">Loading server configuration…</p>
</template>

<style scoped>
.server-config {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.restart-banner {
  font-weight: 600;
}
.section {
  border: 1px solid var(--p-surface-200);
  border-radius: 6px;
  padding: 0.75rem 1rem 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.section legend {
  font-weight: 700;
  padding: 0 0.35rem;
}
.section-alert {
  font-size: 0.85rem;
}
.field {
  display: grid;
  grid-template-columns: 16rem 1fr 1fr max-content;
  gap: 0.5rem 0.75rem;
  align-items: center;
}
.field label {
  font-weight: 600;
  text-align: right;
}
.field-input {
  max-width: 24rem;
  width: 100%;
}
.proxy-warning {
  color: var(--p-red-500);
  font-weight: 600;
}
.form-actions {
  display: flex;
  justify-content: flex-end;
}
.config-loading {
  color: var(--p-text-muted-color);
}
</style>
