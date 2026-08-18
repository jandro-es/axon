import React, { useMemo } from 'react'
import {
  AreaChart, Area, BarChart, Bar, PieChart, Pie, Cell,
  RadialBarChart, RadialBar, PolarAngleAxis,
  XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer,
} from 'recharts'
import { Card, Well, Tile, Empty, ChartTooltip } from '../ui.jsx'
import { usePalette } from '../theme.jsx'
import { kfmt, num, shortDay, shortOp, sumField, tokensDaily } from '../lib.js'

/* ── budget ───────────────────────────────────────────────────────────── */
function gaugeColor(p, pct, paused) {
  if (paused) return p.err
  if (pct >= 80) return p.warn
  return p.signal1
}

function Gauge({ cap, pct, paused }) {
  const p = usePalette()
  const v = Math.max(0, Math.min(100, pct || 0))
  const fill = gaugeColor(p, v, paused)
  return (
    <div className="gauge">
      <ResponsiveContainer width="100%" height="100%">
        <RadialBarChart innerRadius="72%" outerRadius="100%" data={[{ value: v }]} startAngle={90} endAngle={-270}>
          <PolarAngleAxis type="number" domain={[0, 100]} tick={false} />
          <RadialBar background={{ fill: p.track }} dataKey="value" cornerRadius={9} fill={fill} isAnimationActive={false} />
        </RadialBarChart>
      </ResponsiveContainer>
      <div className="readout">
        <div className="pct" style={{ color: fill }}>{v.toFixed(0)}%</div>
        <div className="cap">{cap}</div>
      </div>
    </div>
  )
}

export function BudgetCard({ usage, span }) {
  const p = usePalette()
  const u = usage || {}
  const paused = !!u.guard_paused
  return (
    <Card
      title="Token budget"
      meta={paused ? `guard paused ≥ ${u.guard_pct}%` : 'guard armed'}
      span={span}
    >
      <div className="gauges">
        <Gauge cap="Today" pct={u.day_pct} paused={paused} />
        <Gauge cap="This week" pct={u.week_pct} paused={paused} />
        <div className="gauge-info">
          <div className="legend-row">
            <span className="k"><i className="swatch" style={{ background: gaugeColor(p, u.day_pct, paused) }} />Day</span>
            <span className="v">{num(u.day_used)} / {num(u.day_limit)}</span>
          </div>
          <div className="legend-row">
            <span className="k"><i className="swatch" style={{ background: gaugeColor(p, u.week_pct, paused) }} />Week</span>
            <span className="v">{num(u.week_used)} / {num(u.week_limit)}</span>
          </div>
          {(u.day_cost_cap > 0 || u.day_cost_used > 0) && (
            <div className="legend-row">
              <span className="k"><i className="swatch" style={{ background: gaugeColor(p, u.day_cost_pct, paused) }} />Cost today</span>
              <span className="v">${(u.day_cost_used || 0).toFixed(2)}{u.day_cost_cap > 0 ? ` / $${u.day_cost_cap.toFixed(2)}` : ''}</span>
            </div>
          )}
          <div className="legend-row">
            <span className="k">Automations</span>
            <span className="v" style={{ color: paused ? p.err : p.ok }}>{paused ? 'paused' : 'running'}</span>
          </div>
        </div>
      </div>
      {paused && u.guard_reason && (
        <p className="ask-meta" style={{ marginTop: 12 }}>
          The budget guard paused scheduled automations: {u.guard_reason}. Interactive work is unaffected.
        </p>
      )}
    </Card>
  )
}

/* ── spend over time ──────────────────────────────────────────────────── */
export function TokenTrend({ tokens, span, title = 'Token spend', meta }) {
  const p = usePalette()
  const daily = useMemo(() => tokensDaily(tokens), [tokens])
  const total = sumField(daily, 'total')
  return (
    <Card title={title} meta={meta ?? `${num(total)} tokens · ${daily.length}d`} span={span}>
      {daily.length === 0 ? (
        <Empty>No Claude usage in this range. Spend lands here as automations run.</Empty>
      ) : (
        <Well>
          <ResponsiveContainer width="100%" height={250}>
            <AreaChart data={daily} margin={{ top: 6, right: 8, bottom: 0, left: -8 }}>
              <defs>
                {[['gIn', p.signal2, 0.55], ['gOut', p.signal1, 0.6], ['gCr', p.signal3, 0.5], ['gCw', p.warn, 0.45]].map(([id, c, o]) => (
                  <linearGradient key={id} id={id} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={c} stopOpacity={o} />
                    <stop offset="100%" stopColor={c} stopOpacity={0.02} />
                  </linearGradient>
                ))}
              </defs>
              <CartesianGrid vertical={false} stroke={p.grid} />
              <XAxis dataKey="day" tickFormatter={shortDay} fontSize={11} tickLine={false} axisLine={false} minTickGap={24} />
              <YAxis tickFormatter={kfmt} fontSize={11} tickLine={false} axisLine={false} width={42} />
              <Tooltip content={<ChartTooltip />} labelFormatter={shortDay} />
              <Area type="monotone" dataKey="input" name="input" stackId="1" stroke={p.signal2} strokeWidth={1.5} fill="url(#gIn)" />
              <Area type="monotone" dataKey="output" name="output" stackId="1" stroke={p.signal1} strokeWidth={1.5} fill="url(#gOut)" />
              <Area type="monotone" dataKey="cacheRead" name="cache read" stackId="1" stroke={p.signal3} strokeWidth={1} fill="url(#gCr)" />
              <Area type="monotone" dataKey="cacheWrite" name="cache write" stackId="1" stroke={p.warn} strokeWidth={1} fill="url(#gCw)" />
            </AreaChart>
          </ResponsiveContainer>
        </Well>
      )}
    </Card>
  )
}

