import { act } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { MachineCatalogueResponse } from '@/lib/api'
import { MachinesRoute } from '@/routes/machines'
import {
  control,
  jsonResponse,
  machinesBody,
  mount,
  providerConfigSummary,
  stubFetchWith,
} from '@/routes/test-support'

const hetzner = providerConfigSummary('hetzner')

function catalogueWith(overrides: Partial<MachineCatalogueResponse> = {}): MachineCatalogueResponse {
  return { ...machinesBody([hetzner]), ...overrides }
}

async function settle() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

describe('the machines route', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('shows what would fill the provider config table rather than a bare paragraph', async () => {
    stubFetchWith(() => Promise.resolve(jsonResponse(machinesBody([]))))
    const view = await mount(<MachinesRoute config="" region="" />)

    expect(view.container.textContent).not.toContain('Provider configs')
    expect(view.container.textContent).toContain('The cluster holds no ProviderConfig')
    expect(control<HTMLTableElement>(view.container, 'table')).toBeTruthy()

    await view.unmount()
  })

  it('does not repeat the heading bar around the instance type table', async () => {
    stubFetchWith(() =>
      Promise.resolve(
        jsonResponse(
          catalogueWith({
            config: 'hetzner',
            region: 'nbg1',
            state: 'Listed',
            refreshedAt: '2026-08-21T12:00:00Z',
            types: [],
          }),
        ),
      ),
    )
    const view = await mount(<MachinesRoute config="hetzner" region="nbg1" />)

    expect(view.container.textContent).not.toContain('Instance types')
    expect(view.container.textContent).toContain('Refreshed')
    expect(control(view.container, 'thead th')).toBeTruthy()

    await view.unmount()
  })

  it('keeps the picker mounted while new route props reload the catalogue', async () => {
    const resolvers: ((value: Response) => void)[] = []
    let calls = 0
    stubFetchWith(() => {
      calls += 1
      if (calls === 1) return Promise.resolve(jsonResponse(catalogueWith({})))
      return new Promise<Response>((resolve) => {
        resolvers.push(resolve)
      })
    })

    const view = await mount(<MachinesRoute config="" region="" />)
    expect(control<HTMLSelectElement>(view.container, 'select[name="config"]').value).toBe('')

    await view.render(<MachinesRoute config="hetzner" region="nbg1" />)

    expect(control<HTMLFormElement>(view.container, 'form')).toBeTruthy()
    expect(control<HTMLSelectElement>(view.container, 'select[name="config"]').value).toBe('hetzner')
    expect(view.container.textContent).toContain('Reading the provider configs from the cluster.')
    expect(calls).toBe(2)

    resolvers[0](
      jsonResponse(catalogueWith({ config: 'hetzner', region: 'nbg1', state: 'CatalogueUnfilled' })),
    )
    await settle()

    expect(view.container.textContent).not.toContain('Reading the provider configs')
    expect(view.container.textContent).toContain('has not been filled for hetzner')

    await view.unmount()
  })
})
