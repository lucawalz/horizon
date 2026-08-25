import { ArrowLeft } from 'lucide-react'
import type { FormEvent } from 'react'
import { useState } from 'react'

import { Button, ButtonLink } from '@/components/controls'
import { Cell, HeadCell, Row, Table, TableBody, TableHead } from '@/components/data-table'
import type { LeaseSummary, ProviderConfigDeleteResponse, ProviderConfigSummary } from '@/lib/api'
import {
  conflict,
  deleteProviderConfig,
  fetchLeases,
  fetchMachines,
  leasesPath,
  machinesPath,
  notFound,
  replaceProviderConfig,
  RequestFailed,
} from '@/lib/api'
import { specFrom } from '@/lib/config-spec'
import { errorFor } from '@/lib/errors'
import type { Severity } from '@/lib/status'
import { Countdown, PhaseChip } from '@/routes/chips'
import { ConfigFields } from '@/routes/config-form'
import { ConfigReadiness } from '@/routes/config-readiness'
import {
  Confirmation,
  EmptyState,
  Loading,
  Notice,
  PageHeader,
  Panel,
  Prompt,
} from '@/routes/page'
import { usePolled } from '@/routes/poll'
import { leaseHref, machinesHref, navigate } from '@/routes/router'

const deleteRefused = 'This provider config was not deleted'

const deleteTitles: Record<number, string> = {
  [conflict]: 'A capacity lease still holds this provider config',
  [notFound]: 'No provider config of this name is in the cluster',
}

function deleteNotice(failure: Error): { severity: Severity; title: string } {
  if (!(failure instanceof RequestFailed)) return { severity: 'danger', title: deleteRefused }
  return {
    // a config the controller is holding back is a state to read rather than a fault to report
    severity: failure.status === conflict ? 'attention' : 'danger',
    title: deleteTitles[failure.status] ?? deleteRefused,
  }
}

function boundHeading(count: number, name: string): string {
  const held = count === 1 ? 'One capacity lease holds' : `${count} capacity leases hold`
  return `${held} ${name}`
}

function BoundLeases({ leases }: { leases: LeaseSummary[] }) {
  return (
    <Panel title="Bound leases" note="What a change here reaches">
      {leases.length === 0 ? (
        <p className="px-gutter py-cell text-copy-13 text-subtle">
          No capacity lease names this provider config. Nothing in the cluster is holding capacity
          through it right now.
        </p>
      ) : (
        <Table>
          <TableHead>
            <Row>
              <HeadCell>Lease</HeadCell>
              <HeadCell>Phase</HeadCell>
              <HeadCell numeric>Expires</HeadCell>
            </Row>
          </TableHead>
          <TableBody>
            {leases.map((lease) => (
              <Row key={lease.name} interactive>
                <Cell className="font-emphasis text-ink-strong">
                  <a href={leaseHref(lease.name)}>{lease.name}</a>
                </Cell>
                <Cell>
                  <PhaseChip phase={lease.phase} />
                </Cell>
                <Cell numeric muted>
                  <Countdown at={lease.expiresAt} size="lead" released={lease.releasedAt !== null} />
                </Cell>
              </Row>
            ))}
          </TableBody>
        </Table>
      )}
    </Panel>
  )
}

function EditForm({ config, onReplaced }: { config: ProviderConfigSummary; onReplaced: () => void }) {
  const [failure, setFailure] = useState<Error | null>(null)
  const [replaced, setReplaced] = useState(false)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setFailure(null)
    setReplaced(false)
    try {
      await replaceProviderConfig(config.name, specFrom(new FormData(event.currentTarget)))
      setReplaced(true)
      onReplaced()
    } catch (cause) {
      setFailure(errorFor(cause))
    }
    setPending(false)
  }

  return (
    <form onSubmit={submit} className="space-y-gutter">
      {failure === null ? null : (
        <Notice severity="danger" title="The cluster refused this provider config">
          {failure.message}
        </Notice>
      )}
      {replaced ? (
        <Notice severity="success" title={`${config.name} was replaced`}>
          The controller reads it on its next pass and resolves every reference again. A machine
          already running keeps the lifetime backstop latched when it was created, so no deadline
          moves under a lease that is already holding capacity.
        </Notice>
      ) : null}

      <ConfigFields spec={config.spec} />

      <div className="flex flex-wrap items-center justify-between gap-gutter">
        <p className="max-w-[60ch] text-copy-13 text-subtle">
          Every field is submitted, so a reference cleared here is cleared in the cluster. The
          controller reports what it resolved, and a wrong name reads back as the reason the config
          is unready.
        </p>
        <Button type="submit" tone="primary" disabled={pending}>
          {pending ? 'Replacing the provider config' : 'Replace the provider config'}
        </Button>
      </div>
    </form>
  )
}

