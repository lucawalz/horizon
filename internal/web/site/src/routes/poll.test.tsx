import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { leasesPath } from '@/lib/api'
import { secondMs } from '@/lib/duration'
import { LeaseListRoute } from '@/routes/lease-list'
import { pollIntervalMs } from '@/routes/poll'
import { leaseListBody, leaseSummary, mount, stubFetch } from '@/routes/test-support'

const slowestAllowedMs = 30 * secondMs
const fastestAllowedMs = 15 * secondMs
const clockStart = Date.parse('2026-08-21T12:00:00Z')

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
})
