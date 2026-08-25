import type { FormEvent } from 'react'
import { useEffect, useState } from 'react'

import { Button, ButtonLink, controlClass, Field, Numeric } from '@/components/controls'
import type { LeaseCreateRequest, MachineCatalogueResponse, ProviderConfigSummary } from '@/lib/api'
import { createLease, fetchMachines, fetchNamespaces, machinesPath } from '@/lib/api'
import { errorFor } from '@/lib/errors'
import { fieldValue, fieldValues, numberValue } from '@/lib/form'
import { EmptyState, Loading, Notice, PageHeader, Panel } from '@/routes/page'
import type { Polled } from '@/routes/poll'
import { usePolled } from '@/routes/poll'
import { leaseHref, navigate, newConfigHref } from '@/routes/router'
import {
  bytesFor,
  formatCount,
  leaseMinutes,
  memoryUnits,
  minutesPerHour,
  quantityFor,
  secondsPerMinute,
} from '@/routes/units'

const field = {
  name: 'name',
  providerRef: 'providerRef',
  region: 'region',
  mode: 'mode',
  size: 'size',
  minCPU: 'minCPU',
  memory: 'memory',
  memoryUnit: 'memoryUnit',
  architecture: 'architecture',
  cpuType: 'cpuType',
  strategy: 'strategy',
  replicas: 'replicas',
  duration: 'duration',
  grace: 'grace',
  workload: 'workload',
  workloadSelector: 'workloadSelector',
  workloadMode: 'workloadMode',
  burstReplicas: 'burstReplicas',
} as const

const requirementsMode = 'requirements'
const sizeMode = 'size'
const moveMode = 'move'
const replicateMode = 'replicate'
const namespaceListId = 'workload-namespaces'
const addNamespaceId = 'add-workload-namespace'

const replicaBounds = { min: 1, max: 8, initial: 2 }
const coreBounds = { min: 1, max: 64, initial: 2 }
const durationBounds = { ...leaseMinutes, initial: minutesPerHour }
const graceBounds = { min: 0, max: 15 * secondsPerMinute, initial: 2 * secondsPerMinute }
const burstReplicaBounds = { min: 1, initial: 2 }

const sizingChoices = [
  { value: requirementsMode, label: 'By requirement' },
  { value: sizeMode, label: 'By machine type' },
]

const workloadChoices = [
  { value: moveMode, label: 'Move' },
  { value: replicateMode, label: 'Replicate' },
]

const architectures = ['x86', 'arm']
const cpuTypes = ['shared', 'dedicated']
const strategies = ['LowestPrice', 'LowestPricePerCore']
const anyCPUType = 'any'

interface Sizing {
  mode: string
  memory: string
  unit: string
}

const initialSizing: Sizing = { mode: requirementsMode, memory: '', unit: memoryUnits[0].suffix }

function sizingOf(form: FormData, was: Sizing): Sizing {
  const mode = fieldValue(form, field.mode)
  if (mode !== requirementsMode) return { ...was, mode }
  return {
    mode,
    memory: fieldValue(form, field.memory),
    unit: fieldValue(form, field.memoryUnit) || was.unit,
  }
}

function requestFrom(form: FormData): LeaseCreateRequest {
  const byRequirement = fieldValue(form, field.mode) === requirementsMode
  const replicating = fieldValue(form, field.workloadMode) === replicateMode
  const memory = fieldValue(form, field.memory)
  const cpuType = fieldValue(form, field.cpuType)

  return {
    name: fieldValue(form, field.name),
    providerRef: fieldValue(form, field.providerRef),
    region: fieldValue(form, field.region),
    size: byRequirement ? '' : fieldValue(form, field.size),
    requirements: byRequirement
      ? {
          minCPU: numberValue(form, field.minCPU),
          minMemory:
            memory === '' ? '' : quantityFor(Number(memory), fieldValue(form, field.memoryUnit)),
          architecture: fieldValue(form, field.architecture),
          cpuType: cpuType === anyCPUType ? '' : cpuType,
          strategy: fieldValue(form, field.strategy),
        }
      : null,
    replicas: numberValue(form, field.replicas),
    durationSeconds: numberValue(form, field.duration) * secondsPerMinute,
    teardownGraceSeconds: numberValue(form, field.grace),
    workloadNamespaces: fieldValues(form, field.workload),
    workloadSelector: fieldValue(form, field.workloadSelector),
    workloadMode: fieldValue(form, field.workloadMode),
    // the apiserver refuses a count outside replicate mode, so a moving lease must name none at all
    workloadBurstReplicas: replicating ? numberValue(form, field.burstReplicas) : null,
  }
}

function memoryHint(sizing: Sizing): string {
  if (sizing.memory === '') {
    return 'Optional. The unit is part of the requirement, so choose it deliberately.'
  }
  return `Resolves to ${formatCount(bytesFor(Number(sizing.memory), sizing.unit))} bytes.`
}

