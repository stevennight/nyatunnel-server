import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { FormEvent, ReactNode } from 'react'
import { describeError, isApiError, login, setup } from '../api'
import type { LoginInput } from '../api'
import { Notice } from '../components/ui'

export const MIN_PASSWORD = 10
export const USERNAME_RE = /^[a-z0-9][a-z0-9_.-]{1,31}$/

function AuthCard({ title, hint, serverName, children }: { title: string; hint: ReactNode; serverName?: string; children: ReactNode }) {
  return (
    <div className="auth-wrap">
      <div className="auth-card">
        <div className="logo">
          <i />
          <span>{serverName || 'NyaTunnel'}</span>
        </div>
        <h1>{title}</h1>
        <p className="hint">{hint}</p>
        {children}
      </div>
    </div>
  )
}

/** First run: create the administrator. Needs the setup token printed in the server log. */
export function SetupPage() {
  const client = useQueryClient()
  const [token, setToken] = useState('')
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [problem, setProblem] = useState<string | null>(null)

  const mutation = useMutation({
    mutationFn: () => setup({ token: token.trim(), username: username.trim().toLowerCase(), password }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['bootstrap'] }),
    onError: (err) => setProblem(describeError(err)),
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!token.trim()) return setProblem('请填写初始化令牌')
    if (!USERNAME_RE.test(username.trim().toLowerCase())) return setProblem('用户名为 2–32 位小写字母、数字或 _ . -')
    if ([...password].length < MIN_PASSWORD) return setProblem(`密码至少需要 ${MIN_PASSWORD} 个字符`)
    if (password !== confirm) return setProblem('两次输入的密码不一致')
    setProblem(null)
    mutation.mutate()
  }

  return (
    <AuthCard
      title="初始化服务器"
      hint={
        <>
          创建第一个管理员账号。为防止别人抢先初始化，需要填写服务端启动时打印在日志里的<b>初始化令牌</b>（日志中搜索 “setup token”，
          Docker 部署可用 <code>docker logs</code> 查看）。
        </>
      }
    >
      <form onSubmit={submit} className="stack">
        <label className="field">
          初始化令牌
          <input className="inp mono" value={token} onChange={(e) => setToken(e.target.value)} autoComplete="off" autoFocus />
        </label>
        <label className="field">
          管理员用户名
          <input className="inp" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" />
          <span className="hint">2–32 位小写字母、数字或 _ . -</span>
        </label>
        <label className="field">
          密码
          <input className="inp" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
          <span className="hint">至少 {MIN_PASSWORD} 个字符</span>
        </label>
        <label className="field">
          确认密码
          <input className="inp" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </label>
        {problem && <Notice tone="bad">{problem}</Notice>}
        <button type="submit" className="btn primary block" disabled={mutation.isPending}>
          创建管理员并登录
        </button>
      </form>
    </AuthCard>
  )
}

export function LoginPage({ serverName }: { serverName: string }) {
  const client = useQueryClient()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [needCode, setNeedCode] = useState(false)
  const [useRecovery, setUseRecovery] = useState(false)
  const [code, setCode] = useState('')
  const [problem, setProblem] = useState<string | null>(null)

  const mutation = useMutation({
    mutationFn: () => {
      const body: LoginInput = { username: username.trim().toLowerCase(), password }
      if (needCode) {
        if (useRecovery) body.recoveryCode = code.trim()
        else body.totp = code.replace(/\s+/g, '')
      }
      return login(body)
    },
    onSuccess: () => client.invalidateQueries({ queryKey: ['bootstrap'] }),
    onError: (err) => {
      if (isApiError(err, 'totp_required')) {
        // Password was right; the account has a second factor.
        setNeedCode(true)
        setProblem(null)
        return
      }
      setProblem(describeError(err))
    },
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!username.trim() || !password) return setProblem('请输入用户名和密码')
    if (needCode && !code.trim()) return setProblem(useRecovery ? '请输入恢复码' : '请输入 6 位验证码')
    setProblem(null)
    mutation.mutate()
  }

  return (
    <AuthCard title="登录" hint="登录 NyaTunnel 管理后台。" serverName={serverName}>
      <form onSubmit={submit} className="stack">
        <label className="field">
          用户名
          <input className="inp" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus={!needCode} readOnly={needCode} />
        </label>
        <label className="field">
          密码
          <input className="inp" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} readOnly={needCode} />
        </label>
        {needCode && (
          <>
            {useRecovery ? (
              <label className="field">
                恢复码
                <input className="inp mono" autoComplete="off" value={code} onChange={(e) => setCode(e.target.value)} autoFocus />
                <span className="hint">每个恢复码只能使用一次。</span>
              </label>
            ) : (
              <label className="field">
                两步验证码
                <input
                  className="inp mono"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  maxLength={7}
                  placeholder="6 位数字"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  autoFocus
                />
                <span className="hint">打开身份验证器应用，输入当前显示的 6 位数字。</span>
              </label>
            )}
            <button
              type="button"
              className="link"
              onClick={() => {
                setUseRecovery(!useRecovery)
                setCode('')
                setProblem(null)
              }}
            >
              {useRecovery ? '使用验证器应用' : '手机丢了？使用恢复码'}
            </button>
          </>
        )}
        {problem && <Notice tone="bad">{problem}</Notice>}
        <button type="submit" className="btn primary block" disabled={mutation.isPending}>
          {needCode ? '验证并登录' : '登录'}
        </button>
        {needCode && (
          <button
            type="button"
            className="link"
            onClick={() => {
              setNeedCode(false)
              setCode('')
              setPassword('')
              setProblem(null)
            }}
          >
            换一个账号
          </button>
        )}
      </form>
    </AuthCard>
  )
}
