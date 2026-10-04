import { useEffect, useId, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Check, Copy } from 'lucide-react'
import { copyText } from '../format'
import { tunnelStates } from '../labels'
import type { Tone } from '../labels'
import type { TunnelState } from '../types'

const openModals: symbol[] = []

/**
 * A modal dialog. Escape closes it unless `dismissable` is false (for content that cannot be shown
 * again, such as a one-time enrollment code). A click outside the panel never closes it: most
 * dialogs are forms, and a stray click must not throw away what was typed.
 */
export function Modal({
  title,
  onClose,
  children,
  wide = false,
  dismissable = true,
}: {
  title: string
  onClose: () => void
  children: ReactNode
  wide?: boolean
  dismissable?: boolean
}) {
  const titleId = useId()
  const panel = useRef<HTMLDivElement>(null)
  const close = useRef(onClose)
  close.current = onClose
  const canDismiss = useRef(dismissable)
  canDismiss.current = dismissable
  useEffect(() => {
    // Start keyboard focus inside the dialog: an autoFocus child wins, then the first field.
    const el = panel.current
    if (el && !el.contains(document.activeElement)) {
      const field = el.querySelector<HTMLElement>('input:not([type=hidden]):not([disabled]):not([readonly]), select:not([disabled]), textarea:not([disabled])')
      ;(field ?? el).focus()
    }
  }, [])
  useEffect(() => {
    // Only the topmost dialog reacts to Escape (a confirmation may sit on top of a form).
    const id = Symbol('modal')
    openModals.push(id)
    document.body.style.overflow = 'hidden' // the dialog scrolls on its own
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && canDismiss.current && openModals[openModals.length - 1] === id) close.current()
    }
    window.addEventListener('keydown', onKey)
    return () => {
      openModals.splice(openModals.indexOf(id), 1)
      if (openModals.length === 0) document.body.style.overflow = ''
      window.removeEventListener('keydown', onKey)
    }
  }, [])

  return (
    <div className="mask">
      <div ref={panel} tabIndex={-1} className={`modal${wide ? ' wide' : ''}`} role="dialog" aria-modal="true" aria-labelledby={titleId}>
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
  cancelLabel = '取消',
  danger = false,
  busy = false,
  error,
  onConfirm,
  onClose,
}: {
  title: string
  children: ReactNode
  confirmLabel: string
  cancelLabel?: string
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
        {/* Enter confirms a harmless action; for a dangerous one it only cancels. */}
        <button type="button" className="btn" onClick={onClose} autoFocus={danger}>
          {cancelLabel}
        </button>
        <button type="button" className={`btn ${danger ? 'danger-solid' : 'primary'}`} disabled={busy} onClick={onConfirm} autoFocus={!danger}>
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
  const [done, setDone] = useState<'ok' | 'failed' | null>(null)
  useEffect(() => {
    if (!done) return
    const t = setTimeout(() => setDone(null), done === 'ok' ? 1500 : 3000)
    return () => clearTimeout(t)
  }, [done])
  return (
    <button
      type="button"
      className={`btn${done === 'failed' ? ' danger' : ''}`}
      aria-label={`${label}${text.length < 40 ? ` ${text}` : ''}`}
      onClick={async () => setDone((await copyText(text)) ? 'ok' : 'failed')}
    >
      {done === 'ok' ? <Check size={14} /> : <Copy size={14} />}
      {done === 'ok' ? '已复制' : done === 'failed' ? '复制失败，请手动选中复制' : label}
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
