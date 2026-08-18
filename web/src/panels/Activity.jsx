import React, { useMemo, useRef, useState } from 'react'
import { Card, Empty, Segmented } from '../ui.jsx'
import { fmtTime, fuzzy, num } from '../lib.js'

/* evtKey identifies an event across the two timestamp encodings it can arrive
   with (SSE serialises Go time.Time with sub-second precision + offset; DB rows
   store RFC3339 UTC) by normalising to epoch seconds — string comparison of the
   raw ts values can never match and every live event would duplicate once the
   history poll returns it. */
const evtKey = (e) => `${e.kind}|${e.message}|${Math.floor(Date.parse(e.ts) / 1000)}`

export function ActivityCard({ live, initial, span, height }) {
  const [level, setLevel] = useState('all')
  const [q, setQ] = useState('')
  const [paused, setPaused] = useState(false)
  const frozen = useRef([])

  // Newest first, always: the live stream arrives newest-first while the
  // history poll answers in its own order, and a feed that mixes the two reads
  // as if time ran backwards halfway down.
  const merged = useMemo(() => {
    const seen = new Set(live.map(evtKey))
    const base = (initial || []).filter((e) => !seen.has(evtKey(e)))
    return [...live, ...base]
      .sort((a, b) => Date.parse(b.ts) - Date.parse(a.ts))
      .slice(0, 300)
  }, [live, initial])

  // Pausing holds the list still so a fast-moving stream can be read. New
  // events keep arriving underneath; the count in the button says how many.
  if (!paused) frozen.current = merged
  const rows = paused ? frozen.current : merged
  const behind = paused ? Math.max(0, merged.length - frozen.current.length) : 0

  const counts = useMemo(() => {
    const c = { all: rows.length, info: 0, warn: 0, error: 0 }
    rows.forEach((e) => { if (c[e.level] != null) c[e.level]++ })
    return c
  }, [rows])

  const shown = useMemo(() => {
    let r = rows
    if (level !== 'all') r = r.filter((e) => e.level === level)
    if (q.trim()) r = r.filter((e) => fuzzy(q, `${e.kind} ${e.message}`))
    return r
  }, [rows, level, q])

  return (
    <Card title="Activity" meta={`${num(rows.length)} events`} span={span}>
      <div className="filters">
        <Segmented
          value={level} onChange={setLevel} label="Filter by level"
          options={['all', 'info', 'warn', 'error'].map((l) => ({ id: l, label: counts[l] ? `${l} · ${counts[l]}` : l }))}
        />
        <button className="btn ghost" onClick={() => setPaused((v) => !v)}>
          {paused ? (behind ? `Resume · ${behind} new` : 'Resume') : 'Pause'}
        </button>
        <span className="spacer" />
        <input className="input" placeholder="Filter events…" value={q} onChange={(e) => setQ(e.target.value)}
               aria-label="Filter events" />
      </div>
      <div className="feed grow-fill" style={height ? { maxHeight: height } : { minHeight: 240, maxHeight: 470 }}>
        {shown.map((e, i) => (
          <div className={`evt lvl-${e.level}`} key={`${evtKey(e)}-${i}`}>
            <span className="t">{fmtTime(e.ts)}</span>
            {e.kind && <span className="kind">{e.kind}</span>}
            <span className="msg">{e.message}</span>
          </div>
        ))}
        {shown.length === 0 && (
          <Empty>
            {rows.length > 0
              ? 'No event matches this filter.'
              : 'Nothing yet. Runs, ingests and token events stream here live.'}
          </Empty>
        )}
      </div>
    </Card>
  )
}
