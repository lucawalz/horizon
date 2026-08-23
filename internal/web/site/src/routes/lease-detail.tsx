import { ArrowLeft } from 'lucide-react'
import type { ReactNode } from 'react'
import { useState } from 'react'

import { Button } from '@/components/controls'
import {
  Cell,
  HeadCell,
  Row,
  Table,
  TableBody,
  TableEmpty,
  TableHead,
} from '@/components/data-table'
import { StatusPill } from '@/components/status-pill'
import type {
  ConditionEntry,
  LeaseDetailResponse,
  LeaseInstance,
  LeaseReleaseResponse,
  LeaseRequirements,
  LeaseSelection,
} from '@/lib/api'
import { deleteLease, fetchLease, leasePath, notFound, RequestFailed } from '@/lib/api'
import { errorFor } from '@/lib/errors'
import {
  ConditionChip,
  Countdown,
  InstancePhaseChip,
  InstanceStageChip,
  PhaseChip,
  Since,
} from '@/routes/chips'
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
import { leasesHref, navigate } from '@/routes/router'
import { absent, formatInstant, formatRate, formatSpan } from '@/routes/units'

const conditionColumns = 5
const instanceColumns = 7

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
  )
}

function InstancesPanel({ instances }: { instances: LeaseInstance[] }) {
  return (
    <Table>
      <TableHead>
        <Row>
          <HeadCell>Instance</HeadCell>
          <HeadCell>Phase</HeadCell>
          <HeadCell>Stage</HeadCell>
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
              <Cell>
                <InstanceStageChip stage={instance.stage} />
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
  )
}

function HourlyRate({ selection }: { selection: LeaseSelection }) {
  if (selection.hourlyRate === null) return null

  const amount = Number(selection.hourlyRate)
  const quoted =
    selection.currency !== null && Number.isFinite(amount)
      ? formatRate({ amount, currency: selection.currency })
      : selection.hourlyRate
  return <span className="text-subtle"> at {quoted} an hour</span>
}

function RejectionTally({ rejected }: { rejected: LeaseSelection['rejected'] }) {
  if (rejected.length === 0) {
    return <span className="text-subtle">every candidate qualified</span>
  }
  return (
    <span className="flex flex-wrap gap-tight">
      {rejected.map((candidates) => (
        <StatusPill key={candidates.reason} severity="neutral">
          {candidates.count} {candidates.reason}
        </StatusPill>
      ))}
    </span>
  )
}

// a lease that named its own machine type was never sized by a policy, so the absence is the answer rather than a gap
function SelectionPanel({
  selection,
  size,
}: {
  selection: LeaseSelection | null
  size: string | null
}) {
  if (selection === null) {
    return (
      <Panel title="Selection">
        <p className="px-gutter py-section text-center text-copy-13 text-subtle">
          {size === null
            ? 'The controller has not chosen a machine type for this lease yet. It records the choice, and what it beat, as soon as the lease is accepted.'
            : `This lease named ${size} itself, so no policy chose it and there is nothing to explain.`}
        </p>
      </Panel>
    )
  }

  return (
    <Panel
      title="Selection"
      note={
        <span>
          Decided <Since at={selection.decidedAt} />
        </span>
      }
    >
      <DefinitionGrid>
        <Definition label="Strategy">{selection.strategy}</Definition>
        <Definition label="Chosen">
          {selection.chosen}
          <HourlyRate selection={selection} />
        </Definition>
        <Definition label="Runner up">
          {selection.runnerUp ?? <span className="text-subtle">nothing else qualified</span>}
        </Definition>
        <Definition label="Offered">{selection.offered}</Definition>
        <Definition label="Qualified">{selection.qualified}</Definition>
        <Definition label="Rejected">
          <RejectionTally rejected={selection.rejected} />
        </Definition>
      </DefinitionGrid>
    </Panel>
  )
}

function Confirmation({
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
    <div
      data-severity="attention"
      className="space-y-cell rounded-panel border border-tint-line bg-tint p-gutter"
    >
      <p className="text-label-14 font-emphasis text-tint-fg">{heading}</p>
      <p className="max-w-[70ch] text-copy-13 text-tint-fg/85">{children}</p>
      <div className="flex flex-wrap gap-snug">
        <Button type="button" tone="danger" onClick={onConfirm} disabled={pending}>
          {pending ? pendingLabel : confirmLabel}
        </Button>
        <Button type="button" onClick={onDecline} disabled={pending}>
          {declineLabel}
        </Button>
      </div>
    </div>
  )
}

