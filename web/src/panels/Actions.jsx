import React, { useEffect, useMemo, useState } from 'react'
import { AreaChart, Area, CartesianGrid, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import { Card, Well, Tile, Empty, Segmented, Skeleton, ChartTooltip, useToast } from '../ui.jsx'
import { usePalette } from '../theme.jsx'
import { cleanText, fuzzy, getActions, noteName, num, postComplete, shortDay, vaultURI } from '../lib.js'

const BUCKET_ORDER = ['overdue', 'today', 'scheduled', 'next', 'waiting', 'someday']
const BUCKET_LABEL = {
  overdue: 'Overdue', today: 'Today', scheduled: 'Scheduled',
  next: 'Next actions', waiting: 'Waiting for', someday: 'Someday / Maybe',
}

// Only the priorities that change what you do next; one caret, coloured,
// reads faster than an emoji ladder.
const PRIORITY_GLYPH = { highest: '▲▲', high: '▲', medium: '▲' }

const VIEWS = [
  { id: 'focus', label: 'Focus' },   // what is actually actionable now
  { id: 'all', label: 'All open' },
  { id: 'someday', label: 'Someday' },
]
const VIEW_BUCKETS = {
  focus: ['overdue', 'today', 'next'],
  all: BUCKET_ORDER,
  someday: ['someday', 'waiting'],
}

export function ActionsTab() {
  const p = usePalette()
  const toast = useToast()
  const [nonce, setNonce] = useState(0)
  const [data, setData] = useState(null)
  const [err, setErr] = useState(null)
  const [busy, setBusy] = useState(null)
  const [view, setView] = useState('focus')
  const [q, setQ] = useState('')

  useEffect(() => {
    let live = true
    getActions(nonce)
      .then((d) => { if (live) { setData(d); setErr(null) } })
      .catch((e) => { if (live) setErr(String(e.message || e)) })
    return () => { live = false }
  }, [nonce])

  const complete = (action) => {
    setBusy(action.hash)
    postComplete(action.source_path, action.hash)
      .then(() => toast(`Done: ${cleanText(action.text).slice(0, 60)}`))
      .catch((e) => {
        const msg = String(e.message || e)
        // A stale hash means the note changed under us — say so, don't retry.
        toast(msg.includes('409') ? 'That task changed in the vault. Reloading the queue.' : msg, 'error')
      })
      .finally(() => { setBusy(null); setNonce((n) => n + 1) })
  }

  const open = useMemo(() => (data?.actions || []).filter((a) => a.state === 'open' && !a.archived), [data])
  const matching = useMemo(
    () => (q.trim() ? open.filter((a) => fuzzy(q, `${a.text} ${a.source_path}`)) : open),
    [open, q],
  )
  const buckets = VIEW_BUCKETS[view]
  const visible = useMemo(() => matching.filter((a) => buckets.includes(a.bucket)), [matching, buckets])

  if (err) {
    return (
      <Card title="Actions" span="span-12">
        <Empty>Couldn’t load the action queue: {err}</Empty>
      </Card>
    )
  }
  if (!data) {
    return (
      <Card title="Actions" span="span-12"><Skeleton h="list" /></Card>
    )
  }

  const c = data.counts || {}

  return (
    <>
      <Card title="Actions" meta={`${num(c.open || 0)} open`} span="span-8">
        <div className="tiles grow-fill">
          <Tile label="Open" value={num(c.open || 0)} accent />
          <Tile label="Overdue" value={num(c.overdue || 0)} sub={c.overdue > 0 ? 'needs a decision' : 'none'} />
          <Tile label="Today" value={num(c.today || 0)} />
          <Tile label="Done (7d)" value={num(c.done7 || 0)} />
        </div>
      </Card>

      <Card title="Completions" meta="last 30 days" span="span-4">
        <Well flush fill minHeight={150}>
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={data.trend || []} margin={{ top: 6, right: 6, bottom: 0, left: -12 }}>
              <defs>
                <linearGradient id="gDone" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor={p.ok} stopOpacity={0.5} />
                  <stop offset="100%" stopColor={p.ok} stopOpacity={0.02} />
                </linearGradient>
              </defs>
              <CartesianGrid vertical={false} stroke={p.grid} />
              <XAxis dataKey="day" tickFormatter={shortDay} fontSize={10} tickLine={false} axisLine={false} minTickGap={28} />
              <YAxis allowDecimals={false} fontSize={10} tickLine={false} axisLine={false} width={26} />
              <Tooltip content={<ChartTooltip />} labelFormatter={shortDay} />
              <Area type="monotone" dataKey="done" name="completed" stroke={p.ok} strokeWidth={1.5} fill="url(#gDone)" />
            </AreaChart>
          </ResponsiveContainer>
        </Well>
      </Card>

      <Card title="Open actions" meta={`${num(visible.length)} shown`} span="span-12">
        <div className="filters">
          <Segmented value={view} onChange={setView} options={VIEWS} label="Which actions" wide />
          <span className="spacer" />
          <input className="input" placeholder="Filter actions…" value={q} onChange={(e) => setQ(e.target.value)}
                 aria-label="Filter actions" />
        </div>

        {visible.length === 0 && (
          <Empty>
            {open.length === 0
              ? <>Nothing open. Add a task anywhere in your vault and it shows up here.</>
              : q.trim()
                ? <>No action matches “{q}”.</>
                : <>Nothing in this view. <b>All open</b> has {num(open.length)}.</>}
          </Empty>
        )}

        {visible.length > 0 && (
          <div className="actions-scroll">
            {buckets.filter((b) => visible.some((a) => a.bucket === b)).map((b) => {
              const rows = visible.filter((a) => a.bucket === b)
              return (
                <div key={b} className="act-group">
                  <div className={`act-head ${b}`}>
                    <span className="act-dot" />
                    <span className="act-head-label">{BUCKET_LABEL[b]}</span>
                    <span className="act-count">{rows.length}</span>
                  </div>
                  <ul className="act-list">
                    {rows.map((a) => {
                      const uri = vaultURI(data.vault, a.source_path)
                      return (
                        <li key={a.hash + a.source_path + a.line_no} className="act-row">
                          <span className="act-text">{cleanText(a.text)}</span>
                          <span className="act-meta">
                            {PRIORITY_GLYPH[a.priority] && (
                              <span className={`act-pri p-${a.priority}`} title={`${a.priority} priority`}>{PRIORITY_GLYPH[a.priority]}</span>
                            )}
                            {a.due && (
                              <span className={`act-due${b === 'overdue' ? ' over' : b === 'today' ? ' now' : ''}`}>{a.due}</span>
                            )}
                            {uri ? (
                              <a className="act-src" href={uri} title={`Open ${a.source_path} in Obsidian`}>{noteName(a.source_path)}</a>
                            ) : (
                              <span className="act-src" title={a.source_path}>{noteName(a.source_path)}</span>
                            )}
                            <button className="act-done" disabled={busy === a.hash}
                                    onClick={() => complete(a)}
                                    title="Tick the checkbox in the source note">
                              {busy === a.hash ? '…' : 'Done'}
                            </button>
                          </span>
                        </li>
                      )
                    })}
                  </ul>
                </div>
              )
            })}
          </div>
        )}
      </Card>
    </>
  )
}
