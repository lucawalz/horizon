import { ArrowLeft } from 'lucide-react'
import type { FormEvent, ReactNode } from 'react'
import { useState } from 'react'

import { Button, controlClass } from '@/components/controls'
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
  ConditionStatus,
  LeaseDetailResponse,
  LeaseExtendResponse,
  LeaseInstance,
  LeaseReleaseResponse,
  LeaseRequirements,
  LeaseSelection,
} from '@/lib/api'
import {
  badRequest,
  conflict,
  deleteLease,
  extendLease,
  fetchLease,
  leasePath,
  notFound,
  RequestFailed,
  unprocessable,
} from '@/lib/api'
import { minuteMs } from '@/lib/duration'
import { errorFor } from '@/lib/errors'
import { numberValue } from '@/lib/form'
import type { Severity } from '@/lib/status'
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
import {
  absent,
  formatInstant,
  formatRate,
  formatSpan,
  leaseMinutes,
  secondsPerMinute,
} from '@/routes/units'

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

function ExpiryHero({
  at,
  released,
  extension,
}: {
  at: string | null
  released: boolean
  extension: ReactNode
}) {
  return (
    <div className="flex flex-col items-end gap-cell text-right">
      <div>
        <div className="text-label-12 text-subtle">{released ? 'Expired' : 'Expires'}</div>
        <Countdown at={at} size="hero" released={released} className="block" />
        <div className="mt-tight text-label-12 text-subtle">
          {at === null ? 'The controller has not accepted this lease yet.' : formatInstant(at)}
        </div>
      </div>
      {extension}
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

function Prompt({
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

const expiryClamped = 'ExpiryClamped'
const extensionField = 'minutes'
const extensionStepMinutes = 30

const extensionTitles: Record<number, string> = {
  [badRequest]: 'That is not a duration this interface can submit',
  [conflict]: 'The lease changed while this extension was in flight',
  [unprocessable]: 'The cluster refused this extension',
}

const extensionRefused = 'The lease was not extended'

interface ExtensionBounds {
  min: number
  max: number
  initial: number
  ceiling: string
}

function conditionOf(conditions: ConditionEntry[], type: string): ConditionStatus | null {
  return conditions.find((condition) => condition.type === type)?.status ?? null
}

function backstopMinutes(lease: LeaseDetailResponse): number | null {
  if (lease.backstopAt === null || lease.acceptedAt === null) return null
  const held = Date.parse(lease.backstopAt) - Date.parse(lease.acceptedAt)
  return Number.isNaN(held) ? null : Math.floor(held / minuteMs)
}

function ceilingNote(lease: LeaseDetailResponse, backstop: number | null, max: number): string {
  const longest = `Up to ${formatSpan(max * secondsPerMinute)}`
  if (backstop !== null) {
    return backstop < leaseMinutes.max
      ? `${longest}, when the earliest leased machine destroys itself.`
      : `${longest}, the longest a lease may run.`
  }
  if (conditionOf(lease.conditions, expiryClamped) === 'Unknown') {
    return `${longest}. No leased machine records a lifetime backstop yet, and a deadline past one is held there.`
  }
  return `${longest}. This lease holds no machine yet, so nothing else caps the deadline.`
}

function extensionBounds(lease: LeaseDetailResponse): ExtensionBounds {
  const held = Math.floor(lease.durationSeconds / secondsPerMinute)
  const backstop = backstopMinutes(lease)
  const max = backstop === null ? leaseMinutes.max : Math.min(leaseMinutes.max, backstop)
  const min = Math.max(leaseMinutes.min, held + 1)

  return {
    min,
    max,
    initial: Math.min(max, Math.max(min, held + extensionStepMinutes)),
    ceiling: ceilingNote(lease, backstop, max),
  }
}

function refusalNotice(failure: Error): { severity: Severity; title: string } {
  if (!(failure instanceof RequestFailed)) return { severity: 'danger', title: extensionRefused }
  return {
    // a raced write is the same request measured against a lease that has since moved, so it is worth retrying rather than reporting as a refusal
    severity: failure.status === conflict ? 'attention' : 'danger',
    title: extensionTitles[failure.status] ?? extensionRefused,
  }
}

interface Extension {
  asking: boolean
  pending: boolean
  failure: Error | null
  answer: LeaseExtendResponse | null
  ask: () => void
  decline: () => void
  submit: (minutes: number) => void
}

function useLeaseExtension(name: string, onExtended: () => void): Extension {
  const [asking, setAsking] = useState(false)
  const [pending, setPending] = useState(false)
  const [failure, setFailure] = useState<Error | null>(null)
  const [answer, setAnswer] = useState<LeaseExtendResponse | null>(null)

  const submit = async (minutes: number) => {
    setPending(true)
    setFailure(null)
    try {
      setAnswer(await extendLease(name, minutes * secondsPerMinute))
      setAsking(false)
      onExtended()
    } catch (cause) {
      setFailure(errorFor(cause))
    }
    setPending(false)
  }

  return {
    asking,
    pending,
    failure,
    answer,
    ask: () => {
      setAnswer(null)
      setFailure(null)
      setAsking(true)
    },
    decline: () => setAsking(false),
    submit,
  }
}

function ExtensionForm({
  name,
  bounds,
  extension,
}: {
  name: string
  bounds: ExtensionBounds
  extension: Extension
}) {
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    extension.submit(numberValue(new FormData(event.currentTarget), extensionField))
  }

  return (
    <Prompt severity="info" heading={`Extend ${name}`}>
      <form onSubmit={submit} className="space-y-cell">
        <p className="text-copy-13 text-tint-fg/85">
          The deadline is derived from the moment the lease was accepted, so this is how long the
          lease runs in total rather than the time added to it. {bounds.ceiling}
        </p>
        <span className="flex flex-wrap items-center gap-snug">
          <input
            name={extensionField}
            type="number"
            required
            min={bounds.min}
            max={bounds.max}
            defaultValue={bounds.initial}
            className={`${controlClass} w-[7rem]`}
          />
          <span className="text-label-13 text-tint-fg/85">minutes in total</span>
        </span>
        <div className="flex flex-wrap gap-snug">
          <Button type="submit" tone="primary" disabled={extension.pending}>
            {extension.pending ? 'Asking the controller' : 'Extend the lease'}
          </Button>
          <Button type="button" onClick={extension.decline} disabled={extension.pending}>
            Keep the deadline
          </Button>
        </div>
      </form>
    </Prompt>
  )
}

function ExtendControl({
  lease,
  onExtended,
}: {
  lease: LeaseDetailResponse
  onExtended: () => void
}) {
  const name = lease.summary.name
  const bounds = extensionBounds(lease)
  const extension = useLeaseExtension(name, onExtended)

  if (bounds.min > bounds.max) {
    return (
      <p className="max-w-[44ch] text-label-12 text-subtle">
        {bounds.ceiling} Nothing longer is left to ask for.
      </p>
    )
  }

  return (
    <div className="max-w-[32rem] space-y-cell text-left">
      {extension.answer === null ? null : (
        <Notice
          severity="info"
          title={`${name} now runs for ${formatSpan(extension.answer.durationSeconds)}`}
        >
          {extension.answer.detail}
        </Notice>
      )}
      {extension.failure === null ? null : (
        <Notice {...refusalNotice(extension.failure)}>{extension.failure.message}</Notice>
      )}
      {extension.asking ? (
        <ExtensionForm name={name} bounds={bounds} extension={extension} />
      ) : (
        <Button type="button" onClick={extension.ask}>
          Extend this lease
        </Button>
      )}
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

const replicateMode = 'replicate'
const moveMode = 'move'

const workloadCopy = {
  [moveMode]: {
    warningsTitle: 'Migration warnings',
    warningsIntro:
      'Each workload here either moves onto the leased nodes at a cost or is left where it is, and the reason beside it says which. A workload that does move goes dark while it does, because nothing stands in for a pod between the moment it stops and the moment its replacement is ready.',
    placedTitle: 'Migrated workloads',
    placedEmpty:
      'Nothing has been moved off this lease. Workloads appear here once the controller drains the leased nodes ahead of teardown.',
  },
  [replicateMode]: {
    warningsTitle: 'Replication warnings',
    warningsIntro:
      'Each workload here is either copied onto the leased nodes at a cost or left uncopied, and the reason beside it says which. Nothing is written to the workload itself on either path, so none of it goes dark.',
    placedTitle: 'Burst copies',
    placedEmpty:
      'No copy is running for this lease. Copies appear here once the controller creates one on the leased nodes, and teardown deletes them again.',
  },
} as const

function replicates(lease: LeaseDetailResponse): boolean {
  return lease.workloadMode === replicateMode
}

function copyFor(lease: LeaseDetailResponse) {
  return workloadCopy[replicates(lease) ? replicateMode : moveMode]
}

function WorkloadModeFact({ lease }: { lease: LeaseDetailResponse }) {
  if (lease.workloadMode === null) return <span className="text-subtle">{absent}</span>
  if (!replicates(lease) || lease.workloadBurstReplicas === null) return <>{lease.workloadMode}</>
  return (
    <>
      {lease.workloadMode}, each copy runs {lease.workloadBurstReplicas} pods
    </>
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
          <Definition label="Workload namespaces">
            {lease.workloadNamespaces.length > 0 ? (
              lease.workloadNamespaces.join(', ')
            ) : (
              <span className="text-subtle">none targeted</span>
            )}
          </Definition>
          <Definition label="Workload selector">
            {lease.workloadSelector ?? <span className="text-subtle">every workload</span>}
          </Definition>
          <Definition label="Workload mode">
            <WorkloadModeFact lease={lease} />
          </Definition>
        </DefinitionGrid>
      </Panel>
    </div>
  )
}

const migrationReasons: Record<string, string> = {
  RolloutPaused: 'the rollout is paused, so pods are cycled by horizon instead',
  ManualRollout: 'the update strategy is OnDelete',
  PartitionedRollout: 'a rollout partition holds pods back',
  RecreateStrategy: 'every replica stops before a replacement starts',
  NoSurgeCapacity: 'maxSurge leaves no room for a replacement pod',
  NodeSelectorPinned: 'the node selector is cleared for the duration of the lease',
  HeldByAnotherLease: 'another lease holds this workload, so this lease leaves it where it is',
  TargetedByAutoscaler:
    'a HorizontalPodAutoscaler targets it and would read the copy as over-provisioning and scale the original down; move mode changes no replica count, so it bursts this workload without fighting the autoscaler',
  StatefulSetNotCopyable:
    'a copy would mint empty volumes rather than carry the data the workload holds; move mode bursts a StatefulSet as it stands',
  DisruptionBudgetSpansCopy:
    'a PodDisruptionBudget on the original selects the copy pods as well, so its accounting is wrong for the life of the lease',
  TopologySpreadSpansCopy:
    'the copy pods count into the topology spread of the original, which refuses to schedule where the spread is unmet, so the next pod of the original can be left Pending; move mode adds no second set of pods',
}

// a newer controller can classify a shape this bundle predates, and dropping its reason would hide the warning entirely
const unworded = 'the binary serving this interface reports a reason this build has no wording for'

function MigrationWarningsPanel({ lease }: { lease: LeaseDetailResponse }) {
  const warnings = lease.migrationWarnings
  if (warnings.length === 0) return null

  const copy = copyFor(lease)
  return (
    <Panel title={copy.warningsTitle}>
      <p className="row-rule max-w-[70ch] px-gutter py-cell text-copy-13 text-subtle">
        {copy.warningsIntro}
      </p>
      <ul>
        {warnings.map((warning) => (
          <li
            key={warning.workload}
            data-severity="attention"
            className="row-rule grid gap-tight px-gutter py-cell [--rail:var(--tint-signal,transparent)] last:[--hairline:transparent] sm:grid-cols-[14rem_1fr] sm:gap-gutter"
          >
            <span className="text-label-14 font-emphasis text-ink-strong">{warning.workload}</span>
            <ul className="flex flex-col gap-tight">
              {warning.reasons.map((reason) => (
                <li key={reason} className="flex flex-wrap items-center gap-snug">
                  <StatusPill severity="attention">{reason}</StatusPill>
                  <span className="text-copy-13 text-subtle">
                    {migrationReasons[reason] ?? unworded}
                  </span>
                </li>
              ))}
            </ul>
          </li>
        ))}
      </ul>
    </Panel>
  )
}

function MigratedPanel({ lease }: { lease: LeaseDetailResponse }) {
  const workloads = lease.migratedWorkloads
  const copy = copyFor(lease)
  return (
    <Panel title={copy.placedTitle}>
      <div className="p-gutter">
        {workloads.length === 0 ? (
          <p className="text-copy-13 text-subtle">{copy.placedEmpty}</p>
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
      <MigrationWarningsPanel lease={view.data} />
      <MigratedPanel lease={view.data} />
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
  const released = (summary?.releasedAt ?? null) !== null

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
              <ExpiryHero
                at={summary?.expiresAt ?? null}
                released={released}
                extension={
                  view.data === null || released ? null : (
                    <ExtendControl lease={view.data} onExtended={view.reload} />
                  )
                }
              />
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