interface Deletion {
  confirming: boolean
  pending: boolean
  failure: Error | null
  ask: () => void
  decline: () => void
  confirm: () => void
}

function useLeaseDeletion(name: string, onDeleted: (answer: LeaseReleaseResponse) => void): Deletion {
  const [confirming, setConfirming] = useState(false)
  const [pending, setPending] = useState(false)
  const [failure, setFailure] = useState<Error | null>(null)

  const confirm = async () => {
    setPending(true)
    setFailure(null)
    try {
      onDeleted(await deleteLease(name))
    } catch (cause) {
      setFailure(errorFor(cause))
    }
    setPending(false)
  }

  return {
    confirming,
    pending,
    failure,
    ask: () => setConfirming(true),
    decline: () => setConfirming(false),
    confirm,
  }
}

function ReleasePanel({ name }: { name: string }) {
  // the lease stays in the cluster while its finalizer runs, so the acknowledgement has to come from the answer rather than from the lease disappearing
  const [asked, setAsked] = useState<LeaseReleaseResponse | null>(null)
  const release = useLeaseDeletion(name, setAsked)

  return (
    <Panel title="Release">
      <div className="space-y-cell p-gutter">
        <p className="max-w-[70ch] text-copy-13 text-subtle">
          Releasing deletes the CapacityLease, which is how the controller is asked for a teardown.
          The interface destroys nothing itself.
        </p>
        {asked === null ? (
          release.confirming ? (
            <Confirmation
              heading={`Release ${name}`}
              confirmLabel="Ask the controller to release"
              pendingLabel="Asking the controller"
              declineLabel="Keep the lease"
              pending={release.pending}
              onConfirm={release.confirm}
              onDecline={release.decline}
            >
              The controller is one clock and this starts it: it drains the leased nodes, deletes
              their machines at the provider and removes the lease when its finalizer completes. The
              watchdog on each leased node is the second clock and it is already running. It powers
              its node off at its own deadline whether or not the controller acts, so nothing here is
              what destroys a machine.
            </Confirmation>
          ) : (
            <Button type="button" tone="danger" onClick={release.ask}>
              Release this lease
            </Button>
          )
        ) : (
          <Notice severity="info" title={`The controller was asked to release ${name}`}>
            {asked.detail}
          </Notice>
        )}
        {release.failure === null ? null : (
          <Notice severity="danger" title="The lease was not released">
            {release.failure.message}
          </Notice>
        )}
      </div>
    </Panel>
  )
}

function RecordPanel({ name }: { name: string }) {
  const removal = useLeaseDeletion(name, () => navigate(leasesHref))

  return (
    <Panel title="Record">
      <div className="space-y-cell p-gutter">
        <p className="max-w-[70ch] text-copy-13 text-subtle">
          This lease has been released. Its machines are gone and the controller has nothing left to
          tear down, so what remains is the record of it in the cluster.
        </p>
        {removal.confirming ? (
          <Confirmation
            heading={`Delete the record of ${name}`}
            confirmLabel="Delete the record"
            pendingLabel="Deleting the record"
            declineLabel="Keep the record"
            pending={removal.pending}
            onConfirm={removal.confirm}
            onDecline={removal.decline}
          >
            Nothing is destroyed here. Releasing a running lease is what destroys machines, and this
            lease has none left: the record is all that is still in the cluster. Deleting removes
            that record and its entry from the lease list, and it does not come back.
          </Confirmation>
        ) : (
          <Button type="button" tone="danger" onClick={removal.ask}>
            Delete this record
          </Button>
        )}
        {removal.failure === null ? null : (
          <Notice severity="danger" title="The record was not deleted">
            {removal.failure.message}
          </Notice>
        )}
      </div>
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
      <SelectionPanel selection={view.data.selection} size={view.data.size} />
      <InstancesPanel instances={view.data.instances} />
      <ConditionsPanel conditions={view.data.conditions} />
      <MigratedPanel workloads={view.data.migratedWorkloads} />
      {view.data.summary.releasedAt === null ? (
        <ReleasePanel name={name} />
      ) : (
        <RecordPanel name={name} />
      )}
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
