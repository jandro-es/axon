import React, { useMemo, useState } from 'react'
import {
  BarChart, Bar, PieChart, Pie, Cell,
  CartesianGrid, XAxis, YAxis, Tooltip, ResponsiveContainer,
} from 'recharts'
import { Card, Well, Tile, Empty, ChartTooltip, Segmented } from '../ui.jsx'
import { usePalette } from '../theme.jsx'
import { fmtAge, fmtDur, fuzzy, kfmt, num, shortDay, truncate } from '../lib.js'

const statusColor = (p) => ({ ok: p.ok, failed: p.err, skipped: p.inkFaint, 'dry-run': p.warn })

export function RunsList({ runs, span, limit = 14, title = 'Recent automations', filterable }) {
  const [status, setStatus] = useState('all')
  const [q, setQ] = useState('')

  const shown = useMemo(() => {
    let rows = runs || []
    if (status !== 'all') rows = rows.filter((r) => r.status === status)
    if (q.trim()) rows = rows.filter((r) => fuzzy(q, `${r.automation} ${r.skip_reason || ''} ${r.error || ''}`))
    return rows.slice(0, limit)
  }, [runs, status, q, limit])

  return (
    <Card title={title} meta={runs ? `${num(runs.length)} runs` : ''} span={span}>
      {filterable && (
        <div className="filters">
          <Segmented value={status} onChange={setStatus} label="Filter by status"
                     options={['all', 'ok', 'failed', 'skipped']} />
          <span className="spacer" />
          <input className="input" placeholder="Filter runs…" value={q} onChange={(e) => setQ(e.target.value)}
                 aria-label="Filter runs" />
        </div>
      )}
      <div className="list">
        {shown.map((r) => (
          <div className="li" key={r.id}>
            <span className={`sdot ${r.status}`} />
            <span className="grow">
              {r.automation}
              {r.skip_reason ? <span style={{ color: 'var(--ink-faint)' }}> · {r.skip_reason}</span> : ''}
              {r.status === 'failed' && r.error
                ? <span style={{ color: 'var(--err)' }} title={r.error}> · {truncate(r.error, 110)}</span>
                : ''}
            </span>
            {r.tokens > 0 && <span className="mono">{kfmt(r.tokens)} tok</span>}
            <span className="mono">{r.finished_at ? fmtDur(r.started_at, r.finished_at) : '…'}</span>
            <span className={`badge ${r.status}`}>{r.status}</span>
          </div>
        ))}
        {shown.length === 0 && (
          <Empty>
            {runs && runs.length > 0
              ? <>No run matches this filter.</>
              : <>No automation runs in this range. They appear as the scheduler fires, or run one now with <code>axon run &lt;name&gt;</code>.</>}
          </Empty>
        )}
      </div>
    </Card>
  )
}

