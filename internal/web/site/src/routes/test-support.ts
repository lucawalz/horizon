import type { ReactElement } from 'react'
import { act } from 'react'
import type { Root } from 'react-dom/client'
import { createRoot } from 'react-dom/client'
import { vi } from 'vitest'

import type { LeaseListResponse, LeaseSummary } from '@/lib/api'

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean | undefined
}

export interface Mounted {
  container: HTMLElement
  render: (element: ReactElement) => Promise<void>
  unmount: () => Promise<void>
}

export async function mount(element: ReactElement): Promise<Mounted> {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true

  const container = document.createElement('div')
  document.body.append(container)

  let root: Root | null = null
  await act(async () => {
    root = createRoot(container)
    root.render(element)
  })

  return {
    container,
    render: async (next: ReactElement) => {
      await act(async () => {
        root?.render(next)
      })
    },
    unmount: async () => {
      await act(async () => {
        root?.unmount()
      })
      container.remove()
    },
  }
}

export function stubFetch(body: unknown) {
  const respond = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
    Promise.resolve(
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  )
  vi.stubGlobal('fetch', respond)
  return respond
}

export function leaseSummary(overrides: Partial<LeaseSummary> = {}): LeaseSummary {
  return {
    name: 'batch-run',
    replicas: 1,
    region: 'nbg1',
    phase: 'Active',
    expiresAt: null,
    ready: 'True',
    armed: 'True',
    createdAt: '2026-08-21T11:00:00Z',
    instanceType: 'cx22',
    readyAt: null,
    releasedAt: null,
    ...overrides,
  }
}

export function leaseListBody(leases: LeaseSummary[]): LeaseListResponse {
  return { leases, observedAt: '2026-08-21T12:00:00Z' }
}
