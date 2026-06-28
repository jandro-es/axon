// Generates the AXON architecture diagrams in two formats from a single spec:
//   - <name>.excalidraw  — editable source (open at https://excalidraw.com)
//   - <name>.svg         — rendered, embedded inline in the README/docs
//
// Run:  node docs/diagrams/generate.mjs
//
// The spec uses compact box/arrow/text helpers; both outputs are derived from
// the same Excalidraw element array, so they never drift.

import { writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const OUT = dirname(fileURLToPath(import.meta.url))

// ---- palette ---------------------------------------------------------------
const C = {
  ink: '#1e1e1e',
  blue: '#a5d8ff',
  green: '#b2f2bb',
  yellow: '#ffec99',
  red: '#ffc9c9',
  purple: '#d0bfff',
  orange: '#ffd8a8',
  gray: '#e9ecef',
  white: '#ffffff',
}

// ---- excalidraw element builders ------------------------------------------
let seq = 0
const nid = () => 'el' + (++seq).toString(36).padStart(4, '0')

function base(type, x, y, w, h, extra = {}) {
  return {
    id: nid(), type, x, y, width: w, height: h, angle: 0,
    strokeColor: C.ink, backgroundColor: 'transparent', fillStyle: 'solid',
    strokeWidth: 2, strokeStyle: 'solid', roughness: 1, opacity: 100,
    groupIds: [], frameId: null, roundness: type === 'rectangle' ? { type: 3 } : null,
    seed: 1, versionNonce: 1, version: 1, isDeleted: false,
    boundElements: [], updated: 1, link: null, locked: false, ...extra,
  }
}

// A shape (rectangle/diamond/ellipse) with a centered text label.
function box(els, { x, y, w, h, fill = C.blue, type = 'rectangle', text = '', size = 16, dashed = false }) {
  const shape = base(type, x, y, w, h, { backgroundColor: fill, strokeStyle: dashed ? 'dashed' : 'solid' })
  els.push(shape)
  if (text) {
    const t = base('text', x, y, w, h, {
      backgroundColor: 'transparent', strokeWidth: 1, roundness: null,
      fontSize: size, fontFamily: 5, text, originalText: text,
      textAlign: 'center', verticalAlign: 'middle', containerId: shape.id,
      lineHeight: 1.25,
    })
    els.push(t)
    shape.boundElements = [{ id: t.id, type: 'text' }]
  }
  return {
    id: shape.id, x, y, w, h, cx: x + w / 2, cy: y + h / 2,
    left: [x, y + h / 2], right: [x + w, y + h / 2],
    top: [x + w / 2, y], bottom: [x + w / 2, y + h],
  }
}

function freetext(els, { x, y, text, size = 18, color = C.ink, align = 'left' }) {
  els.push(base('text', x, y, text.length * size * 0.6, size * 1.3, {
    backgroundColor: 'transparent', strokeColor: color, strokeWidth: 1, roundness: null,
    fontSize: size, fontFamily: 5, text, originalText: text,
    textAlign: align, verticalAlign: 'top', containerId: null, lineHeight: 1.25,
  }))
}

// An arrow from p1 to p2 with an optional midpoint label.
function arrow(els, p1, p2, { label = '', dashed = false, color = C.ink, both = false } = {}) {
  const a = base('arrow', p1[0], p1[1], p2[0] - p1[0], p2[1] - p1[1], {
    strokeColor: color, backgroundColor: 'transparent',
    strokeStyle: dashed ? 'dashed' : 'solid', roundness: { type: 2 },
    points: [[0, 0], [p2[0] - p1[0], p2[1] - p1[1]]],
    lastCommittedPoint: null, startBinding: null, endBinding: null,
    startArrowhead: both ? 'arrow' : null, endArrowhead: 'arrow',
  })
  els.push(a)
  if (label) {
    const mx = (p1[0] + p2[0]) / 2, my = (p1[1] + p2[1]) / 2
    const t = base('text', mx, my, label.length * 8, 18, {
      backgroundColor: C.white, strokeColor: C.ink, strokeWidth: 1, roundness: null,
      fontSize: 13, fontFamily: 5, text: label, originalText: label,
      textAlign: 'center', verticalAlign: 'middle', containerId: a.id, lineHeight: 1.25,
    })
    els.push(t)
    a.boundElements = [{ id: t.id, type: 'text' }]
    a._label = { mx, my, text: label }
  }
  return a
}

// ---- SVG renderer (same element array -> svg) ------------------------------
function esc(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

function renderSVG(els, title) {
  // bounds
  let minX = 1e9, minY = 1e9, maxX = -1e9, maxY = -1e9
  for (const e of els) {
    if (e.type === 'text' && e.containerId) continue
    minX = Math.min(minX, e.x); minY = Math.min(minY, e.y)
    maxX = Math.max(maxX, e.x + (e.width || 0)); maxY = Math.max(maxY, e.y + (e.height || 0))
  }
  const pad = 28
  const W = Math.ceil(maxX - minX + pad * 2), H = Math.ceil(maxY - minY + pad * 2 + 36)
  const ox = pad - minX, oy = pad - minY + 36

  const byContainer = {}
  for (const e of els) if (e.type === 'text' && e.containerId) byContainer[e.containerId] = e

  const out = []
  out.push(`<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" font-family="ui-sans-serif, -apple-system, Segoe UI, Roboto, sans-serif">`)
  out.push(`<rect width="${W}" height="${H}" fill="#ffffff"/>`)
  out.push(`<defs><marker id="ah" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="${C.ink}"/></marker></defs>`)
  out.push(`<text x="${pad}" y="24" font-size="20" font-weight="700" fill="${C.ink}">${esc(title)}</text>`)

  const drawLabel = (cx, cy, w, h, t) => {
    const lines = t.text.split('\n')
    const fs = t.fontSize
    const lh = fs * 1.32
    let y = cy - ((lines.length - 1) * lh) / 2 + fs * 0.34
    for (const ln of lines) {
      out.push(`<text x="${cx}" y="${y.toFixed(1)}" font-size="${fs}" fill="${t.strokeColor}" text-anchor="middle">${esc(ln)}</text>`)
      y += lh
    }
  }

  // shapes first
  for (const e of els) {
    const x = e.x + ox, y = e.y + oy, w = e.width, h = e.height
    if (e.type === 'rectangle') {
      const dash = e.strokeStyle === 'dashed' ? ' stroke-dasharray="6 5"' : ''
      out.push(`<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="10" fill="${e.backgroundColor}" stroke="${e.strokeColor}" stroke-width="2"${dash}/>`)
    } else if (e.type === 'ellipse') {
      out.push(`<ellipse cx="${x + w / 2}" cy="${y + h / 2}" rx="${w / 2}" ry="${h / 2}" fill="${e.backgroundColor}" stroke="${e.strokeColor}" stroke-width="2"/>`)
    } else if (e.type === 'diamond') {
      out.push(`<polygon points="${x + w / 2},${y} ${x + w},${y + h / 2} ${x + w / 2},${y + h} ${x},${y + h / 2}" fill="${e.backgroundColor}" stroke="${e.strokeColor}" stroke-width="2"/>`)
    }
    const lbl = byContainer[e.id]
    if (lbl && (e.type === 'rectangle' || e.type === 'ellipse' || e.type === 'diamond')) {
      drawLabel(x + w / 2, y + h / 2, w, h, lbl)
    }
  }
  // arrows on top
  for (const e of els) {
    if (e.type !== 'arrow') continue
    const x1 = e.x + ox, y1 = e.y + oy
    const p = e.points[e.points.length - 1]
    const x2 = e.x + p[0] + ox, y2 = e.y + p[1] + oy
    const dash = e.strokeStyle === 'dashed' ? ' stroke-dasharray="6 5"' : ''
    const startM = e.startArrowhead ? ' marker-start="url(#ah)"' : ''
    out.push(`<line x1="${x1}" y1="${y1}" x2="${x2}" y2="${y2}" stroke="${e.strokeColor}" stroke-width="2"${dash}${startM} marker-end="url(#ah)"/>`)
    const lbl = byContainer[e.id]
    if (lbl) {
      const mx = (x1 + x2) / 2, my = (y1 + y2) / 2
      const lines = lbl.text.split('\n')
      const tw = Math.max(...lines.map((l) => l.length)) * 7 + 8
      const th = lines.length * 16 + 4
      out.push(`<rect x="${(mx - tw / 2).toFixed(1)}" y="${(my - th / 2).toFixed(1)}" width="${tw}" height="${th}" rx="4" fill="#ffffff" stroke="#ced4da" stroke-width="1"/>`)
      let yy = my - (lines.length - 1) * 8 + 4
      for (const ln of lines) {
        out.push(`<text x="${mx}" y="${yy.toFixed(1)}" font-size="12.5" fill="${C.ink}" text-anchor="middle">${esc(ln)}</text>`)
        yy += 16
      }
    }
  }
  // standalone text
  for (const e of els) {
    if (e.type !== 'text' || e.containerId) continue
    const lines = e.text.split('\n')
    let y = e.y + oy + e.fontSize
    for (const ln of lines) {
      const anchor = e.textAlign === 'center' ? 'middle' : 'start'
      const x = e.textAlign === 'center' ? e.x + ox + (e.width || 0) / 2 : e.x + ox
      out.push(`<text x="${x}" y="${y.toFixed(1)}" font-size="${e.fontSize}" fill="${e.strokeColor}" text-anchor="${anchor}" font-weight="600">${esc(ln)}</text>`)
      y += e.fontSize * 1.3
    }
  }
  out.push('</svg>')
  return out.join('\n')
}

// ---- write both formats ----------------------------------------------------
function emit(name, title, els) {
  const doc = {
    type: 'excalidraw', version: 2, source: 'https://excalidraw.com',
    elements: els, appState: { viewBackgroundColor: '#ffffff', gridSize: 20 }, files: {},
  }
  writeFileSync(join(OUT, name + '.excalidraw'), JSON.stringify(doc, null, 2))
  writeFileSync(join(OUT, name + '.svg'), renderSVG(els, title))
  console.log(`wrote ${name}.excalidraw + ${name}.svg (${els.length} elements)`)
}

// ===========================================================================
// Diagram 1 — System architecture
// ===========================================================================
function architecture() {
  seq = 0
  const e = []
  // Left column — the human.
  freetext(e, { x: 60, y: 40, text: 'YOU', size: 13, color: '#868e96' })
  const obs = box(e, { x: 60, y: 64, w: 190, h: 56, fill: C.green, text: 'Obsidian\n(edit the vault)' })
  const cc = box(e, { x: 60, y: 200, w: 190, h: 56, fill: C.green, text: 'Claude Code\n(in the vault)' })
  const br = box(e, { x: 60, y: 320, w: 190, h: 56, fill: C.green, text: 'Browser\n(dashboard)' })

  // Middle column — the vault (truth), the daemon, the derived DB.
  const vault = box(e, { x: 520, y: 44, w: 220, h: 72, fill: C.yellow, text: 'Vault — Markdown\n(source of truth)', size: 15 })
  const daemon = box(e, { x: 520, y: 150, w: 220, h: 232, fill: C.blue, text: 'axon daemon\n\n• scheduler + automations\n• ingestion pipeline\n• token manager\n• MCP server\n• dashboard API', size: 15 })
  const sqlite = box(e, { x: 520, y: 416, w: 220, h: 60, fill: C.purple, text: 'SQLite\n(derived: FTS5 + vectors)' })

  // Right column — external services.
  freetext(e, { x: 900, y: 130, text: 'EXTERNAL', size: 13, color: '#868e96' })
  const claude = box(e, { x: 900, y: 156, w: 190, h: 56, fill: C.orange, text: 'Claude\n(claude -p)' })
  const ollama = box(e, { x: 900, y: 248, w: 190, h: 56, fill: C.orange, text: 'Ollama\n(embeddings)' })
  const web = box(e, { x: 900, y: 340, w: 190, h: 56, fill: C.red, text: 'Web URLs / PDFs' })

  arrow(e, obs.right, vault.left, { label: 'edit' })
  arrow(e, cc.right, [daemon.x, 222], { label: 'MCP tools' })
  arrow(e, br.right, [daemon.x, 348], { label: 'HTTP / SSE' })
  arrow(e, [daemon.cx, daemon.y], vault.bottom, { label: 'read / wikilink-safe write', both: true })
  arrow(e, daemon.bottom, sqlite.top, { label: 'reindex' })
  arrow(e, [daemon.x + daemon.w, 196], claude.left, { label: 'claude -p\n(via token mgr)' })
  arrow(e, [daemon.x + daemon.w, 276], ollama.left, { label: 'embed' })
  arrow(e, [daemon.x + daemon.w, 360], web.left, { label: 'ingest\n(policy)' })

  emit('architecture', 'AXON — system architecture', e)
}

// ===========================================================================
// Diagram 2 — Ingestion pipeline
// ===========================================================================
function ingestion() {
  seq = 0
  const e = []
  const x = 70, w = 360, h = 52, gap = 26
  let y = 60
  const step = (text, fill = C.blue) => { const b = box(e, { x, y, w, h, fill, text }); y += h + gap; return b }

  const input = step('Input:  URL · PDF · local file', C.gray)
  const policy = step('Policy check  (egress + redirects)', C.red)
  const fetch = step('Fetch / read  (size-capped, no JS)')
  const extract = step('Extract & clean → Markdown')
  const redact = step('Redact  (policy rules, pre-persist)', C.red)
  const dw = 300, dh = 72, dx = x + (w - dw) / 2
  const hashD = box(e, { x: dx, y, w: dw, h: dh, fill: C.yellow, type: 'diamond', text: 'Content hash:\nchanged?', size: 15 })
  const skipBox = box(e, { x: x + w + 90, y: y + (dh - 56) / 2, w: 230, h: 56, fill: C.green, text: 'Skip — no model call\n(idempotent)', size: 14 })
  y += dh + gap
  const enrich = step('Enrich → title, summary, tags, links')
  const write = step('Write note → 03-Resources/Knowledge', C.purple)
  const chunk = step('Chunk + embed  (Ollama)')
  const index = step('Index:  FTS5 + vector store', C.purple)
  const event = step('Emit event → dashboard + ledger', C.gray)

  arrow(e, input.bottom, policy.top)
  arrow(e, policy.bottom, fetch.top)
  arrow(e, fetch.bottom, extract.top)
  arrow(e, extract.bottom, redact.top)
  arrow(e, redact.bottom, hashD.top)
  arrow(e, hashD.right, skipBox.left, { label: 'no' })
  arrow(e, hashD.bottom, enrich.top, { label: 'yes' })
  arrow(e, enrich.bottom, write.top)
  arrow(e, write.bottom, chunk.top)
  arrow(e, chunk.bottom, index.top)
  arrow(e, index.bottom, event.top)

  emit('ingestion-pipeline', 'AXON — knowledge ingestion pipeline', e)
}

// ===========================================================================
// Diagram 3 — Token chokepoint / automation run lifecycle
// ===========================================================================
function chokepoint() {
  seq = 0
  const e = []
  const x = 70, w = 380, h = 54, gap = 28
  let y = 60
  const step = (text, fill = C.blue, dh = h) => { const b = box(e, { x, y, w, h: dh, fill, text }); y += dh + gap; return b }
  const dw = 320, dx = x + (w - dw) / 2
  const sideX = x + w + 80
  const diamond = (text, size = 14) => { const b = box(e, { x: dx, y, w: dw, h: 76, fill: C.yellow, type: 'diamond', text, size }); y += 76 + gap; return b }
  const side = (b, text, fill) => box(e, { x: sideX, y: b.cy - 27, w: 240, h: 54, fill, text, size: 14 })

  const start = step('Scheduler fires / caller', C.gray)
  const lock = step('Acquire lock + open run record')
  const gate = diamond('Change-gate:\nnew material?')
  const skip1 = side(gate, 'Skip — no Claude call', C.green)
  const budget = diamond('Budget pre-check:\nguard active?')
  const skip2 = side(budget, 'Skip — budget', C.green)
  const tm = step('Build context + token estimate', C.orange)
  const auth = diamond('Authorize:\nproceed / downgrade /\ndefer / deny', 13)
  const denyBox = side(auth, 'defer / deny\n(surfaced)', C.red)
  const run = step('Run via claude -p\n(the ONLY path to Claude)', C.orange)
  const ledger = step('Record usage → ledger + budget', C.purple)
  const writeV = step('Apply wikilink-safe vault writes', C.blue)
  const ev = step('Emit event → dashboard', C.gray)

  arrow(e, start.bottom, lock.top)
  arrow(e, lock.bottom, gate.top)
  arrow(e, gate.right, skip1.left, { label: 'no' })
  arrow(e, gate.bottom, budget.top, { label: 'yes' })
  arrow(e, budget.right, skip2.left, { label: 'yes' })
  arrow(e, budget.bottom, tm.top, { label: 'no' })
  arrow(e, tm.bottom, auth.top)
  arrow(e, auth.right, denyBox.left)
  arrow(e, auth.bottom, run.top, { label: 'proceed' })
  arrow(e, run.bottom, ledger.top)
  arrow(e, ledger.bottom, writeV.top)
  arrow(e, writeV.bottom, ev.top)

  emit('token-chokepoint', 'AXON — token chokepoint & automation lifecycle', e)
}

// ===========================================================================
// Diagram 4 — Personal memory & identity (Phase 8)
// ===========================================================================
function personalMemory() {
  const e = []
  const onboard = box(e, { x: 24, y: 156, w: 250, h: 64, fill: C.gray, size: 14, text: 'axon onboard\ninterview · idempotent · no model' })

  // Identity-layer container (dashed) with a top label.
  box(e, { x: 320, y: 72, w: 280, h: 232, fill: '#f8f9fa', dashed: true })
  freetext(e, { x: 338, y: 78, text: 'Identity layer · 02-Areas/Profile/', size: 13 })
  box(e, { x: 338, y: 108, w: 244, h: 46, fill: C.blue, size: 15, text: 'USER.md — who you are' })
  box(e, { x: 338, y: 160, w: 244, h: 46, fill: C.blue, size: 15, text: 'SOUL.md — assistant persona' })
  const memory = box(e, { x: 338, y: 216, w: 244, h: 72, fill: C.yellow, size: 14, text: 'MEMORY.md\naxon:memory · durable entries' })

  const sstart = box(e, { x: 636, y: 104, w: 216, h: 64, fill: C.green, size: 14, text: 'SessionStart hook\nbounded · redacted · no model' })
  const session = box(e, { x: 636, y: 200, w: 216, h: 60, fill: C.green, size: 14, text: 'Claude Code session\n“knows you”' })

  const remember = box(e, { x: 322, y: 372, w: 130, h: 58, fill: C.blue, size: 13, text: 'memory_remember\nMCP tool' })
  const distill = box(e, { x: 458, y: 372, w: 130, h: 58, fill: C.blue, size: 13, text: 'memory-distill\n→ token manager' })

  box(e, { x: 636, y: 300, w: 216, h: 100, fill: C.red, size: 12, text: 'Privacy (NFR-14)\nnever in logs, events,\nledger or exports;\nredaction before egress.' })

  arrow(e, onboard.right, [320, 188], { label: 'writes' })
  arrow(e, [600, 136], sstart.left, { label: 'inject' })
  arrow(e, sstart.bottom, session.top)
  arrow(e, remember.top, [remember.cx, 290])
  arrow(e, distill.top, [distill.cx, 290])
  freetext(e, { x: 392, y: 338, text: 'append (wikilink-safe)', size: 12 })
  void memory

  emit('personal-memory', 'AXON — personal memory & identity (Phase 8)', e)
}

// ===========================================================================
// Diagram 5 — One MCP server, many Claude clients (Phase 9)
// ===========================================================================
function multiClient() {
  const e = []
  const code = box(e, { x: 24, y: 84, w: 250, h: 92, fill: C.green, size: 13, text: 'Claude Code\ntools + hooks + skills +\nsubagents + headless automations\nfull-featured' })
  const desktop = box(e, { x: 24, y: 244, w: 250, h: 92, fill: C.blue, size: 13, text: 'Claude Desktop\nAXON tools only\nno hooks / skills / injection\ntools-only client' })
  const server = box(e, { x: 392, y: 120, w: 232, h: 180, fill: C.yellow, size: 12, text: 'axon mcp (stdio)\nvault_search / read / write\nvault_patch / move / links\ndaily_append · knowledge_*\ntokens_status · automations_*\nmemory_remember\n \nevery tool wikilink-safe &\npath-sandboxed in the server' })
  const vault = box(e, { x: 712, y: 160, w: 148, h: 100, fill: C.gray, size: 15, text: 'Vault\n(source of truth)' })
  box(e, { x: 392, y: 328, w: 232, h: 74, fill: C.green, size: 12, text: "Vault safety holds for both:\nenforced in the server,\nnot the client's hooks." })

  arrow(e, code.right, [392, 160])
  arrow(e, desktop.right, [392, 258])
  arrow(e, server.right, vault.left, { label: 'safe ops' })

  emit('multi-client', 'AXON — one MCP server, many Claude clients (Phase 9)', e)
}

architecture()
ingestion()
chokepoint()
personalMemory()
multiClient()
