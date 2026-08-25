import { afterEach, describe, expect, it, vi } from 'vitest'

import type { LeaseSummary, ProviderConfigSpecRequest, ProviderConfigSummary } from '@/lib/api'
import { interfaceHeader, leasesPath, machinesPath, providerConfigPath } from '@/lib/api'
import { ConfigEditRoute } from '@/routes/config-edit'
import {
  buttonLabelled,
  click,
  control,
  fill,
  jsonResponse,
  leaseListBody,
  leaseSummary,
  machinesBody,
  mount,
  providerConfigSpec,
  providerConfigSummary,
  send,
  settle,
  stubFetchWith,
} from '@/routes/test-support'

const name = 'hetzner'
const stillBound =
  'ProviderConfig "hetzner" is still named by batch-run. releasing those leases is what frees this configuration'

function stubCluster(
  config: ProviderConfigSummary,
  leases: LeaseSummary[],
  written: () => Response = () => jsonResponse(config),
) {
  return stubFetchWith((input) => {
    const target = String(input)
    if (target === machinesPath('', '')) return Promise.resolve(jsonResponse(machinesBody([config])))
    if (target === leasesPath) return Promise.resolve(jsonResponse(leaseListBody(leases)))
    return Promise.resolve(written())
  })
}

function written(respond: ReturnType<typeof stubCluster>): RequestInit | undefined {
  const call = respond.mock.calls.find(([input]) => String(input) === providerConfigPath(name))
  if (call === undefined) throw new Error('the view wrote nothing to the provider config')
  return call[1]
}

function submitted(body: BodyInit | null | undefined): ProviderConfigSpecRequest {
  return JSON.parse(String(body)) as ProviderConfigSpecRequest
}

async function editView() {
  const view = await mount(<ConfigEditRoute name={name} />)
  await settle()
  return { view }
}

function valueOf(container: HTMLElement, field: string): string {
  return control<HTMLInputElement>(container, `input[name="${field}"]`).value
}

describe('the provider config edit page', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('fills every field from the spec the summary carries', async () => {
    stubCluster(providerConfigSummary(name), [])
    const { view } = await editView()

    expect(valueOf(view.container, 'image')).toBe('ubuntu-24.04')
    expect(valueOf(view.container, 'credentialsName')).toBe('horizon-hetzner')
    expect(valueOf(view.container, 'credentialsKey')).toBe('token')
    expect(valueOf(view.container, 'nodeCredentialName')).toBe('horizon-hetzner-node')
    expect(valueOf(view.container, 'joinTokenName')).toBe('')
    expect(valueOf(view.container, 'sshKeys')).toBe('workstation')
    expect(valueOf(view.container, 'maxLifetime')).toBe('28800')

    await view.unmount()
  })

  it('replaces the whole spec, with the verb and the header the guard wants', async () => {
    const respond = stubCluster(providerConfigSummary(name), [])
    const { view } = await editView()

    await fill(control<HTMLInputElement>(view.container, 'input[name="credentialsKey"]'), 'rotated')
    await fill(control<HTMLInputElement>(view.container, 'input[name="sshKeys"]'), '')
    await send(control<HTMLFormElement>(view.container, 'form'))

    const init = written(respond)
    expect(init?.method).toBe('PUT')
    expect(new Headers(init?.headers).get(interfaceHeader)).not.toBeNull()

    const spec = submitted(init?.body)
    expect(spec.hetzner?.credentialsSecretRef).toEqual({ name: 'horizon-hetzner', key: 'rotated' })
    expect(spec.hetzner?.sshKeys).toEqual([])
    expect(spec.watchdog.maxLifetimeSeconds).toBe(28800)

    await view.unmount()
  })

  it('names the leases bound to the config before anything is submitted', async () => {
    stubCluster(providerConfigSummary(name), [
      leaseSummary({ name: 'batch-run', providerRef: name }),
      leaseSummary({ name: 'other-run', providerRef: 'aws' }),
    ])
    const { view } = await editView()

    expect(view.container.textContent).toContain('batch-run')
    expect(view.container.textContent).not.toContain('other-run')

    await view.unmount()
  })

  it('offers no delete while a lease holds the config, and says which', async () => {
    stubCluster(providerConfigSummary(name), [leaseSummary({ name: 'batch-run', providerRef: name })])
    const { view } = await editView()

    expect(() => buttonLabelled(view.container, 'Delete this provider config')).toThrow()
    expect(view.container.textContent).toContain('One capacity lease holds hetzner')

    await view.unmount()
  })

  it('renders the refusal the controller answered with rather than a generic failure', async () => {
    stubCluster(providerConfigSummary(name), [], () =>
      jsonResponse({ status: 409, title: 'Conflict', detail: stillBound }, 409),
    )
    const { view } = await editView()

    await click(buttonLabelled(view.container, 'Delete this provider config'))
    await click(buttonLabelled(view.container, 'Ask the controller to delete'))

    expect(view.container.textContent).toContain('A capacity lease still holds this provider config')
    expect(view.container.textContent).toContain(stillBound)

    await view.unmount()
  })

  it('declines to edit a config this form would rewrite rather than change', async () => {
    const selected = providerConfigSummary(name, { spec: null })
    stubCluster(selected, [])
    const { view } = await editView()

    expect(view.container.querySelector('form')).toBeNull()
    expect(view.container.textContent).toContain('holds more than this form can carry')
    expect(view.container.textContent).toContain('by id or by selector')

    await view.unmount()
  })

  it('submits the watchdog seconds a config holds without rounding them', async () => {
    const odd = providerConfigSummary(name, {
      spec: providerConfigSpec({
        watchdog: { renewIntervalSeconds: 15, slackSeconds: 45, maxLifetimeSeconds: 331 },
      }),
    })
    const respond = stubCluster(odd, [])
    const { view } = await editView()

    await send(control<HTMLFormElement>(view.container, 'form'))

    expect(submitted(written(respond)?.body).watchdog).toEqual({
      renewIntervalSeconds: 15,
      slackSeconds: 45,
      maxLifetimeSeconds: 331,
    })

    await view.unmount()
  })
})
