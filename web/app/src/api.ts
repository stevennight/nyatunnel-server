// Thin fetch wrapper over /api/v1. Errors are {"error": "<code>", "message": "..."}.
import type {
  AdminUser,
  AuditEvent,
  Bootstrap,
  Channel,
  ChannelInput,
  Dashboard,
  Device,
  Domain,
  DomainList,
  Enrollment,
  EnrollmentInput,
  LoginSession,
  Me,
  Ok,
  PendingEnrollment,
  PortPool,
  PortProto,
  Quota,
  RequestInput,
  Role,
  Settings,
  TotpSetup,
  TrafficPoint,
  Tunnel,
  TunnelInput,
  TunnelRequest,
  User,
} from './types'

export const CSRF_HEADER = 'X-NyaTunnel-CSRF'
const BASE = '/api/v1'

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message?: string,
  ) {
    super(message || code)
    this.name = 'ApiError'
  }
}

/** Chinese text for error codes whose server message is empty. */
const fallbackText: Record<string, string> = {
  invalid_credentials: '用户名或密码错误',
  user_disabled: '账号已被禁用',
  too_many_attempts: '尝试次数过多，请稍后再试',
  totp_required: '请输入两步验证码',
  invalid_totp: '验证码不正确，请确认手机时间准确',
  invalid_recovery_code: '恢复码无效',
  totp_unavailable: '服务端无法读取两步验证密钥，请联系管理员',
  totp_setup_required: '管理员要求所有账号开启两步验证',
  totp_required_by_policy: '管理员要求所有账号开启两步验证，不能关闭',
  invalid_setup_token: '初始化令牌不正确，请查看服务端日志',
  already_set_up: '服务器已经完成初始化，请直接登录',
  unauthorized: '登录已失效，请重新登录',
  forbidden: '没有权限执行该操作',
  not_found: '对象不存在或已被删除',
  conflict: '名称、域名或端口已被占用',
  username_taken: '用户名已存在',
  invalid_username: '用户名为 2–32 位小写字母、数字或 _ . -',
  invalid_role: '角色不正确',
  cannot_demote_self: '不能取消自己的管理员身份',
  cannot_disable_self: '不能禁用自己',
  invalid_user: '用户不存在或已被禁用',
  invalid_tunnel: '预分配的隧道必须属于该用户',
  tunnel_assigned: '隧道已经绑定了设备',
  domain_in_use: '仍有隧道使用该域名，不能删除',
  enable_own_totp_first: '请先为自己开启两步验证',
  internal_error: '服务端出错了，请稍后再试',
  database_unavailable: '数据库不可用',
  body_too_large: '请求内容过大',
  self_service_disabled: '你的账号未开通自助创建，请提交隧道申请',
  type_not_allowed: '你的额度不允许自助创建该类型的隧道，请提交申请',
  tunnel_limit: '隧道数量已达到额度上限，请提交申请',
  not_pending: '该申请已处理',
  current_session: '请使用“退出登录”结束当前会话',
  totp_disabled: '请先开启两步验证',
  delivery_failed: '发送失败',
  invalid_version: '最低客户端版本应形如 0.2.0',
  domain_not_approved: '该自定义域名尚未批准或已停用',
  domain_not_owned: '该自定义域名属于其他用户',
  invalid_gate_request: '登录跳转参数无效',
}

/** Codes whose server message is English or technical: always show the Chinese text. */
const overrideText: Record<string, string> = {
  weak_password: '密码至少需要 10 个字符',
  bad_request: '请求格式不正确，请刷新页面后重试',
  csrf: '请求被拒绝，请刷新页面后重试',
}

export function describeError(err: unknown): string {
  if (err instanceof ApiError) {
    if (overrideText[err.code]) return overrideText[err.code]
    if (err.message && err.message !== err.code) return err.message
    if (fallbackText[err.code]) return fallbackText[err.code]
    if (err.status === 0) return '无法连接服务端'
    return `请求失败（${err.code}）`
  }
  return err instanceof Error ? err.message : String(err)
}

export function isApiError(err: unknown, code: string): err is ApiError {
  return err instanceof ApiError && err.code === code
}

