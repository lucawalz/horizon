import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { minuteMs, secondMs } from '@/lib/duration'
import { Countdown } from '@/routes/chips'
import { LeaseListRoute } from '@/routes/lease-list'
import { leaseListBody, leaseSummary, mount, stubFetch } from '@/routes/test-support'

const clockStart = Date.parse('2026-08-21T12:00:00Z')
const distantMs = 30 * minuteMs
const closeMs = 3 * minuteMs
const overdueMs = 1 * minuteMs
const tickMs = 5 * secondMs

function at(offsetMs: number): string {
  return new Date(clockStart + offsetMs).toISOString()
}

function countdownIn(container: HTMLElement): HTMLTimeElement {
  const element = container.querySelector('time')
  if (element === null) throw new Error('the countdown rendered no time element')
  return element
}

describe('the expiry countdown', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(clockStart)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('reads neutral while the deadline is distant', async () => {
    const view = await mount(<Countdown at={at(distantMs)} size="lead" />)
    const countdown = countdownIn(view.container)

    expect(countdown.dateTime).toBe(at(distantMs))
    expect(countdown.dataset.severity).toBe('neutral')
    expect(countdown.textContent).toBe('30:00')

    await view.unmount()
  })

  it('ramps to amber inside the last few minutes', async () => {
    const view = await mount(<Countdown at={at(closeMs)} size="lead" />)

    expect(countdownIn(view.container).dataset.severity).toBe('attention')

    await view.unmount()
  })

  it('reads red once the deadline has passed', async () => {
    const view = await mount(<Countdown at={at(-overdueMs)} size="lead" />)
    const countdown = countdownIn(view.container)

    expect(countdown.dataset.severity).toBe('danger')
    expect(countdown.textContent).toBe('1m ago')

    await view.unmount()
  })

  it('advances between polls without reading the cluster again', async () => {
    const respond = stubFetch(leaseListBody([leaseSummary({ expiresAt: at(distantMs) })]))
    const view = await mount(<LeaseListRoute />)

    expect(countdownIn(view.container).textContent).toBe('30:00')
    expect(respond).toHaveBeenCalledTimes(1)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(tickMs)
    })

    expect(countdownIn(view.container).textContent).toBe('29:55')
    expect(respond).toHaveBeenCalledTimes(1)

    await view.unmount()
  })
})
