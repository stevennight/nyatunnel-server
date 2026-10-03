import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { FormEvent } from 'react'
import { Plus, Send } from 'lucide-react'
import { createChannel, deleteChannel, describeError, listChannels, testChannel, updateChannel } from '../api'
import { ConfirmDialog, Empty, Loading, Modal, Notice, PageHead, Tag } from '../components/ui'
import { ago, dateTime } from '../format'
import { channelEvents } from '../labels'
import type { Channel, ChannelConfig, ChannelInput, ChannelKind } from '../types'

const kindLabel: Record<ChannelKind, string> = { webhook: 'Webhook', telegram: 'Telegram' }

export function ChannelsPage() {
  const channels = useQuery({ queryKey: ['channels'], queryFn: listChannels })
  const [editing, setEditing] = useState<Channel | 'new' | null>(null)
  const [deleting, setDeleting] = useState<Channel | null>(null)
  const [tested, setTested] = useState<{ id: string; ok: boolean; text: string } | null>(null)
  const client = useQueryClient()
  const events = channels.data?.events ?? Object.keys(channelEvents)

  const test = useMutation({
    mutationFn: (c: Channel) => testChannel(c.id),
    onSuccess: (_, c) => setTested({ id: c.id, ok: true, text: `已向“${c.name}”发送测试消息。` }),
    onError: (e, c) => setTested({ id: c.id, ok: false, text: `“${c.name}”发送失败：${describeError(e)}` }),
    onSettled: () => client.invalidateQueries({ queryKey: ['channels'] }),
  })

  return (
    <>
      <PageHead title="通知渠道" hint="新申请、新设备、流量配额、登录爆破等事件可以推送到 Webhook 或 Telegram。渠道密钥只写不读。">
        <button type="button" className="btn primary" onClick={() => setEditing('new')}>
          <Plus size={14} /> 添加渠道
        </button>
      </PageHead>

      {channels.isError && <Notice tone="bad">{describeError(channels.error)}</Notice>}
      {tested && <Notice tone={tested.ok ? 'ok' : 'bad'}>{tested.text}</Notice>}
      {channels.isPending ? (
        <Loading />
      ) : !channels.data || channels.data.channels.length === 0 ? (
        <Empty>还没有通知渠道。添加后，有新申请或异常时会第一时间通知你。</Empty>
      ) : (
        <div className="tbl">
          <table>
            <thead>
              <tr>
                <th>名称</th>
                <th>类型</th>
                <th>目标</th>
                <th>事件</th>
                <th>状态</th>
                <th>最近发送</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {channels.data.channels.map((c) => (
                <tr key={c.id} className={c.enabled ? undefined : 'dim'}>
                  <td>
                    <b>{c.name}</b>
                  </td>
                  <td>{kindLabel[c.kind] ?? c.kind}</td>
                  <td className="mono">{c.target || '—'}</td>
                  <td className="clip wide" title={c.events.map((e) => channelEvents[e] ?? e).join('、')}>
                    {c.events.length === 0 ? <span className="hint">未订阅</span> : c.events.map((e) => channelEvents[e] ?? e).join('、')}
                  </td>
                  <td>
                    {!c.enabled ? (
                      <Tag>已停用</Tag>
                    ) : c.lastError ? (
                      <Tag tone="bad" title={c.lastError}>
                        出错
                      </Tag>
                    ) : (
                      <Tag tone="ok">正常</Tag>
                    )}
                    {c.lastError && (
                      <div className="hint bad-text clip" title={c.lastError}>
                        {c.lastError}
                      </div>
                    )}
                  </td>
                  <td title={c.lastSent ? dateTime(c.lastSent) : undefined}>{c.lastSent ? ago(c.lastSent) : <span className="hint">从未</span>}</td>
                  <td>
                    <div className="inline">
                      <button
                        type="button"
                        className="btn sm"
                        disabled={test.isPending}
                        onClick={() => {
                          setTested(null)
                          test.mutate(c)
                        }}
                        aria-label={`测试 ${c.name}`}
                      >
                        <Send size={12} /> 测试
                      </button>
                      <button type="button" className="btn sm" onClick={() => setEditing(c)}>
                        编辑
                      </button>
                      <button type="button" className="btn sm danger" onClick={() => setDeleting(c)}>
                        删除
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {editing && <ChannelForm channel={editing === 'new' ? null : editing} events={events} onClose={() => setEditing(null)} />}
      {deleting && <DeleteChannelDialog channel={deleting} onClose={() => setDeleting(null)} />}
    </>
  )
}

/** Create / edit a channel. On edit the secrets stay untouched unless “修改配置” is used. */
export function ChannelForm({ channel, events, onClose }: { channel: Channel | null; events: string[]; onClose: () => void }) {
  const client = useQueryClient()
  const [kind, setKind] = useState<ChannelKind>(channel?.kind ?? 'webhook')
  const [name, setName] = useState(channel?.name ?? '')
  const [selected, setSelected] = useState<string[]>(channel?.events ?? events)
  const [enabled, setEnabled] = useState(channel?.enabled ?? true)
  const [editConfig, setEditConfig] = useState(channel === null)
  const [cfg, setCfg] = useState<ChannelConfig>({})
  const [problem, setProblem] = useState<string | null>(null)
  const setC = (k: keyof ChannelConfig, v: string) => setCfg((s) => ({ ...s, [k]: v }))

  const mutation = useMutation({
    mutationFn: (body: ChannelInput) => (channel ? updateChannel(channel.id, body) : createChannel(body)),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['channels'] })
      onClose()
    },
    onError: (e) => setProblem(describeError(e)),
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!name.trim()) return setProblem('请填写名称')
    const body: ChannelInput = { kind, name: name.trim(), events: events.filter((x) => selected.includes(x)), enabled }
    if (editConfig) {
      const c: ChannelConfig = {}
      if (kind === 'webhook') {
        const url = (cfg.url ?? '').trim()
        if (!/^https?:\/\/\S+$/i.test(url)) return setProblem('Webhook 地址必须是 http(s) URL')
        c.url = url
        if (cfg.secret?.trim()) c.secret = cfg.secret.trim()
      } else {
        if (!cfg.botToken?.trim() || !cfg.chatId?.trim()) return setProblem('请填写 Bot Token 和 Chat ID')
        c.botToken = cfg.botToken.trim()
        c.chatId = cfg.chatId.trim()
        if (cfg.apiBase?.trim()) c.apiBase = cfg.apiBase.trim()
      }
      body.config = c
    }
    setProblem(null)
    mutation.mutate(body)
  }

  return (
    <Modal title={channel ? `编辑渠道 · ${channel.name}` : '添加通知渠道'} onClose={onClose}>
      <form className="form" onSubmit={submit} aria-label="通知渠道表单">
        <div className="row">
          <label htmlFor="ch-kind">类型</label>
          <select id="ch-kind" className="inp" value={kind} disabled={channel !== null} onChange={(e) => setKind(e.target.value as ChannelKind)}>
            <option value="webhook">Webhook（POST JSON）</option>
            <option value="telegram">Telegram 机器人</option>
          </select>
        </div>
        <div className="row">
          <label htmlFor="ch-name">名称</label>
          <input id="ch-name" className="inp" maxLength={64} value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 运维群" />
        </div>

        <div className="fs">
          <h4>
            配置 <Tag tone="b">只写</Tag>
          </h4>
          {!editConfig ? (
            <div className="inline wrap">
              <span className="hint">
                已保存{channel?.target ? `（${channel.target}）` : ''}。密钥不会显示；不修改则保持原样。
              </span>
              <button type="button" className="btn sm" onClick={() => setEditConfig(true)}>
                修改配置
              </button>
            </div>
          ) : kind === 'webhook' ? (
            <>
              <div className="row">
                <label htmlFor="ch-url">Webhook 地址</label>
                <input id="ch-url" className="inp mono" value={cfg.url ?? ''} onChange={(e) => setC('url', e.target.value)} placeholder="https://example.com/hook" />
              </div>
              <div className="row">
                <label htmlFor="ch-secret">签名密钥</label>
                <div>
                  <input id="ch-secret" className="inp mono" type="password" autoComplete="off" value={cfg.secret ?? ''} onChange={(e) => setC('secret', e.target.value)} placeholder="可选" />
                  <div className="hint">设置后请求带 X-NyaTunnel-Signature: sha256=&lt;HMAC&gt;。</div>
                </div>
              </div>
            </>
          ) : (
            <>
              <div className="row">
                <label htmlFor="ch-token">Bot Token</label>
                <input id="ch-token" className="inp mono" type="password" autoComplete="off" value={cfg.botToken ?? ''} onChange={(e) => setC('botToken', e.target.value)} />
              </div>
              <div className="row">
                <label htmlFor="ch-chat">Chat ID</label>
                <input id="ch-chat" className="inp mono" value={cfg.chatId ?? ''} onChange={(e) => setC('chatId', e.target.value)} placeholder="例如 -1001234567890" />
              </div>
              <div className="row">
                <label htmlFor="ch-api">API 地址</label>
                <div>
                  <input id="ch-api" className="inp mono" value={cfg.apiBase ?? ''} onChange={(e) => setC('apiBase', e.target.value)} placeholder="https://api.telegram.org" />
                  <div className="hint">可选；Telegram 被屏蔽时可填反向代理地址。</div>
                </div>
              </div>
            </>
          )}
          {editConfig && channel && (
            <button type="button" className="link" onClick={() => setEditConfig(false)}>
              不修改配置
            </button>
          )}
        </div>

        <div className="fs" role="group" aria-label="订阅事件">
          <h4>订阅事件</h4>
          <div className="inline wrap">
            {events.map((ev) => (
              <label key={ev} className="chk inline-chk">
                <input type="checkbox" checked={selected.includes(ev)} onChange={(e) => setSelected((s) => (e.target.checked ? [...s, ev] : s.filter((x) => x !== ev)))} />
                {channelEvents[ev] ?? ev}
              </label>
            ))}
          </div>
        </div>
        <label className="chk">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          启用
        </label>
        {problem && <Notice tone="bad">{problem}</Notice>}
        <div className="actions">
          <button type="button" className="btn" onClick={onClose}>
            取消
          </button>
          <button type="submit" className="btn primary" disabled={mutation.isPending}>
            保存
          </button>
        </div>
      </form>
    </Modal>
  )
}

function DeleteChannelDialog({ channel, onClose }: { channel: Channel; onClose: () => void }) {
  const client = useQueryClient()
  const mutation = useMutation({
    mutationFn: () => deleteChannel(channel.id),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['channels'] })
      onClose()
    },
  })
  return (
    <ConfirmDialog
      title="删除通知渠道"
      confirmLabel="删除"
      danger
      busy={mutation.isPending}
      error={mutation.isError ? describeError(mutation.error) : null}
      onConfirm={() => mutation.mutate()}
      onClose={onClose}
    >
      确定删除通知渠道 <b>{channel.name}</b>？
    </ConfirmDialog>
  )
}