export function RunStats({ runs, span }) {
  const p = usePalette()
  const colors = statusColor(p)
  const { dist, daily, statuses, ok, failed, tokens } = useMemo(() => {
    const d = {}, byDay = {}, seen = new Set()
    let okN = 0, failN = 0, tok = 0
    for (const r of runs || []) {
      d[r.status] = (d[r.status] || 0) + 1
      seen.add(r.status)
      const day = (r.started_at || '').slice(0, 10)
      if (day) {
        byDay[day] = byDay[day] || { day }
        byDay[day][r.status] = (byDay[day][r.status] || 0) + 1
      }
      if (r.status === 'ok') okN++
      if (r.status === 'failed') failN++
      tok += r.tokens || 0
    }
    return {
      dist: Object.entries(d).map(([name, value]) => ({ name, value })),
      daily: Object.values(byDay).sort((a, b) => a.day.localeCompare(b.day)),
      statuses: ['ok', 'skipped', 'dry-run', 'failed'].filter((st) => seen.has(st)),
      ok: okN, failed: failN, tokens: tok,
    }
  }, [runs])

  const total = (runs || []).length
  const rate = ok + failed > 0 ? Math.round((ok / (ok + failed)) * 100) : null

  return (
    <Card title="Run outcomes" meta={`${num(total)} runs`} span={span}>
      {total === 0 ? <Empty>No runs to summarise in this range.</Empty> : (
        <>
          <div className="tiles" style={{ marginBottom: 14 }}>
            <Tile label="Runs" value={num(total)} accent />
            <Tile label="Succeeded" value={num(ok)} />
            <Tile label="Failed" value={num(failed)} />
            <Tile label="Success rate" value={rate == null ? '—' : rate} unit={rate == null ? '' : '%'} sub="excludes skips" />
            <Tile label="Tokens" value={kfmt(tokens)} />
          </div>
          <Well><div style={{ display: 'flex', gap: 14, flexWrap: 'wrap', alignItems: 'center' }}>
            <div style={{ width: 150, height: 150, flex: '0 0 auto' }}>
              <ResponsiveContainer width="100%" height="100%">
                <PieChart>
                  <Pie data={dist} dataKey="value" nameKey="name" innerRadius={44} outerRadius={70} paddingAngle={2} stroke="none">
                    {dist.map((d) => <Cell key={d.name} fill={colors[d.name] || p.inkFaint} />)}
                  </Pie>
                  <Tooltip content={<ChartTooltip />} />
                </PieChart>
              </ResponsiveContainer>
            </div>
            <div style={{ flex: 1, minWidth: 260 }}>
              <ResponsiveContainer width="100%" height={160}>
                <BarChart data={daily} margin={{ left: -16, right: 8, top: 6, bottom: 0 }}>
                  <CartesianGrid vertical={false} stroke={p.grid} />
                  <XAxis dataKey="day" tickFormatter={shortDay} fontSize={11} tickLine={false} axisLine={false} minTickGap={22} />
                  <YAxis allowDecimals={false} fontSize={11} tickLine={false} axisLine={false} width={34} />
                  <Tooltip content={<ChartTooltip />} labelFormatter={shortDay} cursor={{ fill: p.cursor }} />
                  {statuses.map((st, i) => (
                    <Bar key={st} dataKey={st} name={st} stackId="s" fill={colors[st] || p.inkFaint}
                         radius={i === statuses.length - 1 ? [3, 3, 0, 0] : 0} maxBarSize={26} />
                  ))}
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div></Well>
        </>
      )}
    </Card>
  )
}

/* Per-automation reliability: the view that answers "which automation is
   costing me, and which one is quietly failing?" — neither is legible from a
   flat run list once there are more than a screenful of runs. */
export function AutomationTable({ runs, span }) {
  const rows = useMemo(() => {
    const by = {}
    for (const r of runs || []) {
      const a = (by[r.automation] = by[r.automation] || {
        name: r.automation, runs: 0, ok: 0, failed: 0, skipped: 0, tokens: 0, durMs: 0, durN: 0, last: null, lastStatus: null,
      })
      a.runs++
      if (r.status === 'ok') a.ok++
      else if (r.status === 'failed') a.failed++
      else if (r.status === 'skipped') a.skipped++
      a.tokens += r.tokens || 0
      if (r.finished_at) {
        const ms = new Date(r.finished_at) - new Date(r.started_at)
        if (isFinite(ms) && ms >= 0) { a.durMs += ms; a.durN++ }
      }
      if (!a.last || (r.started_at || '') > a.last) { a.last = r.started_at; a.lastStatus = r.status }
    }
    return Object.values(by).sort((x, y) => y.tokens - x.tokens || y.runs - x.runs)
  }, [runs])

  return (
    <Card title="By automation" meta={`${rows.length} automations`} span={span}>
      {rows.length === 0 ? <Empty>Nothing has run in this range.</Empty> : (
        <div className="list">
          {rows.map((a) => {
            const graded = a.ok + a.failed
            const rate = graded > 0 ? Math.round((a.ok / graded) * 100) : null
            const avg = a.durN > 0 ? a.durMs / a.durN / 1000 : null
            return (
              <div className="li" key={a.name}>
                <span className={`sdot ${a.lastStatus}`} />
                <span className="grow" title={`last run ${a.last || '—'}`}>{a.name}</span>
                <span className="mono">{num(a.runs)}×</span>
                {a.skipped > 0 && <span className="mono" style={{ color: 'var(--ink-faint)' }}>{a.skipped} skip</span>}
                {avg != null && <span className="mono">{avg < 60 ? `${avg.toFixed(1)}s` : `${Math.round(avg / 60)}m`}</span>}
                <span className="mono">{a.tokens > 0 ? `${kfmt(a.tokens)} tok` : '—'}</span>
                <span className={`badge ${rate == null ? 'skipped' : rate >= 90 ? 'ok' : rate >= 70 ? 'dry-run' : 'failed'}`}>
                  {rate == null ? 'n/a' : `${rate}%`}
                </span>
                <span className="mono" style={{ minWidth: 78, textAlign: 'right' }}>{a.last ? fmtAge(a.last) : '—'}</span>
              </div>
            )
          })}
        </div>
      )}
    </Card>
  )
}
