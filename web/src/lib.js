import { useEffect, useRef, useState } from 'react'

/* ── data ─────────────────────────────────────────────────────────────────
   useFetch polls a JSON endpoint. Failures (network, non-2xx) are surfaced
   via `error` instead of being swallowed — a dead daemon must look degraded,
   not like healthy-but-empty. `error` is `true` when the daemon could not be
   reached at all, or the daemon's own message when it answered with a non-2xx;
   both are truthy, so `error &&` checks read the same either way. The last good
   data is kept while errored, and `loading` is true only until the first
   answer, so panels can show a skeleton once rather than flashing one on every
   poll.
*/
export function useFetch(url, interval = 5000) {
  const [state, setState] = useState({ data: null, error: false, loading: true })
  useEffect(() => {
    let alive = true
    setState((s) => ({ ...s, loading: s.data == null }))
    const load = () =>
      fetch(url)
        .then(async (r) => {
          if (!r.ok) {
            // The daemon DID answer — it just failed. Carry what it said, so a
            // fixable, specific problem (an unreadable vault file, say) is not
            // reported to the user as "not answering".
            const body = (await r.text().catch(() => '')).trim()
            const e = new Error(body || `HTTP ${r.status}`)
            e.fromDaemon = true
            throw e
          }
          return r.json()
        })
        .then((d) => alive && setState({ data: d, error: false, loading: false }))
        .catch((e) => alive && setState((s) => ({
          data: s.data,
          // A string means the daemon replied with this message; `true` means
          // it could not be reached at all.
          error: e?.fromDaemon ? e.message : true,
          loading: false,
        })))
    load()
    const id = setInterval(load, interval)
    return () => { alive = false; clearInterval(id) }
  }, [url, interval])
  return state
}

// SSE_KINDS must mirror the named event kinds the daemon actually emits
// (events are sent with `event: <kind>`, so unregistered kinds never fire).
export const SSE_KINDS = [
  'automation.run', 'automation.skip', 'automation.fail',
  'ingest.done', 'ingest.skip', 'ingest.enrich',
  'ingest.embed.fail', 'ingest.embed.skip', 'ingest.review_queue.fail',
  'token.ledger', 'token.deny', 'token.defer', 'token.downgrade', 'token.error',
  'review.accept', 'review.dismiss',
  'action.done',
]

export function useSSE(onEvent) {
  const [events, setEvents] = useState([])
  const [connected, setConnected] = useState(false)
  // Kept in a ref so a changing callback never tears down the stream.
  const cb = useRef(onEvent)
  cb.current = onEvent

  useEffect(() => {
    const es = new EventSource('/events')
    es.onopen = () => setConnected(true)
    es.onerror = () => setConnected(false)
    const push = (e) => {
      try {
        const evt = JSON.parse(e.data)
        setEvents((prev) => [evt, ...prev].slice(0, 400))
        cb.current?.(evt)
      } catch { /* a malformed frame is not worth breaking the stream over */ }
    }
    es.onmessage = push
    SSE_KINDS.forEach((k) => es.addEventListener(k, push))
    return () => es.close()
  }, [])
  return { events, connected }
}

/* ── ranges ───────────────────────────────────────────────────────────────
   One range control drives every time series on screen. Filtering happens
   client-side over what the daemon already returned — no extra requests, and
   the exports stay whole-series (they say so in their tooltip).
*/
export const RANGES = [
  { id: '24h', label: '24h', days: 1 },
  { id: '7d', label: '7d', days: 7 },
  { id: '30d', label: '30d', days: 30 },
  { id: 'all', label: 'All', days: 0 },
]

const dayCutoff = (days) => {
  const d = new Date()
  d.setDate(d.getDate() - (days - 1))
  return d.toISOString().slice(0, 10)
}

// Filter rows carrying a `YYYY-MM-DD` day field.
export function inRangeByDay(rows, range, key = 'day') {
  const r = RANGES.find((x) => x.id === range)
  if (!r || !r.days) return rows || []
  const cut = dayCutoff(r.days)
  return (rows || []).filter((x) => (x[key] || '') >= cut)
}

// Filter rows carrying an RFC3339 timestamp.
export function inRangeByTime(rows, range, key = 'started_at') {
  const r = RANGES.find((x) => x.id === range)
  if (!r || !r.days) return rows || []
  const cut = Date.now() - r.days * 86400000
  return (rows || []).filter((x) => {
    const t = Date.parse(x[key])
    return !isFinite(t) || t >= cut
  })
}

export const rangeLabel = (id) => (RANGES.find((r) => r.id === id) || {}).label || id

/* ── formatting ───────────────────────────────────────────────────────── */
export const num = (n) => (n || 0).toLocaleString()
export const kfmt = (n) =>
  n >= 1e6 ? (n / 1e6).toFixed(1) + 'M' : n >= 1e3 ? (n / 1e3).toFixed(n >= 1e4 ? 0 : 1) + 'k' : String(n || 0)
export const shortDay = (d) => (d || '').slice(5)
export const parseTags = (t) => { try { const a = JSON.parse(t || '[]'); return Array.isArray(a) ? a : [] } catch { return [] } }
export const shortOp = (op) => (op || '').replace(/^automation\./, '').replace(/^ingest\./, 'ingest:')
export const truncate = (s, n) => ((s || '').length > n ? s.slice(0, n - 1) + '…' : s || '')
export const sumField = (arr, f) => (arr || []).reduce((s, x) => s + (x[f] || 0), 0)

