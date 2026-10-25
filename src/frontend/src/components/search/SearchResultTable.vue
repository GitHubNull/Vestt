<script setup lang="ts">
import Button from 'primevue/button'
import Checkbox from 'primevue/checkbox'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { notify } from '../common/notify'
import { absoluteDate, jacketSize, jacketTimespan } from '../../services/format'
import { getBasePath } from '../../services/api'
import type { TrackerCacheResult } from '../../services/types'

// Result table (SPEC-13 §3.1.4) shared by the search home and the cached
// releases view (§3.2, FirstSeen column). Column set and inline labels port
// upstream updateSearchResultTable / updateReleasesRow (custom.js:1307-1457,
// :969-1045); the dead-torrent filter matches upstream
// showSearch's checkbox (custom.js:1300-1304).

const { t } = useI18n()
const props = defineProps<{
  results: TrackerCacheResult[]
  loading?: boolean
  showFirstSeen?: boolean
}>()

const showDead = ref(true)
const expanded = ref<Record<string, boolean>>({})

const rows = computed(() =>
  showDead.value ? props.results : props.results.filter((r) => (r.Seeders ?? 0) !== 0),
)

const heading = computed(() =>
  t('results.showing', { shown: rows.value.length, total: props.results.length }),
)

// PrimeVue row expansion/expansion state keys off dataKey.
const tableRows = computed(() => rows.value.map((r) => ({ ...r, __key: rowKey(r) })))

function rowKey(r: TrackerCacheResult): string {
  return `${r.TrackerId ?? r.Tracker ?? ''}-${r.Guid ?? r.Title}`
}

function titleLabels(r: TrackerCacheResult) {
  const labels: { text: string; cls: string }[] = []
  const dvf = r.DownloadVolumeFactor
  if (dvf !== undefined && dvf !== null && !isNaN(dvf)) {
    if (dvf === 0) labels.push({ text: 'FREELEECH', cls: 'label-success' })
    else if (dvf < 1) labels.push({ text: `${(dvf * 100).toFixed(0)}%DL`, cls: 'label-primary' })
    else if (dvf > 1) labels.push({ text: `${(dvf * 100).toFixed(0)}%DL`, cls: 'label-danger' })
  }
  const ulf = r.UploadVolumeFactor
  if (ulf !== undefined && ulf !== null && !isNaN(ulf)) {
    if (ulf === 0) labels.push({ text: 'NO UPLOAD', cls: 'label-warning' })
    else if (ulf !== 1) labels.push({ text: `${(ulf * 100).toFixed(0)}%UL`, cls: 'label-info' })
  }
  return labels
}

function externalIdLinks(r: TrackerCacheResult) {
  const links: { label: string; href: string }[] = []
  if (r.Imdb) {
    const len = String(r.Imdb).length > 7 ? 8 : 7
    const id = 'tt' + ('00000000' + r.Imdb).slice(-len)
    links.push({ label: 'IMDB', href: `https://www.imdb.com/title/${id}/` })
  }
  if (r.TMDb && r.TMDb > 0) {
    const type = (r.CategoryDesc ?? '').includes('Movies') ? 'movie' : 'tv'
    links.push({ label: 'TMDB', href: `https://www.themoviedb.org/${type}/${r.TMDb}` })
  }
  if (r.TVDBId && r.TVDBId > 0) {
    links.push({ label: 'TVDB', href: `https://thetvdb.com/?tab=series&id=${r.TVDBId}` })
  }
  return links
}

async function blackhole(r: TrackerCacheResult): Promise<void> {
  if (!r.BlackholeLink) return
  try {
    const res = await fetch(getBasePath() + r.BlackholeLink)
    if (!res.ok) throw new Error(await res.text())
    notify.success(t('results.blackholeOk'))
  } catch (err) {
    notify.danger(`Error: ${err instanceof Error ? err.message : String(err)}`)
  }
}

function human(r: TrackerCacheResult): string {
  return jacketSize(r.Size)
}

function absolute(r: TrackerCacheResult): string {
  return absoluteDate(r.PublishDate)
}

