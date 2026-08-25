import { afterEach, describe, expect, it, vi } from 'vitest'

import type { ProviderConfigCreateRequest } from '@/lib/api'
import { interfaceHeader, machinesPath, providerConfigsPath } from '@/lib/api'
import { ConfigNewRoute } from '@/routes/config-new'
import {
  control,
  fill,
  jsonResponse,
  machinesBody,
  mount,
  providerConfigSummary,
  send,
  stubFetchWith,
} from '@/routes/test-support'

const refused = 'ProviderConfig.horizon.dev "hetzner" is invalid: spec.watchdog: Invalid value'
const unresolved = 'secret "horizon-hetzner" not found'

function stubCluster(created: () => Response, configs = machinesBody([])) {
  return stubFetchWith((input) => {
    const target = String(input)
    if (target === machinesPath('', '')) return Promise.resolve(jsonResponse(configs))
    return Promise.resolve(created())
  })
}

// the form reads the config back once it is created, so the create is found by its path rather than by being last
function created(respond: ReturnType<typeof stubCluster>): RequestInit | undefined {
  const call = respond.mock.calls.find(([input]) => String(input) === providerConfigsPath)
  if (call === undefined) throw new Error('the form submitted no provider config')
  return call[1]
}

function submitted(body: BodyInit | null | undefined): ProviderConfigCreateRequest {
  return JSON.parse(String(body)) as ProviderConfigCreateRequest
}

async function newConfigForm() {
  const view = await mount(<ConfigNewRoute />)
  return { view, form: control<HTMLFormElement>(view.container, 'form') }
}

async function fillReferences(container: HTMLElement) {
  await fill(control<HTMLInputElement>(container, 'input[name="name"]'), 'hetzner')
  await fill(control<HTMLInputElement>(container, 'input[name="credentialsName"]'), 'horizon-hetzner')
  await fill(control<HTMLInputElement>(container, 'input[name="credentialsKey"]'), 'token')
  await fill(control<HTMLInputElement>(container, 'input[name="cloudInitName"]'), 'horizon-cloud-init')
  await fill(control<HTMLInputElement>(container, 'input[name="cloudInitKey"]'), 'cloud-init')
  await fill(control<HTMLInputElement>(container, 'input[name="image"]'), 'ubuntu-24.04')
}

describe('the provider config form', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('submits the references the form holds, and the header the guard wants', async () => {
    const respond = stubCluster(() => jsonResponse(providerConfigSummary('hetzner'), 201))
    const { view, form } = await newConfigForm()

    await fillReferences(view.container)
    await send(form)

    const init = created(respond)
    expect(init?.method).toBe('POST')
    expect(new Headers(init?.headers).get(interfaceHeader)).not.toBeNull()
    expect(submitted(init?.body)).toEqual({
      name: 'hetzner',
      type: 'hetzner',
      hetzner: {
        credentialsSecretRef: { name: 'horizon-hetzner', key: 'token' },
        nodeCredentialSecretRef: null,
        joinTokenSecretRef: null,
        cloudInitSecretRef: { name: 'horizon-cloud-init', key: 'cloud-init' },
        image: 'ubuntu-24.04',
        sshKeys: [],
        firewalls: [],
      },
      watchdog: { renewIntervalSeconds: 60, slackSeconds: 120, maxLifetimeSeconds: 28800 },
    })

    await view.unmount()
  })

  it('carries the optional secret references only once they are named', async () => {
    const respond = stubCluster(() => jsonResponse(providerConfigSummary('hetzner'), 201))
    const { view, form } = await newConfigForm()

    await fillReferences(view.container)
    await fill(
      control<HTMLInputElement>(view.container, 'input[name="nodeCredentialName"]'),
      'horizon-hetzner-node',
    )
    await fill(control<HTMLInputElement>(view.container, 'input[name="nodeCredentialKey"]'), 'token')
    await fill(control<HTMLInputElement>(view.container, 'input[name="sshKeys"]'), 'workstation, laptop')
    await send(form)

    const request = submitted(created(respond)?.body)
    expect(request.hetzner?.nodeCredentialSecretRef).toEqual({
      name: 'horizon-hetzner-node',
      key: 'token',
    })
    expect(request.hetzner?.joinTokenSecretRef).toBeNull()
    expect(request.hetzner?.sshKeys).toEqual(['workstation', 'laptop'])

    await view.unmount()
  })

  it('shows the refusal the cluster answered with', async () => {
    stubCluster(() =>
      jsonResponse({ status: 422, title: 'Unprocessable Entity', detail: refused }, 422),
    )
    const { view, form } = await newConfigForm()

    await fillReferences(view.container)
    await send(form)

    expect(view.container.textContent).toContain(refused)

    await view.unmount()
  })

  it('reads the created config back and names why it is unready', async () => {
    const unready = machinesBody([
      providerConfigSummary('hetzner', {
        ready: 'False',
        reason: 'SecretUnresolved',
        message: unresolved,
        cataloguePublished: 'False',
      }),
    ])
    stubCluster(() => jsonResponse(providerConfigSummary('hetzner'), 201), unready)
    const { view, form } = await newConfigForm()

    await fillReferences(view.container)
    await send(form)

    expect(view.container.textContent).toContain('SecretUnresolved')
    expect(view.container.textContent).toContain(unresolved)

    await view.unmount()
  })

  it('never offers to create a secret', async () => {
    stubCluster(() => jsonResponse(providerConfigSummary('hetzner'), 201))
    const { view } = await newConfigForm()

    expect(view.container.querySelector('input[type="password"]')).toBeNull()
    expect(view.container.textContent).toContain('never creates a Secret')

    await view.unmount()
  })
})
