import type { ReactElement } from 'react'
import { act } from 'react'
import type { Root } from 'react-dom/client'
import { createRoot } from 'react-dom/client'
import { vi } from 'vitest'

import type {
  LeaseDetailResponse,
  LeaseListResponse,
  LeaseSummary,
  MachineCatalogueResponse,
  NamespaceListResponse,
  ProviderConfigSummary,
} from '@/lib/api'

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

type Reply = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>

export function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

export function stubFetchWith(reply: Reply) {
  const respond = vi.fn(reply)
  vi.stubGlobal('fetch', respond)
  return respond
}

export function stubFetch(body: unknown) {
  return stubFetchWith(() => Promise.resolve(jsonResponse(body)))
}

export function control<T extends Element>(container: HTMLElement, selector: string): T {
  const element = container.querySelector<T>(selector)
  if (element === null) throw new Error(`the view rendered no ${selector}`)
  return element
}

export function buttonLabelled(container: HTMLElement, label: string): HTMLButtonElement {
  const found = [...container.querySelectorAll('button')].find(
    (button) => button.textContent === label,
  )
  if (found === undefined) throw new Error(`the view rendered no button labelled ${label}`)
  return found
}

// react patches the value setter on every element it renders to track changes, so an assignment through it looks like no change at all
function writeValue(element: HTMLInputElement | HTMLSelectElement, value: string) {
  const prototype =
    element instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype
  Object.getOwnPropertyDescriptor(prototype, 'value')?.set?.call(element, value)
}

export async function fill(
  element: HTMLInputElement | HTMLSelectElement,
  value: string,
): Promise<void> {
  await act(async () => {
    writeValue(element, value)
    element.dispatchEvent(new Event('input', { bubbles: true }))
    element.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

export async function click(element: Element): Promise<void> {
  await act(async () => {
    element.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
  })
}

export async function send(form: HTMLFormElement): Promise<void> {
  await act(async () => {
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  })
}

export async function settle(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
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

export function providerConfigSummary(name: string): ProviderConfigSummary {
  return { name, type: 'hetzner', ready: 'True', createdAt: '2026-08-21T10:00:00Z' }
}

export function machinesBody(configs: ProviderConfigSummary[]): MachineCatalogueResponse {
  return {
    configs,
    config: '',
    region: '',
    state: 'NoSelection',
    detail: null,
    refreshedAt: null,
    types: [],
    observedAt: '2026-08-21T12:00:00Z',
  }
}

export function leaseDetailBody(overrides: Partial<LeaseDetailResponse> = {}): LeaseDetailResponse {
  return {
    summary: leaseSummary(),
    providerRef: 'hetzner',
    size: 'cx22',
    requirements: null,
    selection: null,
    durationSeconds: 7200,
    teardownGraceSeconds: 120,
    workloadNamespace: null,
    migratedWorkloads: [],
    migrationWarnings: [],
    acceptedAt: '2026-08-21T11:00:00Z',
    backstopAt: null,
    watchdogDeadline: null,
    observedGeneration: 1,
    conditions: [],
    instances: [],
    observedAt: '2026-08-21T12:00:00Z',
    ...overrides,
  }
}

export function namespacesBody(names: string[]): NamespaceListResponse {
  return { namespaces: names, observedAt: '2026-08-21T12:00:00Z' }
}
