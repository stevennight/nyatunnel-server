import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { FormEvent } from 'react'
import { Plus, RefreshCw } from 'lucide-react'
import { checkDomain, createDomain, deleteDomain, describeError, listDomains, listUsers, requestCustomDomain, updateDomain } from '../api'
import { ConfirmDialog, Empty, Loading, Notice, PageHead, Tag } from '../components/ui'
import { ago, date, dateTime } from '../format'
import { domainStatuses } from '../labels'
import { useSession } from '../session'
import type { Domain } from '../types'

export function DomainsPage() {
  const { isAdmin } = useSession()
  const domains = useQuery({ queryKey: ['domains'], queryFn: listDomains })
  const [deleting, setDeleting] = useState<Domain | null>(null)
  const all = domains.data?.domains ?? []
  const roots = all.filter((d) => d.kind !== 'custom')
  const customs = all.filter((d) => d.kind === 'custom')

  return (
    <>
      {isAdmin && <RootDomains roots={roots} loading={domains.isPending} onDelete={setDeleting} />}
      {domains.isError && <Notice tone="bad">{describeError(domains.error)}</Notice>}
      <CustomDomains customs={customs} publicIps={domains.data?.publicIps ?? []} loading={domains.isPending} loaded={!!domains.data} onDelete={setDeleting} />
      {deleting && <DeleteDomainDialog domain={deleting} onClose={() => setDeleting(null)} />}
    </>
  )
}

