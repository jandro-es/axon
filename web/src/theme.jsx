import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'

/* ── theme ────────────────────────────────────────────────────────────────
   Three choices, one resolution. `mode` is what the user picked (system by
   default); `theme` is what that resolves to right now. Only the resolved
   value is ever written to <html data-theme>, so CSS and the chart palette
   (which needs literal colours — Recharts takes props, not CSS variables)
   can never disagree about which theme is on screen.

   The same key is read by the bootstrap script in index.html, which applies
   the attribute before first paint so a dark-mode reload never flashes white.
*/
const KEY = 'axon.theme'
export const MODES = ['system', 'light', 'dark']

const prefersDark = () =>
  typeof matchMedia !== 'undefined' && matchMedia('(prefers-color-scheme: dark)').matches

const readMode = () => {
  try {
    const m = localStorage.getItem(KEY)
    return MODES.includes(m) ? m : 'system'
  } catch { return 'system' }
}

const resolve = (mode) => (mode === 'system' ? (prefersDark() ? 'dark' : 'light') : mode)

/* The chart palette. Recharts needs literal colours, so the CSS variables are
   mirrored here — same names, same values, per theme. Changing a colour means
   changing it in both places; that is the price of charts that theme. */
const PALETTES = {
  dark: {
    signal1: '#2fe0cf', signal2: '#7d89f5', signal3: '#b07cf0',
    ok: '#41d693', warn: '#f5b14b', err: '#fb6f6f',
    ink: '#e9edf6', inkDim: '#a3aec4', inkFaint: '#6b7689',
    line: 'rgba(255,255,255,0.08)', grid: 'rgba(255,255,255,0.07)',
    surface: '#10151f', track: '#1b2333',
    cursor: 'rgba(255,255,255,0.05)',
    series: ['#2fe0cf', '#7d89f5', '#b07cf0', '#f5b14b', '#fb6f8f', '#41d693', '#5bb6f0', '#c4cdde'],
  },
  light: {
    signal1: '#0d9c90', signal2: '#4c56d8', signal3: '#8a5cf0',
    ok: '#0f9d63', warn: '#b06d10', err: '#d0453f',
    ink: '#101725', inkDim: '#4c5872', inkFaint: '#7c879e',
    line: 'rgba(15,23,42,0.10)', grid: 'rgba(15,23,42,0.09)',
    surface: '#ffffff', track: '#e4e9f2',
    cursor: 'rgba(15,23,42,0.04)',
    series: ['#0d9c90', '#4c56d8', '#8a5cf0', '#b06d10', '#d0457f', '#0f9d63', '#2a7fc9', '#6b7689'],
  },
}

const ThemeCtx = createContext({ mode: 'system', theme: 'dark', setMode: () => {}, palette: PALETTES.dark })

export function ThemeProvider({ children }) {
  const [mode, setModeState] = useState(readMode)
  const [theme, setTheme] = useState(() => resolve(readMode()))

  // Apply on change, and follow the OS while the choice is "system".
  useEffect(() => {
    const apply = () => {
      const next = resolve(mode)
      setTheme(next)
      document.documentElement.setAttribute('data-theme', next)
    }
    apply()
    if (mode !== 'system' || typeof matchMedia === 'undefined') return
    const mq = matchMedia('(prefers-color-scheme: dark)')
    mq.addEventListener('change', apply)
    return () => mq.removeEventListener('change', apply)
  }, [mode])

  const setMode = useCallback((m) => {
    if (!MODES.includes(m)) return
    setModeState(m)
    try { localStorage.setItem(KEY, m) } catch { /* private mode — the session still themes */ }
  }, [])

  const value = useMemo(
    () => ({ mode, theme, setMode, palette: PALETTES[theme] || PALETTES.dark }),
    [mode, theme, setMode],
  )
  return <ThemeCtx.Provider value={value}>{children}</ThemeCtx.Provider>
}

export const useTheme = () => useContext(ThemeCtx)
export const usePalette = () => useContext(ThemeCtx).palette

/* Icons kept inline: the dashboard ships offline, so no icon font or CDN. */
const ICONS = {
  system: (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true">
      <rect x="1.8" y="2.6" width="12.4" height="8.4" rx="1.4" />
      <path d="M5.5 13.4h5" strokeLinecap="round" />
    </svg>
  ),
  light: (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true">
      <circle cx="8" cy="8" r="3.1" />
      <path d="M8 1.2v1.6M8 13.2v1.6M1.2 8h1.6M13.2 8h1.6M3.3 3.3l1.1 1.1M11.6 11.6l1.1 1.1M12.7 3.3l-1.1 1.1M4.4 11.6l-1.1 1.1" strokeLinecap="round" />
    </svg>
  ),
  dark: (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true">
      <path d="M13.2 9.6A5.6 5.6 0 0 1 6.4 2.8a5.6 5.6 0 1 0 6.8 6.8Z" strokeLinejoin="round" />
    </svg>
  ),
}
const LABEL = { system: 'Match system', light: 'Light', dark: 'Dark' }

export function ThemeToggle() {
  const { mode, setMode } = useTheme()
  return (
    <div className="seg" role="radiogroup" aria-label="Appearance">
      {MODES.map((m) => (
        <button
          key={m}
          role="radio"
          aria-checked={mode === m}
          aria-label={LABEL[m]}
          title={LABEL[m]}
          className={mode === m ? 'on' : ''}
          onClick={() => setMode(m)}
        >
          {ICONS[m]}
        </button>
      ))}
    </div>
  )
}
