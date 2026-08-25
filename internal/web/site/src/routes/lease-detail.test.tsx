import { afterEach, describe, expect, it, vi } from 'vitest'

import type { LeaseInstance, LeaseSelection, MigrationWarning } from '@/lib/api'
import { interfaceHeader, leasePath } from '@/lib/api'
import { hourMs, minuteMs } from '@/lib/duration'
import { LeaseDetailRoute } from '@/routes/lease-detail'
import { leaseHref, leasesHref } from '@/routes/router'
import {
  buttonLabelled,
  click,
  control,
  fill,
  jsonResponse,
  leaseDetailBody,
  leaseSummary,
  mount,
  send,
  settle,
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
  ['HeldByAnotherLease', 'another lease holds this workload, so this lease leaves it where it is'],
]

const replicationCopy: [string, string][] = [
  [
    'TargetedByAutoscaler',
    'move mode changes no replica count, so it bursts this workload without fighting the autoscaler',
  ],
  [
    'StatefulSetNotCopyable',
    'move mode bursts a StatefulSet as it stands',
  ],
  [
    'DisruptionBudgetSpansCopy',
    'so its accounting is wrong for the life of the lease',
  ],
  [
    'CopySelectorMatchesOriginal',
    'the two replica sets would contend over one set of pods',
  ],
  [
    'TopologySpreadSpansCopy',
    'so the next pod of the original can be left Pending',
  ],
]

function warningsFor(copy: [string, string][]): MigrationWarning[] {
  return copy.map(([reason], index) => ({ workload: `batch/worker-${index}`, reasons: [reason] }))
}

const everyReason = warningsFor(migrationCopy)

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

const extendLabel = 'Extend this lease'
const askLabel = 'Extend the lease'
const requestedMinutes = 180
const accepted = {
  name: leaseName,
  durationSeconds: requestedMinutes * 60,
  detail: 'the controller re-derives the deadline of this lease on its next pass',
}

interface Refusal {
  status: number
  severity: string
  title: string
  detail: string
}

const refusals: Refusal[] = [
  {
    status: 400,
    severity: 'danger',
    title: 'That is not a duration this interface can submit',
    detail: '-1 is not a number of seconds a capacity lease can run for',
  },
  {
    status: 409,
    severity: 'attention',
    title: 'The lease changed while this extension was in flight',
    detail: '"batch-run" changed while this extension was in flight. reading the lease again',
  },
  {
    status: 422,
    severity: 'danger',
    title: 'The cluster refused this extension',
    detail: '"batch-run" already runs for 3h0m0s. this interface lengthens a lease',
  },
]

function heroCountdown(container: HTMLElement): HTMLElement {
  // the hero is the first instant the page renders, and the ramp it carries is what an extension has to move
  return control<HTMLElement>(container, 'time')
}

function stubExtending(answer: () => Response) {
  let expiresAt = new Date(Date.now() + 2 * minuteMs).toISOString()

  return stubFetchWith((_input, init) => {
    if (init?.method !== 'PATCH') {
      return Promise.resolve(jsonResponse(leaseDetailBody({ summary: leaseSummary({ expiresAt }) })))
    }
    const reply = answer()
    if (reply.ok) expiresAt = new Date(Date.now() + 2 * hourMs).toISOString()
    return Promise.resolve(reply)
  })
}

async function askToExtend(container: HTMLElement, minutes: number) {
  await click(buttonLabelled(container, extendLabel))
  await fill(control<HTMLInputElement>(container, 'input[name="minutes"]'), String(minutes))
  await send(control<HTMLFormElement>(container, 'form'))
  await settle()
}

function patches(calls: [RequestInfo | URL, (RequestInit | undefined)?][]) {
  return calls.filter(([, init]) => init?.method === 'PATCH')
}

