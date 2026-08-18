import React, { useMemo, useState } from 'react'
import { Card, Empty, useToast } from '../ui.jsx'
import { getRelated, noteName, vaultURI } from '../lib.js'

/* Related is the zero-token panel: pure vector maths over notes already
   embedded. The path box is the friction — so it autocompletes from the note
   list the graph endpoint already returned, and every result is itself
   clickable to walk the neighbourhood. */
export function RelatedTab({ graph, vault, span }) {
  const toast = useToast()
  const [path, setPath] = useState('')
  const [busy, setBusy] = useState(false)
  const [rows, setRows] = useState(null)
  const [subject, setSubject] = useState('')

  const paths = useMemo(
    () => (graph?.nodes || []).map((n) => n.path).filter(Boolean).sort(),
    [graph],
  )

  const run = (p) => {
    const target = (p || '').trim()
    if (!target || busy) return
    setBusy(true); setRows(null)
    getRelated(target)
      .then((d) => { setRows(d.related || []); setSubject(target) })
      .catch((e) => toast(String(e.message || e), 'error'))
      .finally(() => setBusy(false))
  }

  return (
    <Card title="Related notes" meta="embedding similarity — no tokens spent" span={span}>
      <form className="ask-form" onSubmit={(e) => { e.preventDefault(); run(path) }}>
        <input
          className="input" list="axon-note-paths" autoFocus
          placeholder={paths.length ? 'Start typing a note path…' : 'Vault-relative note path, e.g. 01-Projects/Axon.md'}
          value={path} onChange={(e) => setPath(e.target.value)}
          aria-label="Note path"
        />
        <datalist id="axon-note-paths">
          {paths.slice(0, 1500).map((p) => <option key={p} value={p} />)}
        </datalist>
        <button className="btn primary" type="submit" disabled={busy || !path.trim()}>{busy ? 'Finding…' : 'Find related'}</button>
      </form>

      {!rows && !busy && (
        <Empty>
          Pick a note to see what your vault already knows next to it.
          {paths.length > 0 && <> {paths.length.toLocaleString()} notes are indexed.</>}
        </Empty>
      )}

      {rows && rows.length === 0 && (
        <Empty>Nothing similar to <b>{noteName(subject)}</b> yet. Notes need embeddings first — check the queue on the Knowledge tab.</Empty>
      )}

      {rows && rows.length > 0 && (
        <div className="list">
          {rows.map((r) => {
            const uri = vaultURI(vault, r.path)
            return (
              <div className="li rel-row" key={r.path}>
                <span className="grow" title={r.path}>
                  {uri ? <a className="note-link" href={uri} title={`Open ${r.path} in Obsidian`}>{r.path}</a> : r.path}
                </span>
                <span className="rel-bar"><i style={{ width: `${Math.max(4, Math.min(100, r.similarity * 100))}%` }} /></span>
                <span className="rel-sim">{r.similarity.toFixed(3)}</span>
                <button className="btn ghost" onClick={() => { setPath(r.path); run(r.path) }}>Walk</button>
              </div>
            )
          })}
        </div>
      )}
    </Card>
  )
}
