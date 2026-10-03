import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { FormEvent } from 'react'
import { Download } from 'lucide-react'
import { changePassword, describeError, disableTotp, enableTotp, listSessions, regenerateRecoveryCodes, revokeSession, setupTotp } from '../api'
import { QrCode } from '../components/qr'
import { CopyButton, Empty, Loading, Notice, PageHead, Tag } from '../components/ui'
import { ago, dateTime, downloadText, groupSecret } from '../format'
import { useSession } from '../session'
import type { TotpSetup } from '../types'
import { MIN_PASSWORD } from './auth'

export function SecurityPage() {
  return (
    <>
      <PageHead title="账号安全" />
      <div className="grid2">
        <PasswordCard />
        <TotpCard />
      </div>
      <SessionsCard />
    </>
  )
}

/** "Chrome · Windows" from a User-Agent string; the raw value is kept in the tooltip. */
export function describeUserAgent(ua: string): string {
  if (!ua) return '未知设备'
  const browser = /Edg\//.test(ua)
    ? 'Edge'
    : /OPR\//.test(ua)
      ? 'Opera'
      : /Firefox\//.test(ua)
        ? 'Firefox'
        : /Chrome\//.test(ua)
          ? 'Chrome'
          : /Safari\//.test(ua)
            ? 'Safari'
            : /curl\//i.test(ua)
              ? 'curl'
              : ''
  const os = /Windows/.test(ua)
    ? 'Windows'
    : /iPhone|iPad/.test(ua)
      ? 'iOS'
      : /Android/.test(ua)
        ? 'Android'
        : /Mac OS X|Macintosh/.test(ua)
          ? 'macOS'
          : /Linux/.test(ua)
            ? 'Linux'
            : ''
  return [browser, os].filter(Boolean).join(' · ') || ua.slice(0, 40)
}

