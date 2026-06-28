import React, { useEffect, useMemo, useState } from 'react'
import {
  BarChart, Bar, XAxis, YAxis, Tooltip, Legend, ResponsiveContainer, CartesianGrid,
} from 'recharts'

const COLORS = ['#5b8def', '#3ecf8e', '#e6a23c', '#f06363', '#a78bfa', '#22d3ee', '#f472b6', '#94a3b8']

// useFetch polls a JSON endpoint on an interval.
function useFetch(url, interval = 5000) {
  const [data, setData] = useState(null)
  useEffect(() => {
    let alive = true
    const load = () =>
      fetch(url).then((r) => r.json()).then((d) => alive && setData(d)).catch(() => {})
    load()
    const id = setInterval(load, interval)
    return () => { alive = false; clearInterval(id) }
  }, [url, interval])
  return data
}

// useSSE accumulates live events from the daemon's /events stream.
function useSSE() {
  const [events, setEvents] = useState([])
  const [connected, setConnected] = useState(false)
  useEffect(() => {
    const es = new EventSource('/events')
    es.onopen = () => setConnected(true)
    es.onerror = () => setConnected(false)
    es.onmessage = (e) => pushEvent(e)
    // Named events (kind) also arrive; capture them generically.
    const handler = (e) => pushEvent(e)
    function pushEvent(e) {
      try {
        const ev = JSON.parse(e.data)
        setEvents((prev) => [ev, ...prev].slice(0, 300))
      } catch {}
    }
    ;['ingest.done', 'automation.run', 'automation.skip', 'automation.fail', 'token.ledger', 'token.deny', 'budget'].forEach(
      (k) => es.addEventListener(k, handler),
    )
    return () => es.close()
  }, [])
  return { events, connected }
}

function Gauge({ label, used, limit, pct, paused }) {
  const p = Math.min(100, pct || 0)
  return (
    <div style={{ marginBottom: 14 }}>
      <div className="row" style={{ border: 'none', padding: 0 }}>
        <span className="muted">{label}</span>
        <span>{(used || 0).toLocaleString()} / {(limit || 0).toLocaleString()} ({p.toFixed(1)}%)</span>
      </div>
      <div className="bar"><span className={paused ? 'warn' : ''} style={{ width: `${p}%` }} /></div>
    </div>
  )
}

function UsageCard({ usage }) {
  if (!usage) return <div className="card"><h2>Usage & budget</h2><span className="muted">loading…</span></div>
  return (
    <div className="card">
      <h2>Usage & budget</h2>
      <Gauge label="Day" used={usage.day_used} limit={usage.day_limit} pct={usage.day_pct} paused={usage.guard_paused} />
      <Gauge label="Week" used={usage.week_used} limit={usage.week_limit} pct={usage.week_pct} paused={usage.guard_paused} />
      <div className="muted" style={{ marginTop: 6 }}>
        budget-guard: {usage.guard_paused ? <span className="status-failed">PAUSED (≥ {usage.guard_pct}%)</span> : <span className="status-ok">ok</span>}
      </div>
    </div>
  )
}

function VaultCard({ vault }) {
  if (!vault) return null
  const kpis = [
    ['Notes', vault.notes], ['Links', vault.links], ['Words', vault.words],
    ['Sources', vault.sources], ['Inbox backlog', vault.inbox_backlog],
  ]
  return (
    <div className="card">
      <h2>Vault growth</h2>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        {kpis.map(([k, v]) => (
          <div key={k}><div className="kpi">{(v || 0).toLocaleString()}</div><small className="muted">{k}</small></div>
        ))}
      </div>
    </div>
  )
}

