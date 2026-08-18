import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { num, RANGES } from './lib.js'
import { usePalette } from './theme.jsx'

/* ── surfaces ─────────────────────────────────────────────────────────── */
export function Card({ title, meta, span, children, className = '' }) {
  return (
    <section className={`card glass${span ? ' ' + span : ''}${className ? ' ' + className : ''}`}>
      {title && (
        <div className="eyebrow">
          <span>{title}</span>
          {meta != null && meta !== '' && <span className="meta">{meta}</span>}
        </div>
      )}
      <div className="card-body">{children}</div>
    </section>
  )
}

// Charts never sit on glass — they get an opaque well inside the card, the
// same rule Companion applies with `.axonCard()` (PRD §4).
//
// `fill` makes the well absorb the height a stretched card has spare and hand
// it to the chart, so cards in a row end level without leaving a gap inside
// the shorter one. `minHeight` is the floor the plot never shrinks past.
export const Well = ({ children, flush, fill, minHeight }) => (
  <div
    className={`well${flush ? ' flush' : ''}${fill ? ' grow-fill' : ''}`}
    style={minHeight ? { minHeight } : undefined}
  >
    {children}
  </div>
)

export function Tile({ label, value, unit, accent, sub }) {
  return (
    <div className="tile">
      <div className={`num${accent ? ' accent' : ''}`}>{value}{unit && <small>{unit}</small>}</div>
      <span className="lbl">{label}</span>
      {sub && <span className="sub">{sub}</span>}
    </div>
  )
}

export const Empty = ({ children }) => <div className="empty">{children}</div>
export const Skeleton = ({ h = 'chart' }) => <div className={`skel h-${h}`} />

/* ── controls ─────────────────────────────────────────────────────────── */
export function Segmented({ value, onChange, options, label, wide }) {
  return (
    <div className={`seg${wide ? ' wide' : ''}`} role="radiogroup" aria-label={label}>
      {options.map((o) => {
        const id = typeof o === 'string' ? o : o.id
        const text = typeof o === 'string' ? o : o.label
        return (
          <button key={id} role="radio" aria-checked={value === id}
                  className={value === id ? 'on' : ''} onClick={() => onChange(id)}>
            {text}
          </button>
        )
      })}
    </div>
  )
}

export const RangePicker = ({ value, onChange }) => (
  <Segmented value={value} onChange={onChange} options={RANGES} label="Time range" />
)

export function ExportLinks({ dataset }) {
  return (
    <span className="export-links">
      <a href={`/api/export?dataset=${dataset}&format=csv`}
         title={`Download the full ${dataset} series the daemon holds — not the selected range`}>⤓ CSV</a>
      <a href={`/api/export?dataset=${dataset}&format=json`}
         title={`Download the full ${dataset} series the daemon holds — not the selected range`}>⤓ JSON</a>
    </span>
  )
}

/* ── charts ───────────────────────────────────────────────────────────── */
export function ChartTooltip({ active, payload, label, unit }) {
  const p = usePalette()
  if (!active || !payload || !payload.length) return null
  return (
    <div className="tip">
      {label != null && label !== '' && <div className="tip-h">{label}</div>}
      {payload.map((row, i) => (
        <div className="tip-r" key={i}>
          <span className="k">
            <i className="swatch" style={{ background: row.color || row.payload?.fill || p.signal1 }} />
            {row.name}
          </span>
          <span className="v">{num(row.value)}{unit ? ` ${unit}` : ''}</span>
        </div>
      ))}
    </div>
  )
}

/* ── failure isolation ────────────────────────────────────────────────── */
export class ErrorBoundary extends React.Component {
  constructor(p) { super(p); this.state = { err: null } }
  static getDerivedStateFromError(err) { return { err } }
  render() {
    if (this.state.err) {
      return (
        <Card title="Panel isolated" span="span-12">
          <div className="error-card">
            <p>This panel hit a snag and was isolated so the rest of the dashboard keeps running. Switch tabs and back to retry it.</p>
            <code>{String(this.state.err)}</code>
          </div>
        </Card>
      )
    }
    return this.props.children
  }
}

/* ── toasts ───────────────────────────────────────────────────────────────
   For things that happen away from the pointer: a write that failed, a
   budget denial arriving on the event stream. Never for routine traffic —
   the Activity feed is where the stream lives.
*/
const ToastCtx = createContext(() => {})
export const useToast = () => useContext(ToastCtx)

export function ToastHost({ children }) {
  const [items, setItems] = useState([])
  const seq = useRef(0)

  const dismiss = useCallback((id) => setItems((xs) => xs.filter((x) => x.id !== id)), [])

  const push = useCallback((text, level = 'info', ttl = 6000) => {
    const id = ++seq.current
    setItems((xs) => [...xs, { id, text, level }].slice(-4))
    if (ttl) setTimeout(() => dismiss(id), ttl)
  }, [dismiss])

  const value = useMemo(() => push, [push])
  return (
    <ToastCtx.Provider value={value}>
      {children}
      <div className="toasts" aria-live="polite">
        {items.map((t) => (
          <div key={t.id} className={`toast glass ${t.level}`}>
            <i className="bar" />
            <span className="grow">{t.text}</span>
            <button onClick={() => dismiss(t.id)} aria-label="Dismiss">×</button>
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  )
}

/* ── modal plumbing ───────────────────────────────────────────────────────
   Escape closes, the backdrop closes, and focus is returned to whatever
   opened it. Small enough to keep here rather than take a dependency.
*/
export function Overlay({ onClose, children, label }) {
  const opener = useRef(typeof document !== 'undefined' ? document.activeElement : null)
  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape') { e.stopPropagation(); onClose() } }
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      if (opener.current && opener.current.focus) opener.current.focus()
    }
  }, [onClose])
  return (
    <div className="overlay" role="dialog" aria-modal="true" aria-label={label}
         onMouseDown={(e) => { if (e.target === e.currentTarget) onClose() }}>
      {children}
    </div>
  )
}