export async function api<T>(path: string, init: { method?: string; body?: unknown } = {}): Promise<T> {
  const method = init.method ?? 'GET'
  const headers: Record<string, string> = {}
  if (init.body !== undefined) headers['Content-Type'] = 'application/json'
  // The server refuses every cookie-authenticated write without this header (CSRF protection).
  if (method !== 'GET' && method !== 'HEAD') headers[CSRF_HEADER] = '1'

  let res: Response
  try {
    res = await fetch(BASE + path, {
      method,
      headers,
      credentials: 'same-origin',
      body: init.body === undefined ? undefined : JSON.stringify(init.body),
    })
  } catch {
    throw new ApiError(0, 'network_error', '无法连接服务端')
  }
  if (!res.ok) {
    let code = `http_${res.status}`
    let message: string | undefined
    try {
      const body: unknown = await res.json()
      if (body && typeof body === 'object') {
        const b = body as { error?: unknown; message?: unknown }
        if (typeof b.error === 'string') code = b.error
        if (typeof b.message === 'string' && b.message) message = b.message
      }
    } catch {
      // not JSON (e.g. a reverse proxy error page): keep the generic code
    }
    throw new ApiError(res.status, code, message)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

function qs(params: Record<string, string | number | undefined>): string {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === '') continue
    p.set(k, String(v))
  }
  const s = p.toString()
  return s ? `?${s}` : ''
}

// --- session ---

export const getBootstrap = () => api<Bootstrap>('/bootstrap')
export const setup = (body: { token: string; username: string; password: string }) =>
  api<{ user: User }>('/setup', { method: 'POST', body })
export type LoginInput = { username: string; password: string; totp?: string; recoveryCode?: string }
export const login = (body: LoginInput) => api<{ user: User }>('/auth/login', { method: 'POST', body })
export const logout = () => api<Ok>('/auth/logout', { method: 'POST' })

export const getMe = () => api<Me>('/me')
export const changePassword = (body: { current: string; new: string }) => api<Ok>('/me/password', { method: 'POST', body })
/** A fresh secret to scan. Nothing is stored until enable confirms a code. */
export const setupTotp = () => api<TotpSetup>('/me/totp/setup', { method: 'POST' })
export const enableTotp = (body: { secret: string; code: string }) =>
  api<{ recoveryCodes: string[] }>('/me/totp/enable', { method: 'POST', body })
export const disableTotp = (password: string) => api<Ok>('/me/totp/disable', { method: 'POST', body: { password } })
/** Replaces every recovery code; the new ones are returned once. */
export const regenerateRecoveryCodes = (password: string) =>
  api<{ recoveryCodes: string[] }>('/me/totp/recovery-codes', { method: 'POST', body: { password } })
