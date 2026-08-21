import { ArrowLeft } from 'lucide-react'

import {
  Cell,
  HeadCell,
  Row,
  Table,
  TableBody,
  TableEmpty,
  TableHead,
} from '@/components/data-table'
import type {
  ConditionEntry,
  LeaseDetailResponse,
  LeaseInstance,
  LeaseRequirements,
} from '@/lib/api'
import { fetchLease, leasePath, notFound, RequestFailed } from '@/lib/api'
import { ConditionChip, Countdown, InstancePhaseChip, PhaseChip, Since } from '@/routes/chips'
import {
  Definition,
  DefinitionGrid,
  EmptyState,
  Loading,
  Notice,
  PageHeader,
  Panel,
  Snippet,
} from '@/routes/page'
import type { Polled } from '@/routes/poll'
import { usePolled } from '@/routes/poll'
import { leasesHref } from '@/routes/router'
import { absent, formatInstant, formatSpan } from '@/routes/units'

const conditionColumns = 5
const instanceColumns = 6

function BackLink() {
  return (
    <a
      href={leasesHref}
      className="inline-flex items-center gap-tight rounded-control text-label-13 text-subtle transition-colors hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand"
    >
      <ArrowLeft size={14} strokeWidth={1.5} aria-hidden="true" />
      Leases
    </a>
  )
}

function ExpiryHero({ at, released }: { at: string | null; released: boolean }) {
  return (
    <div className="text-right">
      <div className="text-label-12 text-subtle">{released ? 'Expired' : 'Expires'}</div>
      <Countdown at={at} size="hero" released={released} className="block" />
      <div className="mt-tight text-label-12 text-subtle">
        {at === null ? 'The controller has not accepted this lease yet.' : formatInstant(at)}
      </div>
    </div>
  )
}

function Sizing({ size, requirements }: { size: string | null; requirements: LeaseRequirements | null }) {
  if (size !== null) return <Snippet>{size}</Snippet>
  if (requirements === null) return <span className="text-subtle">{absent}</span>
  return (
    <span>
      {requirements.minCPU} cores or more, {requirements.minMemory ?? 'any memory'},{' '}
      {requirements.architecture}
      {requirements.cpuType === null ? '' : `, ${requirements.cpuType}`}
      {requirements.strategy === null ? '' : `, ${requirements.strategy}`}
    </span>
  )
}

function Stamp({ at }: { at: string | null }) {
  if (at === null) return <span className="text-subtle">{absent}</span>
  return (
    <span className="flex flex-col">
      <Since at={at} />
      <span className="text-label-12 text-subtle">{formatInstant(at)}</span>
    </span>
  )
}

function ConditionsPanel({ conditions }: { conditions: ConditionEntry[] }) {
  return (
    <Panel title="Conditions">
      <Table>
        <TableHead>
          <Row>
            <HeadCell>Condition</HeadCell>
            <HeadCell>Status</HeadCell>
            <HeadCell>Reason</HeadCell>
            <HeadCell>Message</HeadCell>
            <HeadCell numeric>Changed</HeadCell>
          </Row>
        </TableHead>
        <TableBody>
          {conditions.length === 0 ? (
            <TableEmpty span={conditionColumns}>
              The controller has not written a condition for this lease yet.
            </TableEmpty>
          ) : (
            conditions.map((condition) => (
              <Row key={condition.type}>
                <Cell className="font-emphasis text-ink-strong">{condition.type}</Cell>
                <Cell>
                  <ConditionChip type={condition.type} status={condition.status} />
                </Cell>
                <Cell muted>{condition.reason ?? absent}</Cell>
                <Cell muted>
                  <span className="block max-w-[40ch] truncate" title={condition.message ?? undefined}>
                    {condition.message ?? absent}
                  </span>
                </Cell>
                <Cell numeric muted>
                  <Since at={condition.lastTransitionTime} />
                </Cell>
              </Row>
            ))
          )}
        </TableBody>
      </Table>
    </Panel>
  )
}

function InstancesPanel({ instances }: { instances: LeaseInstance[] }) {
  return (
    <Panel title="Instances">
      <Table>
        <TableHead>
          <Row>
            <HeadCell>Instance</HeadCell>
            <HeadCell>Phase</HeadCell>
            <HeadCell>Node</HeadCell>
            <HeadCell>Provider id</HeadCell>
            <HeadCell numeric>Created</HeadCell>
            <HeadCell>Last error</HeadCell>
          </Row>
        </TableHead>
        <TableBody>
          {instances.length === 0 ? (
            <TableEmpty span={instanceColumns}>
              No machine has been claimed for this lease yet.
            </TableEmpty>
          ) : (
            instances.map((instance) => (
              <Row key={instance.name}>
                <Cell className="font-emphasis text-ink-strong">{instance.name}</Cell>
                <Cell>
                  <InstancePhaseChip phase={instance.phase} />
                </Cell>
                <Cell muted>{instance.nodeName ?? 'not joined'}</Cell>
                <Cell muted className="font-mono text-label-12">
                  {instance.providerID ?? absent}
                </Cell>
                <Cell numeric muted>
                  <Since at={instance.createdAt} />
                </Cell>
                <Cell className={instance.lastError === null ? 'text-subtle' : 'text-tint-fg'}>
                  {instance.lastError === null ? 'none' : instance.lastError}
                </Cell>
              </Row>
            ))
          )}
        </TableBody>
      </Table>
    </Panel>
  )
}

