import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { FormEvent } from 'react'
import { Plus } from 'lucide-react'
import { createUser, describeError, listUsers, resetUserTotp, setUserQuota, updateUser } from '../api'
import { ConfirmDialog, Dot, Empty, Loading, Modal, Notice, PageHead, Tag } from '../components/ui'
import { bytes, date, gbToMb, kbpsToMbps, mbToGb, mbpsToKbps, parseAmount, quotaMb } from '../format'
import { tunnelTypes } from '../labels'
import { useSession } from '../session'
import type { AdminUser, Quota, Role, TunnelType } from '../types'
import { MIN_PASSWORD, USERNAME_RE } from './auth'

export function UsersPage() {
  const { user: me, bootstrap } = useSession()
  const users = useQuery({ queryKey: ['users'], queryFn: listUsers })
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<AdminUser | null>(null)
  const [resetting, setResetting] = useState<AdminUser | null>(null)
  const [quota, setQuota] = useState<AdminUser | null>(null)

  return (
    <>
      <PageHead
        title="用户"
        hint={
          <>
            设备和隧道都归属于用户。禁用用户会断开其所有设备并停用其所有隧道。
            {bootstrap.forceTotp ? ' 系统设置：已强制所有账号开启两步验证。' : ''}
          </>
        }
      >
        <button type="button" className="btn primary" onClick={() => setCreating(true)}>
          <Plus size={14} /> 新建用户
        </button>
      </PageHead>

      {users.isError && <Notice tone="bad">{describeError(users.error)}</Notice>}
      {users.isPending ? (
        <Loading />
      ) : !users.data || users.data.users.length === 0 ? (
        <Empty>还没有用户。</Empty>
      ) : (
        <div className="tbl">
          <table>
            <thead>
              <tr>
                <th>用户名</th>
                <th>角色</th>
                <th>两步验证</th>
                <th>设备</th>
                <th>隧道</th>
                <th>自助额度</th>
                <th>本月流量</th>
                <th>状态</th>
                <th>创建于</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {users.data.users.map((u) => (
                <tr key={u.id} className={u.disabled ? 'dim' : undefined}>
                  <td>
                    <b>{u.username}</b>
                    {u.id === me.id && <span className="hint">（我）</span>}
                  </td>
                  <td>{u.role === 'admin' ? <Tag tone="b">管理员</Tag> : '普通用户'}</td>
                  <td>
                    {u.totpEnabled ? (
                      <Tag tone="ok">已开启</Tag>
                    ) : bootstrap.forceTotp ? (
                      <Tag tone="warn">未开启（下次登录强制设置）</Tag>
                    ) : (
                      <Tag>未开启</Tag>
                    )}
                  </td>
                  <td>{u.deviceCount}</td>
                  <td>
                    {u.tunnelCount}
                    {u.role !== 'admin' && u.quota?.enabled && u.quota.maxTunnels > 0 && <span className="hint"> / {u.quota.maxTunnels}</span>}
                  </td>
                  <td>{u.role === 'admin' ? <span className="hint">—</span> : <QuotaSummary q={u.quota} />}</td>
                  <td>
                    <MonthTraffic u={u} />
                  </td>
                  <td>
                    {u.disabled ? (
                      <Tag tone="bad">已禁用</Tag>
                    ) : (
                      <span>
                        <Dot tone="ok" />
                        正常
                      </span>
                    )}
                  </td>
                  <td>{date(u.createdAt)}</td>
                  <td>
                    <div className="inline">
                      <button type="button" className="btn sm" onClick={() => setEditing(u)}>
                        编辑
                      </button>
                      <button type="button" className="btn sm" onClick={() => setQuota(u)} aria-label={`${u.username} 的额度`}>
                        额度
                      </button>
                      {u.totpEnabled && u.id !== me.id && (
                        <button type="button" className="btn sm" onClick={() => setResetting(u)}>
                          重置两步验证
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {creating && <CreateUserDialog onClose={() => setCreating(false)} />}
      {editing && <EditUserDialog user={editing} self={editing.id === me.id} onClose={() => setEditing(null)} />}
      {resetting && <ResetTotpDialog user={resetting} onClose={() => setResetting(null)} />}
      {quota && <QuotaDialog user={quota} onClose={() => setQuota(null)} />}
    </>
  )
}

const emptyQuota: Quota = { enabled: false, maxTunnels: 0, types: [], maxBandwidthKbps: 0, maxDays: 0, interstitial: false, monthlyTrafficMb: 0 }

/** "HTTPS ×3 · 10 Mbps · 7 天" or "关闭（全部审批）". */
export function QuotaSummary({ q }: { q: Quota | undefined }) {
  if (!q || !q.enabled) return <Tag>关闭（全部审批）</Tag>
  const parts = [
    `${q.types.length ? q.types.map((t) => tunnelTypes[t]?.label ?? t).join('/') : '无类型'}${q.maxTunnels > 0 ? ` ×${q.maxTunnels}` : ''}`,
    q.maxBandwidthKbps > 0 ? `${kbpsToMbps(q.maxBandwidthKbps)} Mbps` : '',
    q.maxDays > 0 ? `${q.maxDays} 天` : '',
    q.interstitial ? '提示页' : '',
  ].filter(Boolean)
  return <span>{parts.join(' · ')}</span>
}

function MonthTraffic({ u }: { u: AdminUser }) {
  const limit = u.quota?.monthlyTrafficMb ?? 0
  const over = limit > 0 && u.monthBytes >= limit * 1024 * 1024
  return (
    <span className={over ? 'bad-text' : undefined}>
      {u.monthBytes > 0 ? bytes(u.monthBytes) : <span className="hint">—</span>}
      {limit > 0 && <span className="hint"> / {quotaMb(limit)}</span>}
    </span>
  )
}

const ALL_TYPES: TunnelType[] = ['https', 'tcp', 'udp']

/** The self-service quota of one user (PUT /users/{id}/quota). */
export function QuotaDialog({ user, onClose }: { user: AdminUser; onClose: () => void }) {
  const client = useQueryClient()
  const q: Quota = { ...emptyQuota, ...user.quota, types: user.quota?.types ?? [] }
  const [enabled, setEnabled] = useState(q.enabled)
  const [maxTunnels, setMaxTunnels] = useState(q.maxTunnels > 0 ? String(q.maxTunnels) : '')
  const [types, setTypes] = useState<TunnelType[]>(q.types)
  const [bandwidth, setBandwidth] = useState(kbpsToMbps(q.maxBandwidthKbps))
  const [maxDays, setMaxDays] = useState(q.maxDays > 0 ? String(q.maxDays) : '')
  const [interstitial, setInterstitial] = useState(q.interstitial)
  const [traffic, setTraffic] = useState(mbToGb(q.monthlyTrafficMb))
  const [problem, setProblem] = useState<string | null>(null)

  const mutation = useMutation({
    mutationFn: (body: Quota) => setUserQuota(user.id, body),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['users'] })
      void client.invalidateQueries({ queryKey: ['tunnels'] })
      onClose()
    },
    onError: (e) => setProblem(describeError(e)),
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const n = parseAmount(maxTunnels)
    const bw = parseAmount(bandwidth)
    const days = parseAmount(maxDays)
    const gb = parseAmount(traffic)
    if ([n, bw, days, gb].some(Number.isNaN) || !Number.isInteger(n) || !Number.isInteger(days)) {
      return setProblem('请填写非负数字（隧道数和天数为整数），留空表示不限')
    }
    if (enabled && types.length === 0) return setProblem('开启自助创建时至少允许一种类型')
    setProblem(null)
    mutation.mutate({
      enabled,
      maxTunnels: n,
      types: ALL_TYPES.filter((t) => types.includes(t)),
      maxBandwidthKbps: mbpsToKbps(bw),
      maxDays: days,
      interstitial,
      monthlyTrafficMb: gbToMb(gb),
    })
  }

  return (
    <Modal title={`${user.username} 的自助额度`} onClose={onClose}>
      <form className="form" onSubmit={submit} aria-label="自助额度">
        <p className="hint">超出额度的需求可以提交申请，等待管理员审批。账号月流量对该用户的所有隧道生效，即使未开启自助创建。</p>
        <label className="chk">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          允许自助创建隧道
        </label>
        <div className="fs">
          <div className="row">
            <label htmlFor="q-max">最多隧道数</label>
            <input id="q-max" className="inp mono narrow" inputMode="numeric" value={maxTunnels} disabled={!enabled} onChange={(e) => setMaxTunnels(e.target.value)} placeholder="不限" />
          </div>
          <div className="row">
            <span className="label">允许的类型</span>
            <div className="inline wrap">
              {ALL_TYPES.map((t) => (
                <label key={t} className="chk inline-chk">
                  <input
                    type="checkbox"
                    checked={types.includes(t)}
                    disabled={!enabled}
                    onChange={(e) => setTypes((s) => (e.target.checked ? [...s, t] : s.filter((x) => x !== t)))}
                  />
                  {tunnelTypes[t].label}
                </label>
              ))}
            </div>
          </div>
          <div className="row">
            <label htmlFor="q-bw">单隧道带宽</label>
            <div className="inline">
              <input id="q-bw" className="inp mono narrow" inputMode="decimal" value={bandwidth} disabled={!enabled} onChange={(e) => setBandwidth(e.target.value)} placeholder="不限" />
              <span>Mbps</span>
            </div>
          </div>
          <div className="row">
            <label htmlFor="q-days">最长有效期</label>
            <div className="inline">
              <input id="q-days" className="inp mono narrow" inputMode="numeric" value={maxDays} disabled={!enabled} onChange={(e) => setMaxDays(e.target.value)} placeholder="永久" />
              <span>天</span>
            </div>
          </div>
          <label className="chk">
            <input type="checkbox" checked={interstitial} disabled={!enabled} onChange={(e) => setInterstitial(e.target.checked)} />
            自助创建的 HTTPS 隧道强制显示首次访问提示页
          </label>
          <div className="row">
            <span className="label">子域名规则</span>
            <span className="mono">{user.username}-*.根域名</span>
          </div>
        </div>
        <div className="row">
          <label htmlFor="q-traffic">账号月流量</label>
          <div className="inline wrap">
            <input id="q-traffic" className="inp mono narrow" inputMode="decimal" value={traffic} onChange={(e) => setTraffic(e.target.value)} placeholder="不限" />
            <span>GB</span>
            <span className="hint">本月已用 {bytes(user.monthBytes)}</span>
          </div>
        </div>
        {problem && <Notice tone="bad">{problem}</Notice>}
        <div className="actions">
          <button type="button" className="btn" onClick={onClose}>
            取消
          </button>
          <button type="submit" className="btn primary" disabled={mutation.isPending}>
            保存
          </button>
        </div>
      </form>
    </Modal>
  )
}

function CreateUserDialog({ onClose }: { onClose: () => void }) {
  const client = useQueryClient()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<Role>('user')
  const [problem, setProblem] = useState<string | null>(null)
  const mutation = useMutation({
    mutationFn: () => createUser({ username: username.trim().toLowerCase(), password, role }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['users'] })
      onClose()
    },
    onError: (e) => setProblem(describeError(e)),
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!USERNAME_RE.test(username.trim().toLowerCase())) return setProblem('用户名为 2–32 位小写字母、数字或 _ . -')
    if ([...password].length < MIN_PASSWORD) return setProblem(`密码至少需要 ${MIN_PASSWORD} 个字符`)
    setProblem(null)
    mutation.mutate()
  }
  return (
    <Modal title="新建用户" onClose={onClose}>
      <form className="stack" onSubmit={submit}>
        <label className="field">
          用户名
          <input className="inp" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" autoFocus />
          <span className="hint">2–32 位小写字母、数字或 _ . -。普通用户的 HTTPS 子域名必须以“用户名-”开头。</span>
        </label>
        <label className="field">
          初始密码
          <input className="inp" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
          <span className="hint">至少 {MIN_PASSWORD} 个字符，请通过安全渠道告诉对方，并提醒其登录后修改。</span>
        </label>
        <label className="field">
          角色
          <select className="inp" value={role} onChange={(e) => setRole(e.target.value as Role)}>
            <option value="user">普通用户（管理自己的设备；按额度自助创建隧道或提交申请）</option>
            <option value="admin">管理员（完全控制）</option>
          </select>
        </label>
        {problem && <Notice tone="bad">{problem}</Notice>}
        <div className="actions">
          <button type="button" className="btn" onClick={onClose}>
            取消
          </button>
          <button type="submit" className="btn primary" disabled={mutation.isPending}>
            创建
          </button>
        </div>
      </form>
    </Modal>
  )
}

function EditUserDialog({ user, self, onClose }: { user: AdminUser; self: boolean; onClose: () => void }) {
  const client = useQueryClient()
  const [role, setRole] = useState<Role>(user.role)
  const [password, setPassword] = useState('')
  const [disabled, setDisabled] = useState(user.disabled)
  const [problem, setProblem] = useState<string | null>(null)
  const mutation = useMutation({
    mutationFn: () => {
      const body: { role?: Role; password?: string; disabled?: boolean } = {}
      if (role !== user.role) body.role = role
      if (password) body.password = password
      if (disabled !== user.disabled) body.disabled = disabled
      return updateUser(user.id, body)
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['users'] })
      void client.invalidateQueries({ queryKey: ['tunnels'] })
      void client.invalidateQueries({ queryKey: ['devices'] })
      onClose()
    },
    onError: (e) => {
      // Fields are applied one by one; refresh so the table shows what did change.
      void client.invalidateQueries({ queryKey: ['users'] })
      setProblem(describeError(e))
    },
  })
  const dirty = role !== user.role || password !== '' || disabled !== user.disabled
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (password && [...password].length < MIN_PASSWORD) return setProblem(`密码至少需要 ${MIN_PASSWORD} 个字符`)
    setProblem(null)
    mutation.mutate()
  }
  return (
    <Modal title={`编辑用户 · ${user.username}`} onClose={onClose}>
      <form className="stack" onSubmit={submit}>
        <label className="field">
          角色
          <select className="inp" value={role} disabled={self} onChange={(e) => setRole(e.target.value as Role)}>
            <option value="user">普通用户</option>
            <option value="admin">管理员</option>
          </select>
          {self && <span className="hint">不能取消自己的管理员身份。</span>}
        </label>
        <label className="field">
          重置密码
          <input className="inp" type="password" value={password} placeholder="留空则不修改" onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
          <span className="hint">重置后该用户所有登录会话都会退出。</span>
        </label>
        <label className="chk">
          <input type="checkbox" checked={disabled} disabled={self} onChange={(e) => setDisabled(e.target.checked)} />
          禁用该账号
        </label>
        {disabled && !user.disabled && (
          <Notice tone="warn">禁用后：该用户无法登录，其所有设备立即断开，所有隧道被停用（重新启用账号后需逐条重新启用隧道）。</Notice>
        )}
        {problem && <Notice tone="bad">{problem}</Notice>}
        <div className="actions">
          <button type="button" className="btn" onClick={onClose}>
            取消
          </button>
          <button type="submit" className={`btn ${disabled && !user.disabled ? 'danger-solid' : 'primary'}`} disabled={!dirty || mutation.isPending}>
            保存
          </button>
        </div>
      </form>
    </Modal>
  )
}

function ResetTotpDialog({ user, onClose }: { user: AdminUser; onClose: () => void }) {
  const client = useQueryClient()
  const mutation = useMutation({
    mutationFn: () => resetUserTotp(user.id),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['users'] })
      onClose()
    },
  })
  return (
    <ConfirmDialog
      title="重置两步验证"
      confirmLabel="重置"
      danger
      busy={mutation.isPending}
      error={mutation.isError ? describeError(mutation.error) : null}
      onConfirm={() => mutation.mutate()}
      onClose={onClose}
    >
      确定移除 <b>{user.username}</b> 的两步验证（例如手机丢失）？其恢复码同时作废、登录会话全部退出。若系统强制两步验证，对方下次登录后需重新设置。
    </ConfirmDialog>
  )
}