function SessionsCard() {
  const client = useQueryClient()
  const sessions = useQuery({ queryKey: ['sessions'], queryFn: listSessions })
  const revoke = useMutation({
    mutationFn: (id: string) => revokeSession(id),
    onSettled: () => client.invalidateQueries({ queryKey: ['sessions'] }),
  })
  const rows = [...(sessions.data?.sessions ?? [])].sort((a, b) => Number(b.current) - Number(a.current) || b.lastUsedAt - a.lastUsedAt)
  return (
    <section className="fs mt" aria-label="登录会话">
      <h4>登录会话</h4>
      <p className="hint">在其他地方登录的会话会显示在这里。看到不认识的设备时请退出它，并尽快修改密码。</p>
      {sessions.isError && <Notice tone="bad">{describeError(sessions.error)}</Notice>}
      {revoke.isError && <Notice tone="bad">{describeError(revoke.error)}</Notice>}
      {sessions.isPending ? (
        <Loading />
      ) : rows.length === 0 ? (
        <Empty>没有登录会话。</Empty>
      ) : (
        <div className="tbl">
          <table>
            <thead>
              <tr>
                <th>设备</th>
                <th>IP</th>
                <th>最近活动</th>
                <th>登录于</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {rows.map((x) => (
                <tr key={x.id}>
                  <td title={x.userAgent}>
                    {describeUserAgent(x.userAgent)} {x.current && <Tag tone="ok">当前</Tag>}
                  </td>
                  <td className="mono">{x.ip || '—'}</td>
                  <td title={dateTime(x.lastUsedAt)}>{x.current ? '现在' : ago(x.lastUsedAt)}</td>
                  <td>{dateTime(x.createdAt)}</td>
                  <td>
                    {!x.current && (
                      <button type="button" className="btn sm danger" disabled={revoke.isPending} onClick={() => revoke.mutate(x.id)}>
                        退出
                      </button>
                    )}
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

function PasswordCard() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [problem, setProblem] = useState<string | null>(null)
  const [done, setDone] = useState(false)

  const mutation = useMutation({
    mutationFn: () => changePassword({ current, new: next }),
    onSuccess: () => {
      setCurrent('')
      setNext('')
      setConfirm('')
      setDone(true)
    },
    onError: (e) => setProblem(describeError(e)),
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setDone(false)
    if (!current) return setProblem('请输入当前密码')
    if ([...next].length < MIN_PASSWORD) return setProblem(`新密码至少需要 ${MIN_PASSWORD} 个字符`)
    if (next !== confirm) return setProblem('两次输入的新密码不一致')
    setProblem(null)
    mutation.mutate()
  }

  return (
    <form className="fs" onSubmit={submit} aria-label="修改密码">
      <h4>修改密码</h4>
      <label className="field">
        当前密码
        <input className="inp" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
      </label>
      <label className="field">
        新密码
        <input className="inp" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
        <span className="hint">至少 {MIN_PASSWORD} 个字符。修改后，你在其他地方的登录会话都会退出。</span>
      </label>
      <label className="field">
        确认新密码
        <input className="inp" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
      </label>
      {problem && <Notice tone="bad">{problem}</Notice>}
      {done && <Notice tone="ok">密码已修改。</Notice>}
      <div>
        <button type="submit" className="btn primary" disabled={mutation.isPending}>
          保存
        </button>
      </div>
    </form>
  )
}

function TotpCard() {
  const client = useQueryClient()
  const { user, bootstrap } = useSession()
  const [pending, setPending] = useState<TotpSetup | null>(null)
  const [code, setCode] = useState('')
  const [password, setPassword] = useState('')
  const [codes, setCodes] = useState<string[] | null>(null)
  const [regenerated, setRegenerated] = useState(false)
  const [problem, setProblem] = useState<string | null>(null)
  const [done, setDone] = useState<string | null>(null)

  const refreshMe = () => client.invalidateQueries({ queryKey: ['me'] })

  const start = useMutation({
    mutationFn: setupTotp,
    onSuccess: (r) => {
      setPending(r)
      setCode('')
      setProblem(null)
      setDone(null)
    },
    onError: (e) => setProblem(describeError(e)),
  })
  const enable = useMutation({
    mutationFn: () => enableTotp({ secret: pending!.secret, code: code.replace(/\s+/g, '') }),
    onSuccess: (r) => {
      setPending(null)
      setCode('')
      setProblem(null)
      // /me is reloaded only once the codes were acknowledged: while TOTP is mandatory, reloading it
      // swaps the whole console in and this card (with the codes) would disappear.
      setCodes(r.recoveryCodes)
    },
    onError: (e) => setProblem(describeError(e)),
  })
  const regenerate = useMutation({
    mutationFn: () => regenerateRecoveryCodes(password),
    onSuccess: (r) => {
      setPassword('')
      setProblem(null)
      setDone(null)
      setRegenerated(true)
      setCodes(r.recoveryCodes)
    },
    onError: (e) => setProblem(describeError(e)),
  })
  const disable = useMutation({
    mutationFn: () => disableTotp(password),
    onSuccess: () => {
      setPassword('')
      setProblem(null)
      setDone('两步验证已关闭。')
      void refreshMe()
    },
    onError: (e) => setProblem(describeError(e)),
  })

  if (codes) {
    const text = codes.join('\n')
    return (
      <div className="fs" aria-label="两步验证">
        <h4>
          两步验证（TOTP） <Tag tone="ok">已开启</Tag>
        </h4>
        <Notice tone="warn">{regenerated ? '旧的恢复码已全部作废。' : ''}下面的恢复码只显示这一次。手机丢失时，每个恢复码可代替验证码登录一次。请离线保存。</Notice>
        <ol className="codes mono" aria-label="恢复码">
          {codes.map((c) => (
            <li key={c}>{c}</li>
          ))}
        </ol>
        <div className="inline wrap">
          <CopyButton text={text} label="复制全部" />
          <button
            type="button"
            className="btn"
            onClick={() => downloadText(`nyatunnel-recovery-codes-${user.username}.txt`, `NyaTunnel 恢复码（${bootstrap.serverName} / ${user.username}）\n每个恢复码只能使用一次。\n\n${text}\n`)}
          >
            <Download size={14} /> 下载 .txt
          </button>
          <button
            type="button"
            className="btn primary"
            onClick={() => {
              setCodes(null)
              setDone(regenerated ? '已生成新的恢复码。' : '两步验证已开启，下次登录需要输入验证码。')
              setRegenerated(false)
              void refreshMe()
            }}
          >
            我已保存
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="fs" aria-label="两步验证">
      <h4>
        两步验证（TOTP）{' '}
        {user.totpEnabled ? <Tag tone="ok">已开启</Tag> : bootstrap.forceTotp ? <Tag tone="warn">管理员要求开启</Tag> : <Tag>未开启</Tag>}
      </h4>
      {user.totpEnabled ? (
        <>
          <p className="hint">
            登录时需要输入身份验证器中的 6 位验证码。剩余恢复码：<b>{user.recoveryCodesLeft ?? '—'}</b> 个
            {user.recoveryCodesLeft === 0 && '（已用完，请重新生成）'}。
          </p>
          {bootstrap.forceTotp && <p className="hint">管理员要求所有账号开启两步验证，因此不能关闭。手机丢失时请使用恢复码登录，或请管理员重置。</p>}
          <form className="stack" onSubmit={(e) => e.preventDefault()}>
            <label className="field">
              当前密码
              <input className="inp" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
              <span className="hint">重新生成恢复码或关闭两步验证都需要确认密码。</span>
            </label>
            <div className="inline wrap">
              <button
                type="button"
                className="btn"
                disabled={regenerate.isPending}
                onClick={() => {
                  if (!password) return setProblem('请输入当前密码')
                  regenerate.mutate()
                }}
              >
                重新生成恢复码
              </button>
              {!bootstrap.forceTotp && (
                <button
                  type="button"
                  className="btn danger"
                  disabled={disable.isPending}
                  onClick={() => {
                    if (!password) return setProblem('请输入当前密码')
                    disable.mutate()
                  }}
                >
                  关闭两步验证
                </button>
              )}
            </div>
          </form>
        </>
      ) : pending ? (
        <form
          className="stack"
          onSubmit={(e) => {
            e.preventDefault()
            if (!/^\d{6}$/.test(code.replace(/\s+/g, ''))) return setProblem('请输入 6 位数字验证码')
            enable.mutate()
          }}
        >
          <div className="qr-row">
            <QrCode text={pending.otpauthUrl} label="两步验证二维码" size={140} />
            <div className="hint grow">
              用 Google Authenticator、1Password、Bitwarden 等应用扫码；无法扫码时手动输入密钥：
              <div className="secret mono">{groupSecret(pending.secret)}</div>
              <CopyButton text={pending.secret} label="复制密钥" />
            </div>
          </div>
          <label className="field">
            6 位验证码
            <input
              className="inp mono narrow"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={7}
              placeholder="123456"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              autoFocus
            />
          </label>
          <div className="inline wrap">
            <button type="submit" className="btn primary" disabled={enable.isPending}>
              启用
            </button>
            <button type="button" className="btn" onClick={() => setPending(null)}>
              取消
            </button>
          </div>
          <span className="hint">启用后会生成 10 个一次性恢复码，请离线保存。</span>
        </form>
      ) : (
        <>
          <p className="hint">开启后，登录除了密码还需要身份验证器应用中的 6 位验证码，即使密码泄露也无法登录。</p>
          <div>
            <button type="button" className="btn primary" onClick={() => start.mutate()} disabled={start.isPending}>
              开始设置
            </button>
          </div>
        </>
      )}
      {problem && <Notice tone="bad">{problem}</Notice>}
      {done && <Notice tone="ok">{done}</Notice>}
    </div>
  )
}