export function fmtTime(ts) {
  try { return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) }
  catch { return ts }
}

export function fmtDur(a, b) {
  const s = (new Date(b) - new Date(a)) / 1000
  if (!isFinite(s) || s < 0) return '—'
  if (s < 1) return '<1s'
  if (s < 60) return s.toFixed(s < 10 ? 1 : 0) + 's'
  if (s < 3600) return Math.floor(s / 60) + 'm ' + Math.round(s % 60) + 's'
  return Math.floor(s / 3600) + 'h ' + Math.round((s % 3600) / 60) + 'm'
}

// Uptime from the daemon's own start time (/health.started_at) — a client
// can't track this itself without lying across its own reload.
export function fmtUptime(startedAt) {
  const t = Date.parse(startedAt)
  if (!isFinite(t)) return '—'
  const s = Math.max(0, (Date.now() - t) / 1000)
  if (s < 60) return Math.round(s) + 's'
  if (s < 3600) return Math.round(s / 60) + 'm'
  if (s < 86400) return Math.floor(s / 3600) + 'h ' + Math.round((s % 3600) / 60) + 'm'
  return Math.floor(s / 86400) + 'd ' + Math.round((s % 86400) / 3600) + 'h'
}

// How long ago, in the shortest form that stays unambiguous. A bare clock time
// reads as "today" even when the run was three weeks ago.
export function fmtAge(ts) {
  const t = Date.parse(ts)
  if (!isFinite(t)) return '—'
  const s = Math.max(0, (Date.now() - t) / 1000)
  if (s < 90) return 'just now'
  if (s < 3600) return `${Math.round(s / 60)}m ago`
  if (s < 86400) return `${Math.round(s / 3600)}h ago`
  if (s < 86400 * 30) return `${Math.round(s / 86400)}d ago`
  return new Date(t).toLocaleDateString()
}

/* ── vault ────────────────────────────────────────────────────────────── */
// Source path → the note's bare name (drop folders + .md); full path in a title.
export const noteName = (p) => (p || '').split('/').pop().replace(/\.md$/i, '')

// obsidian://open deep link. Needs the vault name from /health; returns null
// when it's absent, and every caller degrades to plain text.
export const vaultURI = (vault, path) =>
  vault && path
    ? `obsidian://open?vault=${encodeURIComponent(vault)}&file=${encodeURIComponent(path.replace(/\.md$/i, ''))}`
    : null

// Vault text is raw Markdown — unwrap wikilinks and strip emphasis so lines
// read as plain prose instead of a wall of [[…]] and **…**.
export const cleanText = (t) => (t || '')
  .replace(/\[\[[^\]|]+\|([^\]]+)\]\]/g, '$1')
  .replace(/\[\[([^\]]+)\]\]/g, '$1')
  .replace(/\*\*([^*]+)\*\*/g, '$1')
  .replace(/`([^`]+)`/g, '$1')
  .trim()

/* ── aggregations ─────────────────────────────────────────────────────── */
// tokensDaily collapses the per-day/operation/model buckets into a day series
// (with the cache split, FR-60).
export function tokensDaily(tokens) {
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
export function pieBy(tokens, keyFn, top = 6) {
  const m = {}
  for (const b of tokens || []) m[keyFn(b)] = (m[keyFn(b)] || 0) + (b.input || 0) + (b.output || 0)
  const all = Object.entries(m).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value)
  if (all.length <= top) return all
  const head = all.slice(0, top)
  head.push({ name: 'other', value: all.slice(top).reduce((s, x) => s + x.value, 0) })
  return head
}

/* ── mutating + query endpoints ───────────────────────────────────────────
   Every one carries its X-Axon-* header: together with the JSON content type
   they force a CORS preflight no cross-origin page can pass (the daemon's
   guard, mirrored here so a dropped header fails loudly in review).
*/
const json = async (r) => { if (!r.ok) throw new Error((await r.text()) || `HTTP ${r.status}`); return r.json() }

export const postAsk = (question) =>
  fetch('/api/ask', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Axon-Ask': '1' },
    body: JSON.stringify({ question }),
  }).then(json)

export const getRelated = (path) =>
  fetch('/api/related?path=' + encodeURIComponent(path), { headers: { 'X-Axon-Related': '1' } }).then(json)

export const getActions = (nonce) =>
  fetch('/api/actions?n=' + nonce, { headers: { 'X-Axon-Actions': '1' } }).then(json)

export const postComplete = (path, hash) =>
  fetch('/api/actions/complete', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Axon-Actions': '1' },
    body: JSON.stringify({ path, hash }),
  }).then(json)

export const postReviewAction = (id, action) =>
  fetch('/api/review/action', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Axon-Review': '1' },
    body: JSON.stringify({ id, action }),
  }).then(json)

/* ── misc ─────────────────────────────────────────────────────────────── */
// Case-insensitive subsequence match: "tk" finds "tokens". Used by the
// command palette and the in-panel filters.
export function fuzzy(needle, hay) {
  const n = (needle || '').toLowerCase().trim()
  if (!n) return true
  const h = (hay || '').toLowerCase()
  let i = 0
  for (const ch of n) {
    i = h.indexOf(ch, i)
    if (i < 0) return false
    i++
  }
  return true
}

export const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || '')
export const modKey = isMac ? '⌘' : 'Ctrl'
