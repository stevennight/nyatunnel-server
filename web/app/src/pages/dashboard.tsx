import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { describeError, getDashboard, listRequests } from '../api'
import { TrafficChart } from '../components/traffic-chart'
import { Empty, Loading, Notice, PageHead } from '../components/ui'
import { bytes } from '../format'
import { POLL_MS } from '../query'
import { useSession } from '../session'
import { AuditRow } from './audit'

export function DashboardPage() {
  const { bootstrap } = useSession()
  const dash = useQuery({ queryKey: ['dashboard'], queryFn: getDashboard, refetchInterval: POLL_MS })
  const requests = useQuery({ queryKey: ['requests'], queryFn: listRequests, refetchInterval: POLL_MS })
  const pending = requests.data?.pending ?? 0
  const d = dash.data

  return (
    <>
      <PageHead title="仪表盘" hint={`${bootstrap.publicUrl ? bootstrap.publicUrl.replace(/^https?:\/\//, '') + ' · ' : ''}${bootstrap.version}`} />
      {dash.isError && <Notice tone="bad">{describeError(dash.error)}</Notice>}
      {!d ? (
        dash.isPending && <Loading />
      ) : (
        <>
          <div className="cards">
            <div className="card">
              <div className="k">在线设备</div>
              <div className="v">
                {d.devicesOnline} <small>/ {d.devices}</small>
              </div>
              <div className="s">{d.devices - d.devicesOnline > 0 ? `${d.devices - d.devicesOnline} 台离线` : d.devices ? '全部在线' : '还没有设备'}</div>
            </div>
            <div className="card">
              <div className="k">运行中隧道</div>
              <div className="v">
                {d.tunnelsRunning} <small>/ {d.tunnels}</small>
              </div>
              <div className="s">
                HTTPS {d.tunnelsByType.https ?? 0} · TCP {d.tunnelsByType.tcp ?? 0} · UDP {d.tunnelsByType.udp ?? 0} · TCP+UDP {d.tunnelsByType.tcpudp ?? 0}
              </div>
            </div>
            <div className="card">
              <div className="k">用户</div>
              <div className="v">{d.users}</div>
              <div className="s">已开放公网端口 {d.openPorts} 个</div>
            </div>
            <div className="card">
              <div className="k">24 小时内失败与拒绝</div>
              <div className={`v${d.denied24h > 0 ? ' bad-text' : ''}`}>{d.denied24h}</div>
              <div className="s">登录失败、设备认证失败、无效注册码等</div>
            </div>
          </div>

          <div className="cards three">
            <div className="card">
              <div className="k">24 小时流量</div>
              <div className="v">{bytes(d.traffic24h ?? 0)}</div>
              <div className="s">所有隧道入站 + 出站</div>
            </div>
            <div className="card">
              <div className="k">本月流量</div>
              <div className="v">{bytes(d.trafficMonth ?? 0)}</div>
              <div className="s">按自然月（UTC）统计</div>
            </div>
            <Link to="/requests" className="card link-card" aria-label={`待审批申请 ${pending}`}>
              <div className="k">待审批申请</div>
              <div className={`v${pending > 0 ? ' warn-text' : ''}`}>{pending}</div>
              <div className="s">{pending > 0 ? '点击前往审批 →' : '没有等待处理的申请'}</div>
            </Link>
          </div>

          <div className="card">
            <div className="ttl tight">
              <h3>最近 24 小时流量</h3>
              <span className="hint">合计 {bytes(d.traffic24h ?? 0)}</span>
            </div>
            <TrafficChart series={d.trafficSeries ?? []} hours={24} label="最近 24 小时流量" />
          </div>

          <div className="ttl">
            <h3>需要关注</h3>
            <Link to="/audit" className="hint">
              查看全部审计日志 →
            </Link>
          </div>
          {d.recentDenied.length === 0 ? (
            <Empty>最近 24 小时没有失败或被拒绝的请求。</Empty>
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
                  {d.recentDenied.map((e) => (
                    <AuditRow key={e.id} e={e} />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </>
  )
}
