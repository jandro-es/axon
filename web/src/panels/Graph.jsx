import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Card, Empty } from '../ui.jsx'
import { usePalette } from '../theme.jsx'
import { noteName, num, parseTags, vaultURI } from '../lib.js'

const PAD = 40                  // breathing room when the result is fitted to frame
const MAX_NODES = 400           // rendered cap — announced, never silent

/* A small deterministic force layout. Deterministic matters more than it
   sounds: /api/graph is polled, and a random layout would reshuffle the whole
   map every few seconds. Same nodes and edges in, same picture out.

   Kept hand-rolled rather than pulling in d3-force: the dependency list for
   this SPA is React + Recharts, and an ADR is the price of a third. */
function layout(nodes, edges, W, H) {
  const n = nodes.length
  if (n === 0) return []
  const idx = new Map(nodes.map((node, i) => [node.id, i]))
  const x = new Float64Array(n), y = new Float64Array(n)
  const vx = new Float64Array(n), vy = new Float64Array(n)
  const deg = new Float64Array(n)

  // Phyllotaxis seed, stretched to the frame's aspect: even coverage, no clumps
  // to untangle, no randomness — and a wide panel gets a wide map rather than a
  // disc marooned in the middle of it.
  const golden = Math.PI * (3 - Math.sqrt(5))
  for (let i = 0; i < n; i++) {
    const t = Math.sqrt((i + 0.5) / n)
    x[i] = W / 2 + 0.5 * (W - 2 * PAD) * t * Math.cos(i * golden)
    y[i] = H / 2 + 0.5 * (H - 2 * PAD) * t * Math.sin(i * golden)
  }

  const links = []
  for (const e of edges) {
    const a = idx.get(e.source), b = idx.get(e.target)
    if (a == null || b == null || a === b) continue
    links.push([a, b, e.kind === 'similar' ? 0.35 : 1])
    deg[a]++; deg[b]++
  }

  const iterations = n > 260 ? 130 : n > 120 ? 190 : 260
  const repulsion = 5200
  const linkDist = 110
  // Just enough to keep unlinked notes from drifting off — and weaker along the
  // long axis, so the equilibrium shape follows the frame instead of fighting it.
  const gravity = 0.0045
  const gx = gravity * Math.min(1, H / W)
  const gy = gravity * Math.min(1, W / H)
  let alpha = 1

  for (let step = 0; step < iterations; step++) {
    // Repulsion — O(n²), which is why the node count is capped.
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        let dx = x[i] - x[j], dy = y[i] - y[j]
        let d2 = dx * dx + dy * dy
        if (d2 < 1) { dx = (i - j) * 0.01 + 0.5; dy = 0.5; d2 = 1 }
        const f = repulsion / d2
        const d = Math.sqrt(d2)
        const fx = (dx / d) * f, fy = (dy / d) * f
        vx[i] += fx; vy[i] += fy
        vx[j] -= fx; vy[j] -= fy
      }
    }
    // Springs along links.
    for (const [a, b, w] of links) {
      const dx = x[b] - x[a], dy = y[b] - y[a]
      const d = Math.sqrt(dx * dx + dy * dy) || 0.01
      const f = ((d - linkDist) / d) * 0.35 * w
      vx[a] += dx * f; vy[a] += dy * f
      vx[b] -= dx * f; vy[b] -= dy * f
    }
    // Gravity toward the centre keeps orphan notes on screen.
    for (let i = 0; i < n; i++) {
      vx[i] += (W / 2 - x[i]) * gx
      vy[i] += (H / 2 - y[i]) * gy
      x[i] += vx[i] * alpha
      y[i] += vy[i] * alpha
      vx[i] *= 0.72; vy[i] *= 0.72
      x[i] = Math.max(18, Math.min(W - 18, x[i]))
      y[i] = Math.max(18, Math.min(H - 18, y[i]))
    }
    alpha *= 0.985
  }

  // Fit the result to the frame. Without this the picture depends on how the
  // forces happened to balance — a small vault collapses into a dot in the
  // middle of an empty rectangle, which is exactly what a graph must not do.
  let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity
  for (let i = 0; i < n; i++) {
    if (x[i] < minX) minX = x[i]
    if (x[i] > maxX) maxX = x[i]
    if (y[i] < minY) minY = y[i]
    if (y[i] > maxY) maxY = y[i]
  }
  const spanX = Math.max(1, maxX - minX), spanY = Math.max(1, maxY - minY)
  const scale = Math.min((W - 2 * PAD) / spanX, (H - 2 * PAD) / spanY)
  const offX = (W - spanX * scale) / 2, offY = (H - spanY * scale) / 2

  return nodes.map((node, i) => ({
    ...node,
    x: offX + (x[i] - minX) * scale,
    y: offY + (y[i] - minY) * scale,
    degree: deg[i],
  }))
}