function LeaseFacts({ lease }: { lease: LeaseDetailResponse }) {
  const summary = lease.summary
  return (
    <div className="grid gap-gutter lg:grid-cols-2">
      <Panel title="Reservation">
        <DefinitionGrid>
          <Definition label="Provider config">{lease.providerRef}</Definition>
          <Definition label="Region">{summary.region}</Definition>
          <Definition label="Sizing">
            <Sizing size={lease.size} requirements={lease.requirements} />
          </Definition>
          <Definition label="Instance type">
            {summary.instanceType ?? <span className="text-subtle">not chosen yet</span>}
          </Definition>
          <Definition label="Replicas">{summary.replicas}</Definition>
          <Definition label="Duration">{formatSpan(lease.durationSeconds)}</Definition>
          <Definition label="Teardown grace">
            {lease.teardownGraceSeconds === null ? (
              <span className="text-subtle">{absent}</span>
            ) : (
              formatSpan(lease.teardownGraceSeconds)
            )}
          </Definition>
          <Definition label="Observed generation">{lease.observedGeneration}</Definition>
        </DefinitionGrid>
      </Panel>

      <Panel title="Timeline">
        <DefinitionGrid>
          <Definition label="Created">
            <Stamp at={summary.createdAt} />
          </Definition>
          <Definition label="Accepted">
            <Stamp at={lease.acceptedAt} />
          </Definition>
          <Definition label="Instances ready">
            <Stamp at={summary.readyAt} />
          </Definition>
          <Definition label="Released">
            <Stamp at={summary.releasedAt} />
          </Definition>
          <Definition label="Watchdog deadline">
            <Stamp at={lease.watchdogDeadline} />
          </Definition>
          <Definition label="Workload namespace">
            {lease.workloadNamespace ?? <span className="text-subtle">none drained</span>}
          </Definition>
        </DefinitionGrid>
      </Panel>
    </div>
  )
}

function MigratedPanel({ workloads }: { workloads: string[] }) {
  return (
    <Panel title="Migrated workloads">
      <div className="p-gutter">
        {workloads.length === 0 ? (
          <p className="text-copy-13 text-subtle">
            Nothing has been moved off this lease. Workloads appear here once the controller drains
            the leased nodes ahead of teardown.
          </p>
        ) : (
          <ul className="flex flex-wrap gap-snug">
            {workloads.map((workload) => (
              <li key={workload}>
                <Snippet>{workload}</Snippet>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Panel>
  )
}

function MissingLease({ name }: { name: string }) {
  return (
    <EmptyState title="That lease is not in the cluster">
      No capacity lease named {name} exists. It may have been released and garbage collected, or the
      link may be older than the cluster.
    </EmptyState>
  )
}

function LeaseDetailBody({ name, view }: { name: string; view: Polled<LeaseDetailResponse> }) {
  if (view.error instanceof RequestFailed && view.error.status === notFound) {
    return <MissingLease name={name} />
  }
  if (view.data === null) {
    return view.settled ? null : <Loading label="Reading the lease from the cluster." />
  }

  return (
    <div className="space-y-gutter">
      <LeaseFacts lease={view.data} />
      <InstancesPanel instances={view.data.instances} />
      <ConditionsPanel conditions={view.data.conditions} />
      <MigratedPanel workloads={view.data.migratedWorkloads} />
    </div>
  )
}

export function LeaseDetailRoute({ name }: { name: string }) {
  const view = usePolled(() => fetchLease(name), leasePath(name))
  const summary = view.data?.summary ?? null
  const missing = view.error instanceof RequestFailed && view.error.status === notFound

  return (
    <>
      <div className="mb-cell">
        <BackLink />
      </div>
      <PageHeader
        title={name}
        lede={
          summary === null
            ? undefined
            : `Reserved through ${view.data?.providerRef ?? ''} in ${summary.region}.`
        }
        aside={
          missing ? undefined : (
            <>
              <PhaseChip phase={summary?.phase ?? null} />
              <ExpiryHero at={summary?.expiresAt ?? null} released={(summary?.releasedAt ?? null) !== null} />
            </>
          )
        }
      />
      <div className="space-y-gutter">
        {view.error && !missing ? (
          <Notice severity="danger" title="The lease could not be read">
            {view.error.message}
          </Notice>
        ) : null}
        <LeaseDetailBody name={name} view={view} />
      </div>
    </>
  )
}
