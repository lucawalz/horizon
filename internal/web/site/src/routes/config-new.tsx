import type { FormEvent } from 'react'
import { useState } from 'react'

import { Button, ButtonLink, Field } from '@/components/controls'
import type { ProviderConfigCreateRequest, ProviderConfigSummary } from '@/lib/api'
import { createProviderConfig, fetchMachines, machinesPath } from '@/lib/api'
import { field, specFrom } from '@/lib/config-spec'
import { errorFor } from '@/lib/errors'
import { fieldValue } from '@/lib/form'
import { ConfigFields, Text } from '@/routes/config-form'
import { ConfigReadiness } from '@/routes/config-readiness'
import { Loading, Notice, PageHeader } from '@/routes/page'
import { usePolled } from '@/routes/poll'
import { newLeaseHref } from '@/routes/router'

function requestFrom(form: FormData): ProviderConfigCreateRequest {
  return { name: fieldValue(form, field.name), ...specFrom(form) }
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

      <ConfigFields
        spec={null}
        lead={
          <Field label="Name" hint="A provider config is named once and never renamed.">
            <Text name={field.name} placeholder="hetzner" required />
          </Field>
        }
      />

      <div className="flex flex-wrap items-center justify-end gap-gutter">
        <Button type="submit" tone="primary" disabled={pending}>
          {pending ? 'Creating the provider config' : 'Create the provider config'}
        </Button>
      </div>
    </form>
  )
}

// the controller resolves the references within seconds of the create, so the answer is read back rather than assumed
function CreatedConfig({ name }: { name: string }) {
  const view = usePolled(() => fetchMachines('', ''), machinesPath('', ''))
  const config = view.data?.configs.find((one: ProviderConfigSummary) => one.name === name) ?? null

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
        <ConfigReadiness config={config} />
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
      <PageHeader title="New provider config" />
      {created === null ? <ConfigForm onCreated={setCreated} /> : <CreatedConfig name={created} />}
    </>
  )
}
