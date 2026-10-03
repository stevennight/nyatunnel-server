import type { AuditEvent, TunnelState, TunnelType } from './types'

export type Tone = 'ok' | 'warn' | 'bad' | 'n' | 'b'

export const tunnelStates: Record<TunnelState, { label: string; tone: Tone }> = {
  running: { label: '运行中', tone: 'ok' },
  offline: { label: '设备离线', tone: 'n' },
  paused: { label: '客户端已暂停', tone: 'warn' },
  disabled: { label: '已停用', tone: 'bad' },
  expired: { label: '已到期', tone: 'bad' },
  unassigned: { label: '未绑定设备', tone: 'b' },
  error: { label: '出错', tone: 'bad' },
}

export const tunnelTypes: Record<TunnelType, { label: string; tone: Tone }> = {
  https: { label: 'HTTPS', tone: 'b' },
  tcp: { label: 'TCP', tone: 'warn' },
  udp: { label: 'UDP', tone: 'warn' },
}

export const auditActions: Record<string, string> = {
  'auth.setup': '初始化服务器',
  'auth.login': '登录',
  'auth.login_failed': '登录失败',
  'auth.password_changed': '修改密码',
  'auth.recovery_code_used': '使用恢复码登录',
  'auth.totp_enabled': '开启两步验证',
  'auth.totp_disabled': '关闭两步验证',
  'device.enrolled': '设备注册',
  'device.auth_failed': '设备认证失败',
  'device.rename': '重命名设备',
  'device.revoke': '吊销设备',
  'tunnel.create': '新建隧道',
  'tunnel.update': '修改隧道',
  'tunnel.delete': '删除隧道',
  'tunnel.device_update': '设备修改隧道',
  'tunnel.update_denied': '设备修改被拒绝',
  'enroll.create': '生成注册码',
  'enroll.invalid_code': '无效注册码',
  'user.create': '新建用户',
  'user.role': '修改角色',
  'user.password_reset': '重置密码',
  'user.disable': '禁用用户',
  'user.enable': '启用用户',
  'user.totp_reset': '重置两步验证',
  'domain.create': '添加域名',
  'domain.update': '修改域名',
  'domain.delete': '删除域名',
  'port_pool.create': '添加端口池',
  'port_pool.delete': '删除端口池',
  'settings.update': '修改系统设置',
}

/** Failures and refusals: the first sign of someone probing or misusing the system. */
export function isDenial(action: string): boolean {
  return action.endsWith('_failed') || action.endsWith('_denied') || action.endsWith('invalid_code')
}

export function actionTone(action: string): Tone {
  if (isDenial(action)) return 'bad'
  if (action === 'device.enrolled' || action === 'auth.login') return 'ok'
  if (action.endsWith('.delete') || action.endsWith('.revoke') || action.endsWith('.disable') || action.endsWith('_reset')) return 'warn'
  return 'b'
}

export function actorLabel(e: AuditEvent): string {
  if (e.actorType === 'device') return `设备 ${e.actorName || e.actorId}`
  if (e.actorType === 'user') return e.actorName || e.actorId
  return '匿名'
}

export function roleLabel(role: string): string {
  return role === 'admin' ? '管理员' : '普通用户'
}
