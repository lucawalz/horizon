import type { ReactNode } from 'react'

import { Button } from '@/components/controls'
import type { Severity } from '@/lib/status'
import { cn } from '@/lib/utils'

export function PageHeader({
  title,
  lede,
  eyebrow,
  aside,
}: {
  title: ReactNode
  lede?: ReactNode
  eyebrow?: ReactNode
  aside?: ReactNode
}) {
  return (
    <div className="mb-section flex flex-wrap items-end justify-between gap-gutter">
      <div className="space-y-tight">
        {eyebrow ? <div className="text-label-12 text-subtle">{eyebrow}</div> : null}
        <h1 className="text-heading-24 text-ink-strong">{title}</h1>
        {lede ? <p className="max-w-[60ch] text-copy-13 text-subtle">{lede}</p> : null}
      </div>
      {aside ? <div className="flex items-end gap-gutter">{aside}</div> : null}
    </div>
  )
}

export function Panel({
  title,
  note,
  children,
  className,
}: {
  title?: ReactNode
  note?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section
      className={cn('overflow-hidden rounded-panel border border-line bg-base shadow-panel', className)}
    >
      {title ? (
        <header className="row-rule flex h-head items-center justify-between gap-gutter bg-elevated px-gutter">
          <h2 className="text-label-12 font-emphasis text-subtle">{title}</h2>
          {note ? <span className="text-label-12 text-subtle">{note}</span> : null}
        </header>
      ) : null}
      {children}
    </section>
  )
}

export function DefinitionGrid({ children }: { children: ReactNode }) {
  return (
    <dl className="grid grid-cols-[repeat(auto-fill,minmax(13rem,1fr))] gap-x-gutter gap-y-cell p-gutter">
      {children}
    </dl>
  )
}

export function Definition({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="space-y-hair">
      <dt className="text-label-12 text-subtle">{label}</dt>
      <dd className="text-label-14 text-ink-strong">{children}</dd>
    </div>
  )
}

export function EmptyState({
  title,
  children,
  action,
}: {
  title: ReactNode
  children: ReactNode
  action?: ReactNode
}) {
  return (
    <div className="rounded-panel border border-dashed border-line bg-base/40 px-gutter py-page text-center">
      <h2 className="text-heading-20 text-ink-strong">{title}</h2>
      <p className="mx-auto mt-snug max-w-[56ch] text-copy-13 text-subtle">{children}</p>
      {action ? <div className="mt-gutter flex justify-center">{action}</div> : null}
    </div>
  )
}

export function Notice({
  severity,
  title,
  children,
}: {
  severity: Severity
  title: ReactNode
  children?: ReactNode
}) {
  return (
    <div
      data-severity={severity}
      role="status"
      className="rounded-panel border border-tint-line bg-tint px-gutter py-cell"
    >
      <p className="text-label-14 font-emphasis text-tint-fg">{title}</p>
      {children ? <p className="mt-tight text-copy-13 text-tint-fg/85">{children}</p> : null}
    </div>
  )
}

export function Loading({ label }: { label: string }) {
  return (
    <p role="status" className="px-gutter py-section text-center text-copy-13 text-subtle">
      {label}
    </p>
  )
}

export function Snippet({ children }: { children: ReactNode }) {
  return (
    <code className="rounded-control bg-recessed px-tight py-hair font-mono text-label-12 text-ink">
      {children}
    </code>
  )
}

export function Prompt({
  severity,
  heading,
  children,
}: {
  severity: Severity
  heading: string
  children: ReactNode
}) {
  return (
    <div
      data-severity={severity}
      className="space-y-cell rounded-panel border border-tint-line bg-tint p-gutter"
    >
      <p className="text-label-14 font-emphasis text-tint-fg">{heading}</p>
      {children}
    </div>
  )
}

export function Confirmation({
  heading,
  confirmLabel,
  pendingLabel,
  declineLabel,
  pending,
  onConfirm,
  onDecline,
  children,
}: {
  heading: string
  confirmLabel: string
  pendingLabel: string
  declineLabel: string
  pending: boolean
  onConfirm: () => void
  onDecline: () => void
  children: ReactNode
}) {
  return (
    <Prompt severity="attention" heading={heading}>
      <p className="max-w-[70ch] text-copy-13 text-tint-fg/85">{children}</p>
      <div className="flex flex-wrap gap-snug">
        <Button type="button" tone="danger" onClick={onConfirm} disabled={pending}>
          {pending ? pendingLabel : confirmLabel}
        </Button>
        <Button type="button" onClick={onDecline} disabled={pending}>
          {declineLabel}
        </Button>
      </div>
    </Prompt>
  )
}