export function DonutCard({ title, data, span }) {
  const p = usePalette()
  const total = sumField(data, 'value')
  const color = (i) => p.series[i % p.series.length]
  return (
    <Card title={title} meta={kfmt(total)} span={span}>
      {data.length === 0 ? <Empty>Nothing recorded in this range.</Empty> : (
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
          <div style={{ width: 168, height: 168, flex: '0 0 auto' }}>
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Pie data={data} dataKey="value" nameKey="name" innerRadius={50} outerRadius={78} paddingAngle={2} stroke="none">
                  {data.map((d, i) => <Cell key={d.name} fill={color(i)} />)}
                </Pie>
                <Tooltip content={<ChartTooltip />} />
              </PieChart>
            </ResponsiveContainer>
          </div>
          <div className="list" style={{ flex: 1, minWidth: 150 }}>
            {data.map((d, i) => (
              <div className="legend-row" key={d.name} style={{ padding: '5px 0' }}>
                <span className="k">
                  <i className="swatch" style={{ background: color(i) }} />
                  <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 140 }} title={d.name}>{d.name}</span>
                </span>
                <span className="v">{kfmt(d.value)}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </Card>
  )
}

export function TokensByOpModel({ tokens, span }) {
  const p = usePalette()
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
      {rows.length === 0 ? <Empty>No token spend in this range.</Empty> : (
        <Well>
          <ResponsiveContainer width="100%" height={260}>
            <BarChart data={rows} margin={{ top: 6, right: 8, bottom: 0, left: -8 }}>
              <CartesianGrid vertical={false} stroke={p.grid} />
              <XAxis dataKey="day" tickFormatter={shortDay} fontSize={11} tickLine={false} axisLine={false} minTickGap={24} />
              <YAxis tickFormatter={kfmt} fontSize={11} tickLine={false} axisLine={false} width={42} />
              <Tooltip content={<ChartTooltip />} labelFormatter={shortDay} cursor={{ fill: p.cursor }} />
              {keys.map((k, i) => (
                <Bar key={k} dataKey={k} stackId="t" fill={p.series[i % p.series.length]}
                     radius={i === keys.length - 1 ? [3, 3, 0, 0] : 0} maxBarSize={34} />
              ))}
            </BarChart>
          </ResponsiveContainer>
        </Well>
      )}
      {keys.length > 0 && (
        <div className="graph-legend">
          {keys.map((k, i) => (
            <span key={k}><i className="swatch" style={{ background: p.series[i % p.series.length] }} />{k}</span>
          ))}
        </div>
      )}
    </Card>
  )
}

// Totals for the range, including the cache hit rate — the single number that
// says whether prompt caching is doing its job.
export function TokenTotals({ tokens, span }) {
  const input = sumField(tokens, 'input')
  const output = sumField(tokens, 'output')
  const cacheRead = sumField(tokens, 'cache_read')
  const cacheWrite = sumField(tokens, 'cache_write')
  const served = input + cacheRead
  const hit = served > 0 ? Math.round((cacheRead / served) * 100) : 0
  return (
    <Card title="Totals" meta="this range" span={span}>
      <div className="tiles">
        <Tile label="Billed" value={kfmt(input + output)} accent sub="input + output" />
        <Tile label="Input" value={kfmt(input)} />
        <Tile label="Output" value={kfmt(output)} />
        <Tile label="Cache read" value={kfmt(cacheRead)} />
        <Tile label="Cache write" value={kfmt(cacheWrite)} />
        <Tile label="Cache hit" value={hit} unit="%" sub="of prompt tokens" />
      </div>
    </Card>
  )
}
