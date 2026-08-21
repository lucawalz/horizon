import { useEffect, useState } from 'react'

import { formatDelta, minuteMs, secondMs, severityForRemaining } from '@/lib/duration'
import { cn } from '@/lib/utils'

const sizes = {
  quiet: { type: 'text-label-13 font-emphasis', neutral: 'text-subtle' },
  lead: { type: 'text-heading-20', neutral: 'text-ink-strong' },
  hero: { type: 'text-display-48', neutral: 'text-ink-strong' },
} as const

function useNow(target: number): number {
  const [now, setNow] = useState(() => Date.now())
  const cadence = target > now ? secondMs : minuteMs

  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), cadence)
    return () => clearInterval(timer)
  }, [cadence])

  return now
}

export function RelativeTime({
  at,
  size = 'quiet',
  deadline = false,
  className,
}: {
  at: string
  size?: keyof typeof sizes
  deadline?: boolean
  className?: string
}) {
  const target = Date.parse(at)
  const now = useNow(target)

  if (Number.isNaN(target)) {
    return <span className={cn('text-subtle', className)}>unknown</span>
  }

  const remaining = target - now
  const severity = deadline ? severityForRemaining(remaining) : 'neutral'
  const scale = sizes[size]

  return (
    <time
      dateTime={at}
      title={at}
      data-severity={severity}
      className={cn(
        scale.type,
        severity === 'neutral' ? scale.neutral : 'text-tint-fg',
        className,
      )}
    >
      {formatDelta(remaining)}
    </time>
  )
}
