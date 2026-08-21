import type { ReactNode } from 'react'

import type { Severity } from '@/lib/status'
import { cn } from '@/lib/utils'

const cellRhythm = 'row-rule px-cell first:pl-gutter last:pr-gutter first:[--rail:var(--tint-signal,transparent)]'

export function Table({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className="overflow-x-auto rounded-panel border border-line bg-base shadow-panel">
      <table className={cn('w-full min-w-max border-separate border-spacing-0', className)}>
        {children}
      </table>
    </div>
  )
}

export function TableHead({ children }: { children: ReactNode }) {
  return <thead className="bg-elevated">{children}</thead>
}

export function TableBody({ children }: { children: ReactNode }) {
  return (
    <tbody className="[&>tr:last-child>*]:[--hairline:transparent]">{children}</tbody>
  )
}

export function HeadCell({
  numeric,
  children,
  className,
}: {
  numeric?: boolean
  children?: ReactNode
  className?: string
}) {
  return (
    <th
      scope="col"
      className={cn(
        cellRhythm,
        'h-head text-left align-middle text-label-12 font-emphasis text-subtle',
        numeric && 'text-right',
        className,
      )}
    >
      {children}
    </th>
  )
}

export function Row({
  rail,
  interactive,
  children,
  className,
}: {
  rail?: Severity
  interactive?: boolean
  children: ReactNode
  className?: string
}) {
  return (
    <tr
      data-severity={rail}
      className={cn(interactive && 'transition-colors hover:bg-wash', className)}
    >
      {children}
    </tr>
  )
}

export function Cell({
  numeric,
  muted,
  children,
  className,
}: {
  numeric?: boolean
  muted?: boolean
  children?: ReactNode
  className?: string
}) {
  return (
    <td
      className={cn(
        cellRhythm,
        'h-row align-middle text-copy-13',
        numeric && 'text-right',
        muted && 'text-subtle',
        className,
      )}
    >
      {children}
    </td>
  )
}

export function TableEmpty({ span, children }: { span: number; children: ReactNode }) {
  return (
    <tr>
      <td colSpan={span} className="px-gutter py-section text-center text-copy-13 text-subtle">
        {children}
      </td>
    </tr>
  )
}
