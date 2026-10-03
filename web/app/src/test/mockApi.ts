import { vi } from 'vitest'
import type { AdminUser, Bootstrap, Device, Domain, Me, Tunnel, User } from '../types'

export type Recorded = { method: string; path: string; query: URLSearchParams; headers: Record<string, string>; body: unknown }
type Reply = { status: number; body?: unknown } | object
type Handler = (req: Recorded) => Reply

const json = (status: number, body: unknown) =>
  new Response(status === 204 ? null : JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })

/**
 * Installs a fake fetch that answers by "METHOD /path" (path without the /api/v1 prefix). Handlers
 * may return a body, or {status, body}. Unknown routes answer 404 {"error":"not_found"} like the
 * real server. Returns the list of requests seen.
 */
export function mockApi(routes: Record<string, Handler | Reply>) {
  const calls: Recorded[] = []
  const fake = (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost')
    const rec: Recorded = {
      method: init?.method ?? 'GET',
      path: url.pathname.replace(/^\/api\/v1/, ''),
      query: url.searchParams,
      headers: { ...((init?.headers as Record<string, string>) ?? {}) },
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    }
    calls.push(rec)
    const route = routes[`${rec.method} ${rec.path}`]
    if (route === undefined) return Promise.resolve(json(404, { error: 'not_found' }))
    const reply = typeof route === 'function' ? (route as Handler)(rec) : route
    if ('status' in reply && typeof reply.status === 'number') return Promise.resolve(json(reply.status, reply.body ?? {}))
    return Promise.resolve(json(200, reply))
  }
  vi.stubGlobal('fetch', vi.fn(fake))
  return calls
}

const NOW = Date.now()

export const adminUser = (u: Partial<User> = {}): User => ({
  id: 'usr_admin',
  username: 'nya',
  role: 'admin',
  totpEnabled: true,
  disabled: false,
  createdAt: NOW - 86_400_000 * 10,
  recoveryCodesLeft: 10,
  ...u,
})

export const normalUser = (u: Partial<User> = {}): User =>
  adminUser({ id: 'usr_alice', username: 'alice', role: 'user', totpEnabled: false, recoveryCodesLeft: 0, ...u })

export const listed = (u: User, counts: Partial<AdminUser> = {}): AdminUser => ({ deviceCount: 0, tunnelCount: 0, ...u, ...counts })

export const boot = (b: Partial<Bootstrap> = {}): Bootstrap => ({
  version: 'v0.1.0',
  needsSetup: false,
  serverName: 'Nya 的家庭网络',
  publicUrl: 'https://tunnel.example.com',
  forceTotp: false,
  user: adminUser(),
  ...b,
})

export const me = (user: User = adminUser(), mustSetupTotp = false): Me => ({ user, mustSetupTotp })

export const device = (d: Partial<Device> = {}): Device => ({
  id: 'dev_1',
  userId: 'usr_admin',
  username: 'nya',
  name: 'home-server',
  platform: 'linux/amd64',
  clientVersion: '0.1.0',
  gui: false,
  lastIp: '203.0.113.5',
  lastSeenAt: NOW - 60_000,
  revoked: false,
  revokedAt: null,
  createdAt: NOW - 86_400_000,
  online: true,
  connectedAt: NOW - 3_600_000,
  tunnelCount: 1,
  ...d,
})

export const tunnel = (t: Partial<Tunnel> = {}): Tunnel => ({
  id: 'tun_1',
  userId: 'usr_admin',
  username: 'nya',
  deviceId: 'dev_1',
  deviceName: 'home-server',
  name: 'blog',
  type: 'https',
  domainId: 'dom_1',
  subdomain: 'blog',
  host: 'blog.dev.example.com',
  remotePort: null,
  publicUrl: 'https://blog.dev.example.com',
  localIp: '127.0.0.1',
  localPort: 8080,
  clientCanEditLocal: false,
  localLoopbackOnly: false,
  clientCanToggle: true,
  enabled: true,
  pausedByClient: false,
  note: '',
  expiresAt: null,
  createdAt: NOW - 86_400_000,
  updatedAt: NOW - 86_400_000,
  state: 'running',
  ...t,
})

export const domain = (d: Partial<Domain> = {}): Domain => ({
  id: 'dom_1',
  name: 'dev.example.com',
  allowUsers: true,
  tunnelCount: 1,
  createdAt: NOW - 86_400_000,
  ...d,
})

/** The routes most console pages need, for a signed-in administrator. Tests override what they care about. */
export const adminRoutes = () => ({
  'GET /bootstrap': boot(),
  'GET /me': me(),
  'GET /users': { users: [listed(adminUser()), listed(normalUser())] },
  'GET /devices': { devices: [device()] },
  'GET /tunnels': { tunnels: [tunnel()] },
  'GET /domains': { domains: [domain(), domain({ id: 'dom_2', name: 't.example.com', allowUsers: false })] },
  'GET /port-pools': { portPools: [] },
  'GET /audit': { events: [] },
  'GET /settings': { serverName: 'Nya 的家庭网络', forceTotp: false },
  'GET /dashboard': {
    users: 2,
    devices: 1,
    devicesOnline: 1,
    tunnels: 1,
    tunnelsRunning: 1,
    tunnelsByType: { https: 1 },
    openPorts: 0,
    denied24h: 0,
    recentDenied: [],
  },
})

/** A signed-in normal user. */
export const userRoutes = () => ({
  ...adminRoutes(),
  'GET /bootstrap': boot({ user: normalUser() }),
  'GET /me': me(normalUser()),
  'GET /users': { status: 403, body: { error: 'forbidden', message: '' } },
  'GET /tunnels': { tunnels: [tunnel({ id: 'tun_a', userId: 'usr_alice', username: 'alice', name: 'alice-blog', deviceId: null, deviceName: '', state: 'unassigned' })] },
  'GET /devices': { devices: [] },
})
