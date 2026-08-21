import { EmptyState, PageHeader, Snippet } from '@/routes/page'
import { leasesHref } from '@/routes/router'

export function UnknownRoute({ path }: { path: string }) {
  return (
    <>
      <PageHeader title="Nothing is served here" />
      <EmptyState
        title={<Snippet>{path}</Snippet>}
        action={
          <a
            href={leasesHref}
            className="text-label-13 font-emphasis text-brand underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand"
          >
            Back to the leases
          </a>
        }
      >
        The interface serves the lease list, one page per lease, and the machine catalogue. That
        path is none of them.
      </EmptyState>
    </>
  )
}
