import { RelativeTime } from '@/components/relative-time'
import { StatusPill } from '@/components/status-pill'
import type { ConditionStatus, InstancePhase, LeasePhase } from '@/lib/api'
import type { Severity } from '@/lib/status'
import { severityForStatus } from '@/lib/status'
import { cn } from '@/lib/utils'

const unreported = 'not reported'
const noDeadline = 'no expiry recorded'

// a positive condition reads well as met or unmet, and these two are the ones whose true is the bad news
const invertedConditions = new Set(['Degraded', 'Expired'])

const instancePhaseSeverity: Record<string, Severity> = {
  intended: 'neutral',
  created: 'info',
  joined: 'success',
  draining: 'attention',
  released: 'neutral',
}

const absentScale = {
  lead: 'text-label-13',
  hero: 'text-heading-20',
} as const

function severityForCondition(type: string, status: ConditionStatus): Severity {
  if (status === 'Unknown') return 'attention'
  const met = status === 'True'
  return (invertedConditions.has(type) ? !met : met) ? 'success' : 'danger'
}

export function PhaseChip({ phase }: { phase: LeasePhase | null }) {
  if (phase === null) return <span className="text-subtle">{unreported}</span>
  return <StatusPill severity={severityForStatus(phase)}>{phase}</StatusPill>
}

export function InstancePhaseChip({ phase }: { phase: InstancePhase }) {
  return (
    <StatusPill severity={instancePhaseSeverity[phase.toLowerCase()] ?? 'neutral'}>
      {phase}
    </StatusPill>
  )
}

export function ConditionChip({
  type,
  status,
}: {
  type: string
  status: ConditionStatus | null
}) {
  if (status === null) return <span className="text-subtle">{unreported}</span>
  return <StatusPill severity={severityForCondition(type, status)}>{status}</StatusPill>
}

// a released lease met its deadline, so its expiry is history rather than a countdown still running down
export function Countdown({
  at,
  size,
  released = false,
  className,
}: {
  at: string | null
  size: keyof typeof absentScale
  released?: boolean
  className?: string
}) {
  if (at === null) {
    return <span className={cn(absentScale[size], 'text-subtle', className)}>{noDeadline}</span>
  }
  return <RelativeTime at={at} size={size} deadline={!released} className={className} />
}

export function Since({ at }: { at: string | null }) {
  if (at === null) return <span className="text-subtle">{unreported}</span>
  return <RelativeTime at={at} />
}
