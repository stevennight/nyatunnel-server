import type { AccessPolicy, AuditEvent, DomainStatus, RequestStatus, TunnelState, TunnelType } from './types'

export type Tone = 'ok' | 'warn' | 'bad' | 'n' | 'b'

export const tunnelStates: Record<TunnelState, { label: string; tone: Tone }> = {
  running: { label: '运行中', tone: 'ok' },
  offline: { label: '设备离线', tone: 'n' },
  paused: { label: '客户端已暂停', tone: 'warn' },
  disabled: { label: '已停用', tone: 'bad' },
  expired: { label: '已到期', tone: 'bad' },
  over_quota: { label: '流量已用完', tone: 'bad' },
  unassigned: { label: '未绑定设备', tone: 'b' },
  error: { label: '出错', tone: 'bad' },
}

export const tunnelTypes: Record<TunnelType, { label: string; tone: Tone }> = {
  https: { label: 'HTTPS', tone: 'b' },
  tcp: { label: 'TCP', tone: 'warn' },
  udp: { label: 'UDP', tone: 'warn' },
}

export const accessPolicies: Record<AccessPolicy, { label: string; short: string; tone: Tone }> = {
  public: { label: '公开', short: '公开', tone: 'n' },
  password: { label: '访问密码', short: '访问密码', tone: 'ok' },
  basic: { label: 'HTTP Basic 认证', short: 'Basic', tone: 'ok' },
  login: { label: '登录门禁（NyaTunnel 账号）', short: '登录门禁', tone: 'b' },
}

export const domainStatuses: Record<DomainStatus, { label: string; tone: Tone }> = {
  pending: { label: '待审批', tone: 'warn' },
  dns: { label: '等待 DNS', tone: 'n' },
  active: { label: '已生效', tone: 'ok' },
  disabled: { label: '已停用', tone: 'bad' },
}

export const requestStatuses: Record<RequestStatus, { label: string; tone: Tone }> = {
  pending: { label: '待处理', tone: 'warn' },
  approved: { label: '已批准', tone: 'ok' },
  rejected: { label: '已拒绝', tone: 'bad' },
  cancelled: { label: '已撤回', tone: 'n' },
}

/** Notification events (notify.AllEvents). */
export const channelEvents: Record<string, string> = {
  'request.created': '新申请',
  'device.enrolled': '新设备',
  'quota.exceeded': '流量配额',
  'traffic.surge': '流量突增',
  'auth.bruteforce': '登录爆破',
  'domain.changed': '域名变化',
  'abuse.report': '举报',
}

/** Requested lifetimes offered in the request form (hours; 0 = permanent). */
export const durations: { hours: number; label: string }[] = [
  { hours: 24, label: '24 小时' },
  { hours: 24 * 7, label: '7 天' },
  { hours: 24 * 30, label: '30 天' },
  { hours: 0, label: '永久' },
]

export function durationLabel(hours: number | undefined): string {
  if (!hours) return '永久'
  const known = durations.find((d) => d.hours === hours)
  if (known) return known.label
  return hours % 24 === 0 ? `${hours / 24} 天` : `${hours} 小时`
}

export const auditActions: Record<string, string> = {
  'auth.setup': '初始化服务器',
  'auth.login': '登录',
  'auth.login_failed': '登录失败',
  'auth.password_changed': '修改密码',
  'auth.recovery_code_used': '使用恢复码登录',
  'auth.totp_enabled': '开启两步验证',
  'auth.totp_disabled': '关闭两步验证',
  'auth.session_revoked': '退出其他会话',
  'auth.recovery_codes_regenerated': '重新生成恢复码',
  'device.enrolled': '设备注册',
  'device.auth_failed': '设备认证失败',
  'device.rename': '重命名设备',
  'device.revoke': '吊销设备',
  'tunnel.create': '新建隧道',
  'tunnel.update': '修改隧道',
  'tunnel.delete': '删除隧道',
  'tunnel.device_update': '设备修改隧道',
  'tunnel.update_denied': '设备修改被拒绝',
  'tunnel.gate_login': '登录门禁通过',
  'tunnel.quota_paused': '流量用完已暂停',
  'tunnel.quota_exceeded': '超出流量配额',
  'tunnel.traffic_surge': '流量突增',
  'tunnel.reported': '访客举报',
  'enroll.create': '生成注册码',
  'enroll.cancel': '取消注册码',
  'enroll.invalid_code': '无效注册码',
  'user.create': '新建用户',
  'user.role': '修改角色',
  'user.password_reset': '重置密码',
  'user.disable': '禁用用户',
  'user.enable': '启用用户',
  'user.totp_reset': '重置两步验证',
  'user.quota': '修改自助额度',
  'user.quota_paused': '账号流量用完',
  'request.create': '提交隧道申请',
  'request.approve': '批准申请',
  'request.reject': '拒绝申请',
  'request.cancel': '撤回申请',
  'domain.create': '添加域名',
  'domain.request': '申请自定义域名',
  'domain.status': '域名状态变化',
  'domain.update': '修改域名',
  'domain.delete': '删除域名',
  'channel.create': '添加通知渠道',
  'channel.update': '修改通知渠道',
  'channel.delete': '删除通知渠道',
  'port_pool.create': '添加端口池',
  'port_pool.delete': '删除端口池',
  'settings.update': '修改系统设置',
}

/** Failures and refusals: the first sign of someone probing or misusing the system. */
export function isDenial(action: string): boolean {
  return action.endsWith('_failed') || action.endsWith('_denied') || action.endsWith('invalid_code')
}

/** Events shown in red: refusals plus abuse reports from visitors of a tunnel. */
export function needsAttention(action: string): boolean {
  return isDenial(action) || action === 'tunnel.reported'
}

const warnActions = new Set([
  'tunnel.quota_paused',
  'tunnel.quota_exceeded',
  'tunnel.traffic_surge',
  'user.quota_paused',
  'request.reject',
  'auth.session_revoked',
  'enroll.cancel',
])

export function actionTone(action: string): Tone {
  if (needsAttention(action)) return 'bad'
  if (action === 'device.enrolled' || action === 'auth.login' || action === 'request.approve' || action === 'tunnel.gate_login') return 'ok'
  if (warnActions.has(action)) return 'warn'
  if (action.endsWith('.delete') || action.endsWith('.revoke') || action.endsWith('.disable') || action.endsWith('_reset')) return 'warn'
  return 'b'
}

export function actorLabel(e: AuditEvent): string {
  if (e.actorType === 'device') return `设备 ${e.actorName || e.actorId}`
  if (e.actorType === 'user') return e.actorName || e.actorId
  if (e.actorType === 'system') return '系统'
  return '匿名'
}

export function roleLabel(role: string): string {
  return role === 'admin' ? '管理员' : '普通用户'
}
