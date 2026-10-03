import { useInfiniteQuery } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { AUDIT_PAGE, describeError, listAudit } from '../api'
import { Empty, Loading, Notice, PageHead, Tag } from '../components/ui'
import { dateTime } from '../format'
import { actionTone, actorLabel, auditActions, needsAttention } from '../labels'
import type { AuditEvent } from '../types'

type Filter = 'all' | 'denied' | 'admin' | 'device'

export function AuditPage() {
  const [filter, setFilter] = useState<Filter>('all')
  const audit = useInfiniteQuery({
    queryKey: ['audit'],
    queryFn: ({ pageParam }) => listAudit(pageParam),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (last) => (last.events.length < AUDIT_PAGE ? undefined : last.events[last.events.length - 1]?.id),
  })

  const events = useMemo(() => {
    const all = audit.data?.pages.flatMap((p) => p.events) ?? []
    return all.filter((e) => {
      if (filter === 'denied') return needsAttention(e.action)
      if (filter === 'device') return e.action.startsWith('device.') || e.action.startsWith('enroll.') || e.actorType === 'device'
      if (filter === 'admin') return e.actorType === 'user' && !e.action.startsWith('auth.')
      return true
    })
  }, [audit.data, filter])

  return (
    <>
      <PageHead title="审计日志" hint="管理操作、设备注册、被拒绝的请求和访客举报都会留痕，便于追溯滥用来源。">
        <select className="inp auto" aria-label="筛选事件" value={filter} onChange={(e) => setFilter(e.target.value as Filter)}>
          <option value="all">全部事件</option>
          <option value="denied">仅失败、拒绝与举报</option>
          <option value="admin">管理操作</option>
          <option value="device">设备与注册</option>
        </select>
        <button type="button" className="btn" onClick={() => audit.refetch()} disabled={audit.isFetching}>
          刷新
        </button>
      </PageHead>

      {audit.isError && <Notice tone="bad">{describeError(audit.error)}</Notice>}
      {audit.isPending ? (
        <Loading />
      ) : events.length === 0 ? (
        <Empty>{filter === 'all' ? '还没有审计记录。' : '已加载的记录中没有符合条件的事件。'}</Empty>
      ) : (
        <div className="tbl">
          <table>
            <thead>
              <tr>
                <th>时间</th>
                <th>主体</th>
                <th>事件</th>
                <th>目标</th>
                <th>详情</th>
                <th>来源 IP</th>
              </tr>
            </thead>
            <tbody>
              {events.map((e) => (
                <AuditRow key={e.id} e={e} />
              ))}
            </tbody>
          </table>
        </div>
      )}
      {audit.hasNextPage && (
        <div className="center mt">
          <button type="button" className="btn" onClick={() => audit.fetchNextPage()} disabled={audit.isFetchingNextPage}>
            {audit.isFetchingNextPage ? '加载中…' : '加载更多'}
          </button>
        </div>
      )}
    </>
  )
}

export function AuditRow({ e }: { e: AuditEvent }) {
  const denied = needsAttention(e.action)
  return (
    <tr className={denied ? 'denied' : undefined}>
      <td>{dateTime(e.at)}</td>
      <td>{actorLabel(e)}</td>
      <td>
        <Tag tone={actionTone(e.action)} title={e.action}>
          {auditActions[e.action] ?? e.action}
        </Tag>
      </td>
      <td className="mono clip" title={e.target}>
        {e.target || '—'}
      </td>
      <td className="clip wide" title={e.detail}>
        {e.detail || '—'}
      </td>
      <td className="mono">{e.ip || '—'}</td>
    </tr>
  )
}
