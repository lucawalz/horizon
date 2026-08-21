import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { leasesPath } from '@/lib/api'
import { secondMs } from '@/lib/duration'
import { LeaseListRoute } from '@/routes/lease-list'
import { pollIntervalMs, usePolled } from '@/routes/poll'
import { leaseListBody, leaseSummary, mount, stubFetch } from '@/routes/test-support'

const slowestAllowedMs = 30 * secondMs
const fastestAllowedMs = 15 * secondMs
const clockStart = Date.parse('2026-08-21T12:00:00Z')
const leaseName = 'batch-run'
const unreachable = 'the cluster is unreachable'
const firstKey = '/api/leases/alpha'
const secondKey = '/api/leases/beta'
const firstReading = 'alpha is active'
const nothingHeld = 'nothing held'

async function advance(byMs: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(byMs)
  })
}

describe('the lease list poll', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(clockStart)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('waits between 15 and 30 seconds between reads', () => {
    expect(pollIntervalMs).toBeGreaterThanOrEqual(fastestAllowedMs)
    expect(pollIntervalMs).toBeLessThanOrEqual(slowestAllowedMs)
  })

  it('reads the lease endpoint again once the interval elapses', async () => {
    const respond = stubFetch(leaseListBody([leaseSummary()]))
    const view = await mount(<LeaseListRoute />)

    expect(respond).toHaveBeenCalledTimes(1)
    expect(respond.mock.calls[0][0]).toBe(leasesPath)

    await advance(pollIntervalMs - 1)
    expect(respond).toHaveBeenCalledTimes(1)

    await advance(1)
    expect(respond).toHaveBeenCalledTimes(2)
    expect(respond.mock.calls[1][0]).toBe(leasesPath)

    await view.unmount()
  })

  it('stops reading once the view is gone', async () => {
    const respond = stubFetch(leaseListBody([]))
    const view = await mount(<LeaseListRoute />)

    await view.unmount()
    await advance(pollIntervalMs * 2)

    expect(respond).toHaveBeenCalledTimes(1)
  })

  it('leaves the last answer on screen when a read fails', async () => {
    const respond = stubFetch(leaseListBody([leaseSummary({ name: leaseName })]))
    const view = await mount(<LeaseListRoute />)
    expect(view.container.textContent).toContain(leaseName)

    respond.mockRejectedValueOnce(new Error(unreachable))
    await advance(pollIntervalMs)

    expect(view.container.textContent).toContain(unreachable)
    expect(view.container.textContent).toContain(leaseName)

    await view.unmount()
  })
})

function Reading({ pollKey, read }: { pollKey: string; read: (key: string) => Promise<string> }) {
  const view = usePolled(() => read(pollKey), pollKey)
  return <span>{view.data ?? nothingHeld}</span>
}

describe('a polled view whose key changes', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(clockStart)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('holds nothing until the new key has an answer of its own', async () => {
    const unanswered = new Promise<string>(() => undefined)
    const read = (key: string) => (key === firstKey ? Promise.resolve(firstReading) : unanswered)

    const view = await mount(<Reading pollKey={firstKey} read={read} />)
    expect(view.container.textContent).toBe(firstReading)

    await view.render(<Reading pollKey={secondKey} read={read} />)
    expect(view.container.textContent).toBe(nothingHeld)

    await view.unmount()
  })
})
