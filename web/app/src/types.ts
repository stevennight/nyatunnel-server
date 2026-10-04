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

/** What a normal user may do without asking an administrator (store.Quota). */
export type Quota = {
  /** Self-service tunnel creation within the limits below. */
  enabled: boolean
  /** Counts all of the user's tunnels (0 = no limit). */
  maxTunnels: number
  types: TunnelType[]
  /** Cap for self-service tunnels (0 = no cap). */
  maxBandwidthKbps: number
  /** Longest lifetime of a self-service tunnel (0 = unlimited). */
  maxDays: number
  /** Forces the first-visit warning page on self-service HTTPS tunnels. */
  interstitial: boolean
  /** All of the user's tunnels together (0 = unlimited); applies even when self-service is off. */
  monthlyTrafficMb: number
}

/** A row of GET /users. */
export type AdminUser = User & {
  deviceCount: number
  tunnelCount: number
  quota: Quota
  monthBytes: number
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

export type TunnelType = 'https' | 'tcp' | 'udp' | 'tcpudp'

export type TunnelState =
  | 'running'
  | 'offline'
  | 'paused'
  | 'disabled'
  | 'expired'
  | 'over_quota'
  | 'unassigned'
  | 'unconfirmed'
  | 'error'

/** Who may open an HTTPS tunnel; anything but public is HTTPS-only. */
export type AccessPolicy = 'public' | 'password' | 'basic' | 'login'

/** What happens when a tunnel's monthly quota is used up. */
export type QuotaAction = 'pause' | 'alert'

/** Who may pass a login gate: the owner, the owner plus loginUsers, or every account. */
export type LoginAccess = 'owner' | 'users' | 'all'

/** Access control and limits, shared by the tunnel view and the input. */
type TunnelPolicy = {
  accessPolicy: AccessPolicy
  basicUsername: string
  loginAccess: LoginAccess
  /** Usernames allowed besides the owner when loginAccess is "users". */
  loginUsers: string[]
  /** Comma separated IP / CIDR; empty = everyone. */
  ipAllowlist: string
  /** First-visit warning page (HTTPS only). */
  interstitial: boolean
  /** Host header sent to the local service (HTTPS only); empty = unchanged. */
  hostRewrite: string
  /** 0 = unlimited. */
  bandwidthKbps: number
  maxConns: number
  monthlyQuotaMb: number
  quotaAction: QuotaAction
}

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
  /** The access password itself is never returned. */
  hasPassword: boolean
  /** Bytes in + out this calendar month (UTC). */
  monthBytes: number
  activeConns: number
} & TunnelPolicy

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
  /** Write-only: omitted or empty keeps the stored password. */
  accessPassword?: string
} & TunnelPolicy

export type DomainKind = 'root' | 'custom'

/** Custom domains only: pending (asked for) → dns (approved, waiting for DNS) → active; or disabled. */
export type DomainStatus = 'pending' | 'dns' | 'active' | 'disabled'

export type Domain = {
  id: string
  name: string
  kind: DomainKind
  /** Root domains: normal users may use it (with their "<username>-" prefix). */
  allowUsers: boolean
  ownerUserId: string | null
  ownerName: string
  status: DomainStatus
  checkedAt: number | null
  checkError: string
  /** Admins: all tunnels; users: their own. */
  tunnelCount: number
  createdAt: number
}

export type DomainList = {
  domains: Domain[]
  /** Where custom domains must point (A / AAAA). */
  publicIps: string[]
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
  /** Oldest client version allowed to connect, e.g. "0.2.0"; empty = any. */
  minClientVersion: string
  /** Alert when a tunnel moves more than this per hour; 0 = off. */
  surgeMbPerHour: number
}

/** One hour of traffic; `hour` is the bucket start (Unix ms, UTC hour). Hours without traffic are missing. */
export type TrafficPoint = {
  hour: number
  bytesIn: number
  bytesOut: number
  conns: number
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
  traffic24h: number
  trafficMonth: number
  /** Last 24 hours, hourly, all tunnels. */
  trafficSeries: TrafficPoint[]
}

/** What a tunnel request asks for. */
export type RequestPayload = {
  type: TunnelType
  name?: string
  domainId?: string
  subdomain?: string
  customDomain?: string
  remotePort?: number
  localIp: string
  localPort: number
  /** 0 or missing = permanent. */
  durationHours?: number
}

export type RequestStatus = 'pending' | 'approved' | 'rejected' | 'cancelled'

export type TunnelRequest = {
  id: string
  userId: string
  username: string
  deviceId: string | null
  deviceName: string
  payload: RequestPayload
  reason: string
  status: RequestStatus
  reviewNote: string
  reviewedAt: number | null
  tunnelId: string | null
  createdAt: number
}

/** Body of POST /requests. */
export type RequestInput = RequestPayload & {
  reason: string
  deviceId?: string
}

export type ChannelKind = 'webhook' | 'telegram'

export type ChannelEvent = 'request.created' | 'device.enrolled' | 'quota.exceeded' | 'traffic.surge' | 'auth.bruteforce' | 'domain.changed' | 'abuse.report'

export type Channel = {
  id: string
  kind: ChannelKind
  name: string
  events: string[]
  enabled: boolean
  /** A safe summary: webhook origin or Telegram chat id. */
  target: string
  lastError: string
  lastSent: number | null
  createdAt: number
}

/** Secrets of a channel: write-only. */
export type ChannelConfig = {
  url?: string
  secret?: string
  botToken?: string
  chatId?: string
  apiBase?: string
}

/** Body of POST /channels and PUT /channels/{id}. Omit `config` on edit to keep the stored secrets. */
export type ChannelInput = {
  kind: ChannelKind
  name: string
  events: string[]
  enabled: boolean
  config?: ChannelConfig
}

/** A console login session of the signed-in user. */
export type LoginSession = {
  id: string
  current: boolean
  createdAt: number
  lastUsedAt: number
  ip: string
  userAgent: string
}

/** An unused enrollment code (the code itself is not stored). */
export type PendingEnrollment = {
  id: string
  userId: string
  username: string
  deviceNameHint: string
  tunnelIds: string[]
  expiresAt: number
  createdAt: number
}

export type Ok = { ok: true }
