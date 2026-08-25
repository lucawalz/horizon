import type { FormEvent } from 'react'
import { useRef } from 'react'

import { Button, ButtonLink, controlClass } from '@/components/controls'
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
  MachineCatalogueResponse,
  MachineType,
  ProviderConfigSummary,
} from '@/lib/api'
import { fetchMachines, machinesPath } from '@/lib/api'
import { fieldValue } from '@/lib/form'
import { ConditionChip, Since } from '@/routes/chips'
import { EmptyState, Loading, Notice, PageHeader } from '@/routes/page'
import type { Polled } from '@/routes/poll'
import { usePolled } from '@/routes/poll'
import { machinesHrefFor, navigate, newConfigHref } from '@/routes/router'
import { absent, formatBytes, formatRate } from '@/routes/units'

const configField = 'config'
const regionField = 'region'
const readyCondition = 'Ready'
const configColumns = 5
const typeColumns = 8

function Picker({
  configs,
  config,
  region,
}: {
  configs: ProviderConfigSummary[]
  config: string
  region: string
}) {
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    navigate(machinesHrefFor(fieldValue(form, configField), fieldValue(form, regionField)))
  }

  // configs can still be loading when config is already known from the route, so its option is synthesised until the real one arrives
  const configKnown = config === '' || configs.some((one) => one.name === config)

  return (
    <form
      onSubmit={submit}
      className="flex flex-wrap items-end gap-gutter rounded-panel border border-line bg-base px-gutter py-cell shadow-panel"
    >
      <label className="flex flex-col gap-tight">
        <span className="text-label-12 text-subtle">Provider config</span>
        <select
          name={configField}
          value={config}
          onChange={(event) => event.currentTarget.form?.requestSubmit()}
          className={controlClass}
        >
          <option value="">Choose a config</option>
          {configKnown ? null : <option value={config}>{config}</option>}
          {configs.map((one) => (
            <option key={one.name} value={one.name}>
              {one.name}
            </option>
          ))}
        </select>
      </label>
      <label className="flex flex-col gap-tight">
        <span className="text-label-12 text-subtle">Region</span>
        <input
          key={region}
          name={regionField}
          defaultValue={region}
          placeholder="nbg1"
          spellCheck={false}
          autoComplete="off"
          className={controlClass}
        />
      </label>
      <Button type="submit" tone="primary">
        Show types
      </Button>
    </form>
  )
}

function TypesTable({ types }: { types: MachineType[] }) {
  return (
    <Table>
      <TableHead>
        <Row>
          <HeadCell>Instance type</HeadCell>
          <HeadCell numeric>Cores</HeadCell>
          <HeadCell>CPU</HeadCell>
          <HeadCell>Architecture</HeadCell>
          <HeadCell numeric>Memory</HeadCell>
          <HeadCell numeric>Disk</HeadCell>
          <HeadCell numeric>Hourly rate</HeadCell>
          <HeadCell>Offered</HeadCell>
        </Row>
      </TableHead>
      <TableBody>
        {types.length === 0 ? (
          <TableEmpty span={typeColumns}>
            The provider answered with no instance type in this region. Check the region code
            against the provider, or try another one.
          </TableEmpty>
        ) : (
          types.map((type) => (
            <Row key={type.name} interactive className={type.deprecated ? 'opacity-60' : undefined}>
              <Cell className="font-emphasis text-ink-strong">{type.name}</Cell>
              <Cell numeric>{type.cpuCores}</Cell>
              <Cell muted>{type.cpuType ?? absent}</Cell>
              <Cell muted>{type.architecture ?? absent}</Cell>
              <Cell numeric>{formatBytes(type.memoryBytes)}</Cell>
              <Cell numeric>{formatBytes(type.diskBytes)}</Cell>
              <Cell numeric>
                {type.hourlyRate === null ? (
                  <span className="text-subtle">not quoted</span>
                ) : (
                  formatRate(type.hourlyRate)
                )}
              </Cell>
              <Cell>
                <span className="flex flex-wrap items-center gap-tight">
                  <StatusPill severity={type.available ? 'success' : 'neutral'}>
                    {type.available ? 'available' : 'sold out'}
                  </StatusPill>
                  {type.deprecated ? (
                    <StatusPill severity="attention">deprecated</StatusPill>
                  ) : null}
                </span>
              </Cell>
            </Row>
          ))
        )}
      </TableBody>
    </Table>
  )
}

