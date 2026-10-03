import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, Outlet, useRouterState } from '@tanstack/react-router'
import {
  Cable,
  Globe,
  LayoutDashboard,
  LogOut,
  Menu,
  MonitorSmartphone,
  Network,
  ScrollText,
  Settings,
  ShieldCheck,
  Users,
  X,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { describeError, getBootstrap, getMe, logout } from '../api'
import { roleLabel } from '../labels'
import { LoginPage, SetupPage } from '../pages/auth'
import { SecurityPage } from '../pages/security'
import { SessionProvider } from '../session'
import type { Session } from '../session'
import type { Bootstrap } from '../types'
import { Notice } from './ui'

export type AppPath = '/dashboard' | '/users' | '/tunnels' | '/devices' | '/domains' | '/port-pools' | '/audit' | '/settings' | '/security'

type NavItem = { to: AppPath; label: string; icon: LucideIcon }
type NavGroup = { label?: string; items: NavItem[] }

const adminNav: NavGroup[] = [
  {
    items: [
      { to: '/dashboard', label: '仪表盘', icon: LayoutDashboard },
      { to: '/users', label: '用户', icon: Users },
      { to: '/tunnels', label: '隧道', icon: Network },
      { to: '/devices', label: '设备', icon: MonitorSmartphone },
    ],
  },
  {
    label: '资源',
    items: [
      { to: '/domains', label: '域名', icon: Globe },
      { to: '/port-pools', label: '端口池', icon: Cable },
    ],
  },
  {
    label: '系统',
    items: [
      { to: '/audit', label: '审计日志', icon: ScrollText },
      { to: '/settings', label: '系统设置', icon: Settings },
    ],
  },
  { label: '账号', items: [{ to: '/security', label: '账号安全', icon: ShieldCheck }] },
]

const userNav: NavGroup[] = [
  {
    items: [
      { to: '/tunnels', label: '我的隧道', icon: Network },
      { to: '/devices', label: '我的设备', icon: MonitorSmartphone },
    ],
  },
  { label: '账号', items: [{ to: '/security', label: '账号安全', icon: ShieldCheck }] },
]

const securityOnlyNav: NavGroup[] = [{ label: '账号', items: [{ to: '/security', label: '账号安全', icon: ShieldCheck }] }]

/** Decides between first-run setup, login and the console itself. */
export function AppFrame() {
  const bootstrap = useQuery({ queryKey: ['bootstrap'], queryFn: getBootstrap })

  useEffect(() => {
    const name = bootstrap.data?.serverName
    if (name) document.title = name === 'NyaTunnel' ? name : `${name} · NyaTunnel`
  }, [bootstrap.data?.serverName])

  if (bootstrap.isError) {
    return (
      <div className="auth-wrap">
        <div className="auth-card">
          <Notice tone="bad">无法连接服务端：{describeError(bootstrap.error)}</Notice>
          <button type="button" className="btn" onClick={() => bootstrap.refetch()}>
            重试
          </button>
        </div>
      </div>
    )
  }
  if (!bootstrap.data) return <div className="auth-wrap hint">加载中…</div>
  if (bootstrap.data.needsSetup) return <SetupPage />
  if (!bootstrap.data.user) return <LoginPage serverName={bootstrap.data.serverName} />
  return <Console bootstrap={bootstrap.data} />
}

function Console({ bootstrap }: { bootstrap: Bootstrap }) {
  const client = useQueryClient()
  const me = useQuery({ queryKey: ['me'], queryFn: getMe })
  const [drawer, setDrawer] = useState(false)
  const pathname = useRouterState({ select: (s) => s.location.pathname })

  useEffect(() => setDrawer(false), [pathname])

  const signOut = useMutation({
    mutationFn: logout,
    onSettled: () => {
      // Forget everything except bootstrap, which decides which screen comes next.
      client.removeQueries({ predicate: (q) => q.queryKey[0] !== 'bootstrap' })
      void client.invalidateQueries({ queryKey: ['bootstrap'] })
    },
  })

  const session = useMemo<Session | null>(() => {
    if (!me.data) return null
    return {
      bootstrap,
      user: me.data.user,
      isAdmin: me.data.user.role === 'admin',
      mustSetupTotp: me.data.mustSetupTotp,
    }
  }, [bootstrap, me.data])

  if (me.isError && !me.data) {
    return (
      <div className="auth-wrap">
        <div className="auth-card">
          <Notice tone="bad">{describeError(me.error)}</Notice>
          <button type="button" className="btn" onClick={() => me.refetch()}>
            重试
          </button>
        </div>
      </div>
    )
  }
  if (!session) return <div className="auth-wrap hint">加载中…</div>

  const nav = session.mustSetupTotp ? securityOnlyNav : session.isAdmin ? adminNav : userNav

  return (
    <SessionProvider value={session}>
      <div className={`app${drawer ? ' drawer-open' : ''}`}>
        <aside className="side" aria-label="主导航">
          <div className="logo">
            <i />
            <span>NyaTunnel</span>
            <button type="button" className="icon-btn only-mobile" aria-label="关闭菜单" onClick={() => setDrawer(false)}>
              <X size={18} />
            </button>
          </div>
          {nav.map((group, i) => (
            <nav key={group.label ?? i} aria-label={group.label ?? '常用'}>
              {group.label && <div className="sec">{group.label}</div>}
              {group.items.map(({ to, label, icon: Icon }) => (
                <Link key={to} to={to} className="it" activeProps={{ className: 'it on', 'aria-current': 'page' }}>
                  <Icon size={16} aria-hidden="true" />
                  <span>{label}</span>
                </Link>
              ))}
            </nav>
          ))}
          <div className="side-foot hint">
            {session.user.username} · {roleLabel(session.user.role)}
            <br />v{bootstrap.version.replace(/^v/, '')}
          </div>
        </aside>
        <div className="backdrop" onClick={() => setDrawer(false)} />
        <div className="main">
          <header className="top">
            <button type="button" className="icon-btn only-mobile" aria-label="打开菜单" onClick={() => setDrawer(true)}>
              <Menu size={20} />
            </button>
            <b className="server-name">{bootstrap.serverName}</b>
            <span className="grow" />
            <span className="who">
              <span className="hint">{roleLabel(session.user.role)}</span> <b>{session.user.username}</b>
            </span>
            <button type="button" className="btn" onClick={() => signOut.mutate()} disabled={signOut.isPending}>
              <LogOut size={14} /> 退出
            </button>
          </header>
          <main className="content">
            {session.mustSetupTotp ? (
              <>
                <Notice tone="warn">管理员要求所有账号开启两步验证。完成下面的设置后才能使用其他功能。</Notice>
                <SecurityPage />
              </>
            ) : (
              <Outlet />
            )}
          </main>
        </div>
      </div>
    </SessionProvider>
  )
}