function relative(r: TrackerCacheResult): string {
  return jacketTimespan(r.PublishDate)
}

function firstSeenAbsolute(r: TrackerCacheResult): string {
  return absoluteDate(r.FirstSeen)
}

function firstSeenRelative(r: TrackerCacheResult): string {
  return jacketTimespan(r.FirstSeen)
}
</script>

<template>
  <div class="result-table">
    <div class="table-toolbar">
      <p class="row-count">{{ heading }}</p>
      <div class="toolbar-actions">
        <label class="dead-filter">
          <Checkbox v-model="showDead" binary :input-id="`dead-${showFirstSeen ? 'cache' : 'search'}`" />
          <span>{{ $t('results.deadTorrents') }}</span>
        </label>
      </div>
    </div>

    <DataTable
      :value="tableRows"
      :loading="loading ?? false"
      size="small"
      data-key="__key"
      v-model:expanded-rows="expanded"
      scrollable
      scroll-height="flex"
      class="table-flex"
      paginator
      :rows="50"
      :rows-per-page-options="[10, 25, 50, 100]"
      paginator-template="FirstPageLink PrevPageLink CurrentPageReport NextPageLink LastPageLink RowsPerPageDropdown"
      current-page-report-template="{first} to {last} of {totalRecords}"
    >
      <template #empty>{{ $t('results.noResults') }}</template>

      <Column expander style="width: 2rem" />

      <Column v-if="showFirstSeen" :header="$t('results.firstSeen')" sortable field="FirstSeen" header-style="width: 7rem">
        <template #body="{ data }">
          <span :title="firstSeenAbsolute(data)">{{ firstSeenRelative(data) }}</span>
        </template>
      </Column>

      <Column :header="$t('results.published')" sortable field="PublishDate" header-style="width: 7rem">
        <template #body="{ data }">
          <span :title="absolute(data)">{{ relative(data) }}</span>
        </template>
      </Column>

      <Column field="Title" :header="$t('results.name')" sortable>
        <template #body="{ data }">
          <a v-if="data.Details" :href="data.Details" target="_blank" rel="noreferrer">{{ data.Title }}</a>
          <template v-else>{{ data.Title }}</template>
          <span class="release-labels">
            <a
              v-for="link in externalIdLinks(data)"
              :key="link.label"
              :href="link.href"
              target="_blank"
              rel="noreferrer"
              class="release-label label-link"
            >{{ link.label }}</a>
            <span v-for="label in titleLabels(data)" :key="label.text" class="release-label" :class="label.cls">{{ label.text }}</span>
          </span>
        </template>
      </Column>

      <Column field="Size" :header="$t('results.size')" sortable header-style="width: 7rem">
        <template #body="{ data }">
          <span :title="data.Size !== undefined && data.Size !== null ? `${data.Size} bytes` : ''">{{ human(data) }}</span>
        </template>
      </Column>

      <Column field="Seeders" header="S" sortable header-style="width: 4rem" />
      <Column field="Peers" header="L" sortable header-style="width: 4rem" />
      <Column field="CategoryDesc" :header="$t('results.category')" header-style="width: 8rem" />
      <Column field="Tracker" :header="$t('results.tracker')" sortable header-style="width: 8rem" />

      <Column header-style="width: 8rem">
        <template #body="{ data }">
          <div class="row-actions">
            <a
              v-if="data.Link"
              :href="getBasePath() + data.Link"
              :title="$t('results.download')"
              class="action-link"
            ><i class="pi pi-download" /></a>
            <a
              v-if="data.MagnetUri"
              :href="data.MagnetUri"
              :title="$t('results.magnet')"
              class="action-link"
            ><i class="pi pi-magnet" /></a>
            <Button
              v-if="data.BlackholeLink"
              icon="pi pi-upload"
              :title="$t('results.blackhole')"
              severity="secondary"
              size="small"
              text
              @click="blackhole(data)"
            />
          </div>
        </template>
      </Column>

      <template #expansion="{ data }">
        <div class="release-detail">
          <img v-if="data.Poster" :src="data.Poster" :alt="$t('results.poster')" class="poster" />
          <dl>
            <dt v-if="data.InfoHash">{{ $t('results.infoHash') }}</dt>
            <dd v-if="data.InfoHash" class="mono">{{ data.InfoHash }}</dd>
            <dt v-if="data.MinimumRatio != null">{{ $t('results.minRatio') }}</dt>
            <dd v-if="data.MinimumRatio != null">{{ data.MinimumRatio }}</dd>
            <dt v-if="data.MinimumSeedTime != null">{{ $t('results.minSeedTime') }}</dt>
            <dd v-if="data.MinimumSeedTime != null">{{ data.MinimumSeedTime }} s</dd>
            <dt>{{ $t('results.uploadFactor') }}</dt>
            <dd>{{ data.UploadVolumeFactor ?? 1 }}</dd>
            <dt>{{ $t('results.downloadFactor') }}</dt>
            <dd>
              {{ data.DownloadVolumeFactor ?? 1 }}
              <span v-if="data.DownloadVolumeFactor === 0" class="release-label label-success">FREELEECH</span>
            </dd>
            <dt v-if="data.Description">{{ $t('results.description') }}</dt>
            <dd v-if="data.Description" class="description">{{ data.Description }}</dd>
          </dl>
        </div>
      </template>
    </DataTable>
  </div>
