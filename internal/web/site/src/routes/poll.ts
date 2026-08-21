import { useEffect, useRef, useState } from 'react'

import { secondMs } from '@/lib/duration'

// the countdown advances on its own, so the poll only has to observe phase transitions
export const pollIntervalMs = 20 * secondMs

export interface Polled<T> {
  data: T | null
  error: Error | null
  settled: boolean
}

interface Held<T> extends Polled<T> {
  key: string
}

const pending: Polled<never> = { data: null, error: null, settled: false }

function errorFor(cause: unknown): Error {
  return cause instanceof Error ? cause : new Error(String(cause))
}

export function usePolled<T>(load: () => Promise<T>, key: string): Polled<T> {
  const [held, setHeld] = useState<Held<T>>(() => ({ ...pending, key }))
  const latest = useRef(load)

  useEffect(() => {
    latest.current = load
  })

  useEffect(() => {
    let live = true

    const read = async () => {
      try {
        const data = await latest.current()
        if (live) setHeld({ data, error: null, settled: true, key })
      } catch (cause) {
        // a failed poll keeps the last answer on screen rather than blanking a view that was reading fine
        if (live) {
          setHeld((was) => ({
            data: was.key === key ? was.data : null,
            error: errorFor(cause),
            settled: true,
            key,
          }))
        }
      }
    }

    void read()
    const timer = setInterval(() => void read(), pollIntervalMs)
    return () => {
      live = false
      clearInterval(timer)
    }
  }, [key])

  // a key that changed since the last answer has nothing to show yet, so the held one is not its result
  return held.key === key ? held : pending
}
