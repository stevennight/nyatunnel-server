import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { FormEvent } from 'react'
import { Plus } from 'lucide-react'
import { createPortPool, deletePortPool, describeError, listPortPools } from '../api'
import { ConfirmDialog, Empty, Loading, Notice, PageHead, Tag } from '../components/ui'
import type { PortPool, PortProto } from '../types'

export function PortPoolsPage() {
  const client = useQueryClient()
  const pools = useQuery({ queryKey: ['port-pools'], queryFn: listPortPools })
  const [proto, setProto] = useState<PortProto>('tcp')
  const [start, setStart] = useState('')
  const [end, setEnd] = useState('')
  const [problem, setProblem] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<PortPool | null>(null)

  const create = useMutation({
    mutationFn: (body: { proto: PortProto; start: number; end: number }) => createPortPool(body),
    onSuccess: () => {
      setStart('')
      setEnd('')
      void client.invalidateQueries({ queryKey: ['port-pools'] })
    },
    onError: (e) => setProblem(describeError(e)),
  })
  const remove = useMutation({
    mutationFn: (p: PortPool) => deletePortPool(p.id),
    onSuccess: () => {
      setDeleting(null)
      void client.invalidateQueries({ queryKey: ['port-pools'] })
    },
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const s = Number(start)
    const t = end.trim() ? Number(end) : s
    if (!Number.isInteger(s) || !Number.isInteger(t) || s < 1024 || t > 65535 || t < s) return setProblem('端口范围须在 1024–65535 内，且结束端口不小于起始端口')
    if (t - s > 10000) return setProblem('一次最多添加 10000 个端口')
    setProblem(null)
    create.mutate({ proto, start: s, end: t })
  }

  return (
    <>
      <PageHead title="端口池" hint="TCP / UDP 隧道的公网端口从这里分配。新建隧道时不指定端口，会自动选用最小的空闲端口。" />

      <div className="card info">
        服务端会在这些端口上直接监听。请确保防火墙 / 安全组已放行对应端口，使用 Docker 时还需要映射这些端口（例如 <code>-p 20000-20100:20000-20100</code>
        ）。
      </div>

      <form className="card form-inline" onSubmit={submit} aria-label="添加端口池">
        <select className="inp auto" aria-label="协议" value={proto} onChange={(e) => setProto(e.target.value as PortProto)}>
          <option value="tcp">TCP</option>
          <option value="udp">UDP</option>
        </select>
        <input className="inp mono narrow" aria-label="起始端口" inputMode="numeric" placeholder="20000" value={start} onChange={(e) => setStart(e.target.value)} />
        <span>—</span>
        <input className="inp mono narrow" aria-label="结束端口" inputMode="numeric" placeholder="20100" value={end} onChange={(e) => setEnd(e.target.value)} />
        <button type="submit" className="btn primary" disabled={!start.trim() || create.isPending}>
          <Plus size={14} /> 添加
        </button>
      </form>
      {problem && <Notice tone="bad">{problem}</Notice>}

      {pools.isError && <Notice tone="bad">{describeError(pools.error)}</Notice>}
      {pools.isPending ? (
        <Loading />
      ) : !pools.data || pools.data.portPools.length === 0 ? (
        <Empty>还没有端口池。添加后才能创建 TCP / UDP 隧道。</Empty>
      ) : (
        <div className="tbl">
          <table>
            <thead>
              <tr>
                <th>协议</th>
                <th>范围</th>
                <th>端口数</th>
                <th>已使用</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {pools.data.portPools.map((p) => {
                const size = p.end - p.start + 1
                return (
                  <tr key={p.id}>
                    <td>
                      <Tag tone="warn">{p.proto.toUpperCase()}</Tag>
                    </td>
                    <td className="mono">
                      {p.start === p.end ? p.start : `${p.start} – ${p.end}`}
                    </td>
                    <td>{size}</td>
                    <td>
                      <div className="meter" title={`${p.used} / ${size}`}>
                        <span style={{ width: `${Math.min(100, (p.used / size) * 100)}%` }} />
                      </div>{' '}
                      {p.used} / {size}
                    </td>
                    <td>
                      <button type="button" className="btn sm danger" onClick={() => setDeleting(p)}>
                        删除
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {deleting && (
        <ConfirmDialog
          title="删除端口池"
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
          确定删除 {deleting.proto.toUpperCase()} 端口池{' '}
          <b className="mono">
            {deleting.start}–{deleting.end}
          </b>
          ？之后不会再从这个范围分配端口。
          {deleting.used > 0 && ` 仍有 ${deleting.used} 条隧道使用其中的端口：它们暂时保留端口，但再次编辑时必须改用其他端口池中的端口。`}
        </ConfirmDialog>
      )}
    </>
  )
}
