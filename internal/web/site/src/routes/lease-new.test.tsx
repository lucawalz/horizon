import { afterEach, describe, expect, it, vi } from 'vitest'

import type { LeaseCreateRequest } from '@/lib/api'
import { interfaceHeader, leasesPath, machinesPath, namespacesPath } from '@/lib/api'
import { LeaseNewRoute } from '@/routes/lease-new'
import {
  click,
  control,
  fill,
  jsonResponse,
  leaseDetailBody,
  machinesBody,
  mount,
  namespacesBody,
  providerConfigSummary,
  send,
  stubFetchWith,
} from '@/routes/test-support'
import { formatCount } from '@/routes/units'

const catalogue = machinesBody([providerConfigSummary('hetzner')])
const gigabyte = 4_000_000_000
const gibibyte = 4 * 1024 * 1024 * 1024
const refused = 'CapacityLease.horizon.dev "batch-run" is invalid: spec.replicas: Invalid value: 9'
const forbidden = 403
const denied = 'namespaces is forbidden: User "operator" cannot list resource "namespaces"'

const refusedNamespaces = () =>
  jsonResponse({ status: forbidden, title: 'Forbidden', detail: denied }, forbidden)

function stubCluster(
  created: (init: RequestInit | undefined) => Response,
  namespaces: () => Response = refusedNamespaces,
) {
  return stubFetchWith((input, init) => {
    const target = String(input)
    if (target === machinesPath('', '')) return Promise.resolve(jsonResponse(catalogue))
    if (target === namespacesPath) return Promise.resolve(namespaces())
    return Promise.resolve(created(init))
  })
}

function submitted(body: BodyInit | null | undefined): LeaseCreateRequest {
  return JSON.parse(String(body)) as LeaseCreateRequest
}

async function newLeaseForm() {
  const view = await mount(<LeaseNewRoute />)
  return { view, form: control<HTMLFormElement>(view.container, 'form') }
}

describe('the create form', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('shows the byte count a memory requirement resolves to', async () => {
    stubCluster(() => jsonResponse(leaseDetailBody()))
    const { view } = await newLeaseForm()

    await fill(control<HTMLInputElement>(view.container, 'input[name="memory"]'), '4')
    expect(view.container.textContent).toContain(formatCount(gigabyte))

    await fill(control<HTMLSelectElement>(view.container, 'select[name="memoryUnit"]'), 'Gi')
    expect(view.container.textContent).toContain(formatCount(gibibyte))

    await view.unmount()
  })

  // a fraction of a binary unit is not a byte count, and the quantity it parses to is stored in milli-bytes
  it('steps the memory requirement in whole units', async () => {
    stubCluster(() => jsonResponse(leaseDetailBody()))
    const { view } = await newLeaseForm()

    const memory = control<HTMLInputElement>(view.container, 'input[name="memory"]')
    expect(memory.getAttribute('step')).toBe('1')

    await view.unmount()
  })

  it('submits the requirement the form holds, and the header the guard wants', async () => {
    const respond = stubCluster(() => jsonResponse(leaseDetailBody(), 201))
    const { view, form } = await newLeaseForm()

    await fill(control<HTMLInputElement>(view.container, 'input[name="name"]'), 'batch-run')
    await fill(control<HTMLInputElement>(view.container, 'input[name="region"]'), 'nbg1')
    await fill(control<HTMLInputElement>(view.container, 'input[name="minCPU"]'), '4')
    await fill(control<HTMLInputElement>(view.container, 'input[name="memory"]'), '4')
    await fill(control<HTMLSelectElement>(view.container, 'select[name="cpuType"]'), 'dedicated')
    await send(form)

    const [target, init] = respond.mock.calls[respond.mock.calls.length - 1]
    expect(target).toBe(leasesPath)
    expect(init?.method).toBe('POST')
    expect(new Headers(init?.headers).get(interfaceHeader)).not.toBeNull()
    expect(submitted(init?.body)).toEqual({
      name: 'batch-run',
      providerRef: 'hetzner',
      region: 'nbg1',
      size: '',
      requirements: {
        minCPU: 4,
        minMemory: '4G',
        architecture: 'x86',
        cpuType: 'dedicated',
        strategy: 'LowestPrice',
      },
      replicas: 2,
      durationSeconds: 3600,
      teardownGraceSeconds: 120,
      workloadNamespace: '',
    })

    await view.unmount()
  })

  it('submits a named machine type instead of a requirement', async () => {
    const respond = stubCluster(() => jsonResponse(leaseDetailBody(), 201))
    const { view, form } = await newLeaseForm()

    await fill(control<HTMLInputElement>(view.container, 'input[name="name"]'), 'named-run')
    await fill(control<HTMLInputElement>(view.container, 'input[name="region"]'), 'nbg1')
    await click(control<HTMLInputElement>(view.container, 'input[name="mode"][value="size"]'))
    await fill(control<HTMLInputElement>(view.container, 'input[name="size"]'), 'cx23')
    await send(form)

    const request = submitted(respond.mock.calls[respond.mock.calls.length - 1][1]?.body)
    expect(request.size).toBe('cx23')
    expect(request.requirements).toBeNull()

    await view.unmount()
  })

  it('suggests the namespaces the signed in user may list', async () => {
    stubCluster(
      () => jsonResponse(leaseDetailBody()),
      () => jsonResponse(namespacesBody(['batch', 'default'])),
    )
    const { view } = await newLeaseForm()

    const workload = control<HTMLInputElement>(view.container, 'input[name="workload"]')
    const suggestions = control<HTMLDataListElement>(
      view.container,
      `datalist#${workload.getAttribute('list')}`,
    )
    expect([...suggestions.options].map((option) => option.value)).toEqual(['batch', 'default'])

    await view.unmount()
  })

  it('leaves the namespace a plain text field when the cluster refuses the list', async () => {
    stubCluster(() => jsonResponse(leaseDetailBody()))
    const { view } = await newLeaseForm()

    const workload = control<HTMLInputElement>(view.container, 'input[name="workload"]')
    expect(workload.getAttribute('list')).toBeNull()
    expect(view.container.querySelector('datalist')).toBeNull()
    expect(view.container.textContent).not.toContain('forbidden')
    expect(view.container.textContent).not.toContain('namespace could not')

    await view.unmount()
  })

  it('shows the refusal the cluster answered with', async () => {
    stubCluster(() => jsonResponse({ status: 422, title: 'Unprocessable Entity', detail: refused }, 422))
    const { view, form } = await newLeaseForm()

    await fill(control<HTMLInputElement>(view.container, 'input[name="name"]'), 'batch-run')
    await fill(control<HTMLInputElement>(view.container, 'input[name="region"]'), 'nbg1')
    await send(form)

    expect(view.container.textContent).toContain(refused)

    await view.unmount()
  })
})
