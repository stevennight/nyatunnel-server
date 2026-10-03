import { useEffect, useId, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Check, Copy } from 'lucide-react'
import { copyText } from '../format'
import { tunnelStates } from '../labels'
import type { Tone } from '../labels'
import type { TunnelState } from '../types'

const openModals: symbol[] = []

/** A modal dialog: closes on Escape and on a click outside the panel. */
export function Modal({
  title,
  onClose,
  children,
  wide = false,
}: {
  title: string
  onClose: () => void
  children: ReactNode
  wide?: boolean
}) {
  const titleId = useId()
  const close = useRef(onClose)
  close.current = onClose
  useEffect(() => {
    // Only the topmost dialog reacts to Escape (a confirmation may sit on top of a form).
    const id = Symbol('modal')
    openModals.push(id)
    document.body.style.overflow = 'hidden' // the dialog scrolls on its own
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && openModals[openModals.length - 1] === id) close.current()
    }
    window.addEventListener('keydown', onKey)
    return () => {
      openModals.splice(openModals.indexOf(id), 1)
      if (openModals.length === 0) document.body.style.overflow = ''
      window.removeEventListener('keydown', onKey)
    }
  }, [])

  return (
    <div className="mask" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={`modal${wide ? ' wide' : ''}`} role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <h3 id={titleId}>{title}</h3>
        {children}
      </div>
    </div>
  )
}

/** Asks before something irreversible. `onConfirm` may reject; the error is shown in the dialog. */
export function ConfirmDialog({
  title,
  children,
  confirmLabel,
  danger = false,
  busy = false,
  error,
  onConfirm,
  onClose,
}: {
  title: string
  children: ReactNode
  confirmLabel: string
  danger?: boolean
  busy?: boolean
  error?: string | null
  onConfirm: () => void
  onClose: () => void
}) {
  return (
    <Modal title={title} onClose={onClose}>
      <div className="confirm-body">{children}</div>
      {error && <Notice tone="bad">{error}</Notice>}
      <div className="actions">
        <button type="button" className="btn" onClick={onClose}>
          取消
        </button>
        <button type="button" className={`btn ${danger ? 'danger-solid' : 'primary'}`} disabled={busy} onClick={onConfirm}>
          {confirmLabel}
        </button>
      </div>
    </Modal>
  )
}

export function Notice({ tone = 'n', children }: { tone?: Tone; children: ReactNode }) {
  return (
    <div className={`notice ${tone}`} role={tone === 'bad' ? 'alert' : 'status'}>
      {children}
    </div>
  )
}

export function Tag({ tone = 'n', children, title }: { tone?: Tone; children: ReactNode; title?: string }) {
  return (
    <span className={`tag ${tone}`} title={title}>
      {children}
    </span>
  )
}

export function Dot({ tone }: { tone: Tone }) {
  return <span className={`dot ${tone}`} aria-hidden="true" />
}

export function TunnelStateTag({ state, error }: { state: TunnelState; error?: string }) {
  const s = tunnelStates[state] ?? { label: state, tone: 'n' as Tone }
  return (
    <Tag tone={s.tone} title={error || undefined}>
      {s.label}
    </Tag>
  )
}

export function CopyButton({ text, label = '复制' }: { text: string; label?: string }) {
  const [done, setDone] = useState(false)
  useEffect(() => {
    if (!done) return
    const t = setTimeout(() => setDone(false), 1500)
    return () => clearTimeout(t)
  }, [done])
  return (
    <button
      type="button"
      className="btn"
      aria-label={`${label}${text.length < 40 ? ` ${text}` : ''}`}
      onClick={async () => setDone(await copyText(text))}
    >
      {done ? <Check size={14} /> : <Copy size={14} />}
      {done ? '已复制' : label}
    </button>
  )
}

export function PageHead({ title, hint, children }: { title: string; hint?: ReactNode; children?: ReactNode }) {
  return (
    <div className="ttl">
      <div>
        <h2>{title}</h2>
        {hint && <div className="hint">{hint}</div>}
      </div>
      {children && <div className="inline wrap">{children}</div>}
    </div>
  )
}

export function Loading() {
  return <div className="hint pad">加载中…</div>
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>
}
