import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import type { FormEvent } from 'react'
import { FilePlus2 } from 'lucide-react'
import { cancelRequest, createRequest, describeError, listDevices, listDomains, listRequests, listUsers, rejectRequest } from '../api'
import { ConfirmDialog, Empty, Loading, Modal, Notice, PageHead, Tag } from '../components/ui'
import { ago, dateTime } from '../format'
import { durationLabel, durations, requestStatuses, tunnelTypes } from '../labels'
import { POLL_MS } from '../query'
import { useSession } from '../session'
import type { Domain, RequestInput, TunnelRequest, TunnelType } from '../types'
import { TunnelForm, formFromRequest } from './tunnels'

type Tab = 'pending' | 'done'

export function RequestsPage() {
  const { isAdmin } = useSession()
  const requests = useQuery({ queryKey: ['requests'], queryFn: listRequests, refetchInterval: POLL_MS })
  const domains = useQuery({ queryKey: ['domains'], queryFn: listDomains })
  const users = useQuery({ queryKey: ['users'], queryFn: listUsers, enabled: isAdmin })
  const [tab, setTab] = useState<Tab>('pending')
  const [creating, setCreating] = useState(false)
  const [approving, setApproving] = useState<TunnelRequest | null>(null)
  const [rejecting, setRejecting] = useState<TunnelRequest | null>(null)
  const [cancelling, setCancelling] = useState<TunnelRequest | null>(null)

  const all = requests.data?.requests ?? []
  const pending = all.filter((r) => r.status === 'pending')
  const done = all.filter((r) => r.status !== 'pending')
  const rows = isAdmin ? (tab === 'pending' ? pending : done) : all

  return (
    <>
      <PageHead
        title={isAdmin ? '申请审批' : '我的申请'}
        hint={
          isAdmin
            ? '客户端和普通用户不能自行创建超出额度的隧道，但可以申请。批准时可以修改域名、端口、策略后再创建。'
            : '超出自助额度的隧道需要管理员审批。批准后隧道会出现在“我的隧道”中。'
        }
      >
        {isAdmin ? (
          <div className="seg" role="tablist" aria-label="申请状态">
            <button type="button" role="tab" aria-selected={tab === 'pending'} className={tab === 'pending' ? 'on' : undefined} onClick={() => setTab('pending')}>
              待处理 {pending.length}
            </button>
            <button type="button" role="tab" aria-selected={tab === 'done'} className={tab === 'done' ? 'on' : undefined} onClick={() => setTab('done')}>
              已处理
            </button>
          </div>
        ) : (
          <button type="button" className="btn primary" onClick={() => setCreating(true)}>
            <FilePlus2 size={14} /> 申请隧道
          </button>
        )}
      </PageHead>

      {requests.isError && <Notice tone="bad">{describeError(requests.error)}</Notice>}
      {requests.isPending ? (
        <Loading />
      ) : rows.length === 0 ? (
        <Empty>{isAdmin ? (tab === 'pending' ? '没有待处理的申请。' : '还没有处理过的申请。') : '你还没有提交过申请。'}</Empty>
      ) : (
        rows.map((r) => (
          <RequestCard
            key={r.id}
            r={r}
            domains={domains.data?.domains ?? []}
            admin={isAdmin}
            onApprove={() => setApproving(r)}
            approveReady={!!users.data && !!domains.data}
            onReject={() => setRejecting(r)}
            onCancel={() => setCancelling(r)}
          />
        ))
      )}

      {creating && <RequestForm onClose={() => setCreating(false)} />}
      {approving && (
        <TunnelForm
          tunnel={null}
          approve={approving}
          initial={formFromRequest(
            approving,
            users.data?.users.find((u) => u.id === approving.userId),
            domains.data?.domains ?? [],
          )}
          onClose={() => setApproving(null)}
        />
      )}
      {rejecting && <RejectDialog request={rejecting} onClose={() => setRejecting(null)} />}
      {cancelling && <CancelDialog request={cancelling} onClose={() => setCancelling(null)} />}
    </>
  )
}

/** "preview.dev.example.com", "blog.alice.me（自定义域名）", "端口 25565" … */
export function requestedEntry(r: TunnelRequest, domains: Domain[]): string {
  const p = r.payload
  if (p.type === 'https') {
    if (p.customDomain) return `${p.customDomain}（自定义域名）`
    const d = domains.find((x) => x.id === p.domainId)
    if (p.subdomain) return d ? `${p.subdomain}.${d.name}` : `${p.subdomain}.*`
    return '由管理员分配'
  }
  return p.remotePort ? `端口 ${p.remotePort}` : '自动分配端口'
}

