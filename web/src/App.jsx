import React, { useEffect, useMemo, useState } from 'react'
import {
  AreaChart, Area, BarChart, Bar, PieChart, Pie, Cell,
  RadialBarChart, RadialBar, PolarAngleAxis,
  XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer,
} from 'recharts'

/* ── palette ─────────────────────────────────────────────────────────────── */
const SIGNAL = { teal: '#2fe0cf', indigo: '#6f7cf2', violet: '#b07cf0' }
const SEMA = { ok: '#41d693', warn: '#f5b14b', err: '#fb6f6f', faint: '#616d83' }
const PALETTE = ['#2fe0cf', '#6f7cf2', '#b07cf0', '#f5b14b', '#fb6f8f', '#41d693', '#5bb6f0', '#c4cdde']
const color = (i) => PALETTE[((i % PALETTE.length) + PALETTE.length) % PALETTE.length]

/* ── data hooks ──────────────────────────────────────────────────────────── */
// useFetch polls a JSON endpoint. Failures (network, non-2xx) are surfaced via
// `error` instead of being swallowed — a dead daemon must look degraded, not
// like healthy-but-empty. The last good data is kept while errored.
function useFetch(url, interval = 5000) {
  const [state, setState] = useState({ data: null, error: false })
  useEffect(() => {
    let alive = true
    const load = () =>
      fetch(url)
        .then((r) => { if (!r.ok) throw new Error(`HTTP ${r.status}`); return r.json() })
        .then((d) => alive && setState({ data: d, error: false }))
        .catch(() => alive && setState((s) => ({ data: s.data, error: true })))
    load()
    const id = setInterval(load, interval)
    return () => { alive = false; clearInterval(id) }
  }, [url, interval])
  return state
}

// SSE_KINDS must mirror the named event kinds the daemon actually emits
// (events are sent with `event: <kind>`, so unregistered kinds never fire).
const SSE_KINDS = [
  'automation.run', 'automation.skip', 'automation.fail',
  'ingest.done', 'ingest.skip', 'ingest.enrich',
  'ingest.embed.fail', 'ingest.embed.skip', 'ingest.review_queue.fail',
  'token.ledger', 'token.deny', 'token.defer', 'token.downgrade', 'token.error',
  'review.accept', 'review.dismiss',
]

function useSSE() {
  const [events, setEvents] = useState([])
  const [connected, setConnected] = useState(false)
  useEffect(() => {
    const es = new EventSource('/events')
    es.onopen = () => setConnected(true)
    es.onerror = () => setConnected(false)
    const push = (e) => {
      try { setEvents((prev) => [JSON.parse(e.data), ...prev].slice(0, 400)) } catch {}
    }
    es.onmessage = push
    SSE_KINDS.forEach((k) => es.addEventListener(k, push))
    return () => es.close()
  }, [])
  return { events, connected }
}

/* ── formatting ──────────────────────────────────────────────────────────── */
const num = (n) => (n || 0).toLocaleString()
const kfmt = (n) => (n >= 1e6 ? (n / 1e6).toFixed(1) + 'M' : n >= 1e3 ? (n / 1e3).toFixed(n >= 1e4 ? 0 : 1) + 'k' : String(n || 0))
const shortDay = (d) => (d || '').slice(5)
const parseTags = (t) => { try { const a = JSON.parse(t || '[]'); return Array.isArray(a) ? a : [] } catch { return [] } }
const shortOp = (op) => (op || '').replace(/^automation\./, '').replace(/^ingest\./, 'ingest:')
function fmtTime(ts) { try { return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) } catch { return ts } }
function fmtDur(a, b) {
  const s = (new Date(b) - new Date(a)) / 1000
  if (!isFinite(s) || s < 0) return '—'
  if (s < 1) return '<1s'
  if (s < 60) return s.toFixed(s < 10 ? 1 : 0) + 's'
  if (s < 3600) return Math.floor(s / 60) + 'm ' + Math.round(s % 60) + 's'
  return Math.floor(s / 3600) + 'h ' + Math.round((s % 3600) / 60) + 'm'
}

/* ── aggregations ────────────────────────────────────────────────────────── */
// tokensDaily collapses the per-day/operation/model buckets into a day series
// (with the cache split, FR-60).
function tokensDaily(tokens) {
  const by = {}
  for (const b of tokens || []) {
    const d = (by[b.day] = by[b.day] || { day: b.day, input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 })
    d.input += b.input || 0; d.output += b.output || 0
    d.cacheRead += b.cache_read || 0; d.cacheWrite += b.cache_write || 0
    d.total += (b.input || 0) + (b.output || 0)
  }
  return Object.values(by).sort((a, b) => a.day.localeCompare(b.day))
}
// pieBy sums a token field by a key, returns top slices + an "other" rollup.
function pieBy(tokens, keyFn, top = 6) {
  const m = {}
  for (const b of tokens || []) m[keyFn(b)] = (m[keyFn(b)] || 0) + (b.input || 0) + (b.output || 0)
  const all = Object.entries(m).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value)
  if (all.length <= top) return all
  const head = all.slice(0, top)
  head.push({ name: 'other', value: all.slice(top).reduce((s, x) => s + x.value, 0) })
  return head
}
const sumField = (arr, f) => (arr || []).reduce((s, x) => s + (x[f] || 0), 0)

