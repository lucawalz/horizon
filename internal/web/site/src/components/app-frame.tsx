import type { ReactNode } from 'react'

import { ThemeToggle } from '@/components/theme-toggle'
import { cn } from '@/lib/utils'

function HorizonMark() {
  return (
    <svg
      viewBox="0 0 20 20"
      width="18"
      height="18"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      aria-hidden="true"
    >
      <path d="M2 13.25h16" />
      <path d="M5.75 13.25a4.25 4.25 0 0 1 8.5 0" />
      <path d="M10 4v1.75M5.05 6.05l1.25 1.25M14.95 6.05 13.7 7.3" />
    </svg>
  )
}

export function AppFrame({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('min-h-dvh bg-canvas text-ink', className)}>{children}</div>
}

export function AppHeader({ children }: { children?: ReactNode }) {
  return (
    <header className="sticky top-0 z-10 row-rule bg-elevated/85 backdrop-blur-sm">
      <div className="mx-auto flex h-bar max-w-measure items-center gap-gutter px-gutter">
        <a
          href="/"
          className="flex items-center gap-snug rounded-control text-label-14 font-emphasis text-ink-strong focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-brand"
        >
          <span className="text-brand">
            <HorizonMark />
          </span>
          horizon
        </a>
        <nav className="flex items-center gap-hair">{children}</nav>
        <ThemeToggle className="ml-auto" />
      </div>
    </header>
  )
}

export function NavLink({
  href,
  current,
  children,
}: {
  href: string
  current?: boolean
  children: ReactNode
}) {
  return (
    <a
      href={href}
      aria-current={current ? 'page' : undefined}
      className="rounded-control px-snug py-tight text-label-13 text-subtle transition-colors hover:bg-wash hover:text-ink aria-[current=page]:bg-brand-wash aria-[current=page]:text-ink-strong focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand"
    >
      {children}
    </a>
  )
}

export function AppMain({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <main className={cn('mx-auto w-full max-w-measure px-gutter py-section', className)}>
      {children}
    </main>
  )
}
