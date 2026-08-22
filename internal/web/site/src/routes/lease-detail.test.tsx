import { afterEach, describe, expect, it, vi } from 'vitest'

import type { LeaseSelection } from '@/lib/api'
import { interfaceHeader, leasePath } from '@/lib/api'
import { LeaseDetailRoute } from '@/routes/lease-detail'
import {
  buttonLabelled,
  click,
  jsonResponse,
  leaseDetailBody,
  leaseSummary,
  mount,
  stubFetchWith,
} from '@/routes/test-support'

const leaseName = 'batch-run'
const acknowledgement = 'the controller was asked to release it'
const releaseLabel = 'Release this lease'
const confirmLabel = 'Ask the controller to release'
const keepLabel = 'Keep the lease'

const selection: LeaseSelection = {
  strategy: 'LowestPricePerCore',
  chosen: 'cx23',
  hourlyRate: '0.0080',
  currency: 'EUR',
  runnerUp: 'cpx21',
  offered: 31,
  qualified: 4,
  rejected: [
    { reason: 'TooFewCores', count: 19 },
    { reason: 'TooLittleMemory', count: 8 },
  ],
  decidedAt: '2026-08-21T11:30:00Z',
}

function stubLease(body: unknown) {
  return stubFetchWith((_input, init) =>
    Promise.resolve(
      init?.method === 'DELETE'
        ? jsonResponse({ name: leaseName, detail: acknowledgement })
        : jsonResponse(body),
    ),
  )
}

function deletions(calls: [RequestInfo | URL, (RequestInit | undefined)?][]) {
  return calls.filter(([, init]) => init?.method === 'DELETE')
}

describe('the lease detail', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('shows why the instance type was chosen and what it beat', async () => {
    stubLease(leaseDetailBody({ size: null, selection }))
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    const shown = view.container.textContent ?? ''
    expect(shown).toContain('LowestPricePerCore')
    expect(shown).toContain('cx23')
    expect(shown).toContain('cpx21')
    expect(shown).toContain('31')
    expect(shown).toContain('19 TooFewCores')
    expect(shown).toContain('8 TooLittleMemory')

    await view.unmount()
  })

  it('states the absence of a reasoning for a lease that named its own type', async () => {
    stubLease(leaseDetailBody({ size: 'cx22', selection: null }))
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    expect(view.container.textContent).toContain('named cx22 itself')

    await view.unmount()
  })

  it('asks the controller only once the release is confirmed', async () => {
    const respond = stubLease(leaseDetailBody())
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    await click(buttonLabelled(view.container, releaseLabel))
    expect(deletions(respond.mock.calls)).toHaveLength(0)

    const wording = view.container.textContent ?? ''
    expect(wording).toContain('watchdog')
    expect(wording).toContain('second clock')

    await click(buttonLabelled(view.container, confirmLabel))
    const [target, init] = deletions(respond.mock.calls)[0]
    expect(target).toBe(leasePath(leaseName))
    expect(new Headers(init?.headers).get(interfaceHeader)).not.toBeNull()
    expect(view.container.textContent).toContain(acknowledgement)

    await view.unmount()
  })

  it('leaves the lease alone when the confirmation is declined', async () => {
    const respond = stubLease(leaseDetailBody())
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    await click(buttonLabelled(view.container, releaseLabel))
    await click(buttonLabelled(view.container, keepLabel))

    expect(deletions(respond.mock.calls)).toHaveLength(0)
    expect(view.container.textContent).toContain(releaseLabel)

    await view.unmount()
  })

  it('offers no release for a lease that has already been released', async () => {
    stubLease(
      leaseDetailBody({ summary: leaseSummary({ releasedAt: '2026-08-21T11:45:00Z' }) }),
    )
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    expect(view.container.textContent).not.toContain(releaseLabel)
    expect(view.container.textContent).toContain('has been released')

    await view.unmount()
  })
})