/* ── shared UI ───────────────────────────────────────────────────────────── */
function Card({ title, meta, span, children }) {
  return (
    <section className={`card${span ? ' ' + span : ''}`}>
      {title && <div className="eyebrow"><span>{title}</span>{meta != null && <span className="meta">{meta}</span>}</div>}
      {children}
    </section>
  )
}

function Tile({ label, value, unit, accent }) {
  return (
    <div className="tile">
      <div className={`num${accent ? ' accent' : ''}`}>{value}{unit && <small>{unit}</small>}</div>
      <span className="lbl">{label}</span>
    </div>
  )
}

function Empty({ children }) { return <div className="empty">{children}</div> }

function CustomTooltip({ active, payload, label }) {
  if (!active || !payload || !payload.length) return null
  return (
    <div className="tip">
      {label != null && label !== '' && <div className="tip-h">{label}</div>}
      {payload.map((p, i) => (
        <div className="tip-r" key={i}>
          <span className="k"><i className="swatch" style={{ background: p.color || p.payload?.fill || SIGNAL.teal }} />{p.name}</span>
          <span className="v">{num(p.value)}</span>
        </div>
      ))}
    </div>
  )
}

class ErrorBoundary extends React.Component {
  constructor(p) { super(p); this.state = { err: null } }
  static getDerivedStateFromError(err) { return { err } }
  render() {
    if (this.state.err) {
      return (
        <Card title="view isolated" span="span-12">
          <div className="error-card">
            <p>This panel hit a snag, so it was isolated to keep the rest of the dashboard running. Switch tabs and back to retry.</p>
            <code>{String(this.state.err)}</code>
          </div>
        </Card>
      )
    }
    return this.props.children
  }
}

/* ── budget ──────────────────────────────────────────────────────────────── */
function gaugeColor(pct, paused) {
  if (paused) return SEMA.err
  if (pct >= 80) return SEMA.warn
  return SIGNAL.teal
}
function Gauge({ cap, pct, paused }) {
  const p = Math.max(0, Math.min(100, pct || 0))
  const fill = gaugeColor(p, paused)
  return (
    <div className="gauge">
      <ResponsiveContainer width="100%" height="100%">
        <RadialBarChart innerRadius="72%" outerRadius="100%" data={[{ value: p }]} startAngle={90} endAngle={-270}>
          <PolarAngleAxis type="number" domain={[0, 100]} tick={false} />
          <RadialBar background={{ fill: '#1b2333' }} dataKey="value" cornerRadius={9} fill={fill} />
        </RadialBarChart>
      </ResponsiveContainer>
      <div className="readout">
        <div className="pct" style={{ color: fill }}>{p.toFixed(0)}%</div>
        <div className="cap">{cap}</div>
      </div>
    </div>
  )
}

function BudgetCard({ usage, span }) {
  const u = usage || {}
  return (
    <Card title="Token budget" meta={u.guard_paused ? `guard paused ≥ ${u.guard_pct}%` : 'guard armed'} span={span}>
      <div className="gauges">
        <Gauge cap="Today" pct={u.day_pct} paused={u.guard_paused} />
        <Gauge cap="This week" pct={u.week_pct} paused={u.guard_paused} />
        <div className="gauge-info">
          <div className="legend-row"><span className="k"><i className="swatch" style={{ background: gaugeColor(u.day_pct, u.guard_paused) }} />Day</span><span className="v">{num(u.day_used)} / {num(u.day_limit)}</span></div>
          <div className="legend-row"><span className="k"><i className="swatch" style={{ background: gaugeColor(u.week_pct, u.guard_paused) }} />Week</span><span className="v">{num(u.week_used)} / {num(u.week_limit)}</span></div>
          {(u.day_cost_cap > 0 || u.day_cost_used > 0) && (
            <div className="legend-row">
              <span className="k"><i className="swatch" style={{ background: gaugeColor(u.day_cost_pct, u.guard_paused) }} />Cost today</span>
              <span className="v">${(u.day_cost_used || 0).toFixed(2)}{u.day_cost_cap > 0 ? ` / $${u.day_cost_cap.toFixed(2)}` : ''}</span>
            </div>
          )}
          <div className="legend-row">
            <span className="k">budget-guard</span>
            <span className="v" style={{ color: u.guard_paused ? SEMA.err : SEMA.ok }}>{u.guard_paused ? 'PAUSED' : 'ok'}</span>
          </div>
        </div>
      </div>
    </Card>
  )
}