function RequestCard({
  r,
  domains,
  admin,
  approveReady,
  onApprove,
  onReject,
  onCancel,
}: {
  r: TunnelRequest
  domains: Domain[]
  admin: boolean
  approveReady: boolean
  onApprove: () => void
  onReject: () => void
  onCancel: () => void
}) {
  const st = requestStatuses[r.status] ?? { label: r.status, tone: 'n' as const }
  const t = tunnelTypes[r.payload.type] ?? { label: r.payload.type, tone: 'n' as const }
  const who = r.deviceName ? `${admin ? `${r.username} · ` : ''}${r.deviceName}` : admin ? r.username : '后台'
  return (
    <article className="card" aria-label={`申请 ${r.payload.name || requestedEntry(r, domains)}`}>
      <div className="ttl tight">
        <b>
          {who} 申请：{r.payload.name || requestedEntry(r, domains)}
        </b>
        <span className="inline">
          <Tag tone={st.tone}>{st.label}</Tag>
          <span className="hint" title={dateTime(r.createdAt)}>
            {ago(r.createdAt)}
          </span>
        </span>
      </div>
      <dl className="kv">
        <dt>类型</dt>
        <dd>
          <Tag tone={t.tone}>{t.label}</Tag>
        </dd>
        <dt>{r.payload.type === 'https' ? '期望地址' : '期望端口'}</dt>
        <dd className="mono">{requestedEntry(r, domains)}</dd>
        <dt>本地目标</dt>
        <dd className="mono">
          {r.payload.localIp}:{r.payload.localPort}
        </dd>
        <dt>期望时长</dt>
        <dd>{durationLabel(r.payload.durationHours)}</dd>
        {r.deviceName && (
          <>
            <dt>设备</dt>
            <dd>{r.deviceName}</dd>
          </>
        )}
        <dt>理由</dt>
        <dd className="pre">{r.reason || <span className="hint">—</span>}</dd>
        {r.status !== 'pending' && (r.reviewNote || r.reviewedAt) && (
          <>
            <dt>处理</dt>
            <dd>
              {r.reviewedAt ? dateTime(r.reviewedAt) : ''}
              {r.reviewNote ? ` · ${r.reviewNote}` : ''}
            </dd>
          </>
        )}
      </dl>
      {r.status === 'approved' && r.tunnelId && (
        <div className="hint">
          已创建隧道。<Link to="/tunnels">查看隧道 →</Link>
        </div>
      )}
      {r.status === 'pending' && (
        <div className="inline wrap mt">
          {admin ? (
            <>
              <button type="button" className="btn primary" disabled={!approveReady} onClick={onApprove}>
                批准…
              </button>
              <button type="button" className="btn danger" onClick={onReject}>
                拒绝
              </button>
              <span className="hint">批准时会打开隧道表单，可以修改域名、端口、访问策略后再创建。</span>
            </>
          ) : (
            <button type="button" className="btn" onClick={onCancel}>
              撤回
            </button>
          )}
        </div>
      )}
    </article>
  )
}

function RejectDialog({ request, onClose }: { request: TunnelRequest; onClose: () => void }) {
  const client = useQueryClient()
  const [note, setNote] = useState('')
  const mutation = useMutation({
    mutationFn: () => rejectRequest(request.id, note.trim()),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['requests'] })
      void client.invalidateQueries({ queryKey: ['dashboard'] })
      onClose()
    },
  })
  return (
    <Modal title={`拒绝申请 · ${request.username}`} onClose={onClose}>
      <form
        className="stack"
        onSubmit={(e) => {
          e.preventDefault()
          mutation.mutate()
        }}
      >
        <label className="field">
          拒绝理由
          <textarea className="inp" rows={3} maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} placeholder="可选，申请人可以看到" autoFocus />
        </label>
        {mutation.isError && <Notice tone="bad">{describeError(mutation.error)}</Notice>}
        <div className="actions">
          <button type="button" className="btn" onClick={onClose}>
            取消
          </button>
          <button type="submit" className="btn danger-solid" disabled={mutation.isPending}>
            拒绝
          </button>
        </div>
      </form>
    </Modal>
  )
}

function CancelDialog({ request, onClose }: { request: TunnelRequest; onClose: () => void }) {
  const client = useQueryClient()
  const mutation = useMutation({
    mutationFn: () => cancelRequest(request.id),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['requests'] })
      onClose()
    },
  })
  return (
    <ConfirmDialog
      title="撤回申请"
      confirmLabel="撤回"
      danger
      busy={mutation.isPending}
      error={mutation.isError ? describeError(mutation.error) : null}
      onConfirm={() => mutation.mutate()}
      onClose={onClose}
    >
      确定撤回这条申请？撤回后管理员将不再看到它，需要时可以重新提交。
    </ConfirmDialog>
  )
}

