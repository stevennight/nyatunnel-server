import { Link, Navigate, createRootRoute, createRoute, createRouter } from '@tanstack/react-router'
import type { RouterHistory } from '@tanstack/react-router'
import type { ComponentType } from 'react'
import { AppFrame } from './components/layout'
import { Notice } from './components/ui'
import { AuditPage } from './pages/audit'
import { ChannelsPage } from './pages/channels'
import { DashboardPage } from './pages/dashboard'
import { DevicesPage } from './pages/devices'
import { DomainsPage } from './pages/domains'
import { PortPoolsPage } from './pages/port-pools'
import { RequestsPage } from './pages/requests'
import { SecurityPage } from './pages/security'
import { SettingsPage } from './pages/settings'
import { TunnelsPage } from './pages/tunnels'
import { UsersPage } from './pages/users'
import { useSession } from './session'

function Home() {
  const { isAdmin } = useSession()
  return <Navigate to={isAdmin ? '/dashboard' : '/tunnels'} replace />
}

/** Wraps an admin-only page; normal users get a short explanation instead of a wall of 403s. */
function adminOnly(Page: ComponentType) {
  return function AdminPage() {
    const { isAdmin } = useSession()
    if (!isAdmin) {
      return (
        <Notice tone="warn">
          该页面仅管理员可访问。<Link to="/tunnels">返回我的隧道</Link>
        </Notice>
      )
    }
    return <Page />
  }
}

function NotFound() {
  return (
    <Notice tone="warn">
      页面不存在。<Link to="/">返回首页</Link>
    </Notice>
  )
}

const rootRoute = createRootRoute({ component: AppFrame, notFoundComponent: NotFound })

const getParentRoute = () => rootRoute

const routeTree = rootRoute.addChildren([
  createRoute({ getParentRoute, path: '/', component: Home }),
  createRoute({ getParentRoute, path: '/dashboard', component: adminOnly(DashboardPage) }),
  createRoute({ getParentRoute, path: '/users', component: adminOnly(UsersPage) }),
  createRoute({ getParentRoute, path: '/tunnels', component: TunnelsPage }),
  createRoute({ getParentRoute, path: '/devices', component: DevicesPage }),
  createRoute({ getParentRoute, path: '/requests', component: RequestsPage }),
  createRoute({ getParentRoute, path: '/domains', component: DomainsPage }),
  createRoute({ getParentRoute, path: '/port-pools', component: adminOnly(PortPoolsPage) }),
  createRoute({ getParentRoute, path: '/audit', component: adminOnly(AuditPage) }),
  createRoute({ getParentRoute, path: '/channels', component: adminOnly(ChannelsPage) }),
  createRoute({ getParentRoute, path: '/settings', component: adminOnly(SettingsPage) }),
  createRoute({ getParentRoute, path: '/security', component: SecurityPage }),
  // Signed out, AppFrame shows the login screen here; signed in, it hands off to `next`.
  createRoute({ getParentRoute, path: '/login', component: Home }),
])

// A factory (rather than only a singleton) so tests can supply a memory history.
export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, history })
}

export const router = createAppRouter()

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
