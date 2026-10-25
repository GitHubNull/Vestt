<script setup lang="ts">
import Paginator from 'primevue/paginator'
import ProgressBar from 'primevue/progressbar'
import Button from 'primevue/button'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { absoluteDate, jacketSize, jacketTimespan } from '../../services/format'
import { getBasePath } from '../../services/api'
import type { TrackerCacheResult } from '../../services/types'
import { blackhole, externalIdLinks, titleLabels } from './releaseUtils'

// DataView-style card list (UI 改版): the default results view on the search
// home. Large lazy thumbnail left, category/tracker + title + labels middle,
// size/S/L pills and actions right, row expansion identical to the table.
// Paginator slices the loaded set client-side; an IntersectionObserver
// sentinel appends render batches so huge result sets never flood the DOM.

const props = defineProps<{
  rows: (TrackerCacheResult & { __key: string })[]
  loading?: boolean
}>()
const expanded = defineModel<Record<string, boolean>>('expanded', { required: true })

const BATCH = 10
const first = ref(0)
const rowsPerPage = ref(10)
const rendered = ref(BATCH)

const pageRows = computed(() => {
  const start = first.value
  return props.rows.slice(start, start + rowsPerPage.value)
})
const visibleRows = computed(() => pageRows.value.slice(0, rendered.value))

function resetBatch(): void {
  rendered.value = Math.min(BATCH, pageRows.value.length)
}

watch(
  () => props.rows,
  () => {
    first.value = 0
    resetBatch()
  },
)
watch(rowsPerPage, () => {
  first.value = 0
  resetBatch()
})
watch(pageRows, resetBatch)

function toggle(key: string): void {
  expanded.value = { ...expanded.value, [key]: !expanded.value[key] }
}

function meta(r: TrackerCacheResult): string {
  return [r.CategoryDesc, r.Tracker].filter(Boolean).join(' · ')
}

// 无海报占位：站点首字母 + 由站点名散列出的稳定底色，让无图卡片也有辨识度。
function placeholderStyle(r: TrackerCacheResult): { background: string } {
  const seed = r.TrackerId ?? r.Tracker ?? r.Title ?? '?'
  let hash = 0
  for (let i = 0; i < seed.length; i++) hash = (hash * 31 + seed.charCodeAt(i)) | 0
  const hue = ((hash % 360) + 360) % 360
  return { background: `linear-gradient(135deg, hsl(${hue} 35% 30%), hsl(${hue} 30% 18%))` }
}

function placeholderInitial(r: TrackerCacheResult): string {
  return (r.Tracker ?? r.TrackerId ?? '?').trim().charAt(0).toUpperCase()
}

const sentinel = ref<HTMLElement | null>(null)
let observer: IntersectionObserver | null = null

onMounted(() => {
  if (typeof IntersectionObserver === 'undefined' || !sentinel.value) return
  observer = new IntersectionObserver((entries) => {
    if (entries.some((e) => e.isIntersecting) && rendered.value < pageRows.value.length) {
      rendered.value = Math.min(rendered.value + BATCH, pageRows.value.length)
    }
  })
  observer.observe(sentinel.value)
})

onBeforeUnmount(() => {
  observer?.disconnect()
  observer = null
})
</script>

