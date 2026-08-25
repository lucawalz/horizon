import { AppFrame, AppHeader, AppMain, NavLink } from '@/components/app-frame'
import { ConfigDetailRoute } from '@/routes/config-detail'
import { ConfigEditRoute } from '@/routes/config-edit'
import { ConfigNewRoute } from '@/routes/config-new'
import { LeaseDetailRoute } from '@/routes/lease-detail'
import { LeaseListRoute } from '@/routes/lease-list'
import { LeaseNewRoute } from '@/routes/lease-new'
import { MachinesRoute } from '@/routes/machines'
import type { Route } from '@/routes/router'
import { leasesHref, machinesHref, useRoute } from '@/routes/router'
import { UnknownRoute } from '@/routes/unknown'

function View({ route }: { route: Route }) {
  switch (route.name) {
    case 'leases':
      return <LeaseListRoute />
    case 'new':
      return <LeaseNewRoute />
    case 'lease':
      return <LeaseDetailRoute name={route.lease} />
    case 'config':
      return <ConfigDetailRoute name={route.config} />
    case 'machines':
      return <MachinesRoute config={route.config} region={route.region} />
    case 'new-config':
      return <ConfigNewRoute />
    case 'edit-config':
      return <ConfigEditRoute name={route.config} />
    default:
      return <UnknownRoute path={route.path} />
  }
}

export default function App() {
  const route = useRoute()

  return (
    <AppFrame>
      <AppHeader>
        <NavLink
          href={leasesHref}
          current={route.name === 'leases' || route.name === 'lease' || route.name === 'new'}
        >
          Leases
        </NavLink>
        <NavLink
          href={machinesHref}
          current={
            route.name === 'machines' ||
            route.name === 'new-config' ||
            route.name === 'config' ||
            route.name === 'edit-config'
          }
        >
          Machines
        </NavLink>
      </AppHeader>
      <AppMain>
        <View route={route} />
      </AppMain>
    </AppFrame>
  )
}