/* Hubs and orphans: the two questions a map answers badly and a list answers
   well — what is this vault built around, and what is floating unattached? */
export function GraphInsights({ graph, vault, span }) {
  const nodes = graph?.nodes || []
  const edges = graph?.edges || []

  const { hubs, orphans } = useMemo(() => {
    const deg = new Map()
    edges.forEach((e) => {
      if (e.kind === 'similar') return
      deg.set(e.source, (deg.get(e.source) || 0) + 1)
      deg.set(e.target, (deg.get(e.target) || 0) + 1)
    })
    const scored = nodes.map((n) => ({ ...n, degree: deg.get(n.id) || 0 }))
    return {
      hubs: scored.filter((n) => n.degree > 0).sort((a, b) => b.degree - a.degree).slice(0, 8),
      orphans: scored.filter((n) => n.degree === 0),
    }
  }, [nodes, edges])

  const row = (n, right) => {
    const uri = vaultURI(vault, n.path)
    return (
      <div className="li" key={n.id}>
        <span className="grow" title={n.path}>
          {uri ? <a className="note-link" href={uri}>{noteName(n.path)}</a> : noteName(n.path)}
        </span>
        <span className="mono">{right}</span>
      </div>
    )
  }

  return (
    <Card title="Hubs & orphans" meta={`${num(orphans.length)} unlinked`} span={span}>
      {nodes.length === 0 ? <Empty>Nothing indexed yet.</Empty> : (
        <>
          <div className="eyebrow" style={{ marginBottom: 6 }}><span>Most linked</span></div>
          <div className="list">
            {hubs.map((n) => row(n, `${n.degree} link${n.degree === 1 ? '' : 's'}`))}
            {hubs.length === 0 && <Empty>No wikilinks between notes yet.</Empty>}
          </div>
          <div className="eyebrow" style={{ margin: '18px 0 6px' }}><span>Unlinked notes</span></div>
          <div className="list">
            {orphans.slice(0, 8).map((n) => row(n, `${num(n.words || 0)} w`))}
            {orphans.length > 8 && (
              <div className="li"><span className="grow" style={{ color: 'var(--ink-faint)' }}>
                and {num(orphans.length - 8)} more
              </span></div>
            )}
            {orphans.length === 0 && <Empty>Every note is connected to something.</Empty>}
          </div>
        </>
      )}
    </Card>
  )
}

