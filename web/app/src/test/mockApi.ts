import { vi } from 'vitest'
import type { AdminUser, Bootstrap, Channel, Dashboard, Device, Domain, Me, Quota, Tunnel, TunnelRequest, User } from '../types'

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

export const quota = (q: Partial<Quota> = {}): Quota => ({
  enabled: false,
  maxTunnels: 0,
  types: [],
  maxBandwidthKbps: 0,
  maxDays: 0,
  interstitial: false,
  monthlyTrafficMb: 0,
  ...q,
})

export const listed = (u: User, counts: Partial<AdminUser> = {}): AdminUser => ({ deviceCount: 0, tunnelCount: 0, quota: quota(), monthBytes: 0, ...u, ...counts })

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
  hasPassword: false,
  monthBytes: 0,
  activeConns: 0,
  accessPolicy: 'public',
  basicUsername: '',
  loginAccess: 'owner',
  loginUsers: [],
  ipAllowlist: '',
  interstitial: false,
  hostRewrite: '',
  bandwidthKbps: 0,
  maxConns: 0,
  monthlyQuotaMb: 0,
  quotaAction: 'pause',
  ...t,
})

export const domain = (d: Partial<Domain> = {}): Domain => ({
  id: 'dom_1',
  name: 'dev.example.com',
  kind: 'root',
  allowUsers: true,
  ownerUserId: null,
  ownerName: '',
  status: 'active',
  checkedAt: null,
  checkError: '',
  tunnelCount: 1,
  createdAt: NOW - 86_400_000,
  ...d,
})

export const request = (r: Partial<TunnelRequest> = {}): TunnelRequest => ({
  id: 'req_1',
  userId: 'usr_alice',
  username: 'alice',
  deviceId: null,
  deviceName: '',
  payload: { type: 'https', subdomain: 'alice-demo', domainId: 'dom_1', localIp: '127.0.0.1', localPort: 5173, durationHours: 24 },
  reason: '给客户演示新版页面',
  status: 'pending',
  reviewNote: '',
  reviewedAt: null,
  tunnelId: null,
  createdAt: NOW - 600_000,
  ...r,
})

export const channel = (c: Partial<Channel> = {}): Channel => ({
  id: 'ch_1',
  kind: 'webhook',
  name: 'ops',
  events: ['request.created', 'auth.bruteforce'],
  enabled: true,
  target: 'https://hooks.example.com',
  lastError: '',
  lastSent: null,
  createdAt: NOW - 86_400_000,
  ...c,
})

export const dashboard = (d: Partial<Dashboard> = {}): Dashboard => ({
  users: 2,
  devices: 1,
  devicesOnline: 1,
  tunnels: 1,
  tunnelsRunning: 1,
  tunnelsByType: { https: 1 },
  openPorts: 0,
  denied24h: 0,
  recentDenied: [],
  traffic24h: 0,
  trafficMonth: 0,
  trafficSeries: [],
  ...d,
})

/** The routes most console pages need, for a signed-in administrator. Tests override what they care about. */
export const adminRoutes = () => ({
  'GET /bootstrap': boot(),
  'GET /me': me(),
  'GET /users': { users: [listed(adminUser()), listed(normalUser())] },
  'GET /devices': { devices: [device()] },
  'GET /tunnels': { tunnels: [tunnel()] },
  'GET /domains': { domains: [domain(), domain({ id: 'dom_2', name: 't.example.com', allowUsers: false })], publicIps: ['203.0.113.10'] },
  'GET /port-pools': { portPools: [] },
  'GET /audit': { events: [] },
  'GET /settings': { serverName: 'Nya 的家庭网络', forceTotp: false, minClientVersion: '', surgeMbPerHour: 0 },
  'GET /dashboard': dashboard(),
  'GET /requests': { requests: [], pending: 0 },
  'GET /enrollments': { enrollments: [] },
  'GET /channels': { channels: [], events: ['request.created', 'device.enrolled', 'quota.exceeded', 'traffic.surge', 'auth.bruteforce', 'domain.changed'] },
  'GET /me/sessions': { sessions: [] },
  'GET /traffic': { series: [] },
})

/** A signed-in normal user. */
export const userRoutes = () => ({
  ...adminRoutes(),
  'GET /bootstrap': boot({ user: normalUser() }),
  'GET /me': me(normalUser()),
  'GET /users': { status: 403, body: { error: 'forbidden', message: '' } },
  'GET /tunnels': { tunnels: [tunnel({ id: 'tun_a', userId: 'usr_alice', username: 'alice', name: 'alice-blog', deviceId: null, deviceName: '', state: 'unassigned' })] },
  'GET /devices': { devices: [] },
  'GET /domains': { domains: [domain()], publicIps: ['203.0.113.10'] },
})
