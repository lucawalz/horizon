import type { FormEvent } from 'react'
import { useState } from 'react'

import { Button, ButtonLink, controlClass, Field, Numeric } from '@/components/controls'
import type { ProviderConfigCreateRequest, ProviderConfigSummary, SecretKeyRequest } from '@/lib/api'
import { createProviderConfig, fetchMachines, machinesPath } from '@/lib/api'
import { errorFor } from '@/lib/errors'
import { fieldValue, numberValue } from '@/lib/form'
import { ConditionChip } from '@/routes/chips'
import { Definition, DefinitionGrid, Loading, Notice, PageHeader, Panel } from '@/routes/page'
import { usePolled } from '@/routes/poll'
import { newLeaseHref } from '@/routes/router'
import {
  absent,
  minRenewIntervalSeconds,
  secondsPerMinute,
  watchdogLifetimeMinutes,
} from '@/routes/units'

const field = {
  name: 'name',
  type: 'type',
  credentialsName: 'credentialsName',
  credentialsKey: 'credentialsKey',
  cloudInitName: 'cloudInitName',
  cloudInitKey: 'cloudInitKey',
  nodeCredentialName: 'nodeCredentialName',
  nodeCredentialKey: 'nodeCredentialKey',
  joinTokenName: 'joinTokenName',
  joinTokenKey: 'joinTokenKey',
  image: 'image',
  sshKeys: 'sshKeys',
  firewalls: 'firewalls',
  renewInterval: 'renewInterval',
  slack: 'slack',
  maxLifetime: 'maxLifetime',
} as const

const providerTypes = ['hetzner']
const listSeparator = ','

const renewIntervalBounds = { min: minRenewIntervalSeconds, initial: secondsPerMinute }
const slackBounds = { min: 1, initial: 2 * secondsPerMinute }
const lifetimeBounds = { ...watchdogLifetimeMinutes, initial: 8 * 60 }

const readyCondition = 'Ready'
const catalogueCondition = 'CataloguePublished'

const secretsAreReferenced =
  'This form references Secrets and never creates a Secret. Create each one in the namespace the ' +
  'controller runs in, then name it and the key it holds here.'

function secretRef(form: FormData, nameField: string, keyField: string): SecretKeyRequest {
  return { name: fieldValue(form, nameField), key: fieldValue(form, keyField) }
}

function optionalSecretRef(
  form: FormData,
  nameField: string,
  keyField: string,
): SecretKeyRequest | null {
  const named = fieldValue(form, nameField)
  return named === '' ? null : secretRef(form, nameField, keyField)
}

function listValue(form: FormData, name: string): string[] {
  return fieldValue(form, name)
    .split(listSeparator)
    .map((entry) => entry.trim())
    .filter((entry) => entry !== '')
}

function requestFrom(form: FormData): ProviderConfigCreateRequest {
  return {
    name: fieldValue(form, field.name),
    type: fieldValue(form, field.type),
    hetzner: {
      credentialsSecretRef: secretRef(form, field.credentialsName, field.credentialsKey),
      nodeCredentialSecretRef: optionalSecretRef(
        form,
        field.nodeCredentialName,
        field.nodeCredentialKey,
      ),
      joinTokenSecretRef: optionalSecretRef(form, field.joinTokenName, field.joinTokenKey),
      cloudInitSecretRef: secretRef(form, field.cloudInitName, field.cloudInitKey),
      image: fieldValue(form, field.image),
      sshKeys: listValue(form, field.sshKeys),
      firewalls: listValue(form, field.firewalls),
    },
    watchdog: {
      renewIntervalSeconds: numberValue(form, field.renewInterval),
      slackSeconds: numberValue(form, field.slack),
      maxLifetimeSeconds: numberValue(form, field.maxLifetime) * secondsPerMinute,
    },
  }
}

function Text({
  name,
  placeholder,
  required = false,
}: {
  name: string
  placeholder: string
  required?: boolean
}) {
  return (
    <input
      name={name}
      required={required}
      placeholder={placeholder}
      spellCheck={false}
      autoComplete="off"
      className={`${controlClass} w-full`}
    />
  )
}

function SecretReference({
  label,
  hint,
  nameField,
  keyField,
  secret,
  entry,
  required = false,
}: {
  label: string
  hint: string
  nameField: string
  keyField: string
  secret: string
  entry: string
  required?: boolean
}) {
  return (
    <Field label={label} hint={hint}>
      <span className="flex gap-snug">
        <Text name={nameField} placeholder={secret} required={required} />
        <Text name={keyField} placeholder={entry} required={required} />
      </span>
    </Field>
  )
}