// TokensCard stacks token spend by automation × model per day.
function TokensCard({ tokens }) {
  const { rows, keys } = useMemo(() => {
    if (!tokens || !tokens.length) return { rows: [], keys: [] }
    const keySet = new Set()
    const byDay = {}
    for (const b of tokens) {
      const key = `${shortOp(b.operation)} · ${b.model}`
      keySet.add(key)
      byDay[b.day] = byDay[b.day] || { day: b.day }
      byDay[b.day][key] = (byDay[b.day][key] || 0) + (b.input || 0) + (b.output || 0)
    }
    return { rows: Object.values(byDay), keys: [...keySet] }
  }, [tokens])

  return (
    <div className="card full">
      <h2>Tokens — by automation × model</h2>
      {rows.length === 0 ? <span className="muted">no token spend yet</span> : (
        <ResponsiveContainer width="100%" height={260}>
          <BarChart data={rows}>
            <CartesianGrid stroke="#2a2f3a" vertical={false} />
            <XAxis dataKey="day" stroke="#8b93a7" fontSize={11} />
            <YAxis stroke="#8b93a7" fontSize={11} />
            <Tooltip contentStyle={{ background: '#1d212b', border: '1px solid #2a2f3a' }} />
            <Legend wrapperStyle={{ fontSize: 11 }} />
            {keys.map((k, i) => (
              <Bar key={k} dataKey={k} stackId="t" fill={COLORS[i % COLORS.length]} />
            ))}
          </BarChart>
        </ResponsiveContainer>
      )}
    </div>
  )
}

function RunsCard({ runs }) {
  return (
    <div className="card">
      <h2>Automation runs</h2>
      <div className="feed">
        {(runs || []).map((r) => (
          <div className="row" key={r.id}>
            <span>{r.automation}</span>
            <span className={`status-${r.status}`}>{r.status}{r.skip_reason ? ` (${r.skip_reason})` : ''}</span>
          </div>
        ))}
        {(!runs || runs.length === 0) && <span className="muted">no runs yet</span>}
      </div>
    </div>
  )
}

function IngestionCard({ ingestion }) {
  if (!ingestion) return null
  const total = (ingestion.series || []).reduce((a, b) => a + b.count, 0)
  return (
    <div className="card">
      <h2>Ingestion</h2>
      <div className="kpi">{total}<small> sources</small></div>
      <div className="row"><span className="muted">embedding queue</span><span>{ingestion.embedding_queue}</span></div>
      {(ingestion.series || []).map((b, i) => (
        <div className="row" key={i}><span className="muted">{b.day} · {b.status}</span><span>{b.count}</span></div>
      ))}
    </div>
  )
}

