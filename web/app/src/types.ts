// Shapes of the console API (internal/server/api/*.go). Times are Unix milliseconds.

export type Role = 'admin' | 'user'

export type User = {
  id: string
  username: string
  role: Role
  totpEnabled: boolean
  disabled: boolean
  createdAt: number
  /** Only filled for the signed-in user (GET /me). */
  recoveryCodesLeft?: number
}

/** A row of GET /users. */
export type AdminUser = User & {
  deviceCount: number
  tunnelCount: number
}

export type Bootstrap = {
  version: string
  needsSetup: boolean
  serverName: string
  publicUrl: string
  forceTotp: boolean
  user?: User
}

export type Me = {
  user: User
  /** True while the policy forces TOTP and this account has none: every other call answers 403 totp_setup_required. */
  mustSetupTotp: boolean
}

export type TotpSetup = { secret: string; otpauthUrl: string }

export type Device = {
  id: string
  userId: string
  username: string
  name: string
  platform: string
  clientVersion: string
  gui: boolean
  lastIp: string
  lastSeenAt: number | null
  revoked: boolean
  revokedAt: number | null
  createdAt: number
  online: boolean
  connectedAt: number | null
  tunnelCount: number
}

export type Enrollment = {
  code: string
  url: string
  cliCommand: string
  expiresAt: number
}

export type EnrollmentInput = {
  userId?: string
  deviceNameHint: string
  tunnelIds: string[]
  ttlMinutes: number
}

export type TunnelType = 'https' | 'tcp' | 'udp'

export type TunnelState = 'running' | 'offline' | 'paused' | 'disabled' | 'expired' | 'unassigned' | 'error'

export type Tunnel = {
  id: string
  userId: string
  username: string
  deviceId: string | null
  deviceName: string
  name: string
  type: TunnelType
  domainId: string | null
  subdomain: string | null
  host: string | null
  remotePort: number | null
  publicUrl: string
  localIp: string
  localPort: number
  clientCanEditLocal: boolean
  localLoopbackOnly: boolean
  clientCanToggle: boolean
  enabled: boolean
  pausedByClient: boolean
  note: string
  expiresAt: number | null
  createdAt: number
  updatedAt: number
  state: TunnelState
  stateError?: string
}

/** Body of POST /tunnels and PUT /tunnels/{id}: the full editable state. */
export type TunnelInput = {
  userId: string
  deviceId: string | null
  name: string
  type: TunnelType
  domainId: string | null
  subdomain: string | null
  /** tcp/udp: null picks a free port from the pool. */
  remotePort: number | null
  localIp: string
  localPort: number
  clientCanEditLocal: boolean
  localLoopbackOnly: boolean
  clientCanToggle: boolean
  enabled: boolean
  note: string
  expiresAt: number | null
}

export type Domain = {
  id: string
  name: string
  allowUsers: boolean
  /** Only counted for administrators. */
  tunnelCount: number
  createdAt: number
}

export type PortProto = 'tcp' | 'udp'

export type PortPool = {
  id: string
  proto: PortProto
  start: number
  end: number
  used: number
}

export type AuditEvent = {
  id: number
  at: number
  actorType: 'user' | 'device' | 'anonymous' | string
  actorId: string
  actorName: string
  action: string
  target: string
  detail: string
  ip: string
}

export type Settings = {
  serverName: string
  forceTotp: boolean
}

export type Dashboard = {
  users: number
  devices: number
  devicesOnline: number
  tunnels: number
  tunnelsRunning: number
  tunnelsByType: Partial<Record<TunnelType, number>>
  openPorts: number
  denied24h: number
  recentDenied: AuditEvent[]
}

export type Ok = { ok: true }
