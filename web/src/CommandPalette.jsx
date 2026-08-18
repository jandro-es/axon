import React, { useEffect, useMemo, useRef, useState } from 'react'
import { Overlay } from './ui.jsx'
import { fuzzy, modKey } from './lib.js'

/* Everything in the palette is also reachable by pointer — it is a shortcut,
   never the only route to a control. */
export function CommandPalette({ commands, onClose }) {
  const [q, setQ] = useState('')
  const [i, setI] = useState(0)
  const listRef = useRef(null)

  const hits = useMemo(
    () => commands.filter((c) => fuzzy(q, `${c.group} ${c.label} ${c.keywords || ''}`)).slice(0, 40),
    [commands, q],
  )

  useEffect(() => { setI(0) }, [q])
  useEffect(() => {
    const el = listRef.current?.querySelector('[aria-selected="true"]')
    el?.scrollIntoView({ block: 'nearest' })
  }, [i])

  const onKeyDown = (e) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setI((v) => Math.min(hits.length - 1, v + 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setI((v) => Math.max(0, v - 1)) }
    else if (e.key === 'Enter') {
      e.preventDefault()
      const c = hits[i]
      if (c) { c.run(); onClose() }
    }
  }

  return (
    <Overlay onClose={onClose} label="Command palette">
      <div className="palette glass" onKeyDown={onKeyDown}>
        <input
          className="palette-input" autoFocus value={q} onChange={(e) => setQ(e.target.value)}
          placeholder="Jump to a view, change the range, export data…"
          aria-label="Command" role="combobox" aria-expanded="true" aria-controls="axon-palette-list"
        />
        <div className="palette-list" id="axon-palette-list" role="listbox" ref={listRef}>
          {hits.map((c, n) => (
            <button
              key={c.id} role="option" aria-selected={n === i}
              className="palette-item"
              onMouseEnter={() => setI(n)}
              onClick={() => { c.run(); onClose() }}
            >
              <span className="grp">{c.group}</span>
              <span className="grow">{c.label}</span>
              {c.hint && <span className="hint">{c.hint}</span>}
            </button>
          ))}
          {hits.length === 0 && <div className="empty">No command matches “{q}”.</div>}
        </div>
        <div className="palette-foot">
          <span><kbd>↑</kbd><kbd>↓</kbd> move</span>
          <span><kbd>↵</kbd> run</span>
          <span><kbd>esc</kbd> close</span>
          <span style={{ marginLeft: 'auto' }}><kbd>{modKey}</kbd><kbd>K</kbd></span>
        </div>
      </div>
    </Overlay>
  )
}

export function ShortcutsSheet({ onClose, tabs }) {
  return (
    <Overlay onClose={onClose} label="Keyboard shortcuts">
      <div className="shortcuts glass">
        <h3>Keyboard</h3>
        {[
          [`${modKey} K`, 'Command palette'],
          ['1 – 9', `Jump to ${tabs.slice(0, 3).map(([, l]) => l).join(', ')}…`],
          ['/', 'Focus the filter on this view'],
          ['R', 'Cycle the time range'],
          ['T', 'Cycle appearance: system, light, dark'],
          ['?', 'This sheet'],
          ['Esc', 'Close whatever is open'],
        ].map(([k, what]) => (
          <div className="row" key={k}><span>{what}</span><kbd>{k}</kbd></div>
        ))}
      </div>
    </Overlay>
  )
}
