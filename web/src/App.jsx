import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ThemeProvider, ThemeToggle, useTheme, MODES } from './theme.jsx'
import {
  Card, Empty, ErrorBoundary, ExportLinks, RangePicker, Skeleton,
  ToastHost, useToast,
} from './ui.jsx'
import { CommandPalette, ShortcutsSheet } from './CommandPalette.jsx'
import {
  fmtUptime, inRangeByDay, inRangeByTime, modKey, num, pieBy, RANGES,
  rangeLabel, shortOp, useFetch, useSSE,
} from './lib.js'
import { BudgetCard, DonutCard, TokenTotals, TokensByOpModel, TokenTrend } from './panels/Tokens.jsx'
import { AutomationTable, RunsList, RunStats } from './panels/Automations.jsx'
import { FoldersCard, GrowthCard, IngestTrend, VaultTiles } from './panels/Knowledge.jsx'
import { GraphCard, GraphInsights } from './panels/Graph.jsx'
import { ActivityCard } from './panels/Activity.jsx'
import { ActionsTab } from './panels/Actions.jsx'
import { ReviewTab } from './panels/Review.jsx'
import { AskTab } from './panels/Ask.jsx'
import { RelatedTab } from './panels/Related.jsx'

const TABS = [
  ['overview', 'Overview'], ['tokens', 'Tokens'], ['automations', 'Automations'],
  ['review', 'Review'], ['actions', 'Actions'], ['ask', 'Ask'], ['related', 'Related'],
  ['knowledge', 'Knowledge'], ['graph', 'Graph'], ['activity', 'Activity'],
]

/* The URL fragment is the tab: `#review` opens the Review tab on load. Keeps
   tabs bookmarkable and gives external launchers (the macOS Companion app,
   CFR-20) a stable deep link that needs no router dependency. */