<template>
  <div class="result-list">
    <ProgressBar v-if="loading" mode="indeterminate" class="loading-bar" />

    <div v-if="!rows.length" class="empty">{{ $t('results.noResults') }}</div>

    <template v-else>
      <div class="cards">
        <article
          v-for="r in visibleRows"
          :key="r.__key"
          class="result-card"
          :class="{ dead: (r.Seeders ?? 0) === 0 }"
        >
          <div
            class="card-main"
            role="button"
            tabindex="0"
            @click="toggle(r.__key)"
            @keydown.enter.prevent="toggle(r.__key)"
          >
            <div class="thumb">
              <img v-if="r.Poster" :src="r.Poster" :alt="$t('results.poster')" loading="lazy" class="thumb-img" />
              <div v-else class="thumb-placeholder" :style="placeholderStyle(r)">
                <span class="thumb-initial">{{ placeholderInitial(r) }}</span>
                <i class="pi pi-magnet thumb-watermark" />
              </div>
            </div>

            <div class="card-body">
              <p class="card-meta">{{ meta(r) }}</p>
              <h3 class="card-title">
                <a v-if="r.Details" :href="r.Details" target="_blank" rel="noreferrer" @click.stop>{{ r.Title }}</a>
                <template v-else>{{ r.Title }}</template>
              </h3>
              <p class="card-labels">
                <a
                  v-for="link in externalIdLinks(r)"
                  :key="link.label"
                  :href="link.href"
                  target="_blank"
                  rel="noreferrer"
                  class="release-label label-link"
                  @click.stop
                >{{ link.label }}</a>
                <span v-for="label in titleLabels(r)" :key="label.text" class="release-label" :class="label.cls">{{ label.text }}</span>
              </p>
              <p class="card-time" :title="absoluteDate(r.PublishDate)">{{ jacketTimespan(r.PublishDate) }}</p>
            </div>

            <div class="card-side" @click.stop>
              <span class="pill pill-size" :title="r.Size !== undefined && r.Size !== null ? `${r.Size} bytes` : ''">{{ jacketSize(r.Size) }}</span>
              <span class="pill pill-seed"><i class="pi pi-arrow-up" />{{ r.Seeders ?? '—' }}</span>
              <span class="pill pill-peer"><i class="pi pi-arrow-down" />{{ r.Peers ?? '—' }}</span>
              <div class="row-actions">
                <a
                  v-if="r.Link"
                  :href="getBasePath() + r.Link"
                  :title="$t('results.download')"
                  class="action-link"
                ><i class="pi pi-download" /></a>
                <a
                  v-if="r.MagnetUri"
                  :href="r.MagnetUri"
                  :title="$t('results.magnet')"
                  class="action-link"
                ><i class="pi pi-magnet" /></a>
                <Button
                  v-if="r.BlackholeLink"
                  icon="pi pi-upload"
                  :title="$t('results.blackhole')"
                  severity="secondary"
                  size="small"
                  text
                  @click="blackhole(r)"
                />
              </div>
            </div>

            <i class="pi expand-chevron" :class="expanded[r.__key] ? 'pi-chevron-down' : 'pi-chevron-right'" />
          </div>

          <div v-if="expanded[r.__key]" class="release-detail">
            <img v-if="r.Poster" :src="r.Poster" :alt="$t('results.poster')" class="poster" />
            <dl>
              <dt v-if="r.InfoHash">{{ $t('results.infoHash') }}</dt>
              <dd v-if="r.InfoHash" class="mono">{{ r.InfoHash }}</dd>
              <dt v-if="r.MinimumRatio != null">{{ $t('results.minRatio') }}</dt>
              <dd v-if="r.MinimumRatio != null">{{ r.MinimumRatio }}</dd>
              <dt v-if="r.MinimumSeedTime != null">{{ $t('results.minSeedTime') }}</dt>
              <dd v-if="r.MinimumSeedTime != null">{{ r.MinimumSeedTime }} s</dd>
              <dt>{{ $t('results.uploadFactor') }}</dt>
              <dd>{{ r.UploadVolumeFactor ?? 1 }}</dd>
              <dt>{{ $t('results.downloadFactor') }}</dt>
              <dd>
                {{ r.DownloadVolumeFactor ?? 1 }}
                <span v-if="r.DownloadVolumeFactor === 0" class="release-label label-success">FREELEECH</span>
              </dd>
              <dt v-if="r.Description">{{ $t('results.description') }}</dt>
              <dd v-if="r.Description" class="description">{{ r.Description }}</dd>
            </dl>
          </div>
        </article>
        <div ref="sentinel" class="scroll-sentinel" aria-hidden="true" />
      </div>

      <Paginator
        v-model:first="first"
        v-model:rows="rowsPerPage"
        :total-records="rows.length"
        :rows-per-page-options="[10, 25, 50]"
        template="FirstPageLink PrevPageLink CurrentPageReport NextPageLink LastPageLink RowsPerPageDropdown"
        current-page-report-template="{first} to {last} of {totalRecords}"
        class="list-paginator"
      />
    </template>
  </div>
</template>

<style scoped>
/* 与 .result-table 同构：撑满可用高度、滚动发生在卡片体内。 */
.result-list {
  display: flex;
  flex-direction: column;
  flex: 0 1 auto;
  min-height: 0;
}
.loading-bar {
  flex-shrink: 0;
  margin-bottom: 0.5rem;
}
.empty {
  padding: 2rem;
  text-align: center;
  color: var(--p-text-muted-color);
}
.cards {
  flex: 0 1 auto;
  min-height: 0;
  overflow-y: auto;
}
.scroll-sentinel {
  height: 1px;
}
.result-card {
  border-bottom: 1px solid var(--p-content-border-color);
}
.result-card.dead {
  opacity: 0.55;
}
.card-main {
  display: flex;
  align-items: center;
  gap: 1rem;
  padding: 0.75rem 0.5rem;
  cursor: pointer;
}
.card-main:hover {
  background: var(--p-content-hover-background);
}
.thumb {
  flex-shrink: 0;
  width: 9rem;
  aspect-ratio: 4 / 3;
  border-radius: 6px;
  overflow: hidden;
}
.thumb-img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.thumb-placeholder {
  position: relative;
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  color: hsl(0 0% 100% / 0.85);
}
.thumb-initial {
  font-size: 2.2rem;
  font-weight: 700;
  text-shadow: 0 1px 4px rgb(0 0 0 / 0.35);
}
.thumb-watermark {
  position: absolute;
  right: 0.35rem;
  bottom: 0.25rem;
  font-size: 0.9rem;
  opacity: 0.35;
}
.card-body {
  flex: 1;
  min-width: 0;
}
.card-meta {
  margin: 0;
  font-size: 0.85rem;
  color: var(--p-text-muted-color);
}
.card-title {
  margin: 0.15rem 0;
  font-size: 1.05rem;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.card-labels {
  margin: 0.15rem 0;
}
.card-time {
  margin: 0.15rem 0 0;
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
}
.card-side {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 0.4rem;
  flex-wrap: wrap;
  justify-content: flex-end;
  max-width: 22rem;
}
.pill {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  border-radius: 999px;
  padding: 0.15rem 0.6rem;
  font-size: 0.8rem;
  font-weight: 600;
  white-space: nowrap;
  background: var(--p-content-hover-background);
  color: var(--p-text-color);
}
.pill-seed {
  color: #2e9e4f;
}
.pill-peer {
  color: #d97b29;
}
.expand-chevron {
  flex-shrink: 0;
  color: var(--p-text-muted-color);
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
.release-label {
  display: inline-block;
  border-radius: 0.2em;
  padding: 0.05em 0.4em;
  font-size: 75%;
  font-weight: 700;
  margin-right: 0.25rem;
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
  padding: 0.75rem 1rem 0.75rem 10.5rem;
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
.list-paginator {
  flex-shrink: 0;
}
</style>
