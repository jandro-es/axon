import React, { useMemo } from 'react'
import {
  AreaChart, Area, BarChart, Bar, Cell,
  XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer,
} from 'recharts'
import { Card, Well, Tile, Empty, ChartTooltip, Skeleton } from '../ui.jsx'
import { usePalette } from '../theme.jsx'
import { kfmt, num, shortDay } from '../lib.js'

// Growth over the visible range, so the tiles can say "+9 this range" rather
// than only ever showing a running total that never appears to move.
function delta(growth, field) {
  const g = growth || []
  if (g.length < 2) return null
  const d = (g[g.length - 1][field] || 0) - (g[0][field] || 0)
  return d > 0 ? d : null
}

export function VaultTiles({ vault, ingestion, growth, span, loading }) {
  const v = vault?.stats || {}
  const ing = ingestion || {}
  const dNotes = delta(growth, 'notes')
  const dWords = delta(growth, 'words')
  return (
    <Card title="Vault" meta={vault ? 'derived from Markdown' : ''} span={span}>
      {loading && !vault ? <Skeleton h="tiles" /> : (
        <div className="tiles">
          <Tile label="Notes" value={num(v.notes)} accent sub={dNotes ? `+${num(dNotes)} in range` : undefined} />
          <Tile label="Links" value={num(v.links)} />
          <Tile label="Words" value={kfmt(v.words || 0)} sub={dWords ? `+${kfmt(dWords)} in range` : undefined} />
          <Tile label="Sources" value={num(v.sources)} />
          <Tile label="Inbox" value={num(v.inbox_backlog)} sub={v.inbox_backlog > 0 ? 'awaiting triage' : 'clear'} />
          <Tile label="Embed queue" value={num(ing.embedding_queue)} sub={ing.embedding_queue > 0 ? 'pending' : 'drained'} />
        </div>
      )}
    </Card>
  )
}

// Cumulative vault size over time (FR-60), derived from note-creation dates.
export function GrowthCard({ growth, span }) {
  const p = usePalette()
  const rows = growth || []
  return (
    <Card title="Vault growth" meta={rows.length ? `${rows.length} days` : ''} span={span}>
      {rows.length < 2 ? <Empty>Growth appears once your notes span more than one day in this range.</Empty> : (
        <Well>
          <ResponsiveContainer width="100%" height={220}>
            <AreaChart data={rows} margin={{ top: 6, right: 6, bottom: 0, left: -8 }}>
              <defs>
                <linearGradient id="gNotes" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor={p.signal1} stopOpacity={0.5} />
                  <stop offset="100%" stopColor={p.signal1} stopOpacity={0.02} />
                </linearGradient>
              </defs>
              <CartesianGrid vertical={false} stroke={p.grid} />
              <XAxis dataKey="day" tickFormatter={shortDay} fontSize={11} tickLine={false} axisLine={false} minTickGap={24} />
              <YAxis yAxisId="n" tickFormatter={kfmt} fontSize={11} tickLine={false} axisLine={false} width={38} />
              <YAxis yAxisId="w" orientation="right" tickFormatter={kfmt} fontSize={11} tickLine={false} axisLine={false} width={44} />
              <Tooltip content={<ChartTooltip />} labelFormatter={shortDay} />
              <Area yAxisId="n" type="stepAfter" dataKey="notes" name="notes" stroke={p.signal1} strokeWidth={1.6} fill="url(#gNotes)" />
              <Area yAxisId="w" type="stepAfter" dataKey="words" name="words" stroke={p.signal2} strokeWidth={1.2} fill="none" />
            </AreaChart>
          </ResponsiveContainer>
        </Well>
      )}
    </Card>
  )
}

export function IngestTrend({ series, span }) {
  const p = usePalette()
  const { rows, statuses } = useMemo(() => {
    const set = new Set(); const byDay = {}
    for (const b of series || []) {
      const s = b.status || 'unknown'
      set.add(s)
      byDay[b.day] = byDay[b.day] || { day: b.day }
      byDay[b.day][s] = (byDay[b.day][s] || 0) + b.count
    }
    return { rows: Object.values(byDay).sort((a, b) => (a.day || '').localeCompare(b.day || '')), statuses: [...set] }
  }, [series])

  return (
    <Card title="Ingestion" meta={`${rows.length}d`} span={span}>
      {rows.length === 0 ? (
        <Empty>Nothing ingested in this range. Add a source with <code>axon ingest &lt;url&gt;</code>.</Empty>
      ) : (
        <Well>
          <ResponsiveContainer width="100%" height={240}>
            <BarChart data={rows} margin={{ top: 6, right: 6, bottom: 0, left: -12 }}>
              <CartesianGrid vertical={false} stroke={p.grid} />
              <XAxis dataKey="day" tickFormatter={shortDay} fontSize={11} tickLine={false} axisLine={false} minTickGap={20} />
              <YAxis allowDecimals={false} fontSize={11} tickLine={false} axisLine={false} width={34} />
              <Tooltip content={<ChartTooltip />} labelFormatter={shortDay} cursor={{ fill: p.cursor }} />
              {statuses.map((s, i) => (
                <Bar key={s} dataKey={s} stackId="s" fill={s === 'failed' ? p.err : p.series[i % p.series.length]}
                     radius={i === statuses.length - 1 ? [3, 3, 0, 0] : 0} maxBarSize={34} />
              ))}
            </BarChart>
          </ResponsiveContainer>
        </Well>
      )}
    </Card>
  )
}

export function FoldersCard({ graph, span }) {
  const p = usePalette()
  const data = useMemo(() => {
    const m = {}
    for (const n of graph?.nodes || []) {
      const top = (n.path || '').split('/')[0]
      if (top) m[top] = (m[top] || 0) + 1
    }
    return Object.entries(m).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value)
  }, [graph])

  return (
    <Card title="Notes by folder" meta={`${data.length} folders`} span={span}>
      {data.length === 0 ? <Empty>No notes indexed yet. Run <code>axon reindex</code> to rebuild from Markdown.</Empty> : (
        <Well>
          <ResponsiveContainer width="100%" height={Math.max(130, data.length * 30)}>
            <BarChart data={data} layout="vertical" margin={{ left: 4, right: 14, top: 2, bottom: 2 }}>
              <XAxis type="number" hide allowDecimals={false} />
              <YAxis type="category" dataKey="name" width={110} fontSize={11} tickLine={false} axisLine={false} />
              <Tooltip content={<ChartTooltip />} cursor={{ fill: p.cursor }} />
              <Bar dataKey="value" name="notes" radius={[0, 4, 4, 0]} maxBarSize={18}>
                {data.map((d, i) => <Cell key={d.name} fill={p.series[i % p.series.length]} />)}
              </Bar>
            </BarChart>
          </ResponsiveContainer>
        </Well>
      )}
    </Card>
  )
}