const TAB_IDS = TABS.map(([id]) => id)
const tabFromHash = () => {
  const id = (typeof location === 'undefined' ? '' : location.hash).replace(/^#\/?/, '')
  return TAB_IDS.includes(id) ? id : 'overview'
}

const RANGE_KEY = 'axon.range'
const readRange = () => {
  try {
    const r = localStorage.getItem(RANGE_KEY)
    return RANGES.some((x) => x.id === r) ? r : '30d'
  } catch { return '30d' }
}

/* ── header ───────────────────────────────────────────────────────────── */
function StatusSheet({ health, usage, vault, onClose }) {
  const toast = useToast()
  const h = health || {}
  const rows = [
    ['Profile', h.profile || '—'],
    ['Version', h.version ? `v${h.version}` : '—'],
    h.update_available ? ['Update', `v${h.latest_version} available`] : null,
    ['Uptime', fmtUptime(h.started_at)],
    ['Database', h.db ? 'connected' : 'unreachable'],
    ['Vault', vault || 'not wired'],
    ['Embeddings', h.embeddings_provider ? `${h.embeddings_provider} · ${h.embeddings_model} · ${h.embeddings_dim}d` : '—'],
    ['Claude CLI', h.claude_path || 'not on the daemon’s PATH'],
    ['Budget guard', usage?.guard_paused ? `paused ≥ ${usage.guard_pct}%` : 'armed'],
  ].filter(Boolean)

  const copy = () => {
    const text = rows.map(([k, v]) => `${k}: ${v}`).join('\n')
    navigator.clipboard?.writeText(text)
      .then(() => toast('Diagnostics copied.'))
      .catch(() => toast('Clipboard unavailable in this browser.', 'warn'))
  }

  return (
    <div className="sheet glass" role="dialog" aria-label="Daemon status">
      <h4>Daemon</h4>
      {rows.map(([k, v]) => (
        <div className="legend-row" key={k}>
          <span className="k">{k}</span>
          <span className="v" title={String(v)}>{v}</span>
        </div>
      ))}
      <div className="sheet-foot">
        <button className="btn" onClick={copy}>Copy diagnostics</button>
        <button className="btn ghost" onClick={onClose} style={{ marginLeft: 'auto' }}>Close</button>
      </div>
    </div>
  )
}

/* What needs a human — assembled from everything already on screen, so the
   answer to "is anything waiting on me?" doesn't require reading six panels. */
function AttentionCard({ health, usage, review, runs, vault, ingestion, go, span }) {
  const failed = (runs || []).filter((r) => r.status === 'failed').length
  const items = [
    review?.pending > 0 && { k: 'review', label: `${review.pending} proposal${review.pending === 1 ? '' : 's'} waiting in the review queue`, tab: 'review', tone: 'accent' },
    failed > 0 && { k: 'runs', label: `${failed} automation run${failed === 1 ? '' : 's'} failed in this range`, tab: 'automations', tone: 'err' },
    usage?.guard_paused && { k: 'guard', label: `Budget guard paused automations at ${usage.guard_pct}% of the window`, tab: 'tokens', tone: 'warn' },
    vault?.stats?.inbox_backlog > 0 && { k: 'inbox', label: `${vault.stats.inbox_backlog} note${vault.stats.inbox_backlog === 1 ? '' : 's'} sitting in 00-Inbox`, tab: 'knowledge', tone: 'warn' },
    ingestion?.embedding_queue > 0 && { k: 'embed', label: `${ingestion.embedding_queue} chunk${ingestion.embedding_queue === 1 ? '' : 's'} still to embed`, tab: 'knowledge', tone: 'accent' },
    health?.update_available && { k: 'update', label: `AXON v${health.latest_version} is available — update with make update`, tone: 'accent' },
  ].filter(Boolean)

  return (
    <Card title="Needs you" meta={items.length ? `${items.length}` : 'clear'} span={span}>
      {items.length === 0 ? (
        <Empty>Nothing is waiting on you. The vault, the queue and the budget are all clear.</Empty>
      ) : (
        <div className="list">
          {items.map((it) => (
            <div className="li" key={it.k}>
              <span className={`sdot ${it.tone === 'err' ? 'failed' : it.tone === 'warn' ? 'dry-run' : 'ok'}`} />
              <span className="grow">{it.label}</span>
              {it.tab && <button className="btn ghost" onClick={() => go(it.tab)}>Open</button>}
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}

/* ── shell ────────────────────────────────────────────────────────────── */
function Shell() {
  const toast = useToast()
  const { mode, setMode } = useTheme()
  const [tab, setTabState] = useState(tabFromHash)
  const [range, setRangeState] = useState(readRange)
  const [simEdges, setSimEdges] = useState(false)
  const [sheet, setSheet] = useState(false)
  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const mainRef = useRef(null)

  const setTab = useCallback((id) => {
    setTabState(id)
    if (typeof location !== 'undefined' && tabFromHash() !== id) location.hash = id
  }, [])

  const setRange = useCallback((r) => {
    setRangeState(r)
    try { localStorage.setItem(RANGE_KEY, r) } catch { /* session-only is fine */ }
  }, [])

  // Follow hash edits from outside React: Back/Forward, a pasted URL, or the
  // Companion app re-focusing an already-open dashboard window.
  useEffect(() => {
    const onHash = () => setTabState(tabFromHash())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  const { data: health, error: healthErr } = useFetch('/health', 10000)
  const { data: usage, error: usageErr } = useFetch('/api/usage', 4000)
  const { data: tokens, loading: tokensLoading } = useFetch('/api/tokens', 8000)
  const { data: runs } = useFetch('/api/runs', 6000)
  const { data: vault, loading: vaultLoading } = useFetch('/api/vault', 8000)
  const { data: ingestion } = useFetch('/api/ingestion', 8000)
  const { data: graph } = useFetch(simEdges ? '/api/graph?similar=1' : '/api/graph', 15000)
  const { data: activity } = useFetch('/api/activity', 15000)
  const { data: reviewMeta } = useFetch('/api/review', 15000)

  // Errors are the one class of event worth interrupting for; everything else
  // belongs in the Activity feed where it can be read in order.
  const onEvent = useCallback((e) => {
    if (e.level === 'error') toast(e.message, 'error')
  }, [toast])
  const { events, connected } = useSSE(onEvent)

  const vaultName = health?.vault || ''
  const apiDown = healthErr || usageErr
  const healthy = health?.status === 'ok'

  /* Range-scoped views of every series, computed once. */
  const tokensR = useMemo(() => inRangeByDay(tokens, range), [tokens, range])
  const runsR = useMemo(() => inRangeByTime(runs, range), [runs, range])
  const growthR = useMemo(() => inRangeByDay(vault?.growth, range), [vault, range])
  const ingestR = useMemo(() => inRangeByDay(ingestion?.series, range), [ingestion, range])
  const byModel = useMemo(() => pieBy(tokensR, (b) => b.model), [tokensR])
  const byOp = useMemo(() => pieBy(tokensR, (b) => shortOp(b.operation)), [tokensR])

  const visibleTabs = useMemo(
    () => TABS.filter(([id]) =>
      (id !== 'ask' || health?.ask_enabled !== false) &&
      (id !== 'related' || health?.related_enabled !== false) &&
      (id !== 'actions' || health?.actions_enabled !== false)),
    [health],
  )

  /* ── keyboard ───────────────────────────────────────────────────────── */
  const cycleTheme = useCallback(() => {
    setMode(MODES[(MODES.indexOf(mode) + 1) % MODES.length])
  }, [mode, setMode])

  const cycleRange = useCallback(() => {
    const i = RANGES.findIndex((r) => r.id === range)
    setRange(RANGES[(i + 1) % RANGES.length].id)
  }, [range, setRange])

  useEffect(() => {
    const onKey = (e) => {
      const t = e.target
      const typing = t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable)
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault(); setPalette((v) => !v); return
      }
      if (typing || e.metaKey || e.ctrlKey || e.altKey) return
      if (e.key === '?') { e.preventDefault(); setShortcuts(true); return }
      if (e.key === '/') { e.preventDefault(); mainRef.current?.querySelector('input.input')?.focus(); return }
      if (e.key.toLowerCase() === 't') { cycleTheme(); return }
      if (e.key.toLowerCase() === 'r') { cycleRange(); return }
      if (/^[1-9]$/.test(e.key)) {
        const t2 = visibleTabs[Number(e.key) - 1]
        if (t2) setTab(t2[0])
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [visibleTabs, setTab, cycleTheme, cycleRange])

  // Close the status sheet on any outside click.
  useEffect(() => {
    if (!sheet) return
    const close = (e) => { if (!e.target.closest?.('.sheet-wrap')) setSheet(false) }
    window.addEventListener('mousedown', close)
    return () => window.removeEventListener('mousedown', close)
  }, [sheet])

  const commands = useMemo(() => [
    ...visibleTabs.map(([id, label], i) => ({
      id: `tab:${id}`, group: 'Go to', label, hint: i < 9 ? String(i + 1) : '',
      keywords: 'tab view', run: () => setTab(id),
    })),
    ...RANGES.map((r) => ({
      id: `range:${r.id}`, group: 'Range', label: `Show the last ${r.label === 'All' ? 'everything' : r.label}`,
      keywords: 'time window', run: () => setRange(r.id),
    })),
    ...MODES.map((m) => ({
      id: `theme:${m}`, group: 'Appearance',
      label: m === 'system' ? 'Match the system appearance' : `Always ${m}`,
      keywords: 'theme dark light', run: () => setMode(m),
    })),
    ...['tokens', 'runs', 'ingestion', 'vault', 'activity'].map((d) => ({
      id: `export:${d}`, group: 'Export', label: `Download ${d} as CSV`,
      keywords: 'save download data', run: () => { window.location.href = `/api/export?dataset=${d}&format=csv` },
    })),
    { id: 'graph:sim', group: 'Graph', label: `${simEdges ? 'Hide' : 'Show'} similarity edges`, run: () => { setSimEdges((v) => !v); setTab('graph') } },
    { id: 'help', group: 'Help', label: 'Keyboard shortcuts', hint: '?', run: () => setShortcuts(true) },
    { id: 'status', group: 'Help', label: 'Daemon status', run: () => setSheet(true) },
  ], [visibleTabs, setTab, setRange, setMode, simEdges])

  return (
    <div className="app">
      <header className="topbar glass">
        <div className="topbar-inner">
          <div className="brand">
            <span className="brand-mark" />
            <span className="brand-name"><b>AX</b>ON</span>
            <span className="brand-sub">second-brain console</span>
          </div>

          <div className="topbar-spacer" />

          <RangePicker value={range} onChange={setRange} />

          <button className="chip" onClick={() => setPalette(true)} title={`Command palette (${modKey}K)`}>
            <span>⌘</span> Search
          </button>

          {apiDown && <span className="chip err"><i className="dot" />daemon unreachable</span>}
          {health?.update_available && (
            <span className="chip accent" title={`v${health.latest_version} is available`}><i className="dot" />update</span>
          )}
          <span className={`chip ${connected ? 'live' : 'off'}`} title={connected ? 'Live event stream connected' : 'Event stream disconnected'}>
            <i className="dot" />{connected ? 'live' : 'offline'}
          </span>

          <span className="sheet-wrap">
            <button
              className={`chip ${healthy && health?.db ? 'ok' : 'warn'}`}
              onClick={() => setSheet((v) => !v)}
              aria-expanded={sheet}
              title="Daemon status"
            >
              <i className="dot" />{health?.profile || '—'}
            </button>
            {sheet && <StatusSheet health={health} usage={usage} vault={vaultName} onClose={() => setSheet(false)} />}
          </span>

          <ThemeToggle />
        </div>
        <div className="signal-line" />
      </header>

      <nav className="nav">
        {visibleTabs.map(([id, label]) => (
          <button key={id} className={tab === id ? 'active' : ''} onClick={() => setTab(id)}
                  aria-current={tab === id ? 'page' : undefined}>
            {label}
            {id === 'review' && reviewMeta?.pending > 0 && <span className="pip hot">{reviewMeta.pending}</span>}
          </button>
        ))}
      </nav>

      <ErrorBoundary key={tab}>
        <main className="grid" ref={mainRef}>
          {tab === 'overview' && <>
            <AttentionCard health={health} usage={usage} review={reviewMeta} runs={runsR}
                           vault={vault} ingestion={ingestion} go={setTab} span="span-12" />
            <VaultTiles vault={vault} ingestion={ingestion} growth={growthR} loading={vaultLoading} span="span-8" />
            <BudgetCard usage={usage} span="span-4" />
            {tokensLoading && !tokens
              ? <Card title="Token spend" span="span-8"><Skeleton h="chart" /></Card>
              : <TokenTrend tokens={tokensR} span="span-8" meta={`${rangeLabel(range)} · ${num(tokensR.length)} buckets`} />}
            <RunsList runs={runsR} span="span-4" limit={9} />
            <IngestTrend series={ingestR} span="span-6" />
            <ActivityCard live={events} initial={activity} span="span-6" />
          </>}

          {tab === 'tokens' && <>
            <BudgetCard usage={usage} span="span-5" />
            <TokenTrend tokens={tokensR} span="span-7" />
            <DonutCard title="By model" data={byModel} span="span-4" />
            <DonutCard title="By operation" data={byOp} span="span-4" />
            <TokenTotals tokens={tokensR} span="span-4" />
            <TokensByOpModel tokens={tokensR} span="span-12" />
            <div className="export-row"><ExportLinks dataset="tokens" /></div>
          </>}

          {tab === 'automations' && <>
            <RunStats runs={runsR} span="span-12" />
            <AutomationTable runs={runsR} span="span-12" />
            <RunsList runs={runsR} span="span-12" limit={60} title="Run history" filterable />
            <div className="export-row"><ExportLinks dataset="runs" /></div>
          </>}

          {tab === 'knowledge' && <>
            <VaultTiles vault={vault} ingestion={ingestion} growth={growthR} loading={vaultLoading} span="span-12" />
            <GrowthCard growth={growthR} span="span-7" />
            <FoldersCard graph={graph} span="span-5" />
            <IngestTrend series={ingestR} span="span-12" />
            <div className="export-row"><ExportLinks dataset="ingestion" /><ExportLinks dataset="vault" /></div>
          </>}

          {tab === 'review' && <ReviewTab span="span-12" />}
          {tab === 'ask' && <AskTab vault={vaultName} span="span-12" />}
          {tab === 'related' && <RelatedTab graph={graph} vault={vaultName} span="span-12" />}
          {tab === 'actions' && <ActionsTab />}

          {tab === 'graph' && <>
            <GraphCard graph={graph} simEdges={simEdges} onToggleSim={setSimEdges} vault={vaultName} span="span-8" />
            <GraphInsights graph={graph} vault={vaultName} span="span-4" />
          </>}

          {tab === 'activity' && <>
            <ActivityCard live={events} initial={activity} span="span-12" height={640} />
            <div className="export-row"><ExportLinks dataset="activity" /></div>
          </>}
        </main>
      </ErrorBoundary>

      {palette && <CommandPalette commands={commands} onClose={() => setPalette(false)} />}
      {shortcuts && <ShortcutsSheet tabs={visibleTabs} onClose={() => setShortcuts(false)} />}
    </div>
  )
}

export default function App() {
  return (
    <ThemeProvider>
      <ToastHost>
        <Shell />
      </ToastHost>
    </ThemeProvider>
  )
}
