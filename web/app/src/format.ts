const pad = (n: number) => String(n).padStart(2, '0')

/** "刚刚", "12 分钟前", "3 小时前", "5 天前". */
export function ago(ms: number, now = Date.now()): string {
  const minutes = Math.round((now - ms) / 60_000)
  if (minutes < 1) return '刚刚'
  if (minutes < 60) return `${minutes} 分钟前`
  if (minutes < 1440) return `${Math.floor(minutes / 60)} 小时前`
  if (minutes < 1440 * 30) return `${Math.floor(minutes / 1440)} 天前`
  return date(ms)
}

/** "3 小时后", "5 天后"; for a time in the past "已过期". */
export function until(ms: number, now = Date.now()): string {
  const minutes = Math.round((ms - now) / 60_000)
  if (minutes <= 0) return '已过期'
  if (minutes < 60) return `${minutes} 分钟后`
  if (minutes < 1440) return `${Math.floor(minutes / 60)} 小时后`
  return `${Math.floor(minutes / 1440)} 天后`
}

/** 2026-10-03 */
export function date(ms: number): string {
  const d = new Date(ms)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** 2026-10-03 20:15 */
export function dateTime(ms: number): string {
  const d = new Date(ms)
  return `${date(ms)} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** mm:ss for a countdown; "00:00" once it ran out. */
export function countdown(msLeft: number): string {
  const s = Math.max(0, Math.floor(msLeft / 1000))
  const h = Math.floor(s / 3600)
  const mm = pad(Math.floor((s % 3600) / 60))
  const ss = pad(s % 60)
  return h > 0 ? `${h}:${mm}:${ss}` : `${mm}:${ss}`
}

/** Value for <input type="datetime-local"> in local time. */
export function toLocalInput(ms: number | null): string {
  if (!ms) return ''
  const d = new Date(ms)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function fromLocalInput(v: string): number | null {
  if (!v) return null
  const t = new Date(v).getTime()
  return Number.isNaN(t) ? null : t
}

/** "JBSWY3DPEHPK3PXP" → "JBSW Y3DP EHPK 3PXP". */
export function groupSecret(secret: string): string {
  return secret.replace(/\s+/g, '').replace(/(.{4})(?=.)/g, '$1 ')
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

/** Offers `text` as a file download. */
export function downloadText(filename: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** 1536 → "1.5 KB" (binary units, like the server's MB quotas). */
export function bytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${i === 0 ? v : v >= 100 ? Math.round(v) : v.toFixed(1).replace(/\.0$/, '')} ${units[i]}`
}

/** A MB quota for display: 51200 → "50 GB", 500 → "500 MB". */
export function quotaMb(mb: number): string {
  return bytes(mb * 1024 * 1024)
}

/** Number input helpers: '' → 0, invalid → NaN. */
export function parseAmount(v: string): number {
  const t = v.trim()
  if (!t) return 0
  const n = Number(t)
  return Number.isFinite(n) && n >= 0 ? n : NaN
}

/** kbps ↔ Mbps (the server meters 1 kbps = 1000 bit/s). */
export const kbpsToMbps = (kbps: number) => (kbps > 0 ? String(kbps / 1000) : '')
export const mbpsToKbps = (mbps: number) => Math.round(mbps * 1000)

/** MB ↔ GB for quota inputs (1 GB = 1024 MB). */
export const mbToGb = (mb: number) => (mb > 0 ? String(Math.round((mb / 1024) * 100) / 100) : '')
export const gbToMb = (gb: number) => Math.round(gb * 1024)