function ModeChoice({
  name,
  legend,
  choices,
  chosen,
}: {
  name: string
  legend: string
  choices: { value: string; label: string }[]
  chosen: string
}) {
  return (
    <fieldset className="flex flex-wrap items-center gap-gutter">
      <legend className="mb-tight text-label-12 text-subtle">{legend}</legend>
      {choices.map((choice) => (
        <label key={choice.value} className="flex items-center gap-snug text-label-13 text-ink">
          <input
            type="radio"
            name={name}
            value={choice.value}
            defaultChecked={choice.value === chosen}
            className="accent-brand"
          />
          {choice.label}
        </label>
      ))}
    </fieldset>
  )
}

function RequirementFields({ sizing }: { sizing: Sizing }) {
  return (
    <>
      <Field label="Minimum cores">
        <Numeric name={field.minCPU} bounds={coreBounds} />
      </Field>
      <Field label="Minimum memory" hint={memoryHint(sizing)}>
        <span className="flex gap-snug">
          <input
            name={field.memory}
            type="number"
            min={1}
            step={1}
            placeholder="4"
            defaultValue={sizing.memory}
            className={`${controlClass} w-full`}
          />
          <select name={field.memoryUnit} defaultValue={sizing.unit} className={controlClass}>
            {memoryUnits.map((unit) => (
              <option key={unit.suffix} value={unit.suffix}>
                {unit.label}
              </option>
            ))}
          </select>
        </span>
      </Field>
      <Field label="Architecture">
        <select name={field.architecture} defaultValue={architectures[0]} className={controlClass}>
          {architectures.map((architecture) => (
            <option key={architecture} value={architecture}>
              {architecture}
            </option>
          ))}
        </select>
      </Field>
      <Field label="CPU type">
        <select name={field.cpuType} defaultValue={anyCPUType} className={controlClass}>
          <option value={anyCPUType}>{anyCPUType}</option>
          {cpuTypes.map((cpuType) => (
            <option key={cpuType} value={cpuType}>
              {cpuType}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Strategy">
        <select name={field.strategy} defaultValue={strategies[0]} className={controlClass}>
          {strategies.map((strategy) => (
            <option key={strategy} value={strategy}>
              {strategy}
            </option>
          ))}
        </select>
      </Field>
    </>
  )
}

function useNamespaceSuggestions(): string[] {
  const [names, setNames] = useState<string[]>([])

  useEffect(() => {
    let live = true

    const read = async () => {
      try {
        const answer = await fetchNamespaces()
        if (live) setNames(answer.namespaces)
      } catch {
        // listing namespaces is an optional grant, so a refusal leaves the field the free text it already is
      }
    }

    void read()
    return () => {
      live = false
    }
  }, [])

  return names
}

function NamespacesField({ suggestions }: { suggestions: string[] }) {
  const [entries, setEntries] = useState([0])
  const suggested = suggestions.length > 0
  return (
    <div className="flex flex-col gap-tight">
      <Field label="Workload namespaces" hint="Optional. The lease reaches the workloads these namespaces hold.">
        <span className="flex flex-col gap-tight">
          {entries.map((entry) => (
            <input
              key={entry}
              name={field.workload}
              list={suggested ? namespaceListId : undefined}
              placeholder="batch"
              spellCheck={false}
              autoComplete="off"
              className={controlClass}
            />
          ))}
        </span>
      </Field>
      <Button
        id={addNamespaceId}
        type="button"
        onClick={() => setEntries((held) => [...held, held.length])}
      >
        Add a namespace
      </Button>
      {suggested ? (
        <datalist id={namespaceListId}>
          {suggestions.map((name) => (
            <option key={name} value={name} />
          ))}
        </datalist>
      ) : null}
    </div>
  )
}

function WorkloadPanel({ mode, suggestions }: { mode: string; suggestions: string[] }) {
  return (
    <Panel
      title="Workload"
      note="Move repins each matched workload onto the leased nodes and puts it back at expiry. Replicate writes to none of them and runs a lease-owned copy on the leased nodes instead"
    >
      <div className="space-y-gutter p-gutter">
        <ModeChoice
          name={field.workloadMode}
          legend="Mode"
          choices={workloadChoices}
          chosen={mode}
        />
        <div className="grid grid-cols-[repeat(auto-fill,minmax(15rem,1fr))] gap-gutter">
          <NamespacesField suggestions={suggestions} />
          <Field
            label="Workload selector"
            hint="Optional. It narrows the target set to the workloads carrying these labels."
          >
            <input
              name={field.workloadSelector}
              placeholder="tier=batch"
              spellCheck={false}
              autoComplete="off"
              className={controlClass}
            />
          </Field>
          {mode === replicateMode ? (
            <Field
              label="Burst replicas"
              hint="How many pods each copy runs, a count of pods rather than machines."
            >
              <Numeric name={field.burstReplicas} bounds={burstReplicaBounds} />
            </Field>
          ) : null}
        </div>
      </div>
    </Panel>
  )
}

function SizeField() {
  return (
    <Field label="Machine type" hint="The provider's own name for a type, such as cx23.">
      <input
        name={field.size}
        required
        placeholder="cx23"
        spellCheck={false}
        autoComplete="off"
        className={controlClass}
      />
    </Field>
  )
}

function CreateForm({ configs }: { configs: ProviderConfigSummary[] }) {
  const [sizing, setSizing] = useState(initialSizing)
  const [workloadMode, setWorkloadMode] = useState(moveMode)
  const [failure, setFailure] = useState<Error | null>(null)
  const [pending, setPending] = useState(false)
  const namespaces = useNamespaceSuggestions()

  const observe = (event: FormEvent<HTMLFormElement>) => {
    const held = new FormData(event.currentTarget)
    setSizing((was) => sizingOf(held, was))
    setWorkloadMode(fieldValue(held, field.workloadMode))
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setFailure(null)
    try {
      const created = await createLease(requestFrom(new FormData(event.currentTarget)))
      navigate(leaseHref(created.summary.name))
    } catch (cause) {
      setFailure(errorFor(cause))
      setPending(false)
    }
  }

  return (
    <form onSubmit={submit} onChange={observe} className="space-y-gutter">
      {failure === null ? null : (
        <Notice severity="danger" title="The cluster refused this lease">
          {failure.message}
        </Notice>
      )}

      <Panel title="Reservation">
        <div className="grid grid-cols-[repeat(auto-fill,minmax(15rem,1fr))] gap-gutter p-gutter">
          <Field label="Name" hint="A lease is named once and never renamed.">
            <input
              name={field.name}
              required
              placeholder="batch-run"
              spellCheck={false}
              autoComplete="off"
              className={controlClass}
            />
          </Field>
          <Field label="Provider config">
            <select name={field.providerRef} required defaultValue={configs[0].name} className={controlClass}>
              {configs.map((config) => (
                <option key={config.name} value={config.name}>
                  {config.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Region">
            <input
              name={field.region}
              required
              placeholder="nbg1"
              spellCheck={false}
              autoComplete="off"
              className={controlClass}
            />
          </Field>
          <Field label="Replicas">
            <Numeric name={field.replicas} bounds={replicaBounds} />
          </Field>
          <Field label="Duration in minutes" hint="Between 5 minutes and 8 hours.">
            <Numeric name={field.duration} bounds={durationBounds} />
          </Field>
          <Field label="Teardown grace in seconds" hint="How long a drain may take before the machines go anyway.">
            <Numeric name={field.grace} bounds={graceBounds} />
          </Field>
        </div>
      </Panel>

      <Panel
        title="Sizing"
        note="A lease carries a requirement or a machine type, never both, and neither can be changed afterwards"
      >
        <div className="space-y-gutter p-gutter">
          <ModeChoice
            name={field.mode}
            legend="Sizing"
            choices={sizingChoices}
            chosen={sizing.mode}
          />
          <div className="grid grid-cols-[repeat(auto-fill,minmax(15rem,1fr))] gap-gutter">
            {sizing.mode === requirementsMode ? <RequirementFields sizing={sizing} /> : <SizeField />}
          </div>
        </div>
      </Panel>

      <WorkloadPanel mode={workloadMode} suggestions={namespaces} />

      <div className="flex flex-wrap items-center justify-between gap-gutter">
        <p className="max-w-[60ch] text-copy-13 text-subtle">
          The controller reads the requirement against the provider catalogue and records the type it
          chose, what it beat and why the rest were rejected.
        </p>
        <Button type="submit" tone="primary" disabled={pending}>
          {pending ? 'Creating the lease' : 'Create the lease'}
        </Button>
      </div>
    </form>
  )
}

function NoConfigs() {
  return (
    <EmptyState
      title="The cluster holds no provider config"
      action={
        <ButtonLink href={newConfigHref} tone="primary">
          Create a provider config
        </ButtonLink>
      }
    >
      A lease reserves capacity through a ProviderConfig, which carries the credentials and the
      image a burst node boots. Create one and it can be chosen here.
    </EmptyState>
  )
}

function LeaseNewBody({ view }: { view: Polled<MachineCatalogueResponse> }) {
  if (view.data === null) {
    return view.settled ? null : <Loading label="Reading the provider configs from the cluster." />
  }
  return view.data.configs.length === 0 ? <NoConfigs /> : <CreateForm configs={view.data.configs} />
}

export function LeaseNewRoute() {
  const view = usePolled(() => fetchMachines('', ''), machinesPath('', ''))

  return (
    <>
      <PageHeader
        title="New lease"
        lede="Describe the capacity the work needs and the controller picks the machine. Naming a type stays available for the times the choice is already made."
      />
      <div className="space-y-gutter">
        {view.error ? (
          <Notice severity="danger" title="The provider configs could not be read">
            {view.error.message}
          </Notice>
        ) : null}
        <LeaseNewBody view={view} />
      </div>
    </>
  )
}
