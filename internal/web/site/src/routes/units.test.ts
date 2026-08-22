import { describe, expect, it } from 'vitest'

import { bytesFor, memoryUnits, quantityFor } from '@/routes/units'

const gigabyte = 4_000_000_000
const gibibyte = 4 * 1024 * 1024 * 1024

describe('a memory requirement', () => {
  it('resolves a decimal suffix to powers of a thousand', () => {
    expect(bytesFor(4, 'G')).toBe(gigabyte)
    expect(bytesFor(512, 'M')).toBe(512_000_000)
  })

  it('resolves a binary suffix to powers of 1024', () => {
    expect(bytesFor(4, 'Gi')).toBe(gibibyte)
    expect(bytesFor(512, 'Mi')).toBe(512 * 1024 * 1024)
  })

  // the two suffixes differ by 7 percent, which is the difference between offering a 4 GB machine and excluding it
  it('keeps the decimal and binary readings apart', () => {
    expect(bytesFor(4, 'Gi')).toBeGreaterThan(bytesFor(4, 'G'))
  })

  it('reads nothing from a suffix it does not offer', () => {
    expect(bytesFor(4, 'GB')).toBe(0)
  })

  it('writes the quantity the apiserver parses', () => {
    expect(quantityFor(4, 'Gi')).toBe('4Gi')
    expect(quantityFor(4, 'G')).toBe('4G')
  })

  it('offers a decimal unit first, since that is how a provider quotes memory', () => {
    expect(memoryUnits[0].suffix).toBe('G')
  })
})
