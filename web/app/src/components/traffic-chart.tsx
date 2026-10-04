import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { describeError, getTraffic } from '../api'
import { bytes, date, dateTime } from '../format'
import type { TrafficPoint, Tunnel } from '../types'
import { Loading, Modal, Notice } from './ui'

const HOUR = 3_600_000

type Bucket = { start: number; bytesIn: number; bytesOut: number; conns: number }

/**
 * Spreads a sparse hourly series over fixed buckets ending now: hourly up to two days (the last
 * bucket is the current hour), local calendar days beyond that (the last bucket is today).
 */
export function bucketize(series: TrafficPoint[], hours: number, now = Date.now()): Bucket[] {
  let starts: number[]
  if (hours > 48) {
    const today = new Date(now)
    const days = Math.ceil(hours / 24)
    // Built from the calendar rather than 24-hour steps so a DST change cannot shift the days.
    starts = Array.from({ length: days }, (_, i) => new Date(today.getFullYear(), today.getMonth(), today.getDate() - (days - 1 - i)).getTime())
  } else {
    const last = Math.floor(now / HOUR) * HOUR // start of the current hour
    const count = Math.max(1, hours)
    starts = Array.from({ length: count }, (_, i) => last - (count - 1 - i) * HOUR)
  }
  const out: Bucket[] = starts.map((start) => ({ start, bytesIn: 0, bytesOut: 0, conns: 0 }))
  for (const p of series) {
    if (p.hour < starts[0]) continue
    let i = starts.length - 1
    while (starts[i] > p.hour) i--
    out[i].bytesIn += p.bytesIn
    out[i].bytesOut += p.bytesOut
    out[i].conns += p.conns
  }
  return out
}

function hourLabel(ms: number): string {
  const d = new Date(ms)
  return `${String(d.getHours()).padStart(2, '0')}:00`
}

/** A small stacked bar chart (in + out) drawn as inline SVG. */
export function TrafficChart({ series, hours, label = '流量图' }: { series: TrafficPoint[]; hours: number; label?: string }) {
  const buckets = bucketize(series, hours)
  const daily = hours > 48
  const max = Math.max(1, ...buckets.map((b) => b.bytesIn + b.bytesOut))
  const total = buckets.reduce((n, b) => n + b.bytesIn + b.bytesOut, 0)
  const W = 600
  const H = 120
  const gap = buckets.length > 60 ? 1 : 2
  const bw = W / buckets.length
  const y = (v: number) => (v / max) * (H - 4)

  return (
    <figure className="chart">
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={`${label}：合计 ${bytes(total)}，峰值 ${bytes(max === 1 && total === 0 ? 0 : max)}`}>
        <line x1="0" y1={H - 0.5} x2={W} y2={H - 0.5} className="axis" />
        {buckets.map((b, i) => {
          const hIn = y(b.bytesIn)
          const hOut = y(b.bytesOut)
          const x = i * bw + gap / 2
          const w = Math.max(0.5, bw - gap)
          return (
            <g key={b.start}>
              <title>
                {(daily ? date(b.start) : dateTime(b.start)) + `\n入站 ${bytes(b.bytesIn)} · 出站 ${bytes(b.bytesOut)} · 连接 ${b.conns}`}
              </title>
              {/* a transparent full-height bar keeps the tooltip easy to hit */}
              <rect x={x} y={0} width={w} height={H} className="hit" />
              {hIn > 0 && <rect x={x} y={H - hIn} width={w} height={hIn} className="in" />}
              {hOut > 0 && <rect x={x} y={H - hIn - hOut} width={w} height={hOut} className="out" />}
            </g>
          )
        })}
      </svg>
      <figcaption className="chart-foot hint">
        <span>{daily ? date(buckets[0].start) : hourLabel(buckets[0].start)}</span>
        <span className="legend">
          <i className="in" /> 入站 <i className="out" /> 出站
          <span className="legend-peak">
            单{daily ? '日' : '小时'}峰值（入 + 出）{total > 0 ? bytes(max) : '0 B'}
          </span>
        </span>
        <span>{daily ? date(buckets[buckets.length - 1].start) : '现在'}</span>
      </figcaption>
    </figure>
  )
}

export const TRAFFIC_RANGES = [
  { hours: 24, label: '24 小时' },
  { hours: 168, label: '7 天' },
  { hours: 720, label: '30 天' },
]

/** Traffic of one tunnel over 24 h / 7 d / 30 d. */
export function TrafficDialog({ tunnel, onClose }: { tunnel: Tunnel; onClose: () => void }) {
  const [hours, setHours] = useState(24)
  const traffic = useQuery({
    queryKey: ['traffic', tunnel.id, hours],
    queryFn: () => getTraffic({ tunnelId: tunnel.id, hours }),
    placeholderData: keepPreviousData, // keep the old chart while another range loads
  })
  // Sum what the chart shows, so 合计 always matches the bars.
  const buckets = traffic.data ? bucketize(traffic.data.series, hours) : []
  const total = buckets.reduce((n, b) => n + b.bytesIn + b.bytesOut, 0)
  const conns = buckets.reduce((n, b) => n + b.conns, 0)
  return (
    <Modal title={`流量 · ${tunnel.name}`} onClose={onClose} wide>
      <div className="inline wrap mb">
        <div className="seg" role="group" aria-label="时间范围">
          {TRAFFIC_RANGES.map((r) => (
            <button key={r.hours} type="button" className={hours === r.hours ? 'on' : undefined} aria-pressed={hours === r.hours} onClick={() => setHours(r.hours)}>
              {r.label}
            </button>
          ))}
        </div>
        <span className="grow" />
        <span className="hint">
          合计 <b>{bytes(total)}</b> · 连接 {conns} · 本月 {bytes(tunnel.monthBytes)}
        </span>
      </div>
      {traffic.isError && <Notice tone="bad">{describeError(traffic.error)}</Notice>}
      {traffic.isPending ? <Loading /> : traffic.data && <TrafficChart series={traffic.data.series} hours={hours} label={`${tunnel.name} 流量`} />}
      <div className="actions">
        <button type="button" className="btn" onClick={onClose}>
          关闭
        </button>
      </div>
    </Modal>
  )
}