/* ── tokens ──────────────────────────────────────────────────────────────── */
function TokenTrend({ tokens, span, title = 'Token spend' }) {
  const daily = useMemo(() => tokensDaily(tokens), [tokens])
  const total = sumField(daily, 'total')
  return (
    <Card title={title} meta={`${num(total)} tokens · ${daily.length}d`} span={span}>
      {daily.length === 0 ? <Empty>No Claude usage recorded yet. Spend appears here as automations run.</Empty> : (
        <ResponsiveContainer width="100%" height={250}>
          <AreaChart data={daily} margin={{ top: 6, right: 6, bottom: 0, left: -8 }}>
            <defs>
              <linearGradient id="gIn" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor={SIGNAL.indigo} stopOpacity={0.55} /><stop offset="100%" stopColor={SIGNAL.indigo} stopOpacity={0.02} /></linearGradient>
              <linearGradient id="gOut" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor={SIGNAL.teal} stopOpacity={0.6} /><stop offset="100%" stopColor={SIGNAL.teal} stopOpacity={0.02} /></linearGradient>
              <linearGradient id="gCr" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor={SIGNAL.violet} stopOpacity={0.5} /><stop offset="100%" stopColor={SIGNAL.violet} stopOpacity={0.02} /></linearGradient>
              <linearGradient id="gCw" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor={SEMA.warn} stopOpacity={0.45} /><stop offset="100%" stopColor={SEMA.warn} stopOpacity={0.02} /></linearGradient>
            </defs>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="day" tickFormatter={shortDay} fontSize={11} tickLine={false} axisLine={false} minTickGap={24} />
            <YAxis tickFormatter={kfmt} fontSize={11} tickLine={false} axisLine={false} width={42} />
            <Tooltip content={<CustomTooltip />} labelFormatter={shortDay} />
            <Area type="monotone" dataKey="input" name="input" stackId="1" stroke={SIGNAL.indigo} strokeWidth={1.5} fill="url(#gIn)" />
            <Area type="monotone" dataKey="output" name="output" stackId="1" stroke={SIGNAL.teal} strokeWidth={1.5} fill="url(#gOut)" />
            <Area type="monotone" dataKey="cacheRead" name="cache read" stackId="1" stroke={SIGNAL.violet} strokeWidth={1} fill="url(#gCr)" />
            <Area type="monotone" dataKey="cacheWrite" name="cache write" stackId="1" stroke={SEMA.warn} strokeWidth={1} fill="url(#gCw)" />
          </AreaChart>
        </ResponsiveContainer>
      )}
    </Card>
  )
}