export const listSessions = () => api<{ sessions: LoginSession[] }>('/me/sessions')
export const revokeSession = (id: string) => api<Ok>(`/me/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' })

// --- devices & enrollment ---

export const listDevices = (userId?: string) => api<{ devices: Device[] }>(`/devices${qs({ userId })}`)
export const renameDevice = (id: string, name: string) => api<Ok>(`/devices/${encodeURIComponent(id)}`, { method: 'PATCH', body: { name } })
export const revokeDevice = (id: string) => api<Ok>(`/devices/${encodeURIComponent(id)}/revoke`, { method: 'POST' })
export const createEnrollment = (body: EnrollmentInput) => api<Enrollment>('/enrollments', { method: 'POST', body })
export const listEnrollments = () => api<{ enrollments: PendingEnrollment[] }>('/enrollments')
export const cancelEnrollment = (id: string) => api<Ok>(`/enrollments/${encodeURIComponent(id)}`, { method: 'DELETE' })

// --- tunnels ---

export const listTunnels = (userId?: string) => api<{ tunnels: Tunnel[] }>(`/tunnels${qs({ userId })}`)
export const createTunnel = (body: TunnelInput) => api<{ tunnel: Tunnel }>('/tunnels', { method: 'POST', body })
export const updateTunnel = (id: string, body: TunnelInput) =>
  api<{ tunnel: Tunnel }>(`/tunnels/${encodeURIComponent(id)}`, { method: 'PUT', body })
export const deleteTunnel = (id: string) => api<Ok>(`/tunnels/${encodeURIComponent(id)}`, { method: 'DELETE' })
/** Hourly traffic of one tunnel (or, without tunnelId, one user / everything). */
export const getTraffic = (params: { tunnelId?: string; userId?: string; hours: number }) =>
  api<{ series: TrafficPoint[] }>(`/traffic${qs(params)}`)

// --- tunnel requests ---

export const listRequests = () => api<{ requests: TunnelRequest[]; pending: number }>('/requests')
export const createRequest = (body: RequestInput) => api<{ id: string }>('/requests', { method: 'POST', body })
export const cancelRequest = (id: string) => api<Ok>(`/requests/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
export const approveRequest = (id: string, body: { tunnel: TunnelInput; note: string }) =>
  api<{ tunnelId: string }>(`/requests/${encodeURIComponent(id)}/approve`, { method: 'POST', body })
export const rejectRequest = (id: string, note: string) => api<Ok>(`/requests/${encodeURIComponent(id)}/reject`, { method: 'POST', body: { note } })

// --- domains & port pools ---

export const listDomains = () => api<DomainList>('/domains')
export const createDomain = (body: { name: string; allowUsers?: boolean; kind?: 'root' | 'custom'; ownerUserId?: string }) =>
  api<{ domain: Domain }>('/domains', { method: 'POST', body })
/** Root domains: allowUsers. Custom domains: approve / disable / enable. */
export const updateDomain = (id: string, body: { allowUsers?: boolean; action?: 'approve' | 'disable' | 'enable' }) =>
  api<Ok>(`/domains/${encodeURIComponent(id)}`, { method: 'PATCH', body })
export const deleteDomain = (id: string) => api<Ok>(`/domains/${encodeURIComponent(id)}`, { method: 'DELETE' })
/** A user asks for their own custom domain. */
export const requestCustomDomain = (name: string) => api<{ domain: Domain }>('/domains/custom', { method: 'POST', body: { name } })
export const checkDomain = (id: string) => api<{ domain: Domain }>(`/domains/${encodeURIComponent(id)}/check`, { method: 'POST' })

export const listPortPools = () => api<{ portPools: PortPool[] }>('/port-pools')
export const createPortPool = (body: { proto: PortProto; start: number; end: number }) =>
  api<{ portPool: PortPool }>('/port-pools', { method: 'POST', body })
export const deletePortPool = (id: string) => api<Ok>(`/port-pools/${encodeURIComponent(id)}`, { method: 'DELETE' })

// --- users ---

export const listUsers = () => api<{ users: AdminUser[] }>('/users')
export const createUser = (body: { username: string; password: string; role: Role }) => api<{ user: User }>('/users', { method: 'POST', body })
export const updateUser = (id: string, body: { role?: Role; password?: string; disabled?: boolean }) =>
  api<{ user: User }>(`/users/${encodeURIComponent(id)}`, { method: 'PATCH', body })
export const resetUserTotp = (id: string) => api<Ok>(`/users/${encodeURIComponent(id)}/totp/reset`, { method: 'POST' })
export const setUserQuota = (id: string, body: Quota) => api<Ok>(`/users/${encodeURIComponent(id)}/quota`, { method: 'PUT', body })

// --- notification channels ---

export const listChannels = () => api<{ channels: Channel[]; events: string[] }>('/channels')
export const createChannel = (body: ChannelInput) => api<{ channel: Channel }>('/channels', { method: 'POST', body })
export const updateChannel = (id: string, body: ChannelInput) => api<{ channel: Channel }>(`/channels/${encodeURIComponent(id)}`, { method: 'PUT', body })
export const deleteChannel = (id: string) => api<Ok>(`/channels/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const testChannel = (id: string) => api<Ok>(`/channels/${encodeURIComponent(id)}/test`, { method: 'POST' })

// --- system ---

export const AUDIT_PAGE = 100
export const listAudit = (before?: number) => api<{ events: AuditEvent[] }>(`/audit${qs({ before, limit: AUDIT_PAGE })}`)
export const getSettings = () => api<Settings>('/settings')
export const putSettings = (body: Settings) => api<Settings>('/settings', { method: 'PUT', body })
export const getDashboard = () => api<Dashboard>('/dashboard')
