import { afterEach, describe, expect, it, vi } from 'vitest'

import type { MachineCatalogueResponse } from '@/lib/api'
import { MachinesRoute } from '@/routes/machines'
import { machinesHref } from '@/routes/router'
import {
  control,
  fill,
  jsonResponse,
  machinesBody,
  mount,
  providerConfigSummary,
  send,
  settle,
  stubFetchWith,
} from '@/routes/test-support'

const hetzner = providerConfigSummary('hetzner')

function catalogueWith(overrides: Partial<MachineCatalogueResponse> = {}): MachineCatalogueResponse {
  return { ...machinesBody([hetzner]), ...overrides }
}

function pageHeader(container: HTMLElement): HTMLElement {
  const heading = control<HTMLHeadingElement>(container, 'h1')
  return heading.parentElement?.parentElement as HTMLElement
}

describe('the machines route', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    window.history.pushState(null, '', machinesHref)
  })

  it('shows what would fill the provider config table rather than a bare paragraph', async () => {
    stubFetchWith(() => Promise.resolve(jsonResponse(machinesBody([]))))
    const view = await mount(<MachinesRoute config="" region="" />)

    expect(view.container.textContent).not.toContain('Provider configs')
    expect(view.container.textContent).toContain('The cluster holds no ProviderConfig')
    expect(view.container.querySelector('table')).not.toBeNull()

    await view.unmount()
  })

  it('blames the wiring rather than where the interface runs for an absent catalogue', async () => {
    stubFetchWith(() =>
      Promise.resolve(
        jsonResponse(catalogueWith({ config: 'hetzner', region: 'nbg1', state: 'CatalogueAbsent' })),
      ),
    )
    const view = await mount(<MachinesRoute config="hetzner" region="nbg1" />)

    const shown = view.container.textContent ?? ''
    expect(shown).toContain('cached in memory by the horizon controller process')
    expect(shown).not.toContain('started outside')

    await view.unmount()
  })

  it('does not repeat the heading bar around the instance type table, and relocates the refreshed note', async () => {
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
    expect(pageHeader(view.container).textContent).toContain('Refreshed')

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

  it('shows the deep-linked config immediately and keeps it once the catalogue loads', async () => {
    const resolvers: ((value: Response) => void)[] = []
    stubFetchWith(
      () =>
        new Promise<Response>((resolve) => {
          resolvers.push(resolve)
        }),
    )

    const view = await mount(<MachinesRoute config="hetzner" region="nbg1" />)
    const duringLoad = control<HTMLSelectElement>(view.container, 'select[name="config"]').value

    resolvers[0](
      jsonResponse(catalogueWith({ config: 'hetzner', region: 'nbg1', state: 'Listed', types: [] })),
    )
    await settle()
    const afterLoad = control<HTMLSelectElement>(view.container, 'select[name="config"]').value

    expect(duringLoad).toBe('hetzner')
    expect(afterLoad).toBe('hetzner')

    await view.unmount()
  })

  it('keeps the deep-linked config when only the region is resubmitted', async () => {
    stubFetchWith(() =>
      Promise.resolve(
        jsonResponse(catalogueWith({ config: 'hetzner', region: 'nbg1', state: 'Listed', types: [] })),
      ),
    )
    const view = await mount(<MachinesRoute config="hetzner" region="nbg1" />)

    await fill(control<HTMLInputElement>(view.container, 'input[name="region"]'), 'fsn1')
    await send(control<HTMLFormElement>(view.container, 'form'))

    expect(window.location.pathname + window.location.search).toBe(
      '/machines?config=hetzner&region=fsn1',
    )

    await view.unmount()
  })
})
