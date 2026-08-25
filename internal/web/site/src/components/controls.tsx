import type { ButtonHTMLAttributes, ReactNode } from 'react'

import { cn } from '@/lib/utils'

export const controlClass =
  'h-control rounded-control border border-line bg-elevated px-snug text-label-13 text-ink transition-colors hover:bg-wash focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand'

const actionClass =
  'inline-flex h-control items-center justify-center gap-tight rounded-control px-gutter text-label-13 font-emphasis transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand'

const toneClass = {
  primary: 'bg-brand text-brand-ink hover:opacity-90',
  quiet: 'border border-line bg-elevated text-ink hover:bg-wash',
  danger: 'border border-tint-line bg-tint text-tint-fg hover:opacity-90',
} as const

export type Tone = keyof typeof toneClass

const dangerTone: Tone = 'danger'

function toneAttributes(tone: Tone, className?: string) {
  return {
    'data-severity': tone === dangerTone ? dangerTone : undefined,
    className: cn(actionClass, toneClass[tone], 'disabled:cursor-not-allowed disabled:opacity-60', className),
  }
}

export function Button({
  tone = 'quiet',
  className,
  ...rest
}: { tone?: Tone } & ButtonHTMLAttributes<HTMLButtonElement>) {
  return <button {...rest} {...toneAttributes(tone, className)} />
}

export function ButtonLink({
  href,
  tone = 'quiet',
  className,
  children,
}: {
  href: string
  tone?: Tone
  className?: string
  children: ReactNode
}) {
  return (
    <a href={href} {...toneAttributes(tone, className)}>
      {children}
    </a>
  )
}

export interface Bounds {
  min: number
  max?: number
  initial: number
}

export function Numeric({ name, bounds }: { name: string; bounds: Bounds }) {
  return (
    <input
      name={name}
      type="number"
      required
      min={bounds.min}
      max={bounds.max}
      defaultValue={bounds.initial}
      className={controlClass}
    />
  )
}

export function Field({
  label,
  hint,
  children,
}: {
  label: ReactNode
  hint?: ReactNode
  children: ReactNode
}) {
  return (
    <label className="flex flex-col gap-tight">
      <span className="text-label-12 text-subtle">{label}</span>
      {children}
      {hint ? <span className="text-label-12 text-subtle">{hint}</span> : null}
    </label>
  )
}
