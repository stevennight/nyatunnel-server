import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { FormEvent } from 'react'
import { Plus } from 'lucide-react'
import { createUser, describeError, listUsers, resetUserTotp, updateUser } from '../api'
import { ConfirmDialog, Dot, Empty, Loading, Modal, Notice, PageHead, Tag } from '../components/ui'
import { date } from '../format'
import { useSession } from '../session'
import type { AdminUser, Role } from '../types'
import { MIN_PASSWORD, USERNAME_RE } from './auth'

export function UsersPage() {
  const { user: me, bootstrap } = useSession()
  const users = useQuery({ queryKey: ['users'], queryFn: listUsers })
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<AdminUser | null>(null)
  const [resetting, setResetting] = useState<AdminUser | null>(null)

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
                  <td>{u.tunnelCount}</td>
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
    </>
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
            <option value="user">普通用户（只能查看自己的隧道，邀请自己的设备）</option>
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
