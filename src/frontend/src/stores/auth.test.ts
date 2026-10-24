import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { _resetForTests } from '../services/api'
import { useAuthStore } from './auth'

function configResponse(password: string): Response {
  return new Response(JSON.stringify({ password }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('auth store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    _resetForTests()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('probe(): no admin password → authenticated straight away', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(configResponse('')))
    const auth = useAuthStore()
    await expect(auth.probe()).resolves.toBe(true)
    expect(auth.authenticated).toBe(true)
    expect(auth.passwordSet).toBe(false)
    expect(auth.resolved).toBe(true)
  })

  it('probe(): 401 with password set → unauthenticated', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('unauthorized', { status: 401 })),
    )
    const auth = useAuthStore()
    await expect(auth.probe()).resolves.toBe(false)
    expect(auth.authenticated).toBe(false)
    expect(auth.passwordSet).toBe(true)
  })

  it('login(): form accepted → authenticated', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce({} as Response)
        .mockResolvedValueOnce(configResponse('********')),
    )
    const auth = useAuthStore()
    await expect(auth.login('pw')).resolves.toBe(true)
    expect(auth.authenticated).toBe(true)
    expect(auth.passwordSet).toBe(true)
  })

  it('login(): config probe 401 → stays unauthenticated', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce({} as Response)
        .mockResolvedValueOnce(new Response('unauthorized', { status: 401 })),
    )
    const auth = useAuthStore()
    await expect(auth.login('wrong')).resolves.toBe(false)
    expect(auth.authenticated).toBe(false)
  })
})