export function GraphCard({ graph, simEdges, onToggleSim, vault, span }) {
  const p = usePalette()
  const [folder, setFolder] = useState('')
  const [tag, setTag] = useState('')
  const [hover, setHover] = useState(null)
  const [picked, setPicked] = useState(null)
  const [view, setView] = useState({ k: 1, x: 0, y: 0 })
  const [dragging, setDragging] = useState(false)
  const svgRef = useRef(null)
  const wrapRef = useRef(null)
  const drag = useRef(null)
  // The layout runs in the frame's own aspect ratio. A fixed viewBox would
  // letterbox the map on a wide window and waste half the panel.
  const [box, setBox] = useState({ w: 1200, h: 560 })

  useEffect(() => {
    const el = wrapRef.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(([entry]) => {
      const { width, height } = entry.contentRect
      if (width > 0 && height > 0) {
        setBox((b) => (Math.abs(b.w - width) < 24 && Math.abs(b.h - height) < 24 ? b : { w: width, h: height }))
      }
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const nodesAll = graph?.nodes || []
  const edgesAll = graph?.edges || []

  const folders = useMemo(() => {
    const s = new Set()
    nodesAll.forEach((n) => { const t = (n.path || '').split('/')[0]; if (t) s.add(t) })
    return [...s].sort()
  }, [nodesAll])
  const tags = useMemo(() => {
    const s = new Set()
    nodesAll.forEach((n) => parseTags(n.tags).forEach((t) => s.add(t)))
    return [...s].sort()
  }, [nodesAll])
  const folderColor = useMemo(
    () => Object.fromEntries(folders.map((f, i) => [f, p.series[i % p.series.length]])),
    [folders, p],
  )

  // Selection: filter, then keep the best-connected MAX_NODES so a large vault
  // renders the part of the map that carries information.
  const selection = useMemo(() => {
    let nodes = nodesAll
    if (folder) nodes = nodes.filter((n) => (n.path || '').startsWith(folder + '/'))
    if (tag) nodes = nodes.filter((n) => parseTags(n.tags).includes(tag))
    const ids = new Set(nodes.map((n) => n.id))
    let edges = edgesAll.filter((e) => ids.has(e.source) && ids.has(e.target))
    const truncated = nodes.length > MAX_NODES
    if (truncated) {
      const deg = new Map()
      edges.forEach((e) => {
        deg.set(e.source, (deg.get(e.source) || 0) + 1)
        deg.set(e.target, (deg.get(e.target) || 0) + 1)
      })
      nodes = [...nodes].sort((a, b) => (deg.get(b.id) || 0) - (deg.get(a.id) || 0)).slice(0, MAX_NODES)
      const keep = new Set(nodes.map((n) => n.id))
      edges = edges.filter((e) => keep.has(e.source) && keep.has(e.target))
    }
    return { nodes, edges, truncated, total: ids.size }
  }, [nodesAll, edgesAll, folder, tag])

  // Re-run the layout only when the shape of the graph changes, not on every
  // 15s poll — otherwise the map would twitch under the pointer.
  const signature = useMemo(
    () => `${selection.nodes.map((n) => n.id).join(',')}|${selection.edges.length}`,
    [selection],
  )
  const placed = useMemo(
    () => layout(selection.nodes, selection.edges, box.w, box.h),
    [signature, box.w, box.h], // eslint-disable-line react-hooks/exhaustive-deps
  )

  const pos = useMemo(() => Object.fromEntries(placed.map((n) => [n.id, n])), [placed])
  const adj = useMemo(() => {
    const m = {}
    selection.edges.forEach((e) => {
      (m[e.source] = m[e.source] || new Set()).add(e.target)
      ;(m[e.target] = m[e.target] || new Set()).add(e.source)
    })
    return m
  }, [selection])

  const focus = hover ?? picked
  const neighbors = focus != null ? (adj[focus] || new Set()) : null
  const dim = (id) => focus != null && id !== focus && !(neighbors && neighbors.has(id))

  const linkCount = selection.edges.filter((e) => e.kind !== 'similar').length
  const simCount = selection.edges.length - linkCount

  /* pan + zoom */
  const clampK = (k) => Math.max(0.4, Math.min(6, k))
  useEffect(() => {
    const el = svgRef.current
    if (!el) return
    // Non-passive so the page doesn't scroll while zooming the map.
    const onWheel = (e) => {
      e.preventDefault()
      const rect = el.getBoundingClientRect()
      const mx = ((e.clientX - rect.left) / rect.width) * box.w
      const my = ((e.clientY - rect.top) / rect.height) * box.h
      setView((v) => {
        const k = clampK(v.k * (e.deltaY < 0 ? 1.12 : 1 / 1.12))
        return { k, x: mx - ((mx - v.x) * k) / v.k, y: my - ((my - v.y) * k) / v.k }
      })
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [box.w, box.h])

  const onPointerDown = (e) => {
    if (e.button !== 0) return
    drag.current = { x: e.clientX, y: e.clientY, vx: view.x, vy: view.y, moved: false }
    setDragging(true)
    e.currentTarget.setPointerCapture?.(e.pointerId)
  }
  const onPointerMove = (e) => {
    const d = drag.current
    if (!d) return
    const rect = svgRef.current.getBoundingClientRect()
    const dx = ((e.clientX - d.x) / rect.width) * box.w
    const dy = ((e.clientY - d.y) / rect.height) * box.h
    if (Math.abs(dx) + Math.abs(dy) > 2) d.moved = true
    setView((v) => ({ ...v, x: d.vx + dx, y: d.vy + dy }))
  }
  const onPointerUp = () => { drag.current = null; setDragging(false) }
  const reset = useCallback(() => setView({ k: 1, x: 0, y: 0 }), [])

  const open = (node) => {
    const uri = vaultURI(vault, node.path)
    if (uri) window.location.href = uri
  }

  // Small maps read fine with every label on; large ones only once zoomed in.
  const showLabels = placed.length <= 45 || view.k >= 1.8

  return (
    <Card
      title="Knowledge graph"
      meta={
        <>
          {`${num(selection.nodes.length)} notes · ${num(linkCount)} links${simEdges ? ` · ${num(simCount)} similar` : ''}`}
          {selection.truncated && ` · showing the ${MAX_NODES} best-connected of ${num(selection.total)}`}
        </>
      }
      span={span}
    >
      <div className="filters">
        <select value={folder} onChange={(e) => { setFolder(e.target.value); setPicked(null) }} aria-label="Filter by folder">
          <option value="">All folders</option>
          {folders.map((f) => <option key={f} value={f}>{f}</option>)}
        </select>
        <select value={tag} onChange={(e) => { setTag(e.target.value); setPicked(null) }} aria-label="Filter by tag">
          <option value="">All tags</option>
          {tags.map((t) => <option key={t} value={t}>#{t}</option>)}
        </select>
        <label className="toggle">
          <input type="checkbox" checked={!!simEdges} onChange={(e) => onToggleSim?.(e.target.checked)} />
          <span>similarity edges</span>
        </label>
        <span className="spacer" />
        <span className="chip">{vault ? 'click a note to open it in Obsidian' : 'scroll to zoom · drag to pan'}</span>
      </div>

      <div className="graph-wrap" ref={wrapRef} style={{ height: 560 }}>
        <svg
          ref={svgRef}
          viewBox={`0 0 ${box.w} ${box.h}`}
          className={dragging ? 'dragging' : ''}
          role="img"
          aria-label="Knowledge graph"
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerLeave={onPointerUp}
        >
          <defs>
            <filter id="glow" x="-60%" y="-60%" width="220%" height="220%">
              <feGaussianBlur stdDeviation="2.6" result="b" />
              <feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
            </filter>
          </defs>
          <g transform={`translate(${view.x} ${view.y}) scale(${view.k})`}>
            {selection.edges.map((e, i) => {
              const a = pos[e.source], b = pos[e.target]
              if (!a || !b) return null
              const lit = focus != null && (e.source === focus || e.target === focus)
              const similar = e.kind === 'similar'
              return (
                <line
                  key={i} x1={a.x} y1={a.y} x2={b.x} y2={b.y}
                  stroke={lit ? (similar ? p.signal3 : p.signal1) : p.inkFaint}
                  strokeWidth={(lit ? 1.5 : 0.7) / Math.max(1, view.k * 0.6)}
                  strokeDasharray={similar ? '3 3' : undefined}
                  strokeOpacity={focus == null ? 0.28 : lit ? 0.9 : 0.06}
                >
                  {similar && <title>{`similarity ${(e.sim || 0).toFixed(2)}`}</title>}
                </line>
              )
            })}
            {placed.map((n) => {
              const r = 5 + Math.min(9, (n.words || 0) / 200) + (n.id === focus ? 2.5 : 0)
              const fill = folderColor[(n.path || '').split('/')[0]] || p.signal1
              return (
                <circle
                  key={n.id} cx={n.x} cy={n.y} r={r} fill={fill}
                  fillOpacity={dim(n.id) ? 0.18 : 1}
                  filter={n.id === focus ? 'url(#glow)' : undefined}
                  style={{ cursor: 'pointer', transition: 'fill-opacity .12s' }}
                  onMouseEnter={() => setHover(n.id)}
                  onMouseLeave={() => setHover(null)}
                  onClick={() => { if (!drag.current?.moved) { setPicked(n.id); open(n) } }}
                >
                  <title>{n.path}</title>
                </circle>
              )
            })}
            {(showLabels ? placed.filter((n) => n.degree > 0 || placed.length < 40) : placed.filter((n) => n.id === focus))
              .map((n) => (
                <text key={`l${n.id}`} className="node-label" x={n.x} y={n.y - 13}
                      textAnchor="middle" fontSize={12 / Math.max(0.9, view.k * 0.75)}>
                  {noteName(n.path)}
                </text>
              ))}
          </g>
          {placed.length === 0 && (
            <text x={box.w / 2} y={box.h / 2} textAnchor="middle" fill={p.inkFaint} fontSize="14">
              No notes match these filters.
            </text>
          )}
        </svg>

        {focus != null && pos[focus] && (
          <div className="graph-tip glass">
            <div className="p">{pos[focus].path}</div>
            <div className="m">
              {num(pos[focus].words || 0)} words · {num(pos[focus].degree || 0)} links
              {vault ? ' · click to open in Obsidian' : ''}
            </div>
          </div>
        )}

        <div className="graph-hud">
          <button onClick={() => setView((v) => ({ ...v, k: clampK(v.k * 1.25) }))} aria-label="Zoom in" title="Zoom in">+</button>
          <button onClick={() => setView((v) => ({ ...v, k: clampK(v.k / 1.25) }))} aria-label="Zoom out" title="Zoom out">−</button>
          <button onClick={reset} aria-label="Reset view" title="Reset view">⤢</button>
        </div>
      </div>

      {folders.length > 0 && (
        <div className="graph-legend">
          {folders.map((f) => <span key={f}><i className="swatch" style={{ background: folderColor[f] }} />{f}</span>)}
        </div>
      )}
      {placed.length === 0 && nodesAll.length === 0 && (
        <Empty>No notes indexed yet. Rebuild the index from your Markdown with <code>axon reindex</code>.</Empty>
      )}
    </Card>
  )
}
