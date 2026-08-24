import { afterEach, describe, expect, it, vi } from 'vitest'

import type { LeaseInstance, LeaseSelection, MigrationWarning } from '@/lib/api'
import { interfaceHeader, leasePath } from '@/lib/api'
import { LeaseDetailRoute } from '@/routes/lease-detail'
import { leaseHref, leasesHref } from '@/routes/router'
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
const deleteLabel = 'Delete this record'
const confirmDeleteLabel = 'Delete the record'
const releasedAt = '2026-08-21T11:45:00Z'
const unlistedReason = 'SidecarHoldsTheDrain'

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

const instances: LeaseInstance[] = [
  {
    name: 'batch-run-0',
    providerID: 'hcloud://42',
    nodeName: null,
    phase: 'Created',
    stage: 'AwaitingRegistration',
    createdAt: '2026-08-21T11:30:00Z',
    lastError: null,
  },
  {
    name: 'batch-run-1',
    providerID: null,
    nodeName: null,
    phase: 'Released',
    stage: null,
    createdAt: '2026-08-21T11:30:00Z',
    lastError: null,
  },
]

const migrationCopy: [string, string][] = [
  ['RolloutPaused', 'the rollout is paused, so pods are cycled by horizon instead'],
  ['ManualRollout', 'the update strategy is OnDelete'],
  ['PartitionedRollout', 'a rollout partition holds pods back'],
  ['RecreateStrategy', 'every replica stops before a replacement starts'],
  ['NoSurgeCapacity', 'maxSurge leaves no room for a replacement pod'],
  ['NodeSelectorPinned', 'the node selector is cleared for the duration of the lease'],
]

const everyReason: MigrationWarning[] = migrationCopy.map(([reason], index) => ({
  workload: `batch/worker-${index}`,
  reasons: [reason],
}))

function pillLabelled(container: HTMLElement, label: string): HTMLElement {
  const found = [...container.querySelectorAll<HTMLElement>('[data-severity]')].find(
    (pill) => pill.textContent === label,
  )
  if (found === undefined) throw new Error(`the view rendered no pill labelled ${label}`)
  return found
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
    window.history.pushState(null, '', leasesHref)
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

  it('names the stage each instance is waiting in', async () => {
    stubLease(leaseDetailBody({ instances }))
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    const rows = [...view.container.querySelectorAll('tbody tr')]
    const waiting = rows.find((row) => row.textContent?.includes('batch-run-0'))
    const retired = rows.find((row) => row.textContent?.includes('batch-run-1'))
    expect(waiting?.textContent).toContain('AwaitingRegistration')
    expect(retired?.textContent).toContain('not reported')
    expect(waiting?.closest('table')?.querySelectorAll('thead th')).toHaveLength(
      waiting?.querySelectorAll('td').length ?? 0,
    )

    await view.unmount()
  })

  it('states the absence of a reasoning for a lease that named its own type', async () => {
    stubLease(leaseDetailBody({ size: 'cx22', selection: null }))
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    expect(view.container.textContent).toContain('named cx22 itself')

    await view.unmount()
  })

  it('names what the move costs every workload the controller flagged', async () => {
    stubLease(leaseDetailBody({ migrationWarnings: everyReason }))
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    const shown = view.container.textContent ?? ''
    for (const [reason, copy] of migrationCopy) {
      expect(pillLabelled(view.container, reason).dataset.severity).toBe('attention')
      expect(shown).toContain(copy)
    }
    const panel = pillLabelled(view.container, migrationCopy[0][0]).closest('section')
    expect(panel?.querySelector('[data-severity="danger"]')).toBeNull()
    expect(panel?.textContent).toContain('still moves onto the leased nodes')

    await view.unmount()
  })

  it('shows a warning reason it has no wording for rather than dropping it', async () => {
    stubLease(
      leaseDetailBody({
        migrationWarnings: [{ workload: 'batch/worker', reasons: [unlistedReason] }],
      }),
    )
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    expect(pillLabelled(view.container, unlistedReason).dataset.severity).toBe('attention')
    expect(view.container.textContent).toContain('no wording for')

    await view.unmount()
  })

  it('leaves the migration warnings out when the controller flagged nothing', async () => {
    stubLease(leaseDetailBody())
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    expect(view.container.textContent).not.toContain('Migration warnings')

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

  it('offers the leftover record rather than a release once the lease is released', async () => {
    stubLease(leaseDetailBody({ summary: leaseSummary({ releasedAt }) }))
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    expect(view.container.textContent).not.toContain(releaseLabel)
    expect(view.container.textContent).toContain('has been released')
    expect(buttonLabelled(view.container, deleteLabel).textContent).toBe(deleteLabel)

    await view.unmount()
  })

  it('deletes the leftover record on confirmation and returns to the lease list', async () => {
    const respond = stubLease(leaseDetailBody({ summary: leaseSummary({ releasedAt }) }))
    window.history.pushState(null, '', leaseHref(leaseName))
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    await click(buttonLabelled(view.container, deleteLabel))
    expect(deletions(respond.mock.calls)).toHaveLength(0)
    expect(view.container.textContent).toContain('Nothing is destroyed here')

    await click(buttonLabelled(view.container, confirmDeleteLabel))
    const [target, init] = deletions(respond.mock.calls)[0]
    expect(target).toBe(leasePath(leaseName))
    expect(new Headers(init?.headers).get(interfaceHeader)).not.toBeNull()
    expect(window.location.pathname).toBe(leasesHref)

    await view.unmount()
  })
})
