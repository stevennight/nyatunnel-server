import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { FormEvent } from 'react'
import { Plus } from 'lucide-react'
import { createDomain, deleteDomain, describeError, listDomains, updateDomain } from '../api'
import { ConfirmDialog, Empty, Loading, Notice, PageHead } from '../components/ui'
import { date } from '../format'
import { useSession } from '../session'
import type { Domain } from '../types'

export function DomainsPage() {
  const client = useQueryClient()
  const { bootstrap } = useSession()
  const domains = useQuery({ queryKey: ['domains'], queryFn: listDomains })
  const [name, setName] = useState('')
  const [allowUsers, setAllowUsers] = useState(false)
  const [deleting, setDeleting] = useState<Domain | null>(null)

  const create = useMutation({
    mutationFn: () => createDomain({ name: name.trim(), allowUsers }),
    onSuccess: () => {
      setName('')
      setAllowUsers(false)
      void client.invalidateQueries({ queryKey: ['domains'] })
    },
  })
  const toggle = useMutation({
    mutationFn: (d: Domain) => updateDomain(d.id, !d.allowUsers),
    onSettled: () => client.invalidateQueries({ queryKey: ['domains'] }),
  })
  const remove = useMutation({
    mutationFn: (d: Domain) => deleteDomain(d.id),
    onSuccess: () => {
      setDeleting(null)
      void client.invalidateQueries({ queryKey: ['domains'] })
    },
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
    <>
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

      {domains.isError && <Notice tone="bad">{describeError(domains.error)}</Notice>}
      {domains.isPending ? (
        <Loading />
      ) : !domains.data || domains.data.domains.length === 0 ? (
        <Empty>还没有根域名。添加后才能创建 HTTPS 隧道。</Empty>
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
              {domains.data.domains.map((d) => (
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
                    <button type="button" className="btn sm danger" onClick={() => setDeleting(d)}>
                      删除
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {deleting && (
        <ConfirmDialog
          title="删除根域名"
          confirmLabel="删除"
          danger
          busy={remove.isPending}
          error={remove.isError ? describeError(remove.error) : null}
          onConfirm={() => remove.mutate(deleting)}
          onClose={() => {
            setDeleting(null)
            remove.reset()
          }}
        >
          确定删除根域名 <b className="mono">{deleting.name}</b>？
          {deleting.tunnelCount > 0 ? ` 仍有 ${deleting.tunnelCount} 条隧道在使用它，需要先删除或修改这些隧道。` : ''}
        </ConfirmDialog>
      )}
    </>
  )
}
