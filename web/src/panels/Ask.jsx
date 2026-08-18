import React, { useState } from 'react'
import { Card, Empty, useToast } from '../ui.jsx'
import { noteName, postAsk, vaultURI } from '../lib.js'

// Citations are vault paths. When the vault name is known they open the note
// in Obsidian; otherwise they stay plain text rather than a dead link.
function SourceList({ paths, vault }) {
  if (!paths || paths.length === 0) return null
  return (
    <ul className="src-list">
      {paths.map((s) => {
        const uri = vaultURI(vault, s)
        return (
          <li key={s}>
            <i className="swatch" style={{ background: 'var(--signal-1)' }} />
            {uri ? <a className="note-link" href={uri} title={`Open ${s} in Obsidian`}>{noteName(s)}</a> : <span className="p">{s}</span>}
          </li>
        )
      })}
    </ul>
  )
}

export function AskTab({ vault, span }) {
  const toast = useToast()
  const [q, setQ] = useState('')
  const [busy, setBusy] = useState(false)
  const [ans, setAns] = useState(null)

  const submit = (e) => {
    e.preventDefault()
    if (!q.trim() || busy) return
    setBusy(true); setAns(null)
    postAsk(q.trim())
      .then(setAns)
      .catch((e2) => toast(String(e2.message || e2), 'error'))
      .finally(() => setBusy(false))
  }

  return (
    <Card title="Ask your vault" meta="answers are grounded in your notes, or refused" span={span}>
      <form className="ask-form" onSubmit={submit}>
        <input
          className="input" autoFocus
          placeholder="Ask a question answered only from your notes…"
          value={q} onChange={(e) => setQ(e.target.value)}
          aria-label="Question"
        />
        <button className="btn primary" type="submit" disabled={busy || !q.trim()}>{busy ? 'Asking…' : 'Ask'}</button>
      </form>

      {!ans && !busy && (
        <Empty>
          Retrieval first, then one grounded answer with citations. Nothing outside your vault is consulted,
          and every call goes through the token budget.
        </Empty>
      )}

      {ans && ans.refused && (
        <div className="ask-answer refused">
          <p><b>No answer.</b> {ans.reason}</p>
          {ans.sources?.length > 0 && (
            <>
              <p className="ask-src-label">Retrieved, but not enough to answer</p>
              <SourceList paths={ans.sources} vault={vault} />
            </>
          )}
        </div>
      )}

      {ans && !ans.refused && (
        <div className="ask-answer">
          <p className="ask-text">{ans.answer}</p>
          <p className="ask-src-label">Sources</p>
          <SourceList paths={ans.citations || []} vault={vault} />
          <p className="ask-meta">≈{ans.tokens} tokens · recorded in the ledger</p>
        </div>
      )}
    </Card>
  )
}
