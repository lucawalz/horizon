import type { Severity } from '@/lib/status'

export const secondMs = 1000
export const minuteMs = 60 * secondMs
export const hourMs = 60 * minuteMs
export const dayMs = 24 * hourMs

const attentionThresholdMs = 5 * minuteMs

export function severityForRemaining(remainingMs: number): Severity {
  if (remainingMs <= 0) return 'danger'
  if (remainingMs <= attentionThresholdMs) return 'attention'
  return 'neutral'
}

function pad(value: number): string {
  return String(value).padStart(2, '0')
}

function formatRemaining(remainingMs: number): string {
  const days = Math.floor(remainingMs / dayMs)
  const hours = Math.floor((remainingMs % dayMs) / hourMs)
  const minutes = Math.floor((remainingMs % hourMs) / minuteMs)
  const seconds = Math.floor((remainingMs % minuteMs) / secondMs)

  const clock =
    days > 0 || hours > 0
      ? `${pad(hours)}:${pad(minutes)}:${pad(seconds)}`
      : `${pad(minutes)}:${pad(seconds)}`

  return days > 0 ? `${days}d ${clock}` : clock
}

function formatElapsed(elapsedMs: number): string {
  if (elapsedMs < minuteMs) return 'just now'
  if (elapsedMs < hourMs) return `${Math.floor(elapsedMs / minuteMs)}m ago`
  if (elapsedMs < dayMs) return `${Math.floor(elapsedMs / hourMs)}h ago`
  return `${Math.floor(elapsedMs / dayMs)}d ago`
}

export function formatDelta(deltaMs: number): string {
  return deltaMs > 0 ? formatRemaining(deltaMs) : formatElapsed(-deltaMs)
}