</template>

<style scoped>
/* 表体内部滚动：组件撑满可用高度、自身不滚，滚动发生在 PrimeVue 的
   scrollable 表体容器里（thead position: sticky 保持表头可见）。
   高度链无界的页面（如缓存结果页）自动退化为整页滚动，行为不变。 */
.result-table {
  display: flex;
  flex-direction: column;
  flex: 0 1 auto;
  min-height: 0;
}
.table-flex {
  flex: 0 1 auto;
  min-height: 0;
  height: auto;
}
.table-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.5rem;
  flex-shrink: 0;
}
.table-flex > :deep(.p-datatable-paginator-bottom) {
  flex-shrink: 0;
}
/* sticky 表头必须有不透底色，否则滚动行会从半透明 token 下透出；
   支持 backdrop-filter 的浏览器用玻璃磨砂，否则退化为实色。 */
.table-flex :deep(.p-datatable-thead > tr > th) {
  background: var(--results-head-bg-solid);
}
@supports ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px))) {
  .table-flex :deep(.p-datatable-thead > tr > th) {
    background: var(--results-head-bg);
    -webkit-backdrop-filter: blur(12px) saturate(140%);
    backdrop-filter: blur(12px) saturate(140%);
  }
}
.row-count {
  margin: 0;
  color: var(--p-text-muted-color);
}
.toolbar-actions {
  display: flex;
  gap: 0.75rem;
  align-items: center;
}
.dead-filter {
  display: flex;
  gap: 0.4rem;
  align-items: center;
  font-size: 0.85rem;
}
.row-actions {
  display: flex;
  gap: 0.25rem;
  align-items: center;
}
.action-link {
  color: var(--p-primary-color);
  padding: 0.25rem;
}
.release-labels {
  margin-left: 0.35rem;
}
.release-label {
  display: inline-block;
  border-radius: 0.2em;
  padding: 0.05em 0.4em;
  font-size: 75%;
  font-weight: 700;
  margin-left: 0.25rem;
  color: #fff;
  background: #777;
}
.label-link {
  background: #5bc0de;
  text-decoration: none;
}
.label-success {
  background: #5cb85c;
}
.label-primary {
  background: #337ab7;
}
.label-danger {
  background: #d9534f;
}
.label-warning {
  background: #f0ad4e;
}
.label-info {
  background: #5bc0de;
}
.release-detail {
  display: flex;
  gap: 1rem;
  padding: 0.75rem 1rem;
}
.poster {
  max-height: 12rem;
  border-radius: 4px;
}
.release-detail dl {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.25rem 1rem;
  margin: 0;
}
.release-detail dt {
  font-weight: 600;
}
.release-detail dd {
  margin: 0;
}
.mono {
  font-family: monospace;
}
.description {
  white-space: pre-wrap;
  max-width: 60rem;
}
</style>