function RootDomains({ roots, loading, onDelete }: { roots: Domain[]; loading: boolean; onDelete: (d: Domain) => void }) {
  const client = useQueryClient()
  const { bootstrap } = useSession()
  const [name, setName] = useState('')
  const [allowUsers, setAllowUsers] = useState(false)

  const create = useMutation({
    mutationFn: () => createDomain({ name: name.trim(), allowUsers }),
    onSuccess: () => {
      setName('')
      setAllowUsers(false)
      void client.invalidateQueries({ queryKey: ['domains'] })
    },
  })
  const toggle = useMutation({
    mutationFn: (d: Domain) => updateDomain(d.id, { allowUsers: !d.allowUsers }),
    onSettled: () => client.invalidateQueries({ queryKey: ['domains'] }),
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (name.trim()) create.mutate()
  }

  const serverHost = (() => {
    try {
      return new URL(bootstrap.publicUrl).hostname
    } catch {
      return bootstrap.publicUrl
    }
  })()

  return (
    <section aria-label="隧道根域名">
      <PageHead title="隧道根域名" hint="HTTPS 隧道的地址是“子域名.根域名”。根域名是后台数据，可以随时添加。" />

      <div className="card info">
        <b>证书与 DNS 由谁负责？</b>
        <ul>
          <li>
            证书全部由前置的 <b>Caddy</b> 负责：每个根域名需要在 Caddy 中配置通配证书 <code>*.根域名</code>（通过 DNS-01 验证，例如 Cloudflare 插件），NyaTunnel 不处理证书。
          </li>
          <li>
            DNS 中 <code>*.根域名</code> 必须解析到本服务器（A / AAAA 记录，或 CNAME 到 <code>{serverHost || '管理台域名'}</code>）；使用 Cloudflare 时请关闭代理（橙色云朵）。
          </li>
          <li>“允许普通用户使用”的域名，普通用户的子域名仍必须以“用户名-”开头。</li>
        </ul>
      </div>

      <form className="card form-inline" onSubmit={submit} aria-label="添加根域名">
        <input className="inp mono" aria-label="根域名" placeholder="例如 dev.example.com" value={name} onChange={(e) => setName(e.target.value)} />
        <label className="chk inline-chk">
          <input type="checkbox" checked={allowUsers} onChange={(e) => setAllowUsers(e.target.checked)} />
          允许普通用户使用
        </label>
        <button type="submit" className="btn primary" disabled={!name.trim() || create.isPending}>
          <Plus size={14} /> 添加根域名
        </button>
      </form>
      {create.isError && <Notice tone="bad">{describeError(create.error)}</Notice>}
      {toggle.isError && <Notice tone="bad">{describeError(toggle.error)}</Notice>}

      {loading ? (
        <Loading />
      ) : roots.length === 0 ? (
        <Empty>还没有根域名。添加后才能创建使用子域名的 HTTPS 隧道。</Empty>
      ) : (
        <div className="tbl">
          <table>
            <thead>
              <tr>
                <th>根域名</th>
                <th>隧道地址</th>
                <th>允许谁使用</th>
                <th>隧道数</th>
                <th>添加于</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {roots.map((d) => (
                <tr key={d.id}>
                  <td className="mono">
                    <b>{d.name}</b>
                  </td>
                  <td className="mono">*.{d.name}</td>
                  <td>
                    <label className="chk inline-chk">
                      <input type="checkbox" checked={d.allowUsers} disabled={toggle.isPending} onChange={() => toggle.mutate(d)} aria-label={`${d.name} 允许普通用户使用`} />
                      {d.allowUsers ? '所有用户' : '仅管理员'}
                    </label>
                  </td>
                  <td>{d.tunnelCount}</td>
                  <td>{date(d.createdAt)}</td>
                  <td>
                    <button type="button" className="btn sm danger" onClick={() => onDelete(d)}>
                      删除
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

/** "将 blog.example.me 的 A/AAAA 记录指向 203.0.113.5（Cloudflare 上关闭代理）". */
export function dnsInstruction(domain: string, publicIps: string[]): string {
  if (publicIps.length === 0) return `将 ${domain} 的 A/AAAA 记录指向本服务器（服务器未能确定自己的公网地址，请管理员设置 NYATUNNEL_PUBLIC_IPS）`
  return `将 ${domain} 的 A/AAAA 记录指向 ${publicIps.join('、')}（Cloudflare 上关闭代理）`
}

function CustomDomains({
  customs,
  publicIps,
  loading,
  loaded,
  onDelete,
}: {
  customs: Domain[]
  publicIps: string[]
  loading: boolean
  loaded: boolean
  onDelete: (d: Domain) => void
}) {
  const client = useQueryClient()
  const { isAdmin } = useSession()
  const users = useQuery({ queryKey: ['users'], queryFn: listUsers, enabled: isAdmin })
  const [name, setName] = useState('')
  const [owner, setOwner] = useState('')
  const [checked, setChecked] = useState<string | null>(null)
  const refresh = () => client.invalidateQueries({ queryKey: ['domains'] })

  const create = useMutation({
    mutationFn: () => (isAdmin ? createDomain({ name: name.trim(), kind: 'custom', ownerUserId: owner || undefined }) : requestCustomDomain(name.trim())),
    onSuccess: () => {
      setName('')
      setOwner('')
      void refresh()
    },
  })
  const action = useMutation({
    mutationFn: ({ d, act }: { d: Domain; act: 'approve' | 'disable' | 'enable' }) => updateDomain(d.id, { action: act }),
    onSettled: () => {
      void refresh()
      void client.invalidateQueries({ queryKey: ['tunnels'] })
    },
  })
  const check = useMutation({
    mutationFn: (d: Domain) => checkDomain(d.id),
    onSuccess: (r) => {
      setChecked(r.domain.status === 'active' ? `${r.domain.name} 已生效。` : `${r.domain.name}：${r.domain.checkError || '尚未指向本服务器'}`)
      void refresh()
    },
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setChecked(null)
    if (name.trim()) create.mutate()
  }

  return (
    <section aria-label="自定义域名" className={isAdmin ? 'mt-lg' : undefined}>
      <PageHead
        title={isAdmin ? '自定义域名' : '我的自定义域名'}
        hint={
          isAdmin
            ? '整个域名指向一条 HTTPS 隧道。DNS 校验通过后才会生效，访客首次访问时由 Caddy 自动签发证书（只对已生效的域名签发）。'
            : '想用自己的域名访问隧道？先在这里申请，管理员批准并且 DNS 指向本服务器后，就可以在新建隧道时选择它。'
        }
      />

      <div className="card">
        <div className="k">{isAdmin ? '让域名所有者配置 DNS：' : '申请后请在你的 DNS 服务商处配置：'}</div>
        <div className="mono mt-sm" data-testid="dns-instruction">
          {dnsInstruction(name.trim() || '<域名>', publicIps)}
        </div>
      </div>

      <form className="card form-inline" onSubmit={submit} aria-label={isAdmin ? '添加自定义域名' : '申请自定义域名'}>
        <input className="inp mono" aria-label="自定义域名" placeholder="例如 blog.example.me" value={name} onChange={(e) => setName(e.target.value)} />
        {isAdmin && (
          <select className="inp auto" aria-label="归属用户" value={owner} onChange={(e) => setOwner(e.target.value)}>
            <option value="">不归属任何用户</option>
            {users.data?.users.map((u) => (
              <option key={u.id} value={u.id}>
                {u.username}
              </option>
            ))}
          </select>
        )}
        <button type="submit" className="btn primary" disabled={!name.trim() || create.isPending}>
          <Plus size={14} /> {isAdmin ? '添加自定义域名' : '申请自定义域名'}
        </button>
      </form>
      {create.isError && <Notice tone="bad">{describeError(create.error)}</Notice>}
      {action.isError && <Notice tone="bad">{describeError(action.error)}</Notice>}
      {check.isError && <Notice tone="bad">{describeError(check.error)}</Notice>}
      {checked && <Notice tone={checked.endsWith('已生效。') ? 'ok' : 'warn'}>{checked}</Notice>}

      {loading ? (
        <Loading />
      ) : !loaded ? null : customs.length === 0 ? (
        <Empty>{isAdmin ? '还没有自定义域名。' : '你还没有自定义域名。'}</Empty>
      ) : (
        <div className="tbl">
          <table>
            <thead>
              <tr>
                <th>域名</th>
                {isAdmin && <th>归属</th>}
                <th>隧道数</th>
                <th>状态</th>
                <th>DNS 检查</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {customs.map((d) => {
                const st = domainStatuses[d.status] ?? { label: d.status, tone: 'n' as const }
                return (
                  <tr key={d.id} className={d.status === 'disabled' ? 'dim' : undefined}>
                    <td className="mono">
                      <b>{d.name}</b>
                    </td>
                    {isAdmin && <td>{d.ownerName || <span className="hint">—</span>}</td>}
                    <td>{d.tunnelCount}</td>
                    <td>
                      <Tag tone={st.tone}>{st.label}</Tag>
                    </td>
                    <td className="clip wide" title={d.checkError || undefined}>
                      {d.checkedAt ? <span title={dateTime(d.checkedAt)}>{ago(d.checkedAt)}</span> : <span className="hint">未检查</span>}
                      {d.checkError ? (
                        <div className="hint warn-text clip wide">{d.checkError}</div>
                      ) : d.status === 'dns' ? (
                        <div className="hint clip wide">{dnsInstruction(d.name, publicIps)}</div>
                      ) : d.status === 'pending' ? (
                        <div className="hint">等待管理员批准</div>
                      ) : null}
                    </td>
                    <td>
                      <div className="inline">
                        {isAdmin && d.status === 'pending' && (
                          <button type="button" className="btn sm primary" disabled={action.isPending} onClick={() => action.mutate({ d, act: 'approve' })}>
                            批准
                          </button>
                        )}
                        {isAdmin && (d.status === 'dns' || d.status === 'active') && (
                          <button type="button" className="btn sm" disabled={action.isPending} onClick={() => action.mutate({ d, act: 'disable' })}>
                            停用
                          </button>
                        )}
                        {isAdmin && d.status === 'disabled' && (
                          <button type="button" className="btn sm" disabled={action.isPending} onClick={() => action.mutate({ d, act: 'enable' })}>
                            启用
                          </button>
                        )}
                        {(d.status === 'dns' || d.status === 'active') && (
                          <button type="button" className="btn sm" disabled={check.isPending} onClick={() => check.mutate(d)} aria-label={`重新检查 ${d.name}`}>
                            <RefreshCw size={12} /> 重新检查
                          </button>
                        )}
                        <button type="button" className="btn sm danger" onClick={() => onDelete(d)}>
                          {isAdmin && d.status === 'pending' ? '拒绝并删除' : '删除'}
                        </button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

function DeleteDomainDialog({ domain, onClose }: { domain: Domain; onClose: () => void }) {
  const client = useQueryClient()
  const remove = useMutation({
    mutationFn: () => deleteDomain(domain.id),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['domains'] })
      onClose()
    },
  })
  const custom = domain.kind === 'custom'
  return (
    <ConfirmDialog
      title={custom ? '删除自定义域名' : '删除根域名'}
      confirmLabel="删除"
      danger
      busy={remove.isPending}
      error={remove.isError ? describeError(remove.error) : null}
      onConfirm={() => remove.mutate()}
      onClose={onClose}
    >
      确定删除{custom ? '自定义域名' : '根域名'} <b className="mono">{domain.name}</b>？
      {domain.tunnelCount > 0 ? ` 仍有 ${domain.tunnelCount} 条隧道在使用它，需要先删除或修改这些隧道。` : ''}
    </ConfirmDialog>
  )
}
