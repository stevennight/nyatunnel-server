import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { ExternalLink, RefreshCw } from 'lucide-react'
import { createEnrollment, describeError, listTunnels, listUsers } from '../api'
import { countdown } from '../format'
import { tunnelTypes } from '../labels'
import { useSession } from '../session'
import type { Enrollment } from '../types'
import { QrCode } from './qr'
import { CopyButton, Modal, Notice, Tag } from './ui'

const TTL_OPTIONS = [
  { minutes: 10, label: '10 分钟' },
  { minutes: 60, label: '1 小时' },
  { minutes: 1440, label: '24 小时' },
]

/**
 * "邀请设备": makes a one-time enrollment code. The deep link / QR code carry only the server
 * address and the code, never configuration or long-lived credentials.
 */
export function EnrollDialog({
  onClose,
  initialUserId,
  initialTunnelIds = [],
}: {
  onClose: () => void
  initialUserId?: string
  initialTunnelIds?: string[]
}) {
  const client = useQueryClient()
  const { user: me, isAdmin } = useSession()
  const [userId, setUserId] = useState(initialUserId ?? me.id)
  const [nameHint, setNameHint] = useState('')
  const [tunnelIds, setTunnelIds] = useState<string[]>(initialTunnelIds)
  const [ttl, setTtl] = useState(10)
  const [result, setResult] = useState<Enrollment | null>(null)

  const users = useQuery({ queryKey: ['users'], queryFn: listUsers, enabled: isAdmin })
  const tunnels = useQuery({ queryKey: ['tunnels'], queryFn: () => listTunnels() })
  const unassigned = (tunnels.data?.tunnels ?? []).filter((t) => t.userId === userId && t.deviceId === null)
  const owners: { id: string; username: string; disabled: boolean }[] = users.data?.users ?? [{ id: me.id, username: me.username, disabled: false }]

  const mutation = useMutation({
    mutationFn: () =>
      createEnrollment({
        userId: isAdmin ? userId : undefined,
        deviceNameHint: nameHint.trim(),
        tunnelIds: tunnelIds.filter((id) => unassigned.some((t) => t.id === id)),
        ttlMinutes: ttl,
      }),
    onSuccess: (r) => {
      setResult(r)
      void client.invalidateQueries({ queryKey: ['audit'] })
      void client.invalidateQueries({ queryKey: ['enrollments'] })
    },
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    mutation.mutate()
  }

  const toggleTunnel = (id: string, on: boolean) => setTunnelIds((ids) => (on ? [...ids, id] : ids.filter((x) => x !== id)))

  return (
    <Modal title="邀请新设备" onClose={onClose} wide>
      {result ? (
        <EnrollmentResult enrollment={result} onAgain={() => setResult(null)} onClose={onClose} />
      ) : (
        <form onSubmit={submit} className="form">
          <p className="hint">
            生成一次性注册码。链接和二维码里只有“服务器地址 + 注册码”，不包含任何隧道配置或长期凭据；注册码使用一次或到期后即失效。
          </p>
          {isAdmin && (
            <div className="row">
              <label htmlFor="enroll-user">设备归属用户</label>
              <select
                id="enroll-user"
                className="inp"
                value={userId}
                onChange={(e) => {
                  setUserId(e.target.value)
                  setTunnelIds([])
                }}
              >
                {owners.map((u) => (
                  <option key={u.id} value={u.id} disabled={u.disabled}>
                    {u.username}
                    {u.id === me.id ? '（我）' : ''}
                    {u.disabled ? '（已禁用）' : ''}
                  </option>
                ))}
              </select>
            </div>
          )}
          <div className="row">
            <label htmlFor="enroll-name">设备名（建议）</label>
            <input id="enroll-name" className="inp" placeholder="例如 home-server（可选）" maxLength={64} value={nameHint} onChange={(e) => setNameHint(e.target.value)} />
          </div>
          <div className="row align-top">
            <span className="label">预分配隧道</span>
            <div>
              {unassigned.length === 0 ? (
                <span className="hint">{tunnels.isPending ? '加载中…' : '没有待绑定设备的隧道。可以先注册设备，之后再在隧道里绑定。'}</span>
              ) : (
                unassigned.map((t) => (
                  <label key={t.id} className="chk">
                    <input type="checkbox" checked={tunnelIds.includes(t.id)} onChange={(e) => toggleTunnel(t.id, e.target.checked)} />
                    <b>{t.name}</b> <Tag tone={tunnelTypes[t.type].tone}>{tunnelTypes[t.type].label}</Tag>
                    <span className="mono hint">{t.publicUrl}</span>
                  </label>
                ))
              )}
            </div>
          </div>
          <div className="row">
            <label htmlFor="enroll-ttl">有效期</label>
            <select id="enroll-ttl" className="inp" value={ttl} onChange={(e) => setTtl(Number(e.target.value))}>
              {TTL_OPTIONS.map((o) => (
                <option key={o.minutes} value={o.minutes}>
                  {o.label}
                </option>
              ))}
            </select>
          </div>
          {mutation.isError && <Notice tone="bad">{describeError(mutation.error)}</Notice>}
          <div className="actions">
            <button type="button" className="btn" onClick={onClose}>
              取消
            </button>
            <button type="submit" className="btn primary" disabled={mutation.isPending}>
              生成注册码
            </button>
          </div>
        </form>
      )}
    </Modal>
  )
}

function useNow(intervalMs = 1000) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(t)
  }, [intervalMs])
  return now
}

export function EnrollmentResult({ enrollment, onAgain, onClose }: { enrollment: Enrollment; onAgain: () => void; onClose: () => void }) {
  const now = useNow()
  const left = enrollment.expiresAt - now
  const expired = left <= 0

  return (
    <div className="enroll-result">
      <div className="qr-row">
        <QrCode text={enrollment.url} label="注册链接二维码" size={150} />
        <div className="grow">
          <div className="hint">注册码</div>
          <div className={`codebox mono${expired ? ' expired' : ''}`} aria-label="注册码">
            {enrollment.code}
          </div>
          <div className="hint" role="timer">
            {expired ? <Tag tone="bad">已过期</Tag> : <>剩余 {countdown(left)} · 仅可使用一次</>}
          </div>
        </div>
      </div>

      <div className="hint mt">在已安装客户端的电脑上点击（或用手机扫码后在电脑上打开）：</div>
      <div className="inline copy-line">
        <input className="inp mono" readOnly value={enrollment.url} aria-label="注册链接" onFocus={(e) => e.currentTarget.select()} />
        <a className="btn primary" href={enrollment.url}>
          <ExternalLink size={14} /> 打开客户端
        </a>
        <CopyButton text={enrollment.url} label="复制链接" />
      </div>

      <div className="hint mt">没有图形界面的机器（服务器、NAS）：</div>
      <div className="inline copy-line">
        <input className="inp mono" readOnly value={enrollment.cliCommand} aria-label="CLI 命令" onFocus={(e) => e.currentTarget.select()} />
        <CopyButton text={enrollment.cliCommand} label="复制命令" />
      </div>

      <div className="actions">
        <button type="button" className="btn" onClick={onAgain}>
          <RefreshCw size={14} /> 再生成一个
        </button>
        <button type="button" className="btn primary" onClick={onClose}>
          完成
        </button>
      </div>
    </div>
  )
}
