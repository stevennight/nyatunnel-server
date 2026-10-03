import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import { Plus, Search, UserPlus } from 'lucide-react'
import { createTunnel, deleteTunnel, describeError, listDevices, listDomains, listTunnels, listUsers, updateTunnel } from '../api'
import { EnrollDialog } from '../components/enroll-dialog'
import { ConfirmDialog, Empty, Loading, Modal, Notice, PageHead, Tag, TunnelStateTag } from '../components/ui'
import { dateTime, fromLocalInput, toLocalInput, until } from '../format'
import { tunnelTypes } from '../labels'
import { POLL_MS } from '../query'
import { useSession } from '../session'
import type { Tunnel, TunnelInput, TunnelType } from '../types'

const NAME_RE = /^[a-z0-9][a-z0-9-]{0,39}$/

export function TunnelsPage() {
  const { isAdmin } = useSession()
  const tunnels = useQuery({ queryKey: ['tunnels'], queryFn: () => listTunnels(), refetchInterval: POLL_MS })
  const users = useQuery({ queryKey: ['users'], queryFn: listUsers, enabled: isAdmin })
  const [q, setQ] = useState('')
  const [owner, setOwner] = useState('')
  const [editing, setEditing] = useState<Tunnel | 'new' | null>(null)
  const [invite, setInvite] = useState<{ userId?: string; tunnelIds?: string[] } | null>(null)

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
        hint={isAdmin ? '所有对外入口都在这里定义；没有登记的代理，服务端一律拒绝。' : '隧道由管理员分配，这里只能查看。需要新的入口请联系管理员。'}
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
        {isAdmin && (
          <button type="button" className="btn primary" onClick={() => setEditing('new')}>
            <Plus size={14} /> 新建隧道
          </button>
        )}
      </PageHead>

      {tunnels.isError && <Notice tone="bad">{describeError(tunnels.error)}</Notice>}
      {tunnels.isPending ? (
        <Loading />
      ) : rows.length === 0 ? (
        <Empty>{q || owner ? '没有匹配的隧道。' : isAdmin ? '还没有隧道。先在“域名”或“端口池”里准备好资源，再新建隧道。' : '你还没有隧道，请联系管理员分配。'}</Empty>
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
                <th>状态</th>
                <th>到期</th>
                {isAdmin && <th />}
              </tr>
            </thead>
            <tbody>
              {rows.map((t) => (
                <tr key={t.id}>
                  <td>
                    <b>{t.name}</b>
                    {t.note && <div className="hint clip" title={t.note}>{t.note}</div>}
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
                    {t.clientCanEditLocal && <span className="hint" title="客户端可修改本地目标"> ✎</span>}
                  </td>
                  <td>
                    <TunnelStateTag state={t.state} error={t.stateError} />
                    {t.state === 'error' && t.stateError && <div className="hint bad-text clip" title={t.stateError}>{t.stateError}</div>}
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
                  {isAdmin && (
                    <td>
                      <button type="button" className="btn sm" onClick={() => setEditing(t)}>
                        编辑
                      </button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {editing && <TunnelForm tunnel={editing === 'new' ? null : editing} onClose={() => setEditing(null)} />}
      {invite && <EnrollDialog initialUserId={invite.userId} initialTunnelIds={invite.tunnelIds} onClose={() => setInvite(null)} />}
    </>
  )
}

type FormState = {
  userId: string
  deviceId: string
  name: string
  type: TunnelType
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
}

function initialState(t: Tunnel | null, selfId: string): FormState {
  if (!t) {
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
    }
  }
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
  }
}

/** Create / edit a tunnel (administrators only). */
export function TunnelForm({ tunnel, onClose }: { tunnel: Tunnel | null; onClose: () => void }) {
  const client = useQueryClient()
  const { user: me } = useSession()
  const [f, setF] = useState<FormState>(() => initialState(tunnel, me.id))
  const [problem, setProblem] = useState<string | null>(null)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setF((s) => ({ ...s, [k]: v }))

  const users = useQuery({ queryKey: ['users'], queryFn: listUsers })
  const devices = useQuery({ queryKey: ['devices'], queryFn: () => listDevices() })
  const domains = useQuery({ queryKey: ['domains'], queryFn: listDomains })

  const ownerOf = (id: string) => users.data?.users.find((u) => u.id === id)
  const prefixFor = (id: string) => {
    const u = ownerOf(id)
    return u && u.role !== 'admin' ? `${u.username}-` : ''
  }
  const owner = ownerOf(f.userId)
  const prefix = prefixFor(f.userId)
  // A stored subdomain that predates the owner's prefix rule is shown as is (the server keeps it if unchanged).
  const showPrefix = prefix !== '' && (f.subdomain === '' || f.subdomain.startsWith(prefix))
  const subInput = showPrefix ? f.subdomain.slice(prefix.length) : f.subdomain

  const ownerDevices = (devices.data?.devices ?? []).filter((d) => d.userId === f.userId && (!d.revoked || d.id === f.deviceId))
  const usableDomains = (domains.data?.domains ?? []).filter((d) => !prefix || d.allowUsers || d.id === f.domainId)
  const domain = domains.data?.domains.find((d) => d.id === (f.domainId || usableDomains[0]?.id))
  const domainId = domain?.id ?? ''

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

  const save = useMutation({
    mutationFn: (body: TunnelInput) => (tunnel ? updateTunnel(tunnel.id, body) : createTunnel(body)),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['tunnels'] })
      void client.invalidateQueries({ queryKey: ['devices'] })
      void client.invalidateQueries({ queryKey: ['domains'] })
      void client.invalidateQueries({ queryKey: ['port-pools'] })
      onClose()
    },
    onError: (e) => setProblem(describeError(e)),
  })
  const remove = useMutation({
    mutationFn: () => deleteTunnel(tunnel!.id),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['tunnels'] })
      void client.invalidateQueries({ queryKey: ['devices'] })
      onClose()
    },
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const name = f.name.trim()
    if (!NAME_RE.test(name)) return setProblem('名称只能包含小写字母、数字和连字符，最长 40 个字符')
    const localPort = Number(f.localPort)
    if (!Number.isInteger(localPort) || localPort < 1 || localPort > 65535) return setProblem('本地端口必须在 1–65535 之间')
    if (!f.localIp.trim()) return setProblem('请填写本地地址')
    let remotePort: number | null = null
    if (f.type === 'https') {
      if (!domainId) return setProblem('请选择域名')
      if (!subInput.trim()) return setProblem('请填写子域名')
    } else if (f.remotePort.trim()) {
      remotePort = Number(f.remotePort)
      if (!Number.isInteger(remotePort) || remotePort < 1 || remotePort > 65535) return setProblem('公网端口必须在 1–65535 之间，留空自动分配')
    }
    setProblem(null)
    save.mutate({
      userId: f.userId,
      deviceId: f.deviceId || null,
      name,
      type: f.type,
      domainId: f.type === 'https' ? domainId : null,
      subdomain: f.type === 'https' ? f.subdomain.trim().toLowerCase() : null,
      remotePort,
      localIp: f.localIp.trim(),
      localPort,
      clientCanEditLocal: f.clientCanEditLocal,
      localLoopbackOnly: f.localLoopbackOnly,
      clientCanToggle: f.clientCanToggle,
      enabled: f.enabled,
      note: f.note.trim(),
      expiresAt: fromLocalInput(f.expiresAt),
    })
  }

  return (
    <Modal title={tunnel ? `编辑隧道 · ${tunnel.name}` : '新建隧道'} onClose={onClose} wide>
      <form className="form" onSubmit={submit} aria-label="隧道表单">
        <div className="fs">
          <h4>基本信息</h4>
          <div className="row">
            <label htmlFor="tf-name">名称</label>
            <input id="tf-name" className="inp" value={f.name} onChange={(e) => set('name', e.target.value)} placeholder="例如 blog" maxLength={40} />
          </div>
          <div className="row">
            <label htmlFor="tf-user">所属用户</label>
            <select id="tf-user" className="inp" value={f.userId} onChange={(e) => changeOwner(e.target.value)}>
              {(users.data?.users ?? [{ ...me, deviceCount: 0, tunnelCount: 0 }]).map((u) => (
                <option key={u.id} value={u.id}>
                  {u.username}（{u.role === 'admin' ? '管理员' : '普通用户'}）{u.disabled ? ' · 已禁用' : ''}
                </option>
              ))}
            </select>
          </div>
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
              <select id="tf-type" className="inp" value={f.type} disabled={tunnel !== null} onChange={(e) => set('type', e.target.value as TunnelType)}>
                <option value="https">HTTPS（Caddy 终止 TLS）</option>
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
              </select>
              {tunnel && <div className="hint">类型创建后不能修改。</div>}
            </div>
          </div>
          <div className="row">
            <label htmlFor="tf-note">备注</label>
            <input id="tf-note" className="inp" value={f.note} onChange={(e) => set('note', e.target.value)} placeholder="可选" />
          </div>
        </div>

        <div className="fs">
          <h4>
            公网入口 <Tag tone="b">仅管理员可改</Tag>
          </h4>
          {f.type === 'https' ? (
            <>
              <div className="row">
                <label htmlFor="tf-sub">子域名</label>
                <div>
                  <div className="inline affix">
                    {showPrefix && (
                      <span className="prefix mono" data-testid="subdomain-prefix">
                        {prefix}
                      </span>
                    )}
                    <input
                      id="tf-sub"
                      className="inp mono"
                      value={subInput}
                      onChange={(e) => set('subdomain', (showPrefix ? prefix : '') + e.target.value.trim().toLowerCase())}
                      placeholder={showPrefix ? 'blog' : '例如 blog'}
                    />
                    <select className="inp mono auto" aria-label="域名" value={domainId} onChange={(e) => set('domainId', e.target.value)}>
                      {usableDomains.length === 0 && <option value="">（无可用域名）</option>}
                      {usableDomains.map((d) => (
                        <option key={d.id} value={d.id}>
                          .{d.name}
                        </option>
                      ))}
                    </select>
                  </div>
                  {prefix && owner && (
                    <div className="hint">
                      {owner.username} 是普通用户：子域名必须以 <code>{prefix}</code> 开头，且只能使用“允许普通用户使用”的域名。
                    </div>
                  )}
                  {prefix && !showPrefix && <div className="hint warn-text">当前子域名不符合该用户的前缀规则；保持不变可以保存，修改时需以 {prefix} 开头。</div>}
                  {usableDomains.length === 0 && domains.data && <div className="hint warn-text">没有可用的域名，请先在“域名”页面添加。</div>}
                  {domain && subInput && (
                    <div className="hint">
                      访问地址：<span className="mono">https://{f.subdomain}.{domain.name}</span>
                    </div>
                  )}
                </div>
              </div>
              <div className="row">
                <span className="label">证书</span>
                <span className="hint">由前置的 Caddy 负责（{domain ? `*.${domain.name}` : '通配证书'}，DNS-01 自动续期）。</span>
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
                <div className="hint">留空则从 {f.type.toUpperCase()} 端口池自动分配；指定时必须在端口池范围内。</div>
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
                placeholder="8080"
              />
            </div>
          </div>
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

        <div className="fs">
          <h4>状态与期限</h4>
          <label className="chk">
            <input type="checkbox" checked={f.enabled} onChange={(e) => set('enabled', e.target.checked)} />
            启用（取消勾选则由管理员停用）
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
          {!f.expiresAt && <div className="hint">留空表示永久有效。</div>}
        </div>

        {problem && <Notice tone="bad">{problem}</Notice>}
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
            {tunnel ? '保存并下发' : '创建'}
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
