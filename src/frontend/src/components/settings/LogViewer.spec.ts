// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import Select from 'primevue/select'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { CachedLog } from '../../services/types'
import LogViewer from './LogViewer.vue'

// SPEC-13 §5.3: LogViewer polling toggle — manual refresh by default, a 10 s
// interval while enabled, stopped after switching back.

const ENTRIES: CachedLog[] = [
  { level: 'INFO', message: 'Manual search in 0Magnet => Found 1 releases [12ms]', when: '2026-10-24T10:00:00Z' },
  { level: 'ERROR', message: 'Test failed for acgrip', when: '2026-10-24T10:01:00Z' },
]

function stubLogsFetch() {
  const fetchMock = vi.fn().mockImplementation(() =>
    Promise.resolve(
      new Response(JSON.stringify(ENTRIES), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  )
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

function mountViewer() {
  return mount(LogViewer, {
    global: { plugins: [PrimeVue, createPinia()] },
  })
}

async function setPolling(wrapper: ReturnType<typeof mountViewer>, on: boolean) {
  const select = wrapper.findComponent(Select)
  const option = on
    ? { label: 'Refresh every 10 seconds', value: true }
    : { label: 'Manual refresh', value: false }
  select.vm.$emit('update:modelValue', option)
  select.vm.$emit('change')
  await wrapper.vm.$nextTick()
}

describe('LogViewer (SPEC-13 §3.4)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setActivePinia(createPinia())
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('manual mode: initial load only, refresh button reloads on demand', async () => {
    const fetchMock = stubLogsFetch()
    const wrapper = mountViewer()
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))

    await vi.advanceTimersByTimeAsync(30000)
    expect(fetchMock).toHaveBeenCalledTimes(1)

    await wrapper.find('button').trigger('click')
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
  })

  it('polling mode refetches every 10 s until switched off', async () => {
    const fetchMock = stubLogsFetch()
    const wrapper = mountViewer()
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))

    await setPolling(wrapper, true)
    await vi.advanceTimersByTimeAsync(10000)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(10000)
    expect(fetchMock).toHaveBeenCalledTimes(3)

    await setPolling(wrapper, false)
    await vi.advanceTimersByTimeAsync(30000)
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })
})
