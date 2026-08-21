import type { Money } from '@/lib/api'

const binaryStep = 1024
const binaryUnits = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
const compactCeiling = 10
const rateDigits = 4

const secondsPerMinute = 60
const secondsPerHour = 60 * secondsPerMinute

export const absent = 'not set'

export function formatBytes(bytes: number): string {
  let value = bytes
  let unit = 0
  while (value >= binaryStep && unit < binaryUnits.length - 1) {
    value /= binaryStep
    unit += 1
  }
  const digits = value < compactCeiling && !Number.isInteger(value) ? 1 : 0
  return `${value.toFixed(digits)} ${binaryUnits[unit]}`
}

export function formatSpan(totalSeconds: number): string {
  const hours = Math.floor(totalSeconds / secondsPerHour)
  const minutes = Math.floor((totalSeconds % secondsPerHour) / secondsPerMinute)
  const seconds = totalSeconds % secondsPerMinute

  const parts: string[] = []
  if (hours > 0) parts.push(`${hours}h`)
  if (minutes > 0) parts.push(`${minutes}m`)
  if (seconds > 0 || parts.length === 0) parts.push(`${seconds}s`)
  return parts.join(' ')
}

// an unrecognised currency code makes Intl throw, and a price is still worth showing without its symbol
export function formatRate(rate: Money): string {
  try {
    return new Intl.NumberFormat(undefined, {
      style: 'currency',
      currency: rate.currency,
      minimumFractionDigits: rateDigits,
      maximumFractionDigits: rateDigits,
    }).format(rate.amount)
  } catch {
    return `${rate.amount.toFixed(rateDigits)} ${rate.currency}`
  }
}

export function formatInstant(at: string): string {
  const parsed = Date.parse(at)
  if (Number.isNaN(parsed)) return at
  return new Date(parsed).toLocaleString()
}