function UnwritableSpec({ name }: { name: string }) {
  return (
    <Prompt severity="attention" heading={`${name} holds more than this form can carry`}>
      <p className="max-w-[70ch] text-copy-13 text-tint-fg/85">
        This form submits a whole spec, and it can express one hetzner block whose image is named. A
        configuration that picks its image by id or by selector would be rewritten rather than
        edited, so it is left to kubectl instead.
      </p>
    </Prompt>
  )
}

function DeletePanel({ name, bound }: { name: string; bound: LeaseSummary[] }) {
  const [asked, setAsked] = useState<ProviderConfigDeleteResponse | null>(null)
  const [confirming, setConfirming] = useState(false)
  const [pending, setPending] = useState(false)
  const [failure, setFailure] = useState<Error | null>(null)

  const confirm = async () => {
    setPending(true)
    setFailure(null)
    try {
      setAsked(await deleteProviderConfig(name))
    } catch (cause) {
      setFailure(errorFor(cause))
    }
    setPending(false)
  }

  return (
    <Panel title="Delete">
      <div className="space-y-cell p-gutter">
        <p className="max-w-[70ch] text-copy-13 text-subtle">
          Horizon tears a lease down with the credentials this configuration resolves. The
          controller therefore holds the object back through a finalizer until no capacity lease
          names it, whichever client asked for the deletion.
        </p>
        {bound.length > 0 ? (
          <Prompt severity="attention" heading={boundHeading(bound.length, name)}>
            <p className="max-w-[70ch] text-copy-13 text-tint-fg/85">
              Releasing {bound.map((lease) => lease.name).join(', ')} is what frees this
              configuration. Deleting it first would leave their machines billing until the watchdog
              on each node powers it off.
            </p>
          </Prompt>
        ) : asked === null ? (
          confirming ? (
            <Confirmation
              heading={`Delete ${name}`}
              confirmLabel="Ask the controller to delete"
              pendingLabel="Asking the controller"
              declineLabel="Keep the config"
              pending={pending}
              onConfirm={confirm}
              onDecline={() => setConfirming(false)}
            >
              Nothing is destroyed at the provider by this. A configuration is the credentials, the
              image and the watchdog clock a future lease reads, so deleting it stops the next lease
              from being accepted and leaves every machine alone.
            </Confirmation>
          ) : (
            <Button type="button" tone="danger" onClick={() => setConfirming(true)}>
              Delete this provider config
            </Button>
          )
        ) : (
          <Notice severity="info" title={`The controller was asked to delete ${name}`}>
            {asked.detail}
          </Notice>
        )}
        {failure === null ? null : (
          <Notice {...deleteNotice(failure)}>{failure.message}</Notice>
        )}
      </div>
    </Panel>
  )
}

function BackLink() {
  return (
    <a
      href={machinesHref}
      className="inline-flex items-center gap-tight rounded-control text-label-13 text-subtle transition-colors hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand"
    >
      <ArrowLeft size={14} strokeWidth={1.5} aria-hidden="true" />
      Machines
    </a>
  )
}

// the summary the machines route already carries holds the spec in the shape a write accepts, so the form fills from what it submits rather than from a second read
export function ConfigEditRoute({ name }: { name: string }) {
  const configs = usePolled(() => fetchMachines('', ''), machinesPath('', ''))
  const leases = usePolled(fetchLeases, leasesPath)

  const config = configs.data?.configs.find((one) => one.name === name) ?? null
  const bound = (leases.data?.leases ?? []).filter((one) => one.providerRef === name)

  return (
    <>
      <PageHeader
        eyebrow={<BackLink />}
        title={name}
        lede="A configuration can be changed while capacity is held, because rotating a credential during a long lease is the reason to reach for this at all."
        aside={<ButtonLink href={machinesHref}>Machines</ButtonLink>}
      />
      <div className="space-y-gutter">
        {configs.error ? (
          <Notice severity="danger" title="The provider configs could not be read">
            {configs.error.message}
          </Notice>
        ) : null}
        {leases.error ? (
          <Notice severity="danger" title="The capacity leases could not be read">
            {leases.error.message}
          </Notice>
        ) : null}

        {config === null ? (
          configs.settled ? (
            <EmptyState
              title={`No provider config named ${name}`}
              action={
                <Button type="button" onClick={() => navigate(machinesHref)}>
                  Back to machines
                </Button>
              }
            >
              The cluster holds no ProviderConfig of that name. It may have been deleted, or the
              name in the address may be a typo.
            </EmptyState>
          ) : (
            <Loading label="Reading the provider config from the cluster." />
          )
        ) : (
          <>
            <BoundLeases leases={bound} />
            <ConfigReadiness config={config} />
            {config.spec === null ? (
              <UnwritableSpec name={name} />
            ) : (
              <EditForm config={config} onReplaced={configs.reload} />
            )}
            <DeletePanel name={name} bound={bound} />
          </>
        )}
      </div>
    </>
  )
}