function ConfigForm({ onCreated }: { onCreated: (name: string) => void }) {
  const [failure, setFailure] = useState<Error | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setFailure(null)
    try {
      const created = await createProviderConfig(requestFrom(new FormData(event.currentTarget)))
      onCreated(created.name)
    } catch (cause) {
      setFailure(errorFor(cause))
      setPending(false)
    }
  }

  return (
    <form onSubmit={submit} className="space-y-gutter">
      {failure === null ? null : (
        <Notice severity="danger" title="The cluster refused this provider config">
          {failure.message}
        </Notice>
      )}

      <Panel title="Provider">
        <div className="grid grid-cols-[repeat(auto-fill,minmax(15rem,1fr))] gap-gutter p-gutter">
          <Field label="Name" hint="A provider config is named once and never renamed.">
            <Text name={field.name} placeholder="hetzner" required />
          </Field>
          <Field label="Type">
            <select name={field.type} required defaultValue={providerTypes[0]} className={controlClass}>
              {providerTypes.map((type) => (
                <option key={type} value={type}>
                  {type}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Image" hint="The provider's own name for the image a burst node boots.">
            <Text name={field.image} placeholder="ubuntu-24.04" required />
          </Field>
          <Field label="SSH keys" hint="Optional. Names the provider holds, separated by commas.">
            <Text name={field.sshKeys} placeholder="workstation, laptop" />
          </Field>
          <Field label="Firewalls" hint="Optional. Names the provider holds, separated by commas.">
            <Text name={field.firewalls} placeholder="burst" />
          </Field>
        </div>
      </Panel>

      <Panel title="Secret references" note={secretsAreReferenced}>
        <div className="grid grid-cols-[repeat(auto-fill,minmax(20rem,1fr))] gap-gutter p-gutter">
          <SecretReference
            label="Provider credentials"
            hint="The API token the controller rents machines with."
            nameField={field.credentialsName}
            keyField={field.credentialsKey}
            secret="horizon-hetzner"
            entry="token"
            required
          />
          <SecretReference
            label="Cloud-init document"
            hint="The whole document a burst node boots, as rendered by horizon cloud-init."
            nameField={field.cloudInitName}
            keyField={field.cloudInitKey}
            secret="horizon-cloud-init"
            entry="cloud-init"
            required
          />
          <SecretReference
            label="Node credentials"
            hint="A lease is refused while this is unset, since teardown would rest on the node alone."
            nameField={field.nodeCredentialName}
            keyField={field.nodeCredentialKey}
            secret="horizon-hetzner-node"
            entry="token"
          />
          <SecretReference
            label="Join token"
            hint="Needed wherever the rendered document still carries the join token placeholder."
            nameField={field.joinTokenName}
            keyField={field.joinTokenKey}
            secret="horizon-join-token"
            entry="token"
          />
        </div>
      </Panel>

      <Panel
        title="Watchdog"
        note="Each leased node powers itself off on this clock, whatever the control plane is doing"
      >
        <div className="grid grid-cols-[repeat(auto-fill,minmax(15rem,1fr))] gap-gutter p-gutter">
          <Field label="Renew interval in seconds" hint="How often a node renews its lease on itself.">
            <Numeric name={field.renewInterval} bounds={renewIntervalBounds} />
          </Field>
          <Field label="Slack in seconds" hint="How long a missed renewal is tolerated before teardown.">
            <Numeric name={field.slack} bounds={slackBounds} />
          </Field>
          <Field label="Maximum lifetime in minutes" hint="The backstop no lease can extend past.">
            <Numeric name={field.maxLifetime} bounds={lifetimeBounds} />
          </Field>
        </div>
      </Panel>

      <div className="flex flex-wrap items-center justify-between gap-gutter">
        <p className="max-w-[60ch] text-copy-13 text-subtle">
          The controller resolves every reference and reports what it found, so a wrong name or a
          missing key reads back here as the reason the config is unready.
        </p>
        <Button type="submit" tone="primary" disabled={pending}>
          {pending ? 'Creating the provider config' : 'Create the provider config'}
        </Button>
      </div>
    </form>
  )
}

function Readiness({ config }: { config: ProviderConfigSummary }) {
  return (
    <Panel title="Readiness">
      <DefinitionGrid>
        <Definition label="Ready">
          <ConditionChip type={readyCondition} status={config.ready} />
        </Definition>
        <Definition label="Catalogue published">
          <ConditionChip type={catalogueCondition} status={config.cataloguePublished} />
        </Definition>
        <Definition label="Reason">{config.reason ?? absent}</Definition>
      </DefinitionGrid>
      {config.message === null ? null : (
        <p className="border-t border-line px-gutter py-cell text-copy-13 text-subtle">
          {config.message}
        </p>
      )}
    </Panel>
  )
}

// the controller resolves the references within seconds of the create, so the answer is read back rather than assumed
function CreatedConfig({ name }: { name: string }) {
  const view = usePolled(() => fetchMachines('', ''), machinesPath('', ''))
  const config = view.data?.configs.find((one) => one.name === name) ?? null

  return (
    <div className="space-y-gutter">
      <Notice severity="success" title={`${name} was created`}>
        The controller reads it on its next pass and reports what it resolved.
      </Notice>
      {view.error ? (
        <Notice severity="danger" title="The provider configs could not be read">
          {view.error.message}
        </Notice>
      ) : null}
      {config === null ? (
        <Loading label="Reading the config back from the cluster." />
      ) : (
        <Readiness config={config} />
      )}
      <div className="flex flex-wrap items-center gap-gutter">
        <ButtonLink href={newLeaseHref} tone="primary">
          Reserve capacity with it
        </ButtonLink>
        <Button type="button" onClick={view.reload}>
          Read it again
        </Button>
      </div>
    </div>
  )
}

export function ConfigNewRoute() {
  const [created, setCreated] = useState<string | null>(null)

  return (
    <>
      <PageHeader
        title="New provider config"
        lede="Name the Secrets the controller resolves, the image a burst node boots, and the clock each node powers itself off on."
      />
      {created === null ? <ConfigForm onCreated={setCreated} /> : <CreatedConfig name={created} />}
    </>
  )
}
