import { useCallback, useEffect, useRef, useState } from 'react'

import { secondMs } from '@/lib/duration'
import { errorFor } from '@/lib/errors'

// the countdown advances on its own, so the poll only has to observe phase transitions
export const pollIntervalMs = 20 * secondMs

interface Answer<T> {
  data: T | null
  error: Error | null
  settled: boolean
}

export interface Polled<T> extends Answer<T> {
  reload: () => void
}

interface Held<T> extends Answer<T> {
  key: string
}

const pending: Answer<never> = { data: null, error: null, settled: false }

export function usePolled<T>(load: () => Promise<T>, key: string): Polled<T> {
  const [held, setHeld] = useState<Held<T>>(() => ({ ...pending, key }))
  const [attempt, setAttempt] = useState(0)
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
  }, [key, attempt])

  // a mutation lands before the next interval, so the view that made it asks for the answer rather than waiting
  const reload = useCallback(() => setAttempt((was) => was + 1), [])
  // a key that changed since the last answer has nothing to show yet, so the held one is not its result
  const answer = held.key === key ? held : pending

  return { data: answer.data, error: answer.error, settled: answer.settled, reload }
}
