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
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    if (settings.data) {
      setServerName(settings.data.serverName)
      setForceTotp(settings.data.forceTotp)
    }
  }, [settings.data])

  const save = useMutation({
    mutationFn: () => putSettings({ serverName: serverName.trim(), forceTotp }),
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
    save.mutate()
  }

  if (settings.isPending) return <Loading />

  return (
    <>
      <PageHead title="系统设置" hint={`服务端版本 ${bootstrap.version} · 公网地址 ${bootstrap.publicUrl || '未配置'}`} />
      {settings.isError && <Notice tone="bad">{describeError(settings.error)}</Notice>}
      <form className="fs form" onSubmit={submit}>
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
        {saved && <Notice tone="ok">已保存。</Notice>}
        <div className="mt">
          <button type="submit" className="btn primary" disabled={!serverName.trim() || save.isPending}>
            保存
          </button>
        </div>
      </form>
    </>
  )
}
