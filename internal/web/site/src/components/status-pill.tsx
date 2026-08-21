import type { ReactNode } from 'react'

import type { Severity } from '@/lib/status'
import { cn } from '@/lib/utils'

export function StatusPill({
  severity,
  children,
  className,
}: {
  severity: Severity
  children: ReactNode
  className?: string
}) {
  return (
    <span
      data-severity={severity}
      className={cn(
        'inline-flex items-center gap-tight rounded-dot border border-tint-line bg-tint px-snug py-hair text-label-12 font-emphasis text-tint-fg',
        className,
      )}
    >
      <span aria-hidden="true" className="size-dot shrink-0 rounded-dot bg-tint-signal" />
      {children}
    </span>
  )
}
