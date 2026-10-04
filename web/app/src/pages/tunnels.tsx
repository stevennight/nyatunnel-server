import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import { BarChart3, FilePlus2, Plus, Search, UserPlus } from 'lucide-react'
import { ApiError, approveRequest, createTunnel, deleteTunnel, describeError, listDevices, listDomains, listTunnels, listUsers, updateTunnel } from '../api'
import { EnrollDialog } from '../components/enroll-dialog'
import { TrafficDialog } from '../components/traffic-chart'
import { ConfirmDialog, Empty, Loading, Modal, Notice, PageHead, Tag, TunnelStateTag } from '../components/ui'
import {
  bytes,
  dateTime,
  fromLocalInput,
  gbToMb,
  kbpsToMbps,
  localPortProblem,
  mbToGb,
  mbpsToKbps,
  parseAmount,
  poolLabel,
  quotaMb,
  toLocalInput,
  until,
} from '../format'
import { accessPolicies, tunnelTypes } from '../labels'
import { POLL_MS } from '../query'
import { useSession } from '../session'
import type { AccessPolicy, AdminUser, Domain, LoginAccess, QuotaAction, RequestInput, Tunnel, TunnelInput, TunnelRequest, TunnelType, User } from '../types'
import { RequestForm } from './requests'

const NAME_RE = /^[a-z0-9][a-z0-9-]{0,39}$/

/** Codes of a refused self-service change: the user may turn it into a request instead. */
export const SELF_SERVICE_CODES = ['self_service_disabled', 'type_not_allowed', 'tunnel_limit']