function DonutCard({ title, data, span }) {
  const total = sumField(data, 'value')
  return (
    <Card title={title} meta={num(total)} span={span}>
      {data.length === 0 ? <Empty>Nothing recorded yet.</Empty> : (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <div style={{ width: 168, height: 168, flex: '0 0 auto' }}>
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Pie data={data} dataKey="value" nameKey="name" innerRadius={50} outerRadius={78} paddingAngle={2} stroke="none">
                  {data.map((d, i) => <Cell key={i} fill={color(i)} />)}
                </Pie>
                <Tooltip content={<CustomTooltip />} />
              </PieChart>
            </ResponsiveContainer>
          </div>
          <div className="list" style={{ flex: 1, minWidth: 150 }}>
            {data.map((d, i) => (
              <div className="legend-row" key={d.name} style={{ padding: '5px 0' }}>
                <span className="k"><i className="swatch" style={{ background: color(i) }} /><span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 130 }}>{d.name}</span></span>
                <span className="v">{kfmt(d.value)}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </Card>
  )
}

function TokensByOpModel({ tokens, span }) {
  const { rows, keys } = useMemo(() => {
    const keySet = new Set(); const byDay = {}
    for (const b of tokens || []) {
      const k = `${shortOp(b.operation)} · ${b.model}`
      keySet.add(k)
      byDay[b.day] = byDay[b.day] || { day: b.day }
      byDay[b.day][k] = (byDay[b.day][k] || 0) + (b.input || 0) + (b.output || 0)
    }
    return { rows: Object.values(byDay).sort((a, b) => a.day.localeCompare(b.day)), keys: [...keySet] }
  }, [tokens])
  return (
    <Card title="Spend by automation × model" meta={`${keys.length} streams`} span={span}>
      {rows.length === 0 ? <Empty>No token spend yet.</Empty> : (
        <ResponsiveContainer width="100%" height={260}>
          <BarChart data={rows} margin={{ top: 6, right: 6, bottom: 0, left: -8 }}>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="day" tickFormatter={shortDay} fontSize={11} tickLine={false} axisLine={false} minTickGap={24} />
            <YAxis tickFormatter={kfmt} fontSize={11} tickLine={false} axisLine={false} width={42} />
            <Tooltip content={<CustomTooltip />} labelFormatter={shortDay} cursor={{ fill: 'rgba(255,255,255,0.03)' }} />
            {keys.map((k, i) => <Bar key={k} dataKey={k} stackId="t" fill={color(i)} radius={i === keys.length - 1 ? [3, 3, 0, 0] : 0} maxBarSize={34} />)}
          </BarChart>
        </ResponsiveContainer>
      )}
    </Card>
  )
}

/* ── automations / runs ──────────────────────────────────────────────────── */
const STATUS_COLOR = { ok: SEMA.ok, failed: SEMA.err, skipped: SEMA.faint, 'dry-run': SEMA.warn }
function RunsList({ runs, span, limit = 14, title = 'Recent automations' }) {
  const list = (runs || []).slice(0, limit)
  return (
    <Card title={title} meta={runs ? `${runs.length} runs` : ''} span={span}>
      <div className="list">
        {list.map((r) => (
          <div className="li" key={r.id}>
            <span className={`sdot ${r.status}`} />
            <span className="grow">{r.automation}{r.skip_reason ? <span className="muted" style={{ color: SEMA.faint }}> · {r.skip_reason}</span> : ''}</span>
            {r.tokens > 0 && <span className="mono">{kfmt(r.tokens)} tok</span>}
            <span className="mono">{r.finished_at ? fmtDur(r.started_at, r.finished_at) : '…'}</span>
            <span className={`badge ${r.status}`}>{r.status}</span>
          </div>
        ))}
        {list.length === 0 && <Empty>No automation runs yet. They’ll appear as the scheduler fires, or run one with <code>axon run &lt;name&gt;</code>.</Empty>}
      </div>
    </Card>
  )
}

function RunStats({ runs, span }) {
  const { dist, perAuto } = useMemo(() => {
    const d = {}, pa = {}
    for (const r of runs || []) {
      d[r.status] = (d[r.status] || 0) + 1
      pa[r.automation] = (pa[r.automation] || 0) + 1
    }
    return {
      dist: Object.entries(d).map(([name, value]) => ({ name, value })),
      perAuto: Object.entries(pa).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value).slice(0, 8),
    }
  }, [runs])
  return (
    <Card title="Run outcomes" meta={`${runs ? runs.length : 0} total`} span={span}>
      {(!runs || runs.length === 0) ? <Empty>No runs to summarise yet.</Empty> : (
        <div style={{ display: 'flex', gap: 14, flexWrap: 'wrap', alignItems: 'center' }}>
          <div style={{ width: 150, height: 150, flex: '0 0 auto' }}>
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Pie data={dist} dataKey="value" nameKey="name" innerRadius={44} outerRadius={70} paddingAngle={2} stroke="none">
                  {dist.map((d) => <Cell key={d.name} fill={STATUS_COLOR[d.name] || SEMA.faint} />)}
                </Pie>
                <Tooltip content={<CustomTooltip />} />
              </PieChart>
            </ResponsiveContainer>
          </div>
          <div style={{ flex: 1, minWidth: 180 }}>
            <ResponsiveContainer width="100%" height={150}>
              <BarChart data={perAuto} layout="vertical" margin={{ left: 4, right: 12, top: 2, bottom: 2 }}>
                <XAxis type="number" hide />
                <YAxis type="category" dataKey="name" width={108} fontSize={11} tickLine={false} axisLine={false} />
                <Tooltip content={<CustomTooltip />} cursor={{ fill: 'rgba(255,255,255,0.03)' }} />
                <Bar dataKey="value" name="runs" fill={SIGNAL.indigo} radius={[0, 4, 4, 0]} maxBarSize={16} />
              </BarChart>
            </ResponsiveContainer>
          </div>
        </div>
      )}
    </Card>
  )
}

/* ── knowledge / ingestion ───────────────────────────────────────────────── */
function VaultTiles({ vault, ingestion, span }) {
  const v = vault?.stats || {}, ing = ingestion || {}
  return (
    <Card title="Vault" span={span}>
      <div className="tiles">
        <Tile label="Notes" value={num(v.notes)} accent />
        <Tile label="Links" value={num(v.links)} />
        <Tile label="Words" value={kfmt(v.words || 0)} />
        <Tile label="Sources" value={num(v.sources)} />
        <Tile label="Inbox" value={num(v.inbox_backlog)} />
        <Tile label="Embed queue" value={num(ing.embedding_queue)} />
      </div>
    </Card>
  )
}

// GrowthCard charts cumulative vault size over time (FR-60), derived from
// note-creation dates server-side.
function GrowthCard({ vault, span }) {
  const growth = vault?.growth || []
  return (
    <Card title="Vault growth" meta={growth.length ? `${growth.length} days` : ''} span={span}>
      {growth.length < 2 ? <Empty>Growth appears once notes span more than one day.</Empty> : (
        <ResponsiveContainer width="100%" height={220}>
          <AreaChart data={growth} margin={{ top: 6, right: 6, bottom: 0, left: -8 }}>
            <defs>
              <linearGradient id="gNotes" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor={SIGNAL.teal} stopOpacity={0.5} /><stop offset="100%" stopColor={SIGNAL.teal} stopOpacity={0.02} /></linearGradient>
            </defs>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="day" tickFormatter={shortDay} fontSize={11} tickLine={false} axisLine={false} minTickGap={24} />
            <YAxis yAxisId="n" tickFormatter={kfmt} fontSize={11} tickLine={false} axisLine={false} width={38} />
            <YAxis yAxisId="w" orientation="right" tickFormatter={kfmt} fontSize={11} tickLine={false} axisLine={false} width={44} />
            <Tooltip content={<CustomTooltip />} labelFormatter={shortDay} />
            <Area yAxisId="n" type="stepAfter" dataKey="notes" name="notes" stroke={SIGNAL.teal} strokeWidth={1.6} fill="url(#gNotes)" />
            <Area yAxisId="w" type="stepAfter" dataKey="words" name="words" stroke={SIGNAL.indigo} strokeWidth={1.2} fill="none" />
          </AreaChart>
        </ResponsiveContainer>
      )}
    </Card>
  )
}

function IngestTrend({ ingestion, span }) {
  const { rows, statuses } = useMemo(() => {
    const set = new Set(); const byDay = {}
    for (const b of ingestion?.series || []) {
      set.add(b.status || 'unknown')
      byDay[b.day] = byDay[b.day] || { day: b.day }
      byDay[b.day][b.status || 'unknown'] = (byDay[b.day][b.status || 'unknown'] || 0) + b.count
    }
    return { rows: Object.values(byDay).sort((a, b) => (a.day || '').localeCompare(b.day || '')), statuses: [...set] }
  }, [ingestion])
  return (
    <Card title="Ingestion" meta={`${rows.length}d`} span={span}>
      {rows.length === 0 ? <Empty>No sources ingested yet. Try <code>axon ingest &lt;url&gt;</code>.</Empty> : (
        <ResponsiveContainer width="100%" height={240}>
          <BarChart data={rows} margin={{ top: 6, right: 6, bottom: 0, left: -12 }}>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="day" tickFormatter={shortDay} fontSize={11} tickLine={false} axisLine={false} minTickGap={20} />
            <YAxis allowDecimals={false} fontSize={11} tickLine={false} axisLine={false} width={34} />
            <Tooltip content={<CustomTooltip />} labelFormatter={shortDay} cursor={{ fill: 'rgba(255,255,255,0.03)' }} />
            {statuses.map((s, i) => <Bar key={s} dataKey={s} stackId="s" fill={s === 'failed' ? SEMA.err : color(i)} radius={i === statuses.length - 1 ? [3, 3, 0, 0] : 0} maxBarSize={34} />)}
          </BarChart>
        </ResponsiveContainer>
      )}
    </Card>
  )
}

function FoldersCard({ graph, span }) {
  const data = useMemo(() => {
    const m = {}
    for (const n of graph?.nodes || []) { const top = (n.path || '').split('/')[0]; if (top) m[top] = (m[top] || 0) + 1 }
    return Object.entries(m).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value).slice(0, 8)
  }, [graph])
  return (
    <Card title="Notes by folder" meta={`${data.length} folders`} span={span}>
      {data.length === 0 ? <Empty>No notes indexed yet.</Empty> : (
        <ResponsiveContainer width="100%" height={Math.max(120, data.length * 30)}>
          <BarChart data={data} layout="vertical" margin={{ left: 4, right: 14, top: 2, bottom: 2 }}>
            <XAxis type="number" hide allowDecimals={false} />
            <YAxis type="category" dataKey="name" width={104} fontSize={11} tickLine={false} axisLine={false} />
            <Tooltip content={<CustomTooltip />} cursor={{ fill: 'rgba(255,255,255,0.03)' }} />
            <Bar dataKey="value" name="notes" radius={[0, 4, 4, 0]} maxBarSize={18}>
              {data.map((d, i) => <Cell key={d.name} fill={color(i)} />)}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      )}
    </Card>
  )
}

/* ── knowledge graph (the signature) ─────────────────────────────────────── */
function GraphCard({ graph, simEdges, onToggleSim, span }) {
  const [folder, setFolder] = useState('')
  const [tag, setTag] = useState('')
  const [hover, setHover] = useState(null)

  const nodesAll = graph?.nodes || []
  const edgesAll = graph?.edges || []

  const folders = useMemo(() => {
    const s = new Set(); nodesAll.forEach((n) => { const t = (n.path || '').split('/')[0]; if (t) s.add(t) }); return [...s].sort()
  }, [nodesAll])
  const tags = useMemo(() => {
    const s = new Set(); nodesAll.forEach((n) => parseTags(n.tags).forEach((t) => s.add(t))); return [...s].sort()
  }, [nodesAll])
  const folderColor = useMemo(() => Object.fromEntries(folders.map((f, i) => [f, color(i)])), [folders])

  const view = useMemo(() => {
    let nodes = nodesAll
    if (folder) nodes = nodes.filter((n) => (n.path || '').startsWith(folder + '/'))
    if (tag) nodes = nodes.filter((n) => parseTags(n.tags).includes(tag))
    const ids = new Set(nodes.map((n) => n.id))
    const edges = edgesAll.filter((e) => ids.has(e.source) && ids.has(e.target))
    const CX = 300, CY = 232, R = 198, N = nodes.length
    const placed = nodes.map((n, i) => {
      const ang = (2 * Math.PI * i) / Math.max(1, N)
      const j = (i * 0.6180339887) % 1            // deterministic organic jitter
      const rad = R * (0.5 + 0.5 * j)
      return { ...n, x: CX + rad * Math.cos(ang), y: CY + rad * Math.sin(ang) }
    })
    const pos = Object.fromEntries(placed.map((n) => [n.id, n]))
    const adj = {}
    edges.forEach((e) => { (adj[e.source] = adj[e.source] || new Set()).add(e.target); (adj[e.target] = adj[e.target] || new Set()).add(e.source) })
    return { nodes: placed, edges, pos, adj }
  }, [nodesAll, edgesAll, folder, tag])

  const neighbors = hover != null ? (view.adj[hover] || new Set()) : null
  const dim = (id) => hover != null && id !== hover && !(neighbors && neighbors.has(id))

  const linkCount = view.edges.filter((e) => e.kind !== 'similar').length
  const simCount = view.edges.length - linkCount

  return (
    <Card title="Knowledge graph" meta={`${view.nodes.length} notes · ${linkCount} links${simEdges ? ` · ${simCount} similar` : ''}`} span={span}>
      <div className="filters">
        <select value={folder} onChange={(e) => setFolder(e.target.value)} aria-label="filter by folder">
          <option value="">all folders</option>
          {folders.map((f) => <option key={f} value={f}>{f}</option>)}
        </select>
        <select value={tag} onChange={(e) => setTag(e.target.value)} aria-label="filter by tag">
          <option value="">all tags</option>
          {tags.map((t) => <option key={t} value={t}>#{t}</option>)}
        </select>
        <label className="toggle">
          <input type="checkbox" checked={!!simEdges} onChange={(e) => onToggleSim?.(e.target.checked)} />
          <span>similarity edges</span>
        </label>
      </div>
      <div className="graph-wrap">
        <svg viewBox="0 0 600 464" role="img" aria-label="knowledge graph">
          <defs>
            <filter id="glow" x="-60%" y="-60%" width="220%" height="220%">
              <feGaussianBlur stdDeviation="2.4" result="b" /><feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
            </filter>
          </defs>
          {view.edges.map((e, i) => {
            const a = view.pos[e.source], b = view.pos[e.target]
            if (!a || !b) return null
            const lit = hover != null && (e.source === hover || e.target === hover)
            const similar = e.kind === 'similar'
            return <line key={i} x1={a.x} y1={a.y} x2={b.x} y2={b.y}
              stroke={lit ? (similar ? SIGNAL.violet : SIGNAL.teal) : similar ? '#584a78' : '#26304a'}
              strokeWidth={lit ? 1.4 : 0.7}
              strokeDasharray={similar ? '3 3' : undefined}
              strokeOpacity={hover == null ? 0.5 : lit ? 0.85 : 0.08}>
              {similar && <title>{`similarity ${(e.sim || 0).toFixed(2)}`}</title>}
            </line>
          })}
          {view.nodes.map((n) => {
            const r = 3.2 + Math.min(7, (n.words || 0) / 220) + (n.id === hover ? 2 : 0)
            const f = folderColor[(n.path || '').split('/')[0]] || SIGNAL.teal
            return (
              <circle key={n.id} cx={n.x} cy={n.y} r={r} fill={f}
                fillOpacity={dim(n.id) ? 0.22 : 1}
                filter={n.id === hover ? 'url(#glow)' : undefined}
                style={{ cursor: 'pointer', transition: 'fill-opacity .12s' }}
                onMouseEnter={() => setHover(n.id)} onMouseLeave={() => setHover(null)}>
                <title>{n.path}</title>
              </circle>
            )
          })}
          {hover != null && view.pos[hover] && (
            <text className="node-label" x={view.pos[hover].x} y={view.pos[hover].y - 11} textAnchor="middle">
              {(view.pos[hover].path || '').split('/').pop().replace(/\.md$/, '')}
            </text>
          )}
          {view.nodes.length === 0 && (
            <text x="300" y="232" textAnchor="middle" fill={SEMA.faint} fontSize="13">
              No notes match — adjust the filters, or run `axon reindex`.
            </text>
          )}
        </svg>
      </div>
      {folders.length > 0 && (
        <div className="graph-legend">
          {folders.map((f) => <span key={f}><i className="swatch" style={{ background: folderColor[f] }} />{f}</span>)}
        </div>
      )}
    </Card>
  )
}

/* ── activity feed ───────────────────────────────────────────────────────── */
// evtKey identifies an event across the two timestamp encodings it can arrive
// with (SSE serialises Go time.Time with sub-second precision + offset; DB rows
// store RFC3339 UTC) by normalising to epoch seconds — string comparison of the
// raw ts values can never match and every live event would duplicate once the
// history poll returns it.
const evtKey = (e) => `${e.kind}|${e.message}|${Math.floor(Date.parse(e.ts) / 1000)}`

function ActivityCard({ live, initial, span }) {
  const [level, setLevel] = useState('all')
  const merged = useMemo(() => {
    const seen = new Set(live.map(evtKey))
    const base = (initial || []).filter((e) => !seen.has(evtKey(e)))
    return [...live, ...base].slice(0, 300)
  }, [live, initial])
  const counts = useMemo(() => {
    const c = { all: merged.length, info: 0, warn: 0, error: 0 }
    merged.forEach((e) => { if (c[e.level] != null) c[e.level]++ })
    return c
  }, [merged])
  const shown = level === 'all' ? merged : merged.filter((e) => e.level === level)
  return (
    <Card title="Activity" meta={`${merged.length} events`} span={span}>
      <div className="filters">
        <div className="seg">
          {['all', 'info', 'warn', 'error'].map((l) => (
            <button key={l} className={level === l ? 'on' : ''} onClick={() => setLevel(l)}>{l} {counts[l] ? `· ${counts[l]}` : ''}</button>
          ))}
        </div>
      </div>
      <div className="feed">
        {shown.map((e, i) => (
          <div className={`evt lvl-${e.level}`} key={i}>
            <span className="t">{fmtTime(e.ts)}</span>
            {e.kind && <span className="kind">{e.kind}</span>}
            <span className="msg">{e.message}</span>
          </div>
        ))}
        {shown.length === 0 && <Empty>No activity yet. Runs, ingests and token events stream here live.</Empty>}
      </div>
    </Card>
  )
}


/* ── ask tab (ADR-023) ───────────────────────────────────────────────────── */
function postAsk(question) {
  return fetch('/api/ask', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Axon-Ask': '1' },
    body: JSON.stringify({ question }),
  }).then(async (r) => {
    if (!r.ok) throw new Error(await r.text())
    return r.json()
  })
}

function AskTab({ span }) {
  const [q, setQ] = useState('')
  const [busy, setBusy] = useState(false)
  const [ans, setAns] = useState(null)
  const [err, setErr] = useState(null)

  const submit = (e) => {
    e.preventDefault()
    if (!q.trim() || busy) return
    setBusy(true); setErr(null); setAns(null)
    postAsk(q.trim())
      .then(setAns)
      .catch((e2) => setErr(String(e2.message || e2)))
      .finally(() => setBusy(false))
  }

  return (
    <Card title="Ask your vault" meta="grounded — cites sources or refuses" span={span}>
      <form className="ask-form" onSubmit={submit}>
        <input className="ask-input" placeholder="Ask a question answered only from your notes…"
               value={q} onChange={(e) => setQ(e.target.value)} />
        <button type="submit" disabled={busy || !q.trim()}>{busy ? 'Asking…' : 'Ask'}</button>
      </form>
      {err && <Empty>{err}</Empty>}
      {ans && ans.refused && (
        <div className="ask-answer refused">
          <p><b>No answer:</b> {ans.reason}</p>
          {ans.sources?.length > 0 && (
            <><p className="ask-src-label">Retrieved (uncited):</p>
              <ul>{ans.sources.map((s) => <li key={s}>{s}</li>)}</ul></>
          )}
        </div>
      )}
      {ans && !ans.refused && (
        <div className="ask-answer">
          <p className="ask-text">{ans.answer}</p>
          <p className="ask-src-label">Sources:</p>
          <ul>{(ans.citations || []).map((c) => <li key={c}>{c}</li>)}</ul>
          <p className="ask-meta">~{ans.tokens} tokens</p>
        </div>
      )}
    </Card>
  )
}

/* ── review tab (ADR-020) ────────────────────────────────────────────────── */
function postReviewAction(id, action) {
  return fetch('/api/review/action', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Axon-Review': '1' },
    body: JSON.stringify({ id, action }),
  }).then(async (r) => {
    if (!r.ok) throw new Error(await r.text())
    return r.json()
  })
}

function ExportLinks({ dataset }) {
  return (
    <span className="export-links">
      <a href={`/api/export?dataset=${dataset}&format=csv`}>⤓ csv</a>
      <a href={`/api/export?dataset=${dataset}&format=json`}>⤓ json</a>
    </span>
  )
}

const KIND_LABEL = { link: 'Link suggestion', pair: 'Link suggestion', triage: 'Inbox triage', resurface: 'Resurfaced', info: 'Record' }

function ReviewTab({ span }) {
  const [nonce, setNonce] = useState(0)
  const { data, error } = useFetch(`/api/review?n=${nonce}`, 5000)
  const [busy, setBusy] = useState(null)
  const [errs, setErrs] = useState({})
  const items = data?.items || []
  const pending = items.filter((it) => !it.checked)
  const resolved = items.filter((it) => it.checked).slice(-15)

  const act = (id, action) => {
    setBusy(id)
    postReviewAction(id, action)
      .then(() => setErrs((e) => ({ ...e, [id]: null })))
      .catch((err) => setErrs((e) => ({ ...e, [id]: String(err.message || err) })))
      .finally(() => { setBusy(null); setNonce((n) => n + 1) })
  }

  const describe = (it) => {
    if (it.kind === 'triage') return <>move <b>[[{it.note}]]</b> → <b>{it.folder}</b>{it.tags?.length ? ` (${it.tags.join(', ')})` : ''}</>
    if (it.kind === 'link' || it.kind === 'pair') return <>link <b>[[{it.note}]]</b> → <b>[[{it.target}]]</b></>
    if (it.kind === 'resurface') return <>resurface <b>[[{it.target}]]</b> for <b>[[{it.note}]]</b></>
    return it.line.replace(/^- \[.\] /, '')
  }

  return (
    <Card title="Review queue" meta={`${pending.length} pending`} span={span}>
      {error && <Empty>daemon unreachable</Empty>}
      <div className="list">
        {pending.map((it) => (
          <div className="li review-item" key={it.id}>
            <span className={`kind kind-${it.kind}`}>{KIND_LABEL[it.kind] || it.kind}</span>
            <span className="msg">{describe(it)}</span>
            <span className="review-actions">
              {it.kind !== 'info' && (
                <button disabled={busy === it.id} onClick={() => act(it.id, 'accept')}>accept</button>
              )}
              <button className="ghost" disabled={busy === it.id} onClick={() => act(it.id, 'dismiss')}>dismiss</button>
            </span>
            {errs[it.id] && <span className="review-err">{errs[it.id]}</span>}
          </div>
        ))}
        {pending.length === 0 && !error && <Empty>Queue is clear. Automations append proposals here for your review.</Empty>}
      </div>
      {resolved.length > 0 && (
        <div className="list resolved">
          {resolved.map((it) => (
            <div className="li dim" key={it.id}><span className="msg">{it.line.replace(/^- \[.\] /, '')}</span></div>
          ))}
        </div>
      )}
    </Card>
  )
}

/* ── app shell ───────────────────────────────────────────────────────────── */
const TABS = [
  ['overview', 'Overview'], ['tokens', 'Tokens'], ['automations', 'Automations'], ['review', 'Review'],
  ['ask', 'Ask'], ['knowledge', 'Knowledge'], ['graph', 'Graph'], ['activity', 'Activity'],
]

export default function App() {
  const [tab, setTab] = useState('overview')
  const { data: health, error: healthErr } = useFetch('/health', 10000)
  const { data: usage, error: usageErr } = useFetch('/api/usage', 4000)
  const { data: tokens } = useFetch('/api/tokens', 8000)
  const { data: runs } = useFetch('/api/runs', 6000)
  const { data: vault } = useFetch('/api/vault', 8000)
  const { data: ingestion } = useFetch('/api/ingestion', 8000)
  const [simEdges, setSimEdges] = useState(false)
  const { data: graph } = useFetch(simEdges ? '/api/graph?similar=1' : '/api/graph', 15000)
  const { data: activity } = useFetch('/api/activity', 15000)
  const { events, connected } = useSSE()
  const { data: reviewMeta } = useFetch('/api/review', 15000)

  const healthy = health?.status === 'ok'
  const apiDown = healthErr || usageErr
  const byModel = useMemo(() => pieBy(tokens, (b) => b.model), [tokens])
  const byOp = useMemo(() => pieBy(tokens, (b) => shortOp(b.operation)), [tokens])

  return (
    <div className="app">
      <header className="topbar">
        <div className="topbar-inner">
          <div className="brand">
            <span className="brand-mark" />
            <span className="brand-name"><b>AX</b>ON</span>
            <span className="brand-sub">second-brain console</span>
          </div>
          <div className="topbar-spacer" />
          {apiDown && <span className="chip warn"><i className="dot" />daemon unreachable</span>}
          <span className={`chip ${healthy ? 'ok' : 'warn'}`}><i className="dot" />{health?.profile || '—'}</span>
          <span className={`chip ${health?.db ? 'ok' : 'warn'}`}><i className="dot" />db {health?.db ? 'ok' : '—'}</span>
          <span className={`chip ${connected ? 'live' : 'off'}`}><i className="dot" />{connected ? 'live' : 'offline'}</span>
        </div>
        <div className="signal-line" />
      </header>

      <nav className="nav">
        {TABS.filter(([id]) => id !== 'ask' || health?.ask_enabled !== false).map(([id, label]) => (
          <button key={id} className={tab === id ? 'active' : ''} onClick={() => setTab(id)}>{label}{id === 'review' && reviewMeta?.pending ? ` · ${reviewMeta.pending}` : ''}</button>
        ))}
      </nav>

      <ErrorBoundary key={tab}>
        <main className="grid">
          {tab === 'overview' && <>
            <VaultTiles vault={vault} ingestion={ingestion} span="span-8" />
            <BudgetCard usage={usage} span="span-4" />
            <TokenTrend tokens={tokens} span="span-8" />
            <RunsList runs={runs} span="span-4" limit={9} />
            <IngestTrend ingestion={ingestion} span="span-6" />
            <ActivityCard live={events} initial={activity} span="span-6" />
          </>}

          {tab === 'tokens' && <>
            <BudgetCard usage={usage} span="span-5" />
            <TokenTrend tokens={tokens} span="span-7" />
            <DonutCard title="By model" data={byModel} span="span-4" />
            <DonutCard title="By operation" data={byOp} span="span-4" />
            <Card title="Totals" span="span-4">
              <div className="tiles">
                <Tile label="Total" value={kfmt(sumField(tokens, 'input') + sumField(tokens, 'output'))} accent />
                <Tile label="Input" value={kfmt(sumField(tokens, 'input'))} />
                <Tile label="Output" value={kfmt(sumField(tokens, 'output'))} />
                <Tile label="Cache read" value={kfmt(sumField(tokens, 'cache_read'))} />
                <Tile label="Cache write" value={kfmt(sumField(tokens, 'cache_write'))} />
              </div>
            </Card>
            <TokensByOpModel tokens={tokens} span="span-12" />
            <div className="export-row"><ExportLinks dataset="tokens" /></div>
          </>}

          {tab === 'automations' && <>
            <RunStats runs={runs} span="span-12" />
            <RunsList runs={runs} span="span-12" limit={40} title="Run history" />
            <div className="export-row"><ExportLinks dataset="runs" /></div>
          </>}

          {tab === 'knowledge' && <>
            <VaultTiles vault={vault} ingestion={ingestion} span="span-12" />
            <GrowthCard vault={vault} span="span-7" />
            <FoldersCard graph={graph} span="span-5" />
            <IngestTrend ingestion={ingestion} span="span-12" />
            <div className="export-row"><ExportLinks dataset="ingestion" /><ExportLinks dataset="vault" /></div>
          </>}

          {tab === 'review' && <ReviewTab span="span-12" />}
          {tab === 'ask' && <AskTab span="span-12" />}

          {tab === 'graph' && <GraphCard graph={graph} simEdges={simEdges} onToggleSim={setSimEdges} span="span-12" />}

          {tab === 'activity' && <>
            <ActivityCard live={events} initial={activity} span="span-12" />
            <div className="export-row"><ExportLinks dataset="activity" /></div>
          </>}
        </main>
      </ErrorBoundary>
    </div>
  )
}
