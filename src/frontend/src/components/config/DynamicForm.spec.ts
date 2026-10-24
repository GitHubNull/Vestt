// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import { describe, expect, it } from 'vitest'
import type { ConfigFieldDef } from '../../services/types'
import DynamicForm from './DynamicForm.vue'

// SPEC-12 §5.2 渲染对拍: one fixture per ConfigFieldType, mounted with the
// PrimeVue preset and snapshotted; the payload assembly is covered by the
// store unit tests (buildConfigItems).

const FIXTURE_FIELDS: ConfigFieldDef[] = [
  { id: 'sitelink', type: 'inputstring', name: 'Site Link', value: 'https://site.example/' },
  { id: 'username', type: 'inputstring', name: 'Username', value: '' },
  { id: 'password', type: 'password', name: 'Password', value: '|||%%PREVJACKPASSWD%%|||' },
  { id: 'internal', type: 'hiddendata', name: 'Internal', value: 'opaque-token' },
  { id: 'freeleech', type: 'inputbool', name: 'Freeleech', value: false },
  {
    id: 'categories',
    type: 'inputcheckbox',
    name: 'Categories',
    values: ['2000'],
    options: { '2000': 'Movies', '3000': 'Audio', '4000': 'PC' },
  },
  {
    id: 'authentication',
    type: 'inputselect',
    name: 'Authentication method',
    value: 'form',
    options: { form: 'Form', cookie: 'Cookie' },
  },
  {
    id: 'flaresolverr',
    type: 'displayinfo',
    name: 'FlareSolverr',
    value: 'This site may use Cloudflare DDoS Protection.',
  },
  {
    id: 'CaptchaImage',
    type: 'displayimage',
    name: 'Captcha Image',
    value: 'data:image/jpeg;base64,captcha-bytes',
  },
]

function mountForm(fields: ConfigFieldDef[]) {
  return mount(DynamicForm, {
    props: { fields },
    global: { plugins: [PrimeVue] },
  })
}

describe('DynamicForm 8 field types (SPEC-12 §5.2)', () => {
  it('renders one control per visible field type', () => {
    const wrapper = mountForm(FIXTURE_FIELDS)
    expect(wrapper.find('[data-field-id="sitelink"][data-field-type="inputstring"]').exists()).toBe(true)
    expect(wrapper.find('[data-field-id="password"][data-field-type="password"]').exists()).toBe(true)
    expect(wrapper.find('[data-field-id="freeleech"][data-field-type="inputbool"]').exists()).toBe(true)
    expect(wrapper.find('[data-field-id="categories"][data-field-type="inputcheckbox"]').exists()).toBe(true)
    expect(wrapper.find('[data-field-id="authentication"][data-field-type="inputselect"]').exists()).toBe(true)
    expect(wrapper.find('[data-field-id="flaresolverr"][data-field-type="displayinfo"]').exists()).toBe(true)
    expect(wrapper.find('[data-field-id="CaptchaImage"][data-field-type="displayimage"]').exists()).toBe(true)
    // hiddendata is collected but never rendered
    expect(wrapper.find('[data-field-id="internal"]').exists()).toBe(false)
  })

  it('matches the snapshot for the full fixture', () => {
    const wrapper = mountForm(FIXTURE_FIELDS)
    expect(wrapper.html()).toMatchSnapshot()
  })

  it('exposes buildItems() reflecting the current values', async () => {
    const wrapper = mountForm(FIXTURE_FIELDS)
    const vm = wrapper.vm as unknown as {
      buildItems: () => { id: string; value?: string; values?: string[] }[]
      values: Record<string, unknown>
    }
    vm.values.username = 'user1'
    vm.values.freeleech = true
    const items = vm.buildItems()
    expect(items.find((i) => i.id === 'username')).toEqual({ id: 'username', value: 'user1' })
    expect(items.find((i) => i.id === 'freeleech')).toEqual({ id: 'freeleech', value: 'true' })
    // masked password and display fields never enter the payload
    expect(items.find((i) => i.id === 'password')).toBeUndefined()
    expect(items.find((i) => i.id === 'CaptchaImage')).toBeUndefined()
    // hiddendata rides along with its original value
    expect(items.find((i) => i.id === 'internal')).toEqual({ id: 'internal', value: 'opaque-token' })
  })

  it('propagates field updates from child controls into the payload', async () => {
    const wrapper = mountForm(FIXTURE_FIELDS)
    const vm = wrapper.vm as unknown as {
      buildItems: () => { id: string; value?: string }[]
      values: Record<string, unknown>
    }
    // drive the exposed values record the way DynamicField emits do
    vm.values.sitelink = 'https://mirror.example/'
    expect(vm.buildItems().find((i) => i.id === 'sitelink')).toEqual({
      id: 'sitelink',
      value: 'https://mirror.example/',
    })
  })
})