// GraphCard renders the knowledge graph (notes + wikilinks) with folder/tag filters.
function GraphCard({ graph }) {
  const [folder, setFolder] = useState('')
  const [tag, setTag] = useState('')
  const folders = useMemo(() => {
    const s = new Set()
    ;(graph?.nodes || []).forEach((n) => { const top = n.path.split('/')[0]; if (top) s.add(top) })
    return [...s].sort()
  }, [graph])
  const tags = useMemo(() => {
    const s = new Set()
    ;(graph?.nodes || []).forEach((n) => parseTags(n.tags).forEach((t) => s.add(t)))
    return [...s].sort()
  }, [graph])

  const view = useMemo(() => {
    if (!graph) return { nodes: [], edges: [] }
    let nodes = graph.nodes
    if (folder) nodes = nodes.filter((n) => n.path.startsWith(folder + '/'))
    if (tag) nodes = nodes.filter((n) => parseTags(n.tags).includes(tag))
    const ids = new Set(nodes.map((n) => n.id))
    const edges = graph.edges.filter((e) => ids.has(e.source) && ids.has(e.target))
    // Deterministic circular layout.
    const N = nodes.length
    const placed = nodes.map((n, i) => {
      const a = (2 * Math.PI * i) / Math.max(1, N)
      return { ...n, x: 200 + 160 * Math.cos(a), y: 170 + 140 * Math.sin(a) }
    })
    const pos = Object.fromEntries(placed.map((n) => [n.id, n]))
    return { nodes: placed, edges, pos }
  }, [graph, folder, tag])

  return (
    <div className="card full">
      <h2>Knowledge graph — {view.nodes.length} notes, {view.edges.length} links</h2>
      <div className="filters">
        <select value={folder} onChange={(e) => setFolder(e.target.value)}>
          <option value="">all folders</option>
          {folders.map((f) => <option key={f} value={f}>{f}</option>)}
        </select>
        <select value={tag} onChange={(e) => setTag(e.target.value)}>
          <option value="">all tags</option>
          {tags.map((t) => <option key={t} value={t}>#{t}</option>)}
        </select>
      </div>
      <svg width="100%" viewBox="0 0 400 340" style={{ background: '#13161d', borderRadius: 8 }}>
        {view.edges.map((e, i) => {
          const a = view.pos[e.source], b = view.pos[e.target]
          if (!a || !b) return null
          return <line key={i} x1={a.x} y1={a.y} x2={b.x} y2={b.y} stroke="#2a2f3a" strokeWidth="1" />
        })}
        {view.nodes.map((n) => (
          <g key={n.id}>
            <circle cx={n.x} cy={n.y} r={4 + Math.min(6, (n.words || 0) / 200)} fill={COLORS[(n.type || '').length % COLORS.length]}>
              <title>{n.path}</title>
            </circle>
          </g>
        ))}
        {view.nodes.length === 0 && <text x="200" y="170" textAnchor="middle">no notes match</text>}
      </svg>
    </div>
  )
}

function ActivityCard({ live, initial }) {
  const merged = useMemo(() => {
    const seen = new Set(live.map((e) => e.message + e.ts))
    const base = (initial || []).filter((e) => !seen.has(e.message + e.ts))
    return [...live, ...base].slice(0, 200)
  }, [live, initial])
  return (
    <div className="card full">
      <h2>Activity feed</h2>
      <div className="feed">
        {merged.map((e, i) => (
          <div className={`evt lvl-${e.level}`} key={i}>
            <span className="muted">{fmtTime(e.ts)}</span> [{e.kind}] {e.message}
          </div>
        ))}
        {merged.length === 0 && <span className="muted">no activity yet</span>}
      </div>
    </div>
  )
}

export default function App() {
  const [tab, setTab] = useState('overview')
  const health = useFetch('/health', 10000)
  const usage = useFetch('/api/usage', 4000)
  const tokens = useFetch('/api/tokens', 8000)
  const runs = useFetch('/api/runs', 6000)
  const vault = useFetch('/api/vault', 8000)
  const ingestion = useFetch('/api/ingestion', 8000)
  const graph = useFetch('/api/graph', 15000)
  const activity = useFetch('/api/activity', 15000)
  const { events, connected } = useSSE()

  const tabs = ['overview', 'tokens', 'runs', 'ingestion', 'graph', 'activity']
  return (
    <>
      <div className="header">
        <h1>AXON</h1>
        <span className={`pill ${health?.status === 'ok' ? 'ok' : 'warn'}`}>{health?.profile || '—'} · {health?.status || '…'}</span>
        <span className={`pill ${connected ? 'ok' : 'warn'}`}>{connected ? 'live' : 'offline'}</span>
      </div>
      <div className="tabs">
        {tabs.map((t) => (
          <button key={t} className={`tab ${tab === t ? 'active' : ''}`} onClick={() => setTab(t)}>{t}</button>
        ))}
      </div>
      <div className="grid">
        {tab === 'overview' && <><UsageCard usage={usage} /><VaultCard vault={vault} /><RunsCard runs={runs} /><TokensCard tokens={tokens} /></>}
        {tab === 'tokens' && <><TokensCard tokens={tokens} /><UsageCard usage={usage} /></>}
        {tab === 'runs' && <RunsCard runs={runs} />}
        {tab === 'ingestion' && <><IngestionCard ingestion={ingestion} /><VaultCard vault={vault} /></>}
        {tab === 'graph' && <GraphCard graph={graph} />}
        {tab === 'activity' && <ActivityCard live={events} initial={activity} />}
      </div>
    </>
  )
}

function shortOp(op) { return (op || '').replace(/^automation\./, '').replace(/^ingest\./, 'ingest:') }
function parseTags(t) { try { const a = JSON.parse(t || '[]'); return Array.isArray(a) ? a : [] } catch { return [] } }
function fmtTime(ts) { try { return new Date(ts).toLocaleTimeString() } catch { return ts } }