describe('extending a running lease', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('submits the total the form holds and lets the countdown ramp back down', async () => {
    const respond = stubExtending(() => jsonResponse(accepted, 202))
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    expect(heroCountdown(view.container).dataset.severity).toBe('attention')
    expect(patches(respond.mock.calls)).toHaveLength(0)

    await askToExtend(view.container, requestedMinutes)

    const [target, init] = patches(respond.mock.calls)[0]
    expect(target).toBe(leasePath(leaseName))
    expect(new Headers(init?.headers).get(interfaceHeader)).not.toBeNull()
    expect(JSON.parse(String(init?.body))).toEqual({ durationSeconds: accepted.durationSeconds })
    expect(heroCountdown(view.container).dataset.severity).toBe('neutral')
    expect(view.container.textContent).toContain(accepted.detail)
    expect(view.container.textContent).toContain('now runs for 3h')

    await view.unmount()
  })

  it('offers the backstop a leased machine enforces as the ceiling', async () => {
    stubFetchWith(() =>
      Promise.resolve(
        jsonResponse(
          leaseDetailBody({
            acceptedAt: '2026-08-21T11:00:00Z',
            backstopAt: '2026-08-21T14:00:00Z',
          }),
        ),
      ),
    )
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    await click(buttonLabelled(view.container, extendLabel))
    const minutes = control<HTMLInputElement>(view.container, 'input[name="minutes"]')
    expect(minutes.max).toBe('180')
    expect(view.container.textContent).toContain('Up to 3h, when the earliest leased machine')

    await view.unmount()
  })

  it('says the ceiling is unknown while a leased machine has latched none', async () => {
    stubFetchWith(() =>
      Promise.resolve(
        jsonResponse(
          leaseDetailBody({
            conditions: [
              {
                type: 'ExpiryClamped',
                status: 'Unknown',
                reason: 'BackstopUnknown',
                message: 'no backstop is recorded',
                lastTransitionTime: '2026-08-21T11:00:00Z',
              },
            ],
          }),
        ),
      ),
    )
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    await click(buttonLabelled(view.container, extendLabel))
    expect(view.container.textContent).toContain('No leased machine records a lifetime backstop yet')

    await view.unmount()
  })

  for (const refusal of refusals) {
    it(`keeps the ${refusal.status} refusal distinct from the others`, async () => {
      stubExtending(() =>
        jsonResponse({ status: refusal.status, title: 'refused', detail: refusal.detail }, refusal.status),
      )
      const view = await mount(<LeaseDetailRoute name={leaseName} />)

      await askToExtend(view.container, requestedMinutes)

      const notice = control<HTMLElement>(
        view.container,
        `[role="status"][data-severity="${refusal.severity}"]`,
      )
      expect(notice.textContent).toContain(refusal.title)
      expect(notice.textContent).toContain(refusal.detail)
      expect(buttonLabelled(view.container, askLabel).textContent).toBe(askLabel)

      await view.unmount()
    })
  }
})

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

  it('names the mode a lease runs its workload in and the pods a copy carries', async () => {
    stubLease(
      leaseDetailBody({
        workloadNamespaces: ['batch'],
        workloadMode: 'replicate',
        workloadBurstReplicas: 3,
      }),
    )
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    expect(view.container.textContent).toContain('replicate, each copy runs 3 pods')

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
    expect(panel?.textContent).toContain('Migration warnings')
    expect(panel?.textContent).toContain('or is left where it is')

    await view.unmount()
  })

  // a lease that skipped a workload never moved it, so a panel promising a move of every entry reports a move that did not happen
  it('promises no move of a workload the lease may have left alone', async () => {
    stubLease(leaseDetailBody({ migrationWarnings: everyReason }))
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    const panel = pillLabelled(view.container, migrationCopy[0][0]).closest('section')
    expect(panel?.textContent).not.toContain('Every workload here still moves')

    await view.unmount()
  })

  it('describes a copy rather than a move on a replicating lease', async () => {
    stubLease(
      leaseDetailBody({
        workloadMode: 'replicate',
        workloadBurstReplicas: 2,
        migrationWarnings: warningsFor(replicationCopy),
      }),
    )
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    const panel = pillLabelled(view.container, replicationCopy[0][0]).closest('section')
    expect(panel?.textContent).toContain('Replication warnings')
    expect(panel?.textContent).toContain('copied onto the leased nodes')
    expect(panel?.textContent).not.toContain('moves onto the leased nodes')

    await view.unmount()
  })

  // replicate mode reports WorkloadReplicable and never WorkloadMigrated, so a page keyed on the move condition reads blank
  it('reads a replicating lease that reports no migration at all', async () => {
    stubLease(
      leaseDetailBody({
        workloadNamespaces: ['batch'],
        workloadMode: 'replicate',
        workloadBurstReplicas: 2,
        migratedWorkloads: ['batch/deployment/api-burst-1a2b3c4d'],
        conditions: [
          {
            type: 'WorkloadReplicable',
            status: 'False',
            reason: 'EveryWorkloadSkipped',
            message: 'batch/deployment/api was not replicated',
            lastTransitionTime: '2026-08-21T11:00:00Z',
          },
        ],
      }),
    )
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    const conditions = [...view.container.querySelectorAll('tbody tr')].find((row) =>
      row.textContent?.includes('WorkloadReplicable'),
    )
    expect(conditions?.textContent).toContain('EveryWorkloadSkipped')
    const shown = view.container.textContent ?? ''
    expect(shown).toContain('Burst copies')
    expect(shown).toContain('batch/deployment/api-burst-1a2b3c4d')
    expect(shown).not.toContain('Migrated workloads')

    await view.unmount()
  })

  // a reason that only reports the refusal leaves nothing to do about it, and move mode is what bursts these workloads
  it('signposts move mode on every workload a copy would break', async () => {
    stubLease(
      leaseDetailBody({
        workloadMode: 'replicate',
        workloadBurstReplicas: 2,
        migrationWarnings: warningsFor(replicationCopy),
      }),
    )
    const view = await mount(<LeaseDetailRoute name={leaseName} />)

    const shown = view.container.textContent ?? ''
    for (const [reason, copy] of replicationCopy) {
      expect(pillLabelled(view.container, reason).dataset.severity).toBe('attention')
      expect(shown).toContain(copy)
    }
    expect(shown).not.toContain('no wording for')

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
