import { ButtonLink } from '@/components/controls'
import { Cell, HeadCell, Row, Table, TableBody, TableHead } from '@/components/data-table'
import type { LeaseListResponse, LeaseSummary } from '@/lib/api'
import { fetchLeases, leasesPath } from '@/lib/api'
import type { Severity } from '@/lib/status'
import { severityForStatus } from '@/lib/status'
import { ConditionChip, Countdown, PhaseChip, Since } from '@/routes/chips'
import { EmptyState, Loading, Notice, PageHeader } from '@/routes/page'
import type { Polled } from '@/routes/poll'
import { usePolled } from '@/routes/poll'
import { leaseHref, newLeaseHref } from '@/routes/router'

const readyCondition = 'InstancesReady'
const armedCondition = 'WatchdogArmed'
const railSeverities: Severity[] = ['attention', 'danger']
const noInstanceType = 'not chosen yet'

function rowRail(lease: LeaseSummary): Severity | undefined {
  if (lease.phase === null) return undefined
  const severity = severityForStatus(lease.phase)
  return railSeverities.includes(severity) ? severity : undefined
}

function LeaseRow({ lease }: { lease: LeaseSummary }) {
  return (
    <Row rail={rowRail(lease)} interactive>
      <Cell className="text-label-14">
        <a
          href={leaseHref(lease.name)}
          className="font-emphasis text-ink-strong underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand"
        >
          {lease.name}
        </a>
      </Cell>
      <Cell numeric>
        <Countdown at={lease.expiresAt} size="lead" released={lease.releasedAt !== null} />
      </Cell>
      <Cell>
        <PhaseChip phase={lease.phase} />
      </Cell>
      <Cell>
        <ConditionChip type={readyCondition} status={lease.ready} />
      </Cell>
      <Cell>
        <ConditionChip type={armedCondition} status={lease.armed} />
      </Cell>
      <Cell muted>{lease.region}</Cell>
      <Cell muted>{lease.instanceType ?? noInstanceType}</Cell>
      <Cell numeric>{lease.replicas}</Cell>
      <Cell numeric muted>
        <Since at={lease.createdAt} />
      </Cell>
    </Row>
  )
}

function LeaseTable({ leases }: { leases: LeaseSummary[] }) {
  return (
    <Table>
      <TableHead>
        <Row>
          <HeadCell>Lease</HeadCell>
          <HeadCell numeric>Expiry</HeadCell>
          <HeadCell>Phase</HeadCell>
          <HeadCell>Instances ready</HeadCell>
          <HeadCell>Watchdog armed</HeadCell>
          <HeadCell>Region</HeadCell>
          <HeadCell>Instance type</HeadCell>
          <HeadCell numeric>Replicas</HeadCell>
          <HeadCell numeric>Created</HeadCell>
        </Row>
      </TableHead>
      <TableBody>
        {leases.map((lease) => (
          <LeaseRow key={lease.name} lease={lease} />
        ))}
      </TableBody>
    </Table>
  )
}

function NoLeases() {
  return (
    <EmptyState
      title="No capacity lease exists yet"
      action={
        <ButtonLink href={newLeaseHref} tone="primary">
          New lease
        </ButtonLink>
      }
    >
      A lease describes the capacity a run needs and how long it may keep it. As soon as the
      controller accepts one it appears here, counting down to the moment its machines are released.
    </EmptyState>
  )
}

function LeaseListBody({ view }: { view: Polled<LeaseListResponse> }) {
  if (view.data === null) {
    return view.settled ? null : <Loading label="Reading the leases from the cluster." />
  }
  return view.data.leases.length === 0 ? <NoLeases /> : <LeaseTable leases={view.data.leases} />
}

export function LeaseListRoute() {
  const view = usePolled(fetchLeases, leasesPath)

  return (
    <>
      <PageHeader
        title="Leases"
        lede="Every capacity lease the cluster holds, and how long each one has left before its machines are released."
        aside={
          <ButtonLink href={newLeaseHref} tone="primary">
            New lease
          </ButtonLink>
        }
      />
      <div className="space-y-gutter">
        {view.error ? (
          <Notice severity="danger" title="The leases could not be read">
            {view.error.message}
          </Notice>
        ) : null}
        <LeaseListBody view={view} />
      </div>
    </>
  )
}
