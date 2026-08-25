import { afterEach, describe, expect, it, vi } from 'vitest'

import { ConfigDetailRoute } from '@/routes/config-detail'
import { machinesHrefFor } from '@/routes/router'
import {
  control,
  hetznerProviderDetail,
  jsonResponse,
  mount,
  providerConfigDetailBody,
  providerConfigSummary,
  stubFetch,
  stubFetchWith,
} from '@/routes/test-support'

const configName = 'hetzner'

describe('the provider config detail route', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('names every secret the configuration references, by name and key', async () => {
    stubFetch(providerConfigDetailBody(configName))
    const view = await mount(<ConfigDetailRoute name={configName} />)

    const shown = view.container.textContent ?? ''
    expect(shown).toContain('horizon-hetzner')
    expect(shown).toContain('token')
    expect(shown).toContain('horizon-cloud-init')
    expect(shown).toContain('user-data')

    await view.unmount()
  })

  it('states which optional reference the configuration leaves unset', async () => {
    stubFetch(
      providerConfigDetailBody(configName, {
        hetzner: hetznerProviderDetail({ joinTokenSecretRef: null }),
      }),
    )
    const view = await mount(<ConfigDetailRoute name={configName} />)

    const shown = view.container.textContent ?? ''
    expect(shown).toContain('Join token')
    expect(shown).toContain('not set')

    await view.unmount()
  })

  it('carries the reason and the message of an unready condition', async () => {
    stubFetch(
      providerConfigDetailBody(configName, {
        summary: providerConfigSummary(configName, { ready: 'False' }),
        conditions: [
          {
            type: 'Ready',
            status: 'False',
            reason: 'SecretUnresolved',
            message: 'secret "horizon-join-token" not found',
            lastTransitionTime: '2026-08-25T11:00:00Z',
          },
        ],
      }),
    )
    const view = await mount(<ConfigDetailRoute name={configName} />)

    const shown = view.container.textContent ?? ''
    expect(shown).toContain('SecretUnresolved')
    expect(shown).toContain('horizon-join-token')

    await view.unmount()
  })

  it('tallies the published catalogue and links each region to the machines route', async () => {
    stubFetch(
      providerConfigDetailBody(configName, {
        catalogue: {
          types: 3,
          regions: [
            { region: 'fsn1', types: 1 },
            { region: 'nbg1', types: 2 },
          ],
          refreshedAt: '2026-08-25T11:55:00Z',
        },
      }),
    )
    const view = await mount(<ConfigDetailRoute name={configName} />)

    expect(view.container.textContent).toContain('3 instance types')
    const link = control<HTMLAnchorElement>(
      view.container,
      `a[href="${machinesHrefFor(configName, 'nbg1')}"]`,
    )
    expect(link.textContent).toContain('nbg1')

    await view.unmount()
  })

  it('states an unpublished catalogue rather than showing an empty tally', async () => {
    stubFetch(
      providerConfigDetailBody(configName, {
        catalogue: { types: 0, regions: [], refreshedAt: null },
      }),
    )
    const view = await mount(<ConfigDetailRoute name={configName} />)

    expect(view.container.textContent).toContain('has published no instance type')

    await view.unmount()
  })

  it('reports a config the cluster does not hold', async () => {
    stubFetchWith(() =>
      Promise.resolve(
        jsonResponse({ status: 404, title: 'Not Found', detail: 'no provider config named' }, 404),
      ),
    )
    const view = await mount(<ConfigDetailRoute name="absent" />)

    expect(view.container.textContent).toContain('not in the cluster')

    await view.unmount()
  })
})
