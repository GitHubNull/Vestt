<script setup lang="ts">
import Alert from 'primevue/message'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import ProgressSpinner from 'primevue/progressspinner'
import { computed, nextTick, ref, watch } from 'vue'
import { notify } from '../common/notify'
import { errMessage } from '../../stores/indexers'
import { getIndexerConfig, postIndexerConfig } from '../../services/api'
import type { ApiError } from '../../services/api'
import type { ConfigFieldDef, IndexerDTO } from '../../services/types'
import DynamicForm from './DynamicForm.vue'

// IndexerConfigDialog (SPEC-12 §3.4): opens with a GET Config fetch,
// renders the dynamic form, owns submit + error handling, and refreshes
// the captcha image after failures.

const props = defineProps<{
  indexer: IndexerDTO | null
}>()
const visible = defineModel<boolean>({ required: true })
const emit = defineEmits<{ saved: [indexerId: string, wasConfigured: boolean] }>()

const fields = ref<ConfigFieldDef[]>([])
const loading = ref(false)
const submitting = ref(false)
const errorText = ref('')
const formRef = ref<InstanceType<typeof DynamicForm> | null>(null)
const capsOpen = ref(false)

const hasCaptcha = computed(() => fields.value.some((f) => f.type === 'displayimage'))
const hasAltLinks = computed(
  () => (props.indexer?.alternativesitelinks?.length ?? 0) >= 1,
)

async function fetchConfig(): Promise<void> {
  if (!props.indexer) return
  loading.value = true
  errorText.value = ''
  try {
    fields.value = await getIndexerConfig(props.indexer.id)
  } catch (err) {
    errorText.value = errMessage(err)
    notify.danger('Request to Jackett server failed')
  } finally {
    loading.value = false
  }
}

watch(
  [visible, () => props.indexer],
  ([open]) => {
    if (open && props.indexer) {
      capsOpen.value = false
      void fetchConfig()
    }
  },
  { immediate: true },
)

function pickSiteLink(link: string): void {
  formRef.value?.setValue('sitelink', link)
}

async function submit(): Promise<void> {
  if (!props.indexer || !formRef.value) return
  submitting.value = true
  errorText.value = ''
  try {
    await postIndexerConfig(props.indexer.id, formRef.value.buildItems())
    notify.success(`Successfully configured ${props.indexer.name}`)
    visible.value = false
    emit('saved', props.indexer.id, props.indexer.configured)
  } catch (err) {
    const e = err as Partial<ApiError>
    errorText.value = errMessage(err)
    if (hasCaptcha.value) {
      // captcha answers are single-use: fetch a fresh image, but keep the
      // user's input (SPEC-12 §3.3.6: 400 → form is not reset).
      const snapshot = { ...formRef.value.values }
      await fetchConfig()
      await nextTick()
      for (const [key, value] of Object.entries(snapshot)) {
        const f = fields.value.find((x) => x.id === key)
        if (f && f.type !== 'displayimage') formRef.value?.setValue(key, value)
      }
    }
    if (e.status !== 400) {
      notify.danger(`Configuration failed: ${errorText.value}`)
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Dialog
    v-model:visible="visible"
    modal
    :header="indexer ? indexer.name : ''"
    class="config-dialog"
    :dismissable-mask="true"
  >
    <div v-if="indexer" class="dialog-body">
      <p class="site-line">
        <a :href="indexer.site_link" target="_blank" rel="noreferrer">{{ indexer.site_link }}</a>
      </p>
      <Alert v-if="indexer.description" severity="info" :closable="false" class="desc-alert">
        {{ indexer.description }}
      </Alert>

      <p class="meta-line">
        <span class="meta-item">Type: {{ indexer.type }}</span>
        <span class="meta-item">Language: {{ indexer.language }}</span>
        <button type="button" class="caps-toggle" @click="capsOpen = !capsOpen">
          {{ capsOpen ? 'Hide capabilities' : `Capabilities (${indexer.caps.length})` }}
        </button>
      </p>

      <div v-if="capsOpen" class="caps-list">
        <table>
          <thead>
            <tr><th>Category</th><th>Description</th></tr>
          </thead>
          <tbody>
            <tr v-for="cap in indexer.caps" :key="cap.ID">
              <td>{{ cap.ID }}</td>
              <td>{{ cap.Name }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="hasAltLinks" class="alt-links alert-box">
        <p>This indexer has multiple known URLs:</p>
        <ul>
          <li v-for="link in indexer.alternativesitelinks" :key="link">
            <a href="#" @click.prevent="pickSiteLink(link)">{{ link }}</a>
          </li>
        </ul>
        <p>Click on a URL to load it into the Site Link field.</p>
      </div>

      <ProgressSpinner v-if="loading" class="loader" />
      <template v-else>
        <Alert v-if="errorText" severity="error" :closable="false" class="error-alert">
          {{ errorText }}
        </Alert>
        <DynamicForm ref="formRef" :fields="fields" />
      </template>
    </div>

    <template #footer>
      <Button label="Close" severity="secondary" @click="visible = false" />
      <Button
        v-if="hasCaptcha && !loading"
        label="Refresh captcha"
        severity="secondary"
        :disabled="submitting"
        @click="fetchConfig"
      />
      <Button
        label="Okay"
        :loading="submitting"
        :disabled="loading"
        @click="submit"
      />
    </template>
  </Dialog>
</template>

<style scoped>
.config-dialog {
  width: min(42rem, 95vw);
}
.dialog-body {
  display: flex;
  flex-direction: column;
}
.site-line {
  margin: 0 0 0.5rem;
  word-break: break-all;
}
.desc-alert,
.error-alert {
  margin-bottom: 0.75rem;
}
.meta-line {
  display: flex;
  gap: 1rem;
  align-items: center;
  margin: 0 0 0.75rem;
  font-size: 0.85rem;
  color: var(--p-text-muted-color);
}
.caps-toggle {
  background: none;
  border: none;
  color: var(--p-primary-color);
  cursor: pointer;
  padding: 0;
  font-size: inherit;
}
.caps-list {
  max-height: 12rem;
  overflow-y: auto;
  margin-bottom: 0.75rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--p-content-border-radius);
}
.caps-list table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.85rem;
}
.caps-list th,
.caps-list td {
  text-align: left;
  padding: 0.25rem 0.5rem;
  border-bottom: 1px solid var(--p-content-border-color);
}
.alt-links {
  margin-bottom: 0.75rem;
  font-size: 0.9rem;
}
.alt-links ul {
  margin: 0.25rem 0;
  padding-left: 1.25rem;
}
.alert-box {
  background: var(--p-blue-50);
  border: 1px solid var(--p-blue-200);
  border-radius: var(--p-content-border-radius);
  padding: 0.75rem;
}
.loader {
  align-self: center;
  margin: 2rem 0;
}
</style>
