import type { Money } from '@/lib/api'

const binaryStep = 1024
const decimalStep = 1000
const binaryUnits = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
const compactCeiling = 10
const rateDigits = 4

export const secondsPerMinute = 60
const secondsPerHour = 60 * secondsPerMinute

export const absent = 'not set'

export interface MemoryUnit {
  suffix: string
  label: string
  bytes: number
}

// a provider quotes memory in decimal units, so a binary suffix asks for more than the number in a machine's own quote offers
export const memoryUnits: MemoryUnit[] = [
  { suffix: 'G', label: 'GB (1000^3 bytes)', bytes: decimalStep ** 3 },
  { suffix: 'Gi', label: 'GiB (1024^3 bytes)', bytes: binaryStep ** 3 },
  { suffix: 'M', label: 'MB (1000^2 bytes)', bytes: decimalStep ** 2 },
  { suffix: 'Mi', label: 'MiB (1024^2 bytes)', bytes: binaryStep ** 2 },
]

export function quantityFor(value: number, suffix: string): string {
  return `${value}${suffix}`
}

export function bytesFor(value: number, suffix: string): number {
  const unit = memoryUnits.find((one) => one.suffix === suffix)
  return unit === undefined ? 0 : value * unit.bytes
}

export function formatCount(count: number): string {
  return new Intl.NumberFormat().format(count)
}

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
