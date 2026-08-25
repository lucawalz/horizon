import { ArrowLeft } from 'lucide-react'
import type { ReactNode } from 'react'

import { ButtonLink } from '@/components/controls'
import {
  Cell,
  HeadCell,
  Row,
  Table,
  TableBody,
  TableHead,
} from '@/components/data-table'
import type {
  HetznerProviderDetail,
  ProviderConfigDetailResponse,
  PublishedCatalogue,
  SecretKeyReference,
  WatchdogDetail,
} from '@/lib/api'
import { fetchProviderConfig, notFound, providerConfigPath, RequestFailed } from '@/lib/api'
import { ConditionChip, Since } from '@/routes/chips'
import { ConditionsTable } from '@/routes/conditions'
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
import { machinesHref, machinesHrefFor } from '@/routes/router'
import { absent, formatCount, formatSpan } from '@/routes/units'

const readyCondition = 'Ready'
const cataloguePublishedCondition = 'CataloguePublished'

interface SecretRow {
  label: string
  reference: SecretKeyReference | null
  purpose: string
}

function counted(count: number, noun: string): string {
  return `${formatCount(count)} ${noun}${count === 1 ? '' : 's'}`
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

function Labels({ pairs }: { pairs: Record<string, string> }) {
  return (
    <span className="flex flex-wrap gap-tight">
      {Object.entries(pairs).map(([key, value]) => (
        <Snippet key={key}>
          {key}={value}
        </Snippet>
      ))}
    </span>
  )
}

function Missing({ children }: { children: ReactNode }) {
  return <span className="text-subtle">{children}</span>
}

function ImageFact({ hetzner }: { hetzner: HetznerProviderDetail }) {
  const image = hetzner.image
  if (image !== null) {
    if (image.name !== null) return <Snippet>{image.name}</Snippet>
    if (image.id !== null) return <Snippet>{image.id}</Snippet>
    if (image.selector !== null) return <Labels pairs={image.selector} />
  }
  if (hetzner.imageSelector !== null) return <Labels pairs={hetzner.imageSelector} />
  return <Missing>{absent}</Missing>
}

function Names({ names, none }: { names: string[]; none: string }) {
  if (names.length === 0) return <Missing>{none}</Missing>
  return (
    <span className="flex flex-wrap gap-tight">
      {names.map((name) => (
        <Snippet key={name}>{name}</Snippet>
      ))}
    </span>
  )
}

function ProviderPanel({ type, hetzner }: { type: string; hetzner: HetznerProviderDetail | null }) {
  if (hetzner === null) {
    return (
      <Panel title="Provider">
        <p className="px-gutter py-section text-center text-copy-13 text-subtle">
          The binary serving this interface reports a {type} provider, which this build has no
          wording for. The bundled interface is older than the binary serving it.
        </p>
      </Panel>
    )
  }

  return (
    <Panel title="Provider">
      <DefinitionGrid>
        <Definition label="Type">{type}</Definition>
        <Definition label="Image">
          <ImageFact hetzner={hetzner} />
        </Definition>
        <Definition label="SSH keys">
          <Names names={hetzner.sshKeys} none="none attached" />
        </Definition>
        <Definition label="Firewalls">
          <Names names={hetzner.firewalls} none="none attached" />
        </Definition>
      </DefinitionGrid>
    </Panel>
  )
}

function secretRows(hetzner: HetznerProviderDetail): SecretRow[] {
  return [
    {
      label: 'Credentials',
      reference: hetzner.credentialsSecretRef,
      purpose: 'the provider token every machine is created with',
    },
    {
      label: 'Cloud-init',
      reference: hetzner.cloudInitSecretRef,
      purpose: 'the document each machine boots with',
    },
    {
      label: 'Node credential',
      reference: hetzner.nodeCredentialSecretRef,
      purpose: 'the key the controller reaches a leased node with',
    },
    {
      label: 'Join token',
      reference: hetzner.joinTokenSecretRef,
      purpose: 'the token a leased node joins the cluster with',
    },
  ]
}

function SecretsPanel({ hetzner }: { hetzner: HetznerProviderDetail }) {
  // the reference is what tells a missing secret apart from a misnamed one, and no interface reads what a secret holds
  return (
    <Panel title="Secrets">
      <p className="row-rule max-w-[70ch] px-gutter py-cell text-copy-13 text-subtle">
        A configuration points at Secrets and horizon resolves them in the namespace the controller
        runs in. Only the reference is shown here, never what a Secret holds, so a name that resolves
        to nothing reads as an unready configuration rather than as a blank field.
      </p>
      <Table>
        <TableHead>
          <Row>
            <HeadCell>Reference</HeadCell>
            <HeadCell>Secret</HeadCell>
            <HeadCell>Key</HeadCell>
            <HeadCell>Holds</HeadCell>
          </Row>
        </TableHead>
        <TableBody>
          {secretRows(hetzner).map((row) => (
            <Row key={row.label}>
              <Cell className="font-emphasis text-ink-strong">{row.label}</Cell>
              <Cell>
                {row.reference === null ? (
                  <Missing>{absent}</Missing>
                ) : (
                  <Snippet>{row.reference.name}</Snippet>
                )}
              </Cell>
              <Cell>
                {row.reference === null ? (
                  <Missing>{absent}</Missing>
                ) : (
                  <Snippet>{row.reference.key}</Snippet>
                )}
              </Cell>
              <Cell muted>{row.purpose}</Cell>
            </Row>
          ))}
        </TableBody>
      </Table>
    </Panel>
  )
}

function WatchdogPanel({ watchdog }: { watchdog: WatchdogDetail }) {
  return (
    <Panel title="Watchdog">
      <p className="row-rule max-w-[70ch] px-gutter py-cell text-copy-13 text-subtle">
        Every leased node runs a dead man switch on these timings. It renews its own lease on the
        interval, powers the node off once the slack past a renewal is spent, and powers it off at
        the lifetime whatever the controller is doing.
      </p>
      <DefinitionGrid>
        <Definition label="Renew interval">{formatSpan(watchdog.renewIntervalSeconds)}</Definition>
        <Definition label="Slack">{formatSpan(watchdog.slackSeconds)}</Definition>
        <Definition label="Max lifetime">{formatSpan(watchdog.maxLifetimeSeconds)}</Definition>
      </DefinitionGrid>
    </Panel>
  )
}

function CataloguePanel({ name, catalogue }: { name: string; catalogue: PublishedCatalogue }) {
  // a published catalogue runs to hundreds of entries, so it is tallied here and listed on the machines page
  return (
    <Panel
      title="Catalogue"
      note={
        catalogue.refreshedAt === null ? undefined : (
          <span>
            Refreshed <Since at={catalogue.refreshedAt} />
          </span>
        )
      }
    >
      <div className="space-y-cell p-gutter">
        {catalogue.types === 0 ? (
          <p className="max-w-[70ch] text-copy-13 text-subtle">
            The controller has published no instance type for this configuration. It fetches the
            provider catalogue on an interval and writes it to the status, so the tally appears
            within a few minutes of the controller starting.
          </p>
        ) : (
          <>
            <p className="max-w-[70ch] text-copy-13 text-subtle">
              {counted(catalogue.types, 'instance type')} across{' '}
              {counted(catalogue.regions.length, 'region')}, as the controller last fetched them.
              Each region opens the machines page, which lists what is offered there.
            </p>
            <ul className="flex flex-wrap gap-snug">
              {catalogue.regions.map((region) => (
                <li key={region.region}>
                  <ButtonLink href={machinesHrefFor(name, region.region)}>
                    {region.region}, {counted(region.types, 'type')}
                  </ButtonLink>
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
    </Panel>
  )
}

function MissingConfig({ name }: { name: string }) {
  return (
    <EmptyState title="That provider config is not in the cluster">
      No provider config named {name} exists. It may have been deleted, or the link may be older
      than the cluster.
    </EmptyState>
  )
}

function ConfigDetailBody({
  name,
  view,
}: {
  name: string
  view: Polled<ProviderConfigDetailResponse>
}) {
  if (view.error instanceof RequestFailed && view.error.status === notFound) {
    return <MissingConfig name={name} />
  }
  if (view.data === null) {
    return view.settled ? null : <Loading label="Reading the provider config from the cluster." />
  }

  const config = view.data
  return (
    <div className="space-y-gutter">
      <div className="grid gap-gutter lg:grid-cols-2">
        <ProviderPanel type={config.summary.type} hetzner={config.hetzner} />
        <WatchdogPanel watchdog={config.watchdog} />
      </div>
      {config.hetzner === null ? null : <SecretsPanel hetzner={config.hetzner} />}
      <CataloguePanel name={name} catalogue={config.catalogue} />
      <ConditionsTable
        conditions={config.conditions}
        empty="The controller has not written a condition for this provider config yet."
      />
    </div>
  )
}

export function ConfigDetailRoute({ name }: { name: string }) {
  const view = usePolled(() => fetchProviderConfig(name), providerConfigPath(name))
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
            : `The ${summary.type} credentials, image and watchdog policy a lease reserves capacity through.`
        }
        aside={
          missing ? undefined : (
            <div className="flex flex-col items-end gap-cell text-right">
              <div className="flex items-center gap-snug">
                <span className="text-label-12 text-subtle">Ready</span>
                <ConditionChip type={readyCondition} status={summary?.ready ?? null} />
              </div>
              <div className="flex items-center gap-snug">
                <span className="text-label-12 text-subtle">Catalogue published</span>
                <ConditionChip
                  type={cataloguePublishedCondition}
                  status={summary?.cataloguePublished ?? null}
                />
              </div>
            </div>
          )
        }
      />
      <div className="space-y-gutter">
        {view.error && !missing ? (
          <Notice severity="danger" title="The provider config could not be read">
            {view.error.message}
          </Notice>
        ) : null}
        <ConfigDetailBody name={name} view={view} />
      </div>
    </>
  )
}
