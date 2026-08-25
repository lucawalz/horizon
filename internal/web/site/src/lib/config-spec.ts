import type { ProviderConfigSpecRequest, SecretKeyRequest } from '@/lib/api'
import { fieldValue, numberValue } from '@/lib/form'

export const field = {
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

export const listSeparator = ', '

const listDelimiter = ','

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
    .split(listDelimiter)
    .map((entry) => entry.trim())
    .filter((entry) => entry !== '')
}

// every field the form holds is submitted, so a reference cleared in the form is cleared in the cluster
export function specFrom(form: FormData): ProviderConfigSpecRequest {
  return {
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
      maxLifetimeSeconds: numberValue(form, field.maxLifetime),
    },
  }
}
