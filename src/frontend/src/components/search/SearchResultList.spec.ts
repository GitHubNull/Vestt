// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import { createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { i18n } from '../../i18n'
import type { TrackerCacheResult } from '../../services/types'
import SearchResultList from './SearchResultList.vue'

// UI 改版: DataView-style card list — large lazy thumbnail, meta + title +
// labels, size/S/L pills, actions, row expansion, paginator + IntersectionObserver
// render batches.

type Row = TrackerCacheResult & { __key: string }

function row(i: number, overrides: Partial<TrackerCacheResult> = {}): Row {
  return {
    Title: `Release ${i} 1080p`,
    Guid: `guid-${i}`,
    Tracker: '0Magnet',
    TrackerId: '0magnet',
    Size: 1024 * 1024 * 500,
    Seeders: i,
    Peers: 1,
    PublishDate: '2026-10-24T10:00:00Z',
    CategoryDesc: 'Movies/HD',
    ...overrides,
    __key: `0magnet-guid-${i}`,
  }
}

function rows(n: number, overrides: Partial<TrackerCacheResult> = {}): Row[] {
  return Array.from({ length: n }, (_, i) => row(i, overrides))
}

let ioCallback: ((entries: { isIntersecting: boolean }[]) => void) | null = null

class IntersectionObserverStub {
  observe = vi.fn()
  disconnect = vi.fn()
  constructor(cb: (entries: { isIntersecting: boolean }[]) => void) {
    ioCallback = cb
  }
}

function mountList(propsRows: Row[]) {
  return mount(SearchResultList, {
    props: { rows: propsRows, expanded: {} },
    global: { plugins: [PrimeVue, i18n, createPinia()] },
  })
}

describe('SearchResultList', () => {
  beforeEach(() => {
    ioCallback = null
    vi.stubGlobal('IntersectionObserver', IntersectionObserverStub)
  })

  it('renders one card per visible row with meta, title, pills and actions', () => {
    const wrapper = mountList(
      rows(3, {
        Details: 'https://example/details/1',
        Imdb: 123456,
        DownloadVolumeFactor: 0,
        MagnetUri: 'magnet:?xt=urn:btih:abc',
        BlackholeLink: '/blackhole/1',
      }),
    )
    expect(wrapper.findAll('.result-card')).toHaveLength(3)
    expect(wrapper.find('.card-meta').text()).toContain('Movies/HD · 0Magnet')
    expect(wrapper.find('.card-title a').attributes('href')).toBe('https://example/details/1')
    expect(wrapper.find('.card-labels').text()).toContain('IMDB')
    expect(wrapper.find('.card-labels').text()).toContain('FREELEECH')
    expect(wrapper.find('.pill-size').text()).toContain('500 MB')
    expect(wrapper.find('.pill-seed').text()).toContain('0')
    expect(wrapper.find('.pill-peer').text()).toContain('1')
    expect(wrapper.find('a.action-link[title="本地下载"]').exists()).toBe(false)
    expect(wrapper.find('a.action-link .pi-magnet').exists()).toBe(true)
    expect(wrapper.find('button[title="保存到服务器黑洞目录"]').exists()).toBe(true)
  })

  it('renders a lazy poster image or a placeholder', () => {
    const wrapper = mountList([
      row(1, { Poster: 'https://img/poster.jpg' }),
      row(2),
    ])
    const img = wrapper.find('img.thumb-img')
    expect(img.attributes('src')).toBe('https://img/poster.jpg')
    expect(img.attributes('loading')).toBe('lazy')
    const ph = wrapper.find('.thumb-placeholder')
    expect(ph.exists()).toBe(true)
    expect(ph.find('.thumb-initial').text()).toBe('0')
    expect(ph.attributes('style')).toContain('background')
    expect(ph.find('.thumb-watermark.pi-magnet').exists()).toBe(true)
  })

  it('renders a plain title span without a Details link', () => {
    const wrapper = mountList(rows(1))
    expect(wrapper.find('.card-title a').exists()).toBe(false)
    expect(wrapper.find('.card-title').text()).toContain('Release 0 1080p')
  })

  it('toggles the detail expansion on card click', async () => {
    const wrapper = mountList(rows(1, { InfoHash: 'deadbeef', Description: 'line1\nline2' }))
    expect(wrapper.find('.release-detail').exists()).toBe(false)
    await wrapper.find('.card-main').trigger('click')
    expect(wrapper.find('.release-detail').text()).toContain('deadbeef')
    expect(wrapper.find('.release-detail .description').text()).toContain('line1')
    await wrapper.find('.card-main').trigger('click')
    expect(wrapper.find('.release-detail').exists()).toBe(false)
  })

  it('shows the empty state when there are no rows', () => {
    const wrapper = mountList([])
    expect(wrapper.find('.empty').text()).toContain('没有结果')
    expect(wrapper.findAll('.result-card')).toHaveLength(0)
  })

  it('renders only the first batch and appends more when the sentinel intersects', async () => {
    const wrapper = mountList(rows(60))
    expect(wrapper.findAll('.result-card')).toHaveLength(10)

    // 切到 25 条/页后页切片超出首批 10 张，哨兵触发追加。
    await wrapper.find('.p-paginator-rpp-dropdown').trigger('click')
    await wrapper.vm.$nextTick()
    const opt25 = Array.from(document.querySelectorAll('.p-select-option')).find(
      (o) => o.textContent?.trim() === '25',
    ) as HTMLElement
    opt25.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }))
    opt25.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    opt25.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await wrapper.vm.$nextTick()
    expect(wrapper.findAll('.result-card')).toHaveLength(10)

    ioCallback?.([{ isIntersecting: true }])
    await wrapper.vm.$nextTick()
    expect(wrapper.findAll('.result-card')).toHaveLength(20)
    ioCallback?.([{ isIntersecting: true }])
    await wrapper.vm.$nextTick()
    expect(wrapper.findAll('.result-card')).toHaveLength(25)
  })

  it('paginates: jumping to the next page shows the remaining rows', async () => {
    const wrapper = mountList(rows(12))
    expect(wrapper.findAll('.result-card')).toHaveLength(10)
    await wrapper.find('.p-paginator-next').trigger('click')
    expect(wrapper.findAll('.result-card')).toHaveLength(2)
    expect(wrapper.find('.card-title').text()).toContain('Release 10 1080p')
  })

  it('marks dead torrents (zero seeders) as muted', () => {
    const wrapper = mountList([row(0, { Seeders: 0 }), row(1, { Seeders: 5 })])
    const cards = wrapper.findAll('.result-card')
    expect(cards[0].classes()).toContain('dead')
    expect(cards[1].classes()).not.toContain('dead')
  })
})
