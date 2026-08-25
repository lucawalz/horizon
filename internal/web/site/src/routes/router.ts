import { useEffect, useState } from 'react'

export const leasesHref = '/'
export const machinesHref = '/machines'
export const newLeaseHref = '/new'
export const newConfigHref = '/configs/new'

const leasePrefix = '/leases/'
const configQueryKey = 'config'
const regionQueryKey = 'region'
const primaryButton = 0

export type Route =
  | { name: 'leases' }
  | { name: 'new' }
  | { name: 'new-config' }
  | { name: 'lease'; lease: string }
  | { name: 'machines'; config: string; region: string }
  | { name: 'unknown'; path: string }

export function leaseHref(name: string): string {
  return leasePrefix + encodeURIComponent(name)
}

export function machinesHrefFor(config: string, region: string): string {
  const query = new URLSearchParams()
  if (config !== '') query.set(configQueryKey, config)
  if (region !== '') query.set(regionQueryKey, region)
  const suffix = query.toString()
  return suffix === '' ? machinesHref : `${machinesHref}?${suffix}`
}

function parseRoute(path: string, search: string): Route {
  if (path === leasesHref) return { name: 'leases' }
  if (path === newLeaseHref) return { name: 'new' }
  if (path === newConfigHref) return { name: 'new-config' }

  if (path.startsWith(leasePrefix)) {
    const lease = decodeURIComponent(path.slice(leasePrefix.length))
    return lease === '' ? { name: 'leases' } : { name: 'lease', lease }
  }

  if (path === machinesHref) {
    const query = new URLSearchParams(search)
    return {
      name: 'machines',
      config: query.get(configQueryKey) ?? '',
      region: query.get(regionQueryKey) ?? '',
    }
  }

  return { name: 'unknown', path }
}

function currentRoute(): Route {
  return parseRoute(window.location.pathname, window.location.search)
}

export function navigate(href: string): void {
  if (href === window.location.pathname + window.location.search) return
  window.history.pushState(null, '', href)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

function internalTarget(event: MouseEvent): string | null {
  if (event.defaultPrevented || event.button !== primaryButton) return null
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return null

  const anchor = (event.target as Element | null)?.closest?.('a')
  if (!anchor || anchor.target !== '' || anchor.hasAttribute('download')) return null

  const href = anchor.getAttribute('href')
  if (href === null || href.startsWith('#')) return null

  const resolved = new URL(anchor.href)
  if (resolved.origin !== window.location.origin) return null
  return resolved.pathname + resolved.search
}

// the shell is served for every unbundled path, so a same-origin link is routed here rather than reloading the bundle
export function useRoute(): Route {
  const [route, setRoute] = useState<Route>(currentRoute)

  useEffect(() => {
    const follow = () => setRoute(currentRoute())
    const intercept = (event: MouseEvent) => {
      const href = internalTarget(event)
      if (href === null) return
      event.preventDefault()
      navigate(href)
    }

    window.addEventListener('popstate', follow)
    document.addEventListener('click', intercept)
    return () => {
      window.removeEventListener('popstate', follow)
      document.removeEventListener('click', intercept)
    }
  }, [])

  return route
}
