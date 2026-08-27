import type { ReactNode } from 'react'

import { controlClass, Field, Numeric } from '@/components/controls'
import type { ProviderConfigSpecRequest, SecretKeyRequest } from '@/lib/api'
import { field, listSeparator } from '@/lib/config-spec'
import { Panel } from '@/routes/page'
import { minRenewIntervalSeconds, secondsPerMinute, watchdogLifetimeSeconds } from '@/routes/units'

const providerTypes = ['hetzner']

const renewIntervalBounds = { min: minRenewIntervalSeconds, initial: secondsPerMinute }
const slackBounds = { min: 1, initial: 2 * secondsPerMinute }
const lifetimeBounds = { ...watchdogLifetimeSeconds, initial: 8 * 60 * secondsPerMinute }

const secretsAreReferenced =
  'This form references Secrets and never creates a Secret; each must already exist in the ' +
  'namespace the controller runs in'

export function Text({
  name,
  placeholder,
  value,
  required = false,
}: {
  name: string
  placeholder: string
  value?: string
  required?: boolean
}) {
  return (
    <input
      name={name}
      required={required}
      placeholder={placeholder}
      defaultValue={value}
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
  held,
  required = false,
}: {
  label: string
  hint: string
  nameField: string
  keyField: string
  secret: string
  entry: string
  held: SecretKeyRequest | null | undefined
  required?: boolean
}) {
  return (
    <Field label={label} hint={hint}>
      <span className="flex gap-snug">
        <Text name={nameField} placeholder={secret} value={held?.name} required={required} />
        <Text name={keyField} placeholder={entry} value={held?.key} required={required} />
      </span>
    </Field>
  )
}

function boundsFrom(bounds: { min: number; max?: number; initial: number }, held?: number) {
  return held === undefined ? bounds : { ...bounds, initial: held }
}

// a config the summary reports as unrepresentable never reaches this form, so an absent spec here means a form that starts empty
export function ConfigFields({
  spec,
  lead,
}: {
  spec: ProviderConfigSpecRequest | null
  lead?: ReactNode
}) {
  const hetzner = spec?.hetzner ?? null

  return (
    <>
      <Panel title="Provider">
        <div className="grid grid-cols-[repeat(auto-fill,minmax(15rem,1fr))] gap-gutter p-gutter">
          {lead}
          <Field label="Type">
            <select
              name={field.type}
              required
              defaultValue={spec?.type ?? providerTypes[0]}
              className={controlClass}
            >
              {providerTypes.map((type) => (
                <option key={type} value={type}>
                  {type}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Image" hint="The provider's own name for the image a burst node boots.">
            <Text name={field.image} placeholder="ubuntu-24.04" value={hetzner?.image} required />
          </Field>
          <Field label="SSH keys" hint="Optional. Names the provider holds, separated by commas.">
            <Text
              name={field.sshKeys}
              placeholder="workstation, laptop"
              value={hetzner?.sshKeys.join(listSeparator)}
            />
          </Field>
          <Field label="Firewalls" hint="Optional. Names the provider holds, separated by commas.">
            <Text
              name={field.firewalls}
              placeholder="burst"
              value={hetzner?.firewalls.join(listSeparator)}
            />
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
            held={hetzner?.credentialsSecretRef}
            required
          />
          <SecretReference
            label="Cloud-init document"
            hint="The whole document a burst node boots, as rendered by horizon cloud-init."
            nameField={field.cloudInitName}
            keyField={field.cloudInitKey}
            secret="horizon-cloud-init"
            entry="cloud-init"
            held={hetzner?.cloudInitSecretRef}
            required
          />
          <SecretReference
            label="Node credentials"
            hint="A lease is refused while this is unset, since teardown would rest on the node alone."
            nameField={field.nodeCredentialName}
            keyField={field.nodeCredentialKey}
            secret="horizon-hetzner-node"
            entry="token"
            held={hetzner?.nodeCredentialSecretRef}
          />
          <SecretReference
            label="Join token"
            hint="Needed wherever the rendered document still carries the join token placeholder."
            nameField={field.joinTokenName}
            keyField={field.joinTokenKey}
            secret="horizon-join-token"
            entry="token"
            held={hetzner?.joinTokenSecretRef}
          />
        </div>
      </Panel>

      <Panel title="Watchdog">
        <div className="grid grid-cols-[repeat(auto-fill,minmax(15rem,1fr))] gap-gutter p-gutter">
          <Field label="Renew interval in seconds" hint="How often a node renews its lease on itself.">
            <Numeric
              name={field.renewInterval}
              bounds={boundsFrom(renewIntervalBounds, spec?.watchdog.renewIntervalSeconds)}
            />
          </Field>
          <Field label="Slack in seconds" hint="How long a missed renewal is tolerated before teardown.">
            <Numeric
              name={field.slack}
              bounds={boundsFrom(slackBounds, spec?.watchdog.slackSeconds)}
            />
          </Field>
          <Field
            label="Maximum lifetime in seconds"
            hint="The backstop no lease can extend past. A machine already running keeps the one latched when it was created."
          >
            <Numeric
              name={field.maxLifetime}
              bounds={boundsFrom(lifetimeBounds, spec?.watchdog.maxLifetimeSeconds)}
            />
          </Field>
        </div>
      </Panel>
    </>
  )
}