type RequestFormState = {
  type: TunnelType
  name: string
  /** A root domain id, "custom:<name>" for one of the user's custom domains, or "other". */
  domain: string
  sub: string
  otherDomain: string
  remotePort: string
  localIp: string
  localPort: string
  durationHours: number
  deviceId: string
  reason: string
}

/** "申请隧道": asks an administrator for a tunnel. `initial` comes from a refused self-service form. */
export function RequestForm({ initial = {}, onClose }: { initial?: Partial<RequestInput>; onClose: () => void }) {
  const client = useQueryClient()
  const { user: me } = useSession()
  const domains = useQuery({ queryKey: ['domains'], queryFn: listDomains })
  const devices = useQuery({ queryKey: ['devices'], queryFn: () => listDevices() })
  const prefix = me.role === 'admin' ? '' : `${me.username}-`

  const [f, setF] = useState<RequestFormState>(() => {
    const sub = (initial.subdomain ?? '').toLowerCase()
    return {
      type: initial.type ?? 'https',
      name: initial.name ?? '',
      domain: initial.customDomain ? `custom:${initial.customDomain}` : (initial.domainId ?? ''),
      sub: prefix && sub.startsWith(prefix) ? sub.slice(prefix.length) : sub,
      otherDomain: initial.customDomain ?? '',
      remotePort: initial.remotePort ? String(initial.remotePort) : '',
      localIp: initial.localIp || '127.0.0.1',
      localPort: initial.localPort ? String(initial.localPort) : '',
      durationHours: initial.durationHours ?? 24 * 7,
      deviceId: initial.deviceId ?? '',
      reason: initial.reason ?? '',
    }
  })
  const [problem, setProblem] = useState<string | null>(null)
  const [sent, setSent] = useState(false)
  const set = <K extends keyof RequestFormState>(k: K, v: RequestFormState[K]) => setF((s) => ({ ...s, [k]: v }))

  const all = domains.data?.domains ?? []
  const roots = all.filter((d) => d.kind !== 'custom' && (d.allowUsers || me.role === 'admin'))
  const customs = all.filter((d) => d.kind === 'custom' && d.status !== 'disabled' && (d.ownerUserId === me.id || me.role === 'admin'))
  // An initial custom domain the list does not know becomes "other".
  const knownChoice = f.domain === 'other' || roots.some((d) => d.id === f.domain) || customs.some((d) => `custom:${d.name}` === f.domain)
  const choice = !domains.data ? f.domain : knownChoice ? f.domain : f.domain.startsWith('custom:') ? 'other' : (roots[0]?.id ?? 'other')
  const otherDomain = f.otherDomain
  const root = roots.find((d) => d.id === choice)
  const myDevices = (devices.data?.devices ?? []).filter((d) => d.userId === me.id && !d.revoked)
  const durationOptions = durations.some((d) => d.hours === f.durationHours) ? durations : [...durations, { hours: f.durationHours, label: durationLabel(f.durationHours) }]

  const mutation = useMutation({
    mutationFn: (body: RequestInput) => createRequest(body),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['requests'] })
      setSent(true)
    },
    onError: (e) => setProblem(describeError(e)),
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const localPort = Number(f.localPort)
    if (!Number.isInteger(localPort) || localPort < 1 || localPort > 65535) return setProblem('本地端口必须在 1–65535 之间')
    if (!f.localIp.trim()) return setProblem('请填写本地地址')
    if (!f.reason.trim()) return setProblem('请填写申请理由，方便管理员判断')
    const body: RequestInput = {
      type: f.type,
      localIp: f.localIp.trim(),
      localPort,
      durationHours: f.durationHours,
      reason: f.reason.trim(),
    }
    if (f.name.trim()) body.name = f.name.trim().toLowerCase()
    if (f.deviceId) body.deviceId = f.deviceId
    if (f.type === 'https') {
      if (choice === 'other') {
        if (!otherDomain.trim()) return setProblem('请填写想使用的自定义域名')
        body.customDomain = otherDomain.trim().toLowerCase()
      } else if (choice.startsWith('custom:')) {
        body.customDomain = choice.slice(7)
      } else {
        if (!f.sub.trim()) return setProblem('请填写期望的子域名')
        if (root) body.domainId = root.id
        body.subdomain = prefix + f.sub.trim().toLowerCase()
      }
    } else if (f.remotePort.trim()) {
      const port = Number(f.remotePort)
      if (!Number.isInteger(port) || port < 1 || port > 65535) return setProblem('公网端口必须在 1–65535 之间，留空由管理员分配')
      body.remotePort = port
    }
    setProblem(null)
    mutation.mutate(body)
  }

  if (sent) {
    return (
      <Modal title="申请隧道" onClose={onClose}>
        <Notice tone="ok">申请已提交，等待管理员审批。批准后隧道会出现在“我的隧道”中。</Notice>
        <div className="actions">
          <Link to="/requests" className="btn" onClick={onClose}>
            查看我的申请
          </Link>
          <button type="button" className="btn primary" onClick={onClose}>
            完成
          </button>
        </div>
      </Modal>
    )
  }

  return (
    <Modal title="申请隧道" onClose={onClose} wide>
      <form className="form" onSubmit={submit} aria-label="申请表单">
        <div className="fs">
          <div className="row">
            <label htmlFor="rq-type">类型</label>
            <select id="rq-type" className="inp" value={f.type} onChange={(e) => set('type', e.target.value as TunnelType)}>
              <option value="https">HTTPS（网站）</option>
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
              <option value="tcpudp">TCP+UDP（同一端口）</option>
            </select>
          </div>
          <div className="row">
            <label htmlFor="rq-name">名称</label>
            <input id="rq-name" className="inp" maxLength={40} value={f.name} onChange={(e) => set('name', e.target.value)} placeholder="可选，例如 blog" />
          </div>
          {f.type === 'https' ? (
            <>
              <div className="row">
                <label htmlFor="rq-domain">域名</label>
                <select id="rq-domain" className="inp mono" value={choice} onChange={(e) => set('domain', e.target.value)}>
                  {roots.map((d) => (
                    <option key={d.id} value={d.id}>
                      {prefix}*.{d.name}
                    </option>
                  ))}
                  {customs.map((d) => (
                    <option key={d.id} value={`custom:${d.name}`}>
                      {d.name}（我的自定义域名）
                    </option>
                  ))}
                  <option value="other">其他自定义域名…</option>
                </select>
              </div>
              {root && (
                <div className="row">
                  <label htmlFor="rq-sub">子域名</label>
                  <div>
                    <div className="inline affix">
                      {prefix && <span className="prefix mono">{prefix}</span>}
                      <input id="rq-sub" className="inp mono" value={f.sub} onChange={(e) => set('sub', e.target.value.trim().toLowerCase())} placeholder="blog" />
                      <span className="mono hint">.{root.name}</span>
                    </div>
                  </div>
                </div>
              )}
              {choice === 'other' && (
                <div className="row">
                  <label htmlFor="rq-custom">自定义域名</label>
                  <div>
                    <input id="rq-custom" className="inp mono" value={otherDomain} onChange={(e) => set('otherDomain', e.target.value)} placeholder="例如 blog.example.me" />
                    <div className="hint">管理员批准前需要先添加该域名，并把它的 DNS 指向本服务器。</div>
                  </div>
                </div>
              )}
            </>
          ) : (
            <div className="row">
              <label htmlFor="rq-port">期望端口</label>
              <input id="rq-port" className="inp mono narrow" inputMode="numeric" value={f.remotePort} onChange={(e) => set('remotePort', e.target.value)} placeholder="自动" />
            </div>
          )}
          <div className="row">
            <label htmlFor="rq-ip">本地目标</label>
            <div className="inline">
              <input id="rq-ip" className="inp mono" value={f.localIp} onChange={(e) => set('localIp', e.target.value)} />
              <span>:</span>
              <input className="inp mono narrow" aria-label="本地端口" inputMode="numeric" value={f.localPort} onChange={(e) => set('localPort', e.target.value)} placeholder="8080" />
            </div>
          </div>
          <div className="row">
            <label htmlFor="rq-duration">期望时长</label>
            <select id="rq-duration" className="inp" value={f.durationHours} onChange={(e) => set('durationHours', Number(e.target.value))}>
              {durationOptions.map((d) => (
                <option key={d.hours} value={d.hours}>
                  {d.label}
                </option>
              ))}
            </select>
          </div>
          <div className="row">
            <label htmlFor="rq-device">设备</label>
            <select id="rq-device" className="inp" value={f.deviceId} onChange={(e) => set('deviceId', e.target.value)}>
              <option value="">暂不指定</option>
              {myDevices.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.name}
                </option>
              ))}
            </select>
          </div>
          <div className="row align-top">
            <label htmlFor="rq-reason">理由</label>
            <textarea id="rq-reason" className="inp" rows={3} maxLength={500} value={f.reason} onChange={(e) => set('reason', e.target.value)} placeholder="例如：给客户演示新版页面" />
          </div>
        </div>
        {problem && <Notice tone="bad">{problem}</Notice>}
        <div className="actions">
          <button type="button" className="btn" onClick={onClose}>
            取消
          </button>
          <button type="submit" className="btn primary" disabled={mutation.isPending}>
            提交申请
          </button>
        </div>
      </form>
    </Modal>
  )
}
