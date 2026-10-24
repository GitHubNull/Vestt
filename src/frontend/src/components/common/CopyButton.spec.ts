// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import { afterEach, describe, expect, it, vi } from 'vitest'
import CopyButton from './CopyButton.vue'

// SPEC-12 §5.4: the Torznab copy action writes the feed URL to the clipboard.

describe('CopyButton', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('copies the feed URL via the Clipboard API', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })

    const wrapper = mount(CopyButton, {
      props: { text: '/api/v2.0/indexers/alpha/results/torznab/', title: 'Copy Torznab Feed' },
      global: { plugins: [PrimeVue] },
    })
    await wrapper.find('button').trigger('click')
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledOnce())
    expect(writeText).toHaveBeenCalledWith('/api/v2.0/indexers/alpha/results/torznab/')
  })

  it('falls back to the legacy execCommand path when Clipboard API fails', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('denied'))
    const execCommand = vi.fn().mockReturnValue(true)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    ;(document as unknown as { execCommand: unknown }).execCommand = execCommand

    const wrapper = mount(CopyButton, {
      props: { text: 'torznab-url' },
      global: { plugins: [PrimeVue] },
    })
    await wrapper.find('button').trigger('click')
    await vi.waitFor(() => expect(execCommand).toHaveBeenCalledWith('copy'))
  })
})
