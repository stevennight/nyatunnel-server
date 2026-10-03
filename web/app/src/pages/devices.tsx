import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import { UserPlus } from 'lucide-react'
import { cancelEnrollment, describeError, listDevices, listEnrollments, listTunnels, listUsers, renameDevice, revokeDevice } from '../api'
import { EnrollDialog } from '../components/enroll-dialog'
import { ConfirmDialog, Dot, Empty, Loading, Modal, Notice, PageHead, Tag } from '../components/ui'
import { ago, dateTime, until } from '../format'
import { POLL_MS } from '../query'
import { useSession } from '../session'
import type { Device, PendingEnrollment } from '../types'

export function DevicesPage() {
  const { isAdmin } = useSession()
  const devices = useQuery({ queryKey: ['devices'], queryFn: () => listDevices(), refetchInterval: POLL_MS })
  const users = useQuery({ queryKey: ['users'], queryFn: listUsers, enabled: isAdmin })
  const [owner, setOwner] = useState('')
  const [showRevoked, setShowRevoked] = useState(false)
  const [invite, setInvite] = useState(false)
  const [renaming, setRenaming] = useState<Device | null>(null)
  const [revoking, setRevoking] = useState<Device | null>(null)

  const all = devices.data?.devices ?? []
  const revokedCount = all.filter((d) => d.revoked).length
  const rows = useMemo(
    () =>
      all
        .filter((d) => (showRevoked || !d.revoked) && (!owner || d.userId === owner))
        .sort((a, b) => Number(b.online) - Number(a.online) || Number(a.revoked) - Number(b.revoked)),
    [all, owner, showRevoked],
  )

  return (
    <>
      <PageHead
        title={isAdmin ? '设备' : '我的设备'}
        hint="每个客户端都是一台独立的设备，拥有自己的密钥。吊销某台设备会立即断开它的所有隧道，不影响其他设备。"
      >
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
        {revokedCount > 0 && (
          <label className="chk inline-chk">
            <input type="checkbox" checked={showRevoked} onChange={(e) => setShowRevoked(e.target.checked)} />
            显示已吊销（{revokedCount}）
          </label>
        )}
        <button type="button" className="btn primary" onClick={() => setInvite(true)}>
          <UserPlus size={14} /> {isAdmin ? '邀请新设备' : '邀请我的设备'}
        </button>
      </PageHead>

      {devices.isError && <Notice tone="bad">{describeError(devices.error)}</Notice>}
      {devices.isPending ? (
        <Loading />
      ) : rows.length === 0 ? (
        <Empty>
          {all.length === 0 ? (
            <>
              还没有设备。点击“{isAdmin ? '邀请新设备' : '邀请我的设备'}”生成注册码，在要运行客户端的电脑上打开链接或执行命令即可。
            </>
          ) : (
            '没有匹配的设备。'
          )}
        </Empty>
      ) : (
        <div className="tbl">
          <table>
            <thead>
              <tr>
                <th>设备名</th>
                {isAdmin && <th>用户</th>}
                <th>平台</th>
                <th>客户端版本</th>
                <th>最近地址</th>
                <th>隧道</th>
                <th>状态</th>
                <th>最近在线</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {rows.map((d) => (
                <tr key={d.id} className={d.revoked ? 'dim' : undefined}>
                  <td>
                    <b>{d.name}</b>
                  </td>
                  {isAdmin && <td>{d.username}</td>}
                  <td>
                    {d.platform || '—'} · {d.gui ? 'GUI' : '无界面'}
                  </td>
                  <td className="mono">{d.clientVersion || '—'}</td>
                  <td className="mono">{d.lastIp || '—'}</td>
                  <td>{d.tunnelCount}</td>
                  <td>
                    {d.revoked ? (
                      <Tag tone="bad" title={d.revokedAt ? `吊销于 ${dateTime(d.revokedAt)}` : undefined}>
                        已吊销
                      </Tag>
                    ) : d.online ? (
                      <span title={d.connectedAt ? `连接于 ${dateTime(d.connectedAt)}` : undefined}>
                        <Dot tone="ok" />
                        在线
                      </span>
                    ) : (
                      <span>
                        <Dot tone="n" />
                        离线
                      </span>
                    )}
                  </td>
                  <td title={d.lastSeenAt ? dateTime(d.lastSeenAt) : undefined}>
                    {d.online ? '现在' : d.lastSeenAt ? ago(d.lastSeenAt) : <span className="hint">从未连接</span>}
                  </td>
                  <td>
                    {!d.revoked && (
                      <div className="inline">
                        <button type="button" className="btn sm" onClick={() => setRenaming(d)}>
                          重命名
                        </button>
                        <button type="button" className="btn sm danger" onClick={() => setRevoking(d)}>
                          吊销
                        </button>
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <PendingInvitations />

      {invite && <EnrollDialog onClose={() => setInvite(false)} />}
      {renaming && <RenameDialog device={renaming} onClose={() => setRenaming(null)} />}
      {revoking && <RevokeDialog device={revoking} onClose={() => setRevoking(null)} />}
    </>
  )
}

/** Codes that were generated but not used yet; cancelling one makes it unusable at once. */
function PendingInvitations() {
  const { isAdmin } = useSession()
  const enrollments = useQuery({ queryKey: ['enrollments'], queryFn: listEnrollments, refetchInterval: POLL_MS })
  const tunnels = useQuery({ queryKey: ['tunnels'], queryFn: () => listTunnels() })
  const [cancelling, setCancelling] = useState<PendingEnrollment | null>(null)
  const now = Date.now()
  const rows = (enrollments.data?.enrollments ?? []).filter((e) => e.expiresAt > now)
  if (enrollments.isError) return <Notice tone="bad">{describeError(enrollments.error)}</Notice>
  if (rows.length === 0) return null
  const tunnelName = (id: string) => tunnels.data?.tunnels.find((t) => t.id === id)?.name ?? id
  return (
    <section aria-label="未使用的邀请" className="mt-lg">
      <div className="ttl">
        <h3>未使用的邀请</h3>
        <span className="hint">注册码本身不保存在服务端，只能看到用途和有效期。不再需要时可以取消。</span>
      </div>
      <div className="tbl">
        <table>
          <thead>
            <tr>
              <th>设备名（建议）</th>
              {isAdmin && <th>用户</th>}
              <th>预分配隧道</th>
              <th>生成于</th>
              <th>过期</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {rows.map((e) => (
              <tr key={e.id}>
                <td>{e.deviceNameHint || <span className="hint">未填写</span>}</td>
                {isAdmin && <td>{e.username}</td>}
                <td>{e.tunnelIds.length ? e.tunnelIds.map(tunnelName).join('、') : <span className="hint">—</span>}</td>
                <td title={dateTime(e.createdAt)}>{ago(e.createdAt)}</td>
                <td title={dateTime(e.expiresAt)}>{until(e.expiresAt)}</td>
                <td>
                  <button type="button" className="btn sm danger" onClick={() => setCancelling(e)}>
                    取消邀请
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {cancelling && <CancelInvitationDialog enrollment={cancelling} onClose={() => setCancelling(null)} />}
    </section>
  )
}

function CancelInvitationDialog({ enrollment, onClose }: { enrollment: PendingEnrollment; onClose: () => void }) {
  const client = useQueryClient()
  const mutation = useMutation({
    mutationFn: () => cancelEnrollment(enrollment.id),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['enrollments'] })
      onClose()
    },
  })
  return (
    <ConfirmDialog
      title="取消邀请"
      confirmLabel="取消邀请"
      danger
      busy={mutation.isPending}
      error={mutation.isError ? describeError(mutation.error) : null}
      onConfirm={() => mutation.mutate()}
      onClose={onClose}
    >
      确定取消这个注册码{enrollment.deviceNameHint ? `（${enrollment.deviceNameHint}）` : ''}？取消后它立即失效，已经发出的链接和命令都无法再使用。
    </ConfirmDialog>
  )
}

function RenameDialog({ device, onClose }: { device: Device; onClose: () => void }) {
  const client = useQueryClient()
  const [name, setName] = useState(device.name)
  const mutation = useMutation({
    mutationFn: () => renameDevice(device.id, name.trim()),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['devices'] })
      void client.invalidateQueries({ queryKey: ['tunnels'] })
      onClose()
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (name.trim()) mutation.mutate()
  }
  return (
    <Modal title="重命名设备" onClose={onClose}>
      <form onSubmit={submit} className="stack">
        <label className="field">
          设备名
          <input className="inp" value={name} maxLength={64} onChange={(e) => setName(e.target.value)} autoFocus />
        </label>
        {mutation.isError && <Notice tone="bad">{describeError(mutation.error)}</Notice>}
        <div className="actions">
          <button type="button" className="btn" onClick={onClose}>
            取消
          </button>
          <button type="submit" className="btn primary" disabled={!name.trim() || mutation.isPending}>
            保存
          </button>
        </div>
      </form>
    </Modal>
  )
}

function RevokeDialog({ device, onClose }: { device: Device; onClose: () => void }) {
  const client = useQueryClient()
  const mutation = useMutation({
    mutationFn: () => revokeDevice(device.id),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['devices'] })
      void client.invalidateQueries({ queryKey: ['tunnels'] })
      onClose()
    },
  })
  return (
    <ConfirmDialog
      title="吊销设备"
      confirmLabel="吊销"
      danger
      busy={mutation.isPending}
      error={mutation.isError ? describeError(mutation.error) : null}
      onConfirm={() => mutation.mutate()}
      onClose={onClose}
    >
      <p>
        确定吊销设备 <b>{device.name}</b>？
      </p>
      <ul className="hint">
        <li>该设备会立即断开，绑定的 {device.tunnelCount} 条隧道全部下线并解除绑定（变为“未绑定设备”）；</li>
        <li>吊销<b>不可撤销</b>，这台机器需要用新的注册码重新注册；</li>
        <li>解除绑定的隧道可在邀请新设备时预分配，或在隧道编辑里重新绑定。</li>
      </ul>
    </ConfirmDialog>
  )
}
