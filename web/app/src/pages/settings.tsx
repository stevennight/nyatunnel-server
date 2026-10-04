import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { Link } from '@tanstack/react-router'
import { describeError, getSettings, isApiError, putSettings } from '../api'
import { Loading, Notice, PageHead } from '../components/ui'
import { useSession } from '../session'

export function SettingsPage() {
  const client = useQueryClient()
  const { user, bootstrap } = useSession()
  const settings = useQuery({ queryKey: ['settings'], queryFn: getSettings })
  const [serverName, setServerName] = useState('')
  const [forceTotp, setForceTotp] = useState(false)
  const [minClient, setMinClient] = useState('')
  const [surge, setSurge] = useState('')
  const [saved, setSaved] = useState(false)
  const [problem, setProblem] = useState<string | null>(null)

  useEffect(() => {
    if (settings.data) {
      setServerName(settings.data.serverName)
      setForceTotp(settings.data.forceTotp)
      setMinClient(settings.data.minClientVersion ?? '')
      setSurge(settings.data.surgeMbPerHour > 0 ? String(settings.data.surgeMbPerHour) : '')
    }
  }, [settings.data])

  const save = useMutation({
    mutationFn: () =>
      putSettings({
        serverName: serverName.trim(),
        forceTotp,
        minClientVersion: minClient.trim().replace(/^v/, ''),
        surgeMbPerHour: surge.trim() ? Number(surge) : 0,
      }),
    onSuccess: (s) => {
      client.setQueryData(['settings'], s)
      setSaved(true)
      void client.invalidateQueries({ queryKey: ['bootstrap'] })
      void client.invalidateQueries({ queryKey: ['users'] })
    },
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setSaved(false)
    const v = minClient.trim().replace(/^v/, '')
    if (v && !/^\d+\.\d+\.\d+$/.test(v)) return setProblem('最低客户端版本应形如 0.2.0，留空表示不限制')
    if (surge.trim() && !(Number.isInteger(Number(surge)) && Number(surge) >= 0)) return setProblem('流量突增阈值必须是非负整数（MB），0 或留空表示关闭')
    setProblem(null)
    save.mutate()
  }

  if (settings.isPending) return <Loading />

  return (
    <>
      <PageHead title="系统设置" hint={`服务端版本 ${bootstrap.version} · 公网地址 ${bootstrap.publicUrl || '未配置'}`} />
      {settings.isError ? (
        // Never offer the form with blank values: saving it would overwrite the real settings.
        <>
          <Notice tone="bad">{describeError(settings.error)}</Notice>
          <button type="button" className="btn" onClick={() => settings.refetch()} disabled={settings.isFetching}>
            重试
          </button>
        </>
      ) : (
        <form className="fs form" onSubmit={submit} onChange={() => setSaved(false)}>
          <h4>常规</h4>
          <div className="row">
            <label htmlFor="set-name">服务器名称</label>
            <div>
              <input id="set-name" className="inp" maxLength={64} value={serverName} onChange={(e) => setServerName(e.target.value)} />
              <div className="hint">显示在后台顶部，以及客户端注册时的确认对话框中。</div>
            </div>
          </div>
          <h4>安全</h4>
          <label className="chk">
            <input type="checkbox" checked={forceTotp} onChange={(e) => setForceTotp(e.target.checked)} />
            强制所有账号开启两步验证（TOTP）
          </label>
          <div className="hint">开启后，尚未设置两步验证的账号登录后只能进入“账号安全”页面完成设置。</div>
          <div className="row">
            <label htmlFor="set-min-client">最低客户端版本</label>
            <div>
              <input id="set-min-client" className="inp mono narrow" value={minClient} onChange={(e) => setMinClient(e.target.value)} placeholder="不限制" />
              <div className="hint">例如 0.2.0。低于该版本的客户端连接时会被拒绝并提示升级；留空表示不限制。</div>
            </div>
          </div>
          <h4>告警</h4>
          <div className="row">
            <label htmlFor="set-surge">流量突增阈值</label>
            <div>
              <div className="inline">
                <input id="set-surge" className="inp mono narrow" inputMode="numeric" value={surge} onChange={(e) => setSurge(e.target.value)} placeholder="0" />
                <span>MB / 小时</span>
              </div>
              <div className="hint">单条隧道一小时内的流量超过该值时发送“流量突增”通知；0 或留空表示关闭。</div>
            </div>
          </div>
          {forceTotp && !user.totpEnabled && (
            <Notice tone="warn">
              你自己还没有开启两步验证，需要先在 <Link to="/security">账号安全</Link> 中开启。
            </Notice>
          )}
          {save.isError && (
            <Notice tone="bad">
              {describeError(save.error)}
              {isApiError(save.error, 'enable_own_totp_first') && (
                <>
                  {' '}
                  <Link to="/security">去开启</Link>
                </>
              )}
            </Notice>
          )}
          {problem && <Notice tone="bad">{problem}</Notice>}
          {saved && <Notice tone="ok">已保存。</Notice>}
          <div className="mt">
            <button type="submit" className="btn primary" disabled={!serverName.trim() || save.isPending}>
              保存
            </button>
          </div>
        </form>
      )}
    </>
  )
}