export function TunnelsPage() {
  const { isAdmin } = useSession()
  const tunnels = useQuery({ queryKey: ['tunnels'], queryFn: () => listTunnels(), refetchInterval: POLL_MS })
  const users = useQuery({ queryKey: ['users'], queryFn: listUsers, enabled: isAdmin })
  const [q, setQ] = useState('')
  const [owner, setOwner] = useState('')
  const [editing, setEditing] = useState<Tunnel | 'new' | null>(null)
  const [invite, setInvite] = useState<{ userId?: string; tunnelIds?: string[] } | null>(null)
  const [traffic, setTraffic] = useState<Tunnel | null>(null)
  const [requesting, setRequesting] = useState<Partial<RequestInput> | null>(null)

  const rows = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return (tunnels.data?.tunnels ?? []).filter((t) => {
      if (owner && t.userId !== owner) return false
      if (!needle) return true
      return [t.name, t.publicUrl, t.deviceName, t.username, t.note].some((s) => s?.toLowerCase().includes(needle))
    })
  }, [tunnels.data, q, owner])

  return (
    <>
      <PageHead
        title={isAdmin ? '隧道' : '我的隧道'}
        hint={
          isAdmin
            ? '所有对外入口都在这里定义；没有登记的代理，服务端一律拒绝。'
            : '在管理员给你的自助额度内可以直接新建隧道；超出额度的需求请提交申请，等待管理员审批。'
        }
      >
        <div className="search">
          <Search size={14} aria-hidden="true" />
          <input className="inp" placeholder="搜索名称 / 地址 / 设备" aria-label="搜索隧道" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        {isAdmin && (
          <select className="inp auto" aria-label="按用户筛选" value={owner} onChange={(e) => setOwner(e.target.value)}>
            <option value="">全部用户</option>
            {users.data?.users.map((u) => (
              <option key={u.id} value={u.id}>
                {u.username}
              </option>
            ))}
          </select>
        )}
        <button type="button" className="btn" onClick={() => setInvite({})}>
          <UserPlus size={14} /> {isAdmin ? '邀请设备' : '邀请我的设备'}
        </button>
        {!isAdmin && (
          <button type="button" className="btn" onClick={() => setRequesting({})}>
            <FilePlus2 size={14} /> 申请隧道
          </button>
        )}
        <button type="button" className="btn primary" onClick={() => setEditing('new')}>
          <Plus size={14} /> 新建隧道
        </button>
      </PageHead>

      {tunnels.isError && <Notice tone="bad">{describeError(tunnels.error)}</Notice>}
      {tunnels.isPending ? (
        <Loading />
      ) : rows.length === 0 ? (
        <Empty>
          {q || owner
            ? '没有匹配的隧道。'
            : isAdmin
              ? '还没有隧道。先在“域名”或“端口池”里准备好资源，再新建隧道。'
              : '你还没有隧道。可以在额度内新建，或提交申请等待管理员审批。'}
        </Empty>
      ) : (
        <div className="tbl">
          <table>
            <thead>
              <tr>
                <th>名称</th>
                {isAdmin && <th>用户</th>}
                <th>类型</th>
                <th>公网入口</th>
                <th>绑定设备</th>
                <th>本地目标</th>
                <th>访问</th>
                <th>本月流量</th>
                <th>状态</th>
                <th>到期</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {rows.map((t) => (
                <tr key={t.id}>
                  <td>
                    <b>{t.name}</b>
                    {t.note && (
                      <div className="hint clip" title={t.note}>
                        {t.note}
                      </div>
                    )}
                  </td>
                  {isAdmin && <td>{t.username}</td>}
                  <td>
                    <Tag tone={tunnelTypes[t.type].tone}>{tunnelTypes[t.type].label}</Tag>
                  </td>
                  <td className="mono">
                    {t.type === 'https' ? (
                      <a href={t.publicUrl} target="_blank" rel="noreferrer noopener">
                        {t.publicUrl.replace(/^https:\/\//, '')}
                      </a>
                    ) : (
                      t.publicUrl
                    )}
                  </td>
                  <td>
                    {t.deviceId ? (
                      t.deviceName || <span className="hint">—</span>
                    ) : isAdmin ? (
                      <button type="button" className="btn sm" onClick={() => setInvite({ userId: t.userId, tunnelIds: [t.id] })}>
                        邀请设备
                      </button>
                    ) : (
                      <span className="hint">等待设备注册</span>
                    )}
                  </td>
                  <td className="mono">
                    {t.localIp}:{t.localPort}
                    {t.clientCanEditLocal && (
                      <span className="hint" title="客户端可修改本地目标">
                        {' '}
                        ✎
                      </span>
                    )}
                  </td>
                  <td>
                    <PolicyTags t={t} />
                  </td>
                  <td>
                    <TrafficCell t={t} />
                  </td>
                  <td>
                    <TunnelStateTag state={t.state} error={t.stateError} />
                    {t.state === 'unconfirmed' && <div className="hint">需在设备上确认后才会接通</div>}
                    {t.state === 'error' && t.stateError && (
                      <div className="hint bad-text clip" title={t.stateError}>
                        {t.stateError}
                      </div>
                    )}
                  </td>
                  <td>
                    {t.expiresAt ? (
                      <Tag tone={t.expiresAt < Date.now() ? 'bad' : t.expiresAt - Date.now() < 3 * 86_400_000 ? 'warn' : 'n'} title={dateTime(t.expiresAt)}>
                        {until(t.expiresAt)}
                      </Tag>
                    ) : (
                      <span className="hint">—</span>
                    )}
                  </td>
                  <td>
                    <div className="inline">
                      <button type="button" className="btn sm" aria-label={`${t.name} 流量`} onClick={() => setTraffic(t)}>
                        <BarChart3 size={13} /> 流量
                      </button>
                      <button type="button" className="btn sm" onClick={() => setEditing(t)}>
                        编辑
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {editing && (
        <TunnelForm
          tunnel={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onRequestInstead={(payload) => {
            setEditing(null)
            setRequesting(payload)
          }}
        />
      )}
      {invite && <EnrollDialog initialUserId={invite.userId} initialTunnelIds={invite.tunnelIds} onClose={() => setInvite(null)} />}
      {traffic && <TrafficDialog tunnel={traffic} onClose={() => setTraffic(null)} />}
      {requesting && <RequestForm initial={requesting} onClose={() => setRequesting(null)} />}
    </>
  )
}

function PolicyTags({ t }: { t: Tunnel }) {
  const p = accessPolicies[t.accessPolicy] ?? accessPolicies.public
  return (
    <div className="inline wrap">
      <Tag tone={p.tone} title={t.accessPolicy === 'basic' && t.basicUsername ? `用户名 ${t.basicUsername}` : undefined}>
        {p.short}
      </Tag>
      {t.ipAllowlist && (
        <Tag tone="b" title={t.ipAllowlist}>
          IP 白名单
        </Tag>
      )}
      {t.interstitial && <Tag title="首次访问显示风险提示页">提示页</Tag>}
    </div>
  )
}

function TrafficCell({ t }: { t: Tunnel }) {
  const quota = t.monthlyQuotaMb > 0 ? t.monthlyQuotaMb * 1024 * 1024 : 0
  const pct = quota ? Math.min(100, (t.monthBytes / quota) * 100) : 0
  return (
    <div>
      <span className={quota && t.monthBytes >= quota ? 'bad-text' : undefined}>{bytes(t.monthBytes)}</span>
      {quota > 0 && (
        <>
          <span className="hint"> / {quotaMb(t.monthlyQuotaMb)}</span>{' '}
          <span className="meter" title={`${pct.toFixed(0)}%`}>
            <span style={{ width: `${pct}%`, background: pct >= 100 ? 'var(--bad)' : pct >= 80 ? 'var(--warn)' : undefined }} />
          </span>
        </>
      )}
      {t.activeConns > 0 && <div className="hint">{t.activeConns} 个活动连接</div>}
    </div>
  )
}

export type FormState = {
  userId: string
  deviceId: string
  name: string
  type: TunnelType
  /** A root domain (with subdomain) or a custom domain (the whole host). */
  domainId: string
  /** The full subdomain label (including a user's "<username>-" prefix). */
  subdomain: string
  remotePort: string
  localIp: string
  localPort: string
  clientCanEditLocal: boolean
  localLoopbackOnly: boolean
  clientCanToggle: boolean
  enabled: boolean
  note: string
  expiresAt: string
  accessPolicy: AccessPolicy
  /** Write-only; empty keeps the stored password. */
  accessPassword: string
  basicUsername: string
  loginAccess: LoginAccess
  /** Comma separated usernames. */
  loginUsers: string
  ipAllowlist: string
  interstitial: boolean
  hostRewrite: string
  bandwidthMbps: string
  maxConns: string
  quotaGb: string
  quotaAction: QuotaAction
}

export function emptyForm(selfId: string): FormState {
  return {
    userId: selfId,
    deviceId: '',
    name: '',
    type: 'https',
    domainId: '',
    subdomain: '',
    remotePort: '',
    localIp: '127.0.0.1',
    localPort: '',
    clientCanEditLocal: false,
    localLoopbackOnly: false,
    clientCanToggle: true,
    enabled: true,
    note: '',
    expiresAt: '',
    accessPolicy: 'public',
    accessPassword: '',
    basicUsername: '',
    loginAccess: 'owner',
    loginUsers: '',
    ipAllowlist: '',
    interstitial: false,
    hostRewrite: '',
    bandwidthMbps: '',
    maxConns: '',
    quotaGb: '',
    quotaAction: 'pause',
  }
}

function formFromTunnel(t: Tunnel): FormState {
  return {
    userId: t.userId,
    deviceId: t.deviceId ?? '',
    name: t.name,
    type: t.type,
    domainId: t.domainId ?? '',
    subdomain: t.subdomain ?? '',
    remotePort: t.remotePort ? String(t.remotePort) : '',
    localIp: t.localIp,
    localPort: String(t.localPort),
    clientCanEditLocal: t.clientCanEditLocal,
    localLoopbackOnly: t.localLoopbackOnly,
    clientCanToggle: t.clientCanToggle,
    enabled: t.enabled,
    note: t.note,
    expiresAt: toLocalInput(t.expiresAt),
    accessPolicy: t.accessPolicy || 'public',
    accessPassword: '',
    basicUsername: t.basicUsername ?? '',
    loginAccess: t.loginAccess || 'owner',
    loginUsers: (t.loginUsers ?? []).join(', '),
    ipAllowlist: t.ipAllowlist ?? '',
    interstitial: t.interstitial,
    hostRewrite: t.hostRewrite ?? '',
    bandwidthMbps: kbpsToMbps(t.bandwidthKbps),
    maxConns: t.maxConns > 0 ? String(t.maxConns) : '',
    quotaGb: mbToGb(t.monthlyQuotaMb),
    quotaAction: t.quotaAction || 'pause',
  }
}

/** A valid tunnel name from free text ("My Blog!" → "my-blog"). */
export function toTunnelName(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+/, '')
    .replace(/-+$/, '')
    .slice(0, 40)
}

/**
 * The approval form starts from what was asked for: requester and device fixed, the requested
 * subdomain (with the owner's prefix), custom domain if it exists, and expiry = now + duration.
 */
export function formFromRequest(req: TunnelRequest, owner: Pick<User, 'role' | 'username'> | undefined, domains: Domain[], now = Date.now()): FormState {
  const p = req.payload
  const f = emptyForm(req.userId)
  f.type = p.type
  f.deviceId = req.deviceId ?? ''
  f.localIp = p.localIp || '127.0.0.1'
  f.localPort = p.localPort ? String(p.localPort) : ''
  f.remotePort = p.remotePort ? String(p.remotePort) : ''
  if (p.durationHours && p.durationHours > 0) f.expiresAt = toLocalInput(now + p.durationHours * 3_600_000)
  const prefix = owner && owner.role !== 'admin' ? `${owner.username}-` : ''
  if (p.type === 'https') {
    const custom = p.customDomain ? domains.find((d) => d.kind === 'custom' && d.name === p.customDomain) : undefined
    if (custom) {
      f.domainId = custom.id
    } else {
      if (p.domainId && domains.some((d) => d.id === p.domainId)) f.domainId = p.domainId
      const sub = (p.subdomain ?? '').toLowerCase()
      f.subdomain = sub && prefix && !sub.startsWith(prefix) ? prefix + sub : sub
    }
  }
  f.name = toTunnelName(p.name || (p.type === 'https' && f.subdomain ? f.subdomain : '') || p.customDomain?.split('.')[0] || `${p.type}-${p.localPort}`)
  return f
}

/** Usable for a new / edited tunnel of this owner: approved custom domains of theirs (or unowned). */
function usableCustom(d: Domain, ownerId: string, currentId: string): boolean {
  if (d.kind !== 'custom') return false
  if (d.id === currentId) return true
  if (d.status !== 'active' && d.status !== 'dns') return false
  return d.ownerUserId === null || d.ownerUserId === ownerId
}

/**
 * Create / edit a tunnel. Administrators edit everything; normal users their own tunnels within
 * their quota (the server enforces it). In approval mode it creates the tunnel for a request.
 */
export function TunnelForm({
  tunnel,
  onClose,
  approve,
  initial,
  onRequestInstead,
}: {
  tunnel: Tunnel | null
  onClose: () => void
  /** Approval mode: the request being approved (owner fixed to the requester). */
  approve?: TunnelRequest
  /** Prefilled state for a new tunnel (approval). */
  initial?: FormState
  /** A normal user's change was refused by their quota: offer a request instead. */
  onRequestInstead?: (payload: Partial<RequestInput>) => void
}) {
  const client = useQueryClient()
  const { user: me, isAdmin } = useSession()
  const [f, setF] = useState<FormState>(() => initial ?? (tunnel ? formFromTunnel(tunnel) : emptyForm(me.id)))
  const [problem, setProblem] = useState<string | null>(null)
  const [refused, setRefused] = useState<string | null>(null)
  const [note, setNote] = useState('')
  const [confirmDelete, setConfirmDelete] = useState(false)
  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setF((s) => ({ ...s, [k]: v }))

  const users = useQuery({ queryKey: ['users'], queryFn: listUsers, enabled: isAdmin })
  const devices = useQuery({ queryKey: ['devices'], queryFn: () => listDevices() })
  const domains = useQuery({ queryKey: ['domains'], queryFn: listDomains })

  const ownerOf = (id: string): Pick<AdminUser, 'id' | 'username' | 'role' | 'disabled'> | undefined =>
    users.data?.users.find((u) => u.id === id) ?? (id === me.id ? me : approve && approve.userId === id ? { id, username: approve.username, role: 'user', disabled: false } : undefined)
  const prefixFor = (id: string) => {
    const u = ownerOf(id)
    return u && u.role !== 'admin' ? `${u.username}-` : ''
  }
  const owner = ownerOf(f.userId)
  const prefix = prefixFor(f.userId)
  // A stored subdomain that predates the owner's prefix rule is shown as is (the server keeps it if unchanged).
  const showPrefix = prefix !== '' && (f.subdomain === '' || f.subdomain.startsWith(prefix))
  const subInput = showPrefix ? f.subdomain.slice(prefix.length) : f.subdomain

  const allDomains = domains.data?.domains ?? []
  const ownerDevices = (devices.data?.devices ?? []).filter((d) => d.userId === f.userId && (!d.revoked || d.id === f.deviceId))
  const rootDomains = allDomains.filter((d) => d.kind !== 'custom' && (!prefix || d.allowUsers || d.id === f.domainId))
  const customDomains = allDomains.filter((d) => usableCustom(d, f.userId, f.domainId))
  const usableDomains = [...rootDomains, ...customDomains]
  const domain = allDomains.find((d) => d.id === (f.domainId || usableDomains[0]?.id))
  const domainId = domain?.id ?? ''
  const isCustom = domain?.kind === 'custom'
  const https = f.type === 'https'
  const needsPassword = https && (f.accessPolicy === 'password' || f.accessPolicy === 'basic')
  // The stored password survives only while the policy keeps using one.
  const keepsPassword = !!tunnel?.hasPassword && (tunnel.accessPolicy === 'password' || tunnel.accessPolicy === 'basic')
  const fixedOwner = !isAdmin || !!approve

  const changeOwner = (id: string) => {
    const oldPrefix = prefix
    const rest = oldPrefix && f.subdomain.startsWith(oldPrefix) ? f.subdomain.slice(oldPrefix.length) : f.subdomain
    const newPrefix = prefixFor(id)
    setF((s) => ({
      ...s,
      userId: id,
      deviceId: (devices.data?.devices ?? []).some((d) => d.id === s.deviceId && d.userId === id) ? s.deviceId : '',
      subdomain: rest ? newPrefix + rest : '',
    }))
  }

  const changeType = (type: TunnelType) =>
    setF((s) => ({
      ...s,
      type,
      // Password / Basic / login gates, the warning page and Host rewriting are HTTPS-only.
      ...(type === 'https' ? {} : { accessPolicy: 'public' as AccessPolicy, interstitial: false, hostRewrite: '' }),
    }))

  const invalidate = () => {
    void client.invalidateQueries({ queryKey: ['tunnels'] })
    void client.invalidateQueries({ queryKey: ['devices'] })
    void client.invalidateQueries({ queryKey: ['domains'] })
    void client.invalidateQueries({ queryKey: ['port-pools'] })
    void client.invalidateQueries({ queryKey: ['users'] })
  }

  const save = useMutation({
    mutationFn: async (body: TunnelInput): Promise<void> => {
      if (approve) await approveRequest(approve.id, { tunnel: body, note: note.trim() })
      else if (tunnel) await updateTunnel(tunnel.id, body)
      else await createTunnel(body)
    },
    onSuccess: () => {
      invalidate()
      if (approve) {
        void client.invalidateQueries({ queryKey: ['requests'] })
        void client.invalidateQueries({ queryKey: ['dashboard'] })
      }
      onClose()
    },
    onError: (e) => {
      if (!isAdmin && e instanceof ApiError && e.status === 403 && SELF_SERVICE_CODES.includes(e.code)) {
        setProblem(null)
        setRefused(describeError(e))
        return
      }
      setRefused(null)
      setProblem(describeError(e))
    },
  })
  const remove = useMutation({
    mutationFn: () => deleteTunnel(tunnel!.id),
    onSuccess: () => {
      invalidate()
      onClose()
    },
  })

  const requestPayload = (): Partial<RequestInput> => {
    const exp = fromLocalInput(f.expiresAt)
    const hours = exp ? Math.max(1, Math.round((exp - Date.now()) / 3_600_000)) : 0
    return {
      type: f.type,
      name: f.name.trim(),
      domainId: https && !isCustom ? domainId || undefined : undefined,
      subdomain: https && !isCustom ? f.subdomain.trim().toLowerCase() : undefined,
      customDomain: https && isCustom ? domain?.name : undefined,
      remotePort: !https && f.remotePort.trim() ? Number(f.remotePort) : undefined,
      localIp: f.localIp.trim(),
      localPort: Number(f.localPort) || 0,
      durationHours: hours,
      deviceId: f.deviceId || undefined,
    }
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setRefused(null)
    const name = f.name.trim()
    if (!NAME_RE.test(name)) return setProblem('名称只能包含小写字母、数字和连字符，最长 40 个字符')
    const portProblem = localPortProblem(f.localPort)
    if (portProblem) return setProblem(portProblem)
    const localPort = Number(f.localPort)
    if (!f.localIp.trim()) return setProblem('请填写本地地址')
    let remotePort: number | null = null
    if (https) {
      if (!domainId) return setProblem('请选择域名')
      if (!isCustom && !subInput.trim()) return setProblem('请填写子域名')
    } else if (f.remotePort.trim()) {
      remotePort = Number(f.remotePort)
      if (!Number.isInteger(remotePort) || remotePort < 1 || remotePort > 65535) return setProblem('公网端口必须在 1–65535 之间，留空自动分配')
    }
    const policy: AccessPolicy = https ? f.accessPolicy : 'public'
    const password = needsPassword ? f.accessPassword : ''
    if (needsPassword) {
      if (!password && !keepsPassword) return setProblem('请设置访问密码')
      if (password && [...password].length < 6) return setProblem('访问密码至少 6 个字符')
    }
    if (https && policy === 'basic' && !f.basicUsername.trim()) return setProblem('请设置 Basic 认证用户名')
    const loginUsers = f.loginUsers
      .split(/[\s,，]+/)
      .map((u) => u.trim().toLowerCase())
      .filter(Boolean)
    const loginAccess: LoginAccess = https && policy === 'login' ? f.loginAccess : 'owner'
    if (loginAccess === 'users' && loginUsers.length === 0) return setProblem('请填写允许访问的用户名')
    const bw = parseAmount(f.bandwidthMbps)
    const conns = parseAmount(f.maxConns)
    const quota = parseAmount(f.quotaGb)
    if (Number.isNaN(bw) || Number.isNaN(conns) || Number.isNaN(quota) || !Number.isInteger(conns)) {
      return setProblem('带宽、连接数和流量配额必须是非负数字，留空表示不限')
    }
    setProblem(null)
    const body: TunnelInput = {
      userId: f.userId,
      deviceId: f.deviceId || null,
      name,
      type: f.type,
      domainId: https ? domainId : null,
      subdomain: https && !isCustom ? f.subdomain.trim().toLowerCase() : null,
      remotePort,
      localIp: f.localIp.trim(),
      localPort,
      clientCanEditLocal: f.clientCanEditLocal,
      localLoopbackOnly: f.localLoopbackOnly,
      clientCanToggle: f.clientCanToggle,
      enabled: f.enabled,
      note: f.note.trim(),
      expiresAt: fromLocalInput(f.expiresAt),
      accessPolicy: policy,
      basicUsername: https && policy === 'basic' ? f.basicUsername.trim() : '',
      loginAccess,
      loginUsers: loginAccess === 'users' ? loginUsers : [],
      ipAllowlist: f.ipAllowlist
        .split(/[\s,，]+/)
        .filter(Boolean)
        .join(', '),
      interstitial: https && f.interstitial,
      hostRewrite: https ? f.hostRewrite.trim() : '',
      bandwidthKbps: mbpsToKbps(bw),
      maxConns: conns,
      monthlyQuotaMb: gbToMb(quota),
      quotaAction: f.quotaAction,
    }
    if (password) body.accessPassword = password
    save.mutate(body)
  }

  const title = approve ? `批准申请 · ${approve.username}` : tunnel ? `编辑隧道 · ${tunnel.name}` : '新建隧道'

  return (
    <Modal title={title} onClose={onClose} wide>
      <form className="form" onSubmit={submit} aria-label="隧道表单">
        {approve && approve.reason && (
          <Notice tone="b">
            申请理由：{approve.reason}
            {approve.payload.customDomain && !isCustom && (
              <div className="warn-text">
                申请的自定义域名 <span className="mono">{approve.payload.customDomain}</span> 尚未添加或尚未批准：可先在“域名”页面添加，或改用子域名。
              </div>
            )}
          </Notice>
        )}
        {!isAdmin && !tunnel && <Notice>在你的自助额度内创建；超出额度（类型、数量）时可以改为提交申请。带宽和有效期会被限制在额度以内。</Notice>}
        <div className="fs">
          <h4>基本信息</h4>
          <div className="row">
            <label htmlFor="tf-name">名称</label>
            <input id="tf-name" className="inp" value={f.name} onChange={(e) => set('name', e.target.value)} placeholder="例如 blog" maxLength={40} />
          </div>
          {isAdmin && (
            <div className="row">
              <label htmlFor="tf-user">所属用户</label>
              <select id="tf-user" className="inp" value={f.userId} disabled={fixedOwner} onChange={(e) => changeOwner(e.target.value)}>
                {(users.data?.users ?? [{ ...me, deviceCount: 0, tunnelCount: 0 }]).map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.username}（{u.role === 'admin' ? '管理员' : '普通用户'}）{u.disabled ? ' · 已禁用' : ''}
                  </option>
                ))}
                {approve && !users.data?.users.some((u) => u.id === approve.userId) && <option value={approve.userId}>{approve.username}</option>}
              </select>
            </div>
          )}
          <div className="row">
            <label htmlFor="tf-device">绑定设备</label>
            <select id="tf-device" className="inp" value={f.deviceId} onChange={(e) => set('deviceId', e.target.value)}>
              <option value="">暂不绑定（等待设备注册）</option>
              {ownerDevices.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.name}
                  {d.revoked ? '（已吊销）' : d.online ? '（在线）' : '（离线）'}
                </option>
              ))}
            </select>
          </div>
          <div className="row">
            <label htmlFor="tf-type">类型</label>
            <div>
              <select id="tf-type" className="inp" value={f.type} disabled={tunnel !== null} onChange={(e) => changeType(e.target.value as TunnelType)}>
                <option value="https">HTTPS（Caddy 终止 TLS）</option>
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
                <option value="tcpudp">TCP+UDP（同一端口）</option>
              </select>
              {f.type === 'tcpudp' && <div className="hint">同一个公网端口同时转发 TCP 和 UDP，端口需同时位于 TCP 与 UDP 端口池内。</div>}
              {tunnel && <div className="hint">类型创建后不能修改。</div>}
            </div>
          </div>
          <div className="row">
            <label htmlFor="tf-note">备注</label>
            <input id="tf-note" className="inp" value={f.note} onChange={(e) => set('note', e.target.value)} placeholder="可选" />
          </div>
        </div>

        <div className="fs">
          <h4>公网入口 {isAdmin && <Tag tone="b">仅管理员可改</Tag>}</h4>
          {https ? (
            <>
              <div className="row">
                <label htmlFor={isCustom ? 'tf-domain' : 'tf-sub'}>{isCustom ? '域名' : '子域名'}</label>
                <div>
                  <div className="inline affix">
                    {!isCustom && showPrefix && (
                      <span className="prefix mono" data-testid="subdomain-prefix">
                        {prefix}
                      </span>
                    )}
                    {!isCustom && (
                      <input
                        id="tf-sub"
                        className="inp mono"
                        value={subInput}
                        onChange={(e) => set('subdomain', (showPrefix ? prefix : '') + e.target.value.trim().toLowerCase())}
                        placeholder={showPrefix ? 'blog' : '例如 blog'}
                      />
                    )}
                    <select
                      id="tf-domain"
                      className={`inp mono${isCustom ? '' : ' auto'}`}
                      aria-label="域名"
                      value={domainId}
                      onChange={(e) => set('domainId', e.target.value)}
                    >
                      {usableDomains.length === 0 && <option value="">（无可用域名）</option>}
                      {rootDomains.map((d) => (
                        <option key={d.id} value={d.id}>
                          .{d.name}
                        </option>
                      ))}
                      {customDomains.map((d) => (
                        <option key={d.id} value={d.id}>
                          {d.name}（自定义域名{d.status === 'dns' ? '，等待 DNS' : ''}）
                        </option>
                      ))}
                    </select>
                  </div>
                  {!isCustom && prefix && owner && (
                    <div className="hint">
                      {owner.id === me.id ? '你的' : `${owner.username} 是普通用户：`}子域名必须以 <code>{prefix}</code> 开头，且只能使用“允许普通用户使用”的根域名或自己的自定义域名。
                    </div>
                  )}
                  {!isCustom && prefix && !showPrefix && (
                    <div className="hint warn-text">当前子域名不符合该用户的前缀规则；保持不变可以保存，修改时需以 {prefix} 开头。</div>
                  )}
                  {usableDomains.length === 0 && domains.data && (
                    <div className="hint warn-text">{isAdmin ? '没有可用的域名，请先在“域名”页面添加。' : '没有可用的域名，请联系管理员或申请自定义域名。'}</div>
                  )}
                  {domain && (isCustom || subInput) && (
                    <div className="hint">
                      访问地址：<span className="mono">https://{isCustom ? domain.name : `${f.subdomain}.${domain.name}`}</span>
                    </div>
                  )}
                  {isCustom && domain?.status === 'dns' && <div className="hint warn-text">该域名还没有解析到本服务器，DNS 生效后才能访问。</div>}
                </div>
              </div>
              <div className="row">
                <span className="label">证书</span>
                <span className="hint">
                  {isCustom
                    ? '由前置的 Caddy 在访客首次访问时自动签发（仅对已生效的自定义域名）。'
                    : `由前置的 Caddy 负责（${domain ? `*.${domain.name}` : '通配证书'}，DNS-01 自动续期）。`}
                </span>
              </div>
            </>
          ) : (
            <div className="row">
              <label htmlFor="tf-port">公网端口</label>
              <div>
                <input
                  id="tf-port"
                  className="inp mono narrow"
                  inputMode="numeric"
                  value={f.remotePort}
                  onChange={(e) => set('remotePort', e.target.value)}
                  placeholder="自动"
                />
                <div className="hint">留空则从 {poolLabel(f.type)} 端口池自动分配；指定时必须在端口池范围内。</div>
              </div>
            </div>
          )}
        </div>

        <div className="fs">
          <h4>本地目标</h4>
          <div className="row">
            <label htmlFor="tf-ip">地址</label>
            <div className="inline">
              <input id="tf-ip" className="inp mono" value={f.localIp} onChange={(e) => set('localIp', e.target.value)} placeholder="127.0.0.1" />
              <span>:</span>
              <input
                className="inp mono narrow"
                aria-label="本地端口"
                inputMode="numeric"
                value={f.localPort}
                onChange={(e) => set('localPort', e.target.value)}
                placeholder="端口"
              />
            </div>
          </div>
          <div className="hint" style={{ marginBottom: 8 }}>设备上要转发的服务，例如 127.0.0.1:25565。本地端口必填，不会自动分配。</div>
          <label className="chk">
            <input type="checkbox" checked={f.clientCanEditLocal} onChange={(e) => set('clientCanEditLocal', e.target.checked)} />
            允许客户端修改本地目标地址
          </label>
          <label className="chk">
            <input type="checkbox" checked={f.localLoopbackOnly} onChange={(e) => set('localLoopbackOnly', e.target.checked)} />
            本地目标仅允许回环地址（127.0.0.0/8、::1）
          </label>
          <label className="chk">
            <input type="checkbox" checked={f.clientCanToggle} onChange={(e) => set('clientCanToggle', e.target.checked)} />
            允许客户端自行暂停 / 启用
          </label>
        </div>

        <div className="fs" role="group" aria-label="访问控制">
          <h4>访问控制</h4>
          <div className="row">
            <label htmlFor="tf-policy">访问策略</label>
            <div>
              <select id="tf-policy" className="inp" value={https ? f.accessPolicy : 'public'} onChange={(e) => set('accessPolicy', e.target.value as AccessPolicy)}>
                <option value="public">公开（任何人可访问）</option>
                {https && (
                  <>
                    <option value="password">访问密码</option>
                    <option value="basic">HTTP Basic 认证</option>
                    <option value="login">登录门禁（NyaTunnel 账号）</option>
                  </>
                )}
              </select>
              {!https && <div className="hint">TCP / UDP 隧道只能公开访问，可用下面的 IP 白名单限制来源。</div>}
              {https && f.accessPolicy === 'login' && <div className="hint">访客需要先登录本后台的账号。</div>}
            </div>
          </div>
          {https && f.accessPolicy === 'login' && (
            <div className="row">
              <label htmlFor="tf-login-access">允许谁访问</label>
              <div>
                <select id="tf-login-access" className="inp" value={f.loginAccess} onChange={(e) => set('loginAccess', e.target.value as LoginAccess)}>
                  <option value="owner">仅隧道所有人</option>
                  <option value="users">隧道所有人 + 指定用户</option>
                  <option value="all">本站所有账号</option>
                </select>
                {f.loginAccess === 'users' && (
                  <input
                    id="tf-login-users"
                    className="inp"
                    style={{ marginTop: 6 }}
                    aria-label="允许访问的用户名"
                    placeholder="用户名，用逗号分隔"
                    value={f.loginUsers}
                    onChange={(e) => set('loginUsers', e.target.value)}
                  />
                )}
                <div className="hint">修改访问范围后，之前已通过门禁的访客需要重新登录。</div>
              </div>
            </div>
          )}
          {https && f.accessPolicy === 'basic' && (
            <div className="row">
              <label htmlFor="tf-basic-user">Basic 用户名</label>
              <input id="tf-basic-user" className="inp" autoComplete="off" value={f.basicUsername} onChange={(e) => set('basicUsername', e.target.value)} />
            </div>
          )}
          {needsPassword && (
            <div className="row">
              <label htmlFor="tf-pass">访问密码</label>
              <div>
                <div className="inline">
                  <input
                    id="tf-pass"
                    className="inp"
                    type="password"
                    autoComplete="new-password"
                    value={f.accessPassword}
                    onChange={(e) => set('accessPassword', e.target.value)}
                    placeholder={keepsPassword ? '留空保持不变' : '至少 6 个字符'}
                  />
                  {keepsPassword && <Tag tone="ok">已设置</Tag>}
                </div>
                <div className="hint">{keepsPassword ? '密码不会显示；填写新密码则替换，留空保持不变。' : '密码只保存哈希，保存后无法查看。'}</div>
              </div>
            </div>
          )}
          <div className="row align-top">
            <label htmlFor="tf-allow">IP 白名单</label>
            <div>
              <textarea
                id="tf-allow"
                className="inp mono"
                rows={2}
                value={f.ipAllowlist}
                onChange={(e) => set('ipAllowlist', e.target.value)}
                placeholder="例如 10.0.0.0/8, 203.0.113.4（留空不限制）"
              />
              <div className="hint">IP 或 CIDR，用逗号分隔。</div>
            </div>
          </div>
          {https && (
            <>
              <label className="chk">
                <input type="checkbox" checked={f.interstitial} onChange={(e) => set('interstitial', e.target.checked)} />
                首次访问显示风险提示页（防钓鱼）
              </label>
              <div className="row">
                <label htmlFor="tf-host">Host 头改写</label>
                <input id="tf-host" className="inp mono" value={f.hostRewrite} onChange={(e) => set('hostRewrite', e.target.value)} placeholder="不改写，例如 localhost:8080" />
              </div>
            </>
          )}
        </div>

        <div className="fs" role="group" aria-label="限制与配额">
          <h4>限制与配额</h4>
          <div className="row">
            <label htmlFor="tf-bw">带宽上限</label>
            <div className="inline">
              <input id="tf-bw" className="inp mono narrow" inputMode="decimal" value={f.bandwidthMbps} onChange={(e) => set('bandwidthMbps', e.target.value)} placeholder="不限" />
              <span>Mbps</span>
            </div>
          </div>
          <div className="row">
            <label htmlFor="tf-conns">最大并发连接</label>
            <input id="tf-conns" className="inp mono narrow" inputMode="numeric" value={f.maxConns} onChange={(e) => set('maxConns', e.target.value)} placeholder="不限" />
          </div>
          <div className="row">
            <label htmlFor="tf-quota">月流量配额</label>
            <div className="inline wrap">
              <input id="tf-quota" className="inp mono narrow" inputMode="decimal" value={f.quotaGb} onChange={(e) => set('quotaGb', e.target.value)} placeholder="不限" />
              <span>GB，超出后</span>
              <select className="inp auto" aria-label="超出配额后" value={f.quotaAction} onChange={(e) => set('quotaAction', e.target.value as QuotaAction)}>
                <option value="pause">暂停隧道</option>
                <option value="alert">仅告警</option>
              </select>
            </div>
          </div>
          {tunnel && (
            <div className="hint">
              本月已用 {bytes(tunnel.monthBytes)}
              {tunnel.monthlyQuotaMb > 0 ? ` / ${quotaMb(tunnel.monthlyQuotaMb)}` : ''}。配额按自然月（UTC）重置。
            </div>
          )}
        </div>

        <div className="fs">
          <h4>状态与期限</h4>
          <label className="chk">
            <input type="checkbox" checked={f.enabled} onChange={(e) => set('enabled', e.target.checked)} />
            {isAdmin ? '启用（取消勾选则由管理员停用）' : '启用'}
          </label>
          <div className="row">
            <label htmlFor="tf-exp">有效期至</label>
            <div className="inline">
              <input id="tf-exp" className="inp" type="datetime-local" value={f.expiresAt} onChange={(e) => set('expiresAt', e.target.value)} />
              {f.expiresAt && (
                <button type="button" className="btn sm" onClick={() => set('expiresAt', '')}>
                  永久
                </button>
              )}
            </div>
          </div>
          {!f.expiresAt && <div className="hint">留空表示永久有效{isAdmin ? '' : '（额度限制了最长有效期时会自动缩短）'}。</div>}
        </div>

        {approve && (
          <div className="row">
            <label htmlFor="tf-review-note">审批备注</label>
            <input id="tf-review-note" className="inp" value={note} onChange={(e) => setNote(e.target.value)} placeholder="可选，申请人可以看到" />
          </div>
        )}

        {problem && <Notice tone="bad">{problem}</Notice>}
        {refused && (
          <Notice tone="warn">
            {refused}
            {onRequestInstead && (
              <div className="mt">
                <button type="button" className="btn primary sm" onClick={() => onRequestInstead(requestPayload())}>
                  改为提交申请
                </button>
              </div>
            )}
          </Notice>
        )}
        <div className="actions">
          {tunnel && (
            <button type="button" className="btn danger" onClick={() => setConfirmDelete(true)}>
              删除
            </button>
          )}
          <span className="grow" />
          <button type="button" className="btn" onClick={onClose}>
            取消
          </button>
          <button type="submit" className="btn primary" disabled={save.isPending}>
            {approve ? '批准并创建' : tunnel ? '保存并下发' : '创建'}
          </button>
        </div>
      </form>
      {confirmDelete && tunnel && (
        <ConfirmDialog
          title="删除隧道"
          confirmLabel="删除"
          danger
          busy={remove.isPending}
          error={remove.isError ? describeError(remove.error) : null}
          onConfirm={() => remove.mutate()}
          onClose={() => setConfirmDelete(false)}
        >
          确定删除隧道 <b>{tunnel.name}</b>？公网入口 <span className="mono">{tunnel.publicUrl}</span> 会立即失效。
        </ConfirmDialog>
      )}
    </Modal>
  )
}