function ConfigsPanel({ configs }: { configs: ProviderConfigSummary[] }) {
  return (
    <Table>
      <TableHead>
        <Row>
          <HeadCell>Config</HeadCell>
          <HeadCell>Provider</HeadCell>
          <HeadCell>Ready</HeadCell>
          <HeadCell>Reason</HeadCell>
          <HeadCell numeric>Created</HeadCell>
        </Row>
      </TableHead>
      <TableBody>
        {configs.length === 0 ? (
          <TableEmpty span={configColumns}>
            The cluster holds no ProviderConfig. Create one with provider credentials before a lease
            can reserve anything.
          </TableEmpty>
        ) : (
          configs.map((one) => (
            <Row key={one.name}>
              <Cell className="font-emphasis text-ink-strong">{one.name}</Cell>
              <Cell muted>{one.type}</Cell>
              <Cell>
                <ConditionChip type={readyCondition} status={one.ready} />
              </Cell>
              <Cell muted>{one.reason ?? absent}</Cell>
              <Cell numeric muted>
                <Since at={one.createdAt} />
              </Cell>
            </Row>
          ))
        )}
      </TableBody>
    </Table>
  )
}

// the state crosses the wire machine readable so the wording lives here, and an unlisted one still has to render
function Catalogue({ answer }: { answer: MachineCatalogueResponse }) {
  switch (answer.state) {
    case 'Listed':
      return <TypesTable types={answer.types} />
    case 'NoSelection':
      return (
        <EmptyState title="Choose a provider config and a region">
          The catalogue is held per config and per region. Pick both above and the instance types
          the provider offers there are listed here.
        </EmptyState>
      )
    case 'CatalogueAbsent':
      return (
        <EmptyState title="This process holds no catalogue">
          Instance types are fetched and cached in memory by the horizon controller process. The
          interface runs as a separate process and keeps no copy of that cache, wherever it runs,
          so there is nothing here to list. Lease phases and expiry are unaffected.
        </EmptyState>
      )
    case 'CatalogueUnfilled':
      return (
        <EmptyState title={`The catalogue has not been filled for ${answer.config}`}>
          The refresher has not completed a fetch for this provider config. It runs on an interval,
          so the list usually appears within a few minutes of the controller starting.
        </EmptyState>
      )
    case 'NoMatch':
      return (
        <EmptyState title={`Nothing is offered in ${answer.region}`}>
          The catalogue answered for {answer.config} and holds no instance type in that region.
          Check the region code against the provider, or try another one.
        </EmptyState>
      )
    case 'ReadFailed':
      return (
        <Notice severity="danger" title="The catalogue could not be read">
          The provider reported: {answer.detail ?? 'no reason was given'}
        </Notice>
      )
    default:
      return (
        <EmptyState title="This build does not recognise that catalogue state">
          The server answered with the state {answer.state}, which this interface has no wording
          for. The bundled interface is older than the binary serving it.
        </EmptyState>
      )
  }
}

function MachinesBody({ view }: { view: Polled<MachineCatalogueResponse> }) {
  if (view.data === null) {
    return view.settled ? null : <Loading label="Reading the provider configs from the cluster." />
  }
  return (
    <>
      <Catalogue answer={view.data} />
      <ConfigsPanel configs={view.data.configs} />
    </>
  )
}

export function MachinesRoute({ config, region }: { config: string; region: string }) {
  const view = usePolled(() => fetchMachines(config, region), machinesPath(config, region))
  // a reload clears view.data, so the ref keeps the picker's options from emptying while the next answer is in flight
  const knownConfigs = useRef<ProviderConfigSummary[]>([])
  if (view.data !== null) knownConfigs.current = view.data.configs

  return (
    <>
      <PageHeader
        title="Machines"
        lede="The instance types each provider config offers in a region, as the controller last fetched them."
        aside={
          <>
            {view.data !== null && view.data.state === 'Listed' && view.data.refreshedAt !== null ? (
              <span className="text-label-12 text-subtle">
                Refreshed <Since at={view.data.refreshedAt} />
              </span>
            ) : null}
            <ButtonLink href={newConfigHref}>New provider config</ButtonLink>
          </>
        }
      />
      <div className="space-y-gutter">
        {view.error ? (
          <Notice severity="danger" title="The provider configs could not be read">
            {view.error.message}
          </Notice>
        ) : null}
        <Picker configs={knownConfigs.current} config={config} region={region} />
        <MachinesBody view={view} />
      </div>
    </>
  )
}
