import React, { useState } from 'react'
import { Card, Empty, useToast } from '../ui.jsx'
import { useFetch, postReviewAction } from '../lib.js'

const KIND_LABEL = {
  link: 'Link suggestion', pair: 'Link suggestion', triage: 'Inbox triage',
  resurface: 'Resurfaced', info: 'Record',
}

export function ReviewTab({ span }) {
  const toast = useToast()
  const [nonce, setNonce] = useState(0)
  const { data, error } = useFetch(`/api/review?n=${nonce}`, 5000)
  const [busy, setBusy] = useState(null)

  const items = data?.items || []
  const pending = items.filter((it) => !it.checked)
  const resolved = items.filter((it) => it.checked).slice(-15)

  const act = (id, action) => {
    setBusy(id)
    postReviewAction(id, action)
      .then(() => toast(action === 'accept' ? 'Applied to the vault.' : 'Dismissed.'))
      .catch((e) => toast(String(e.message || e), 'error'))
      .finally(() => { setBusy(null); setNonce((n) => n + 1) })
  }

  // The raw line carries the checkbox and the machine-readable marker the queue
  // file needs; neither belongs on screen.
  const plain = (line) => (line || '').replace(/^- \[.\] /, '').replace(/<!--.*?-->/g, '').trim()

  const describe = (it) => {
    if (it.kind === 'triage') return <>Move <b>{it.note}</b> to <b>{it.folder}</b>{it.tags?.length ? ` (${it.tags.join(', ')})` : ''}</>
    if (it.kind === 'link' || it.kind === 'pair') return <>Link <b>{it.note}</b> to <b>{it.target}</b></>
    if (it.kind === 'resurface') return <>Resurface <b>{it.target}</b> alongside <b>{it.note}</b></>
    return plain(it.line)
  }

  return (
    <Card title="Review queue" meta={`${pending.length} pending`} span={span}>
      {error && <Empty>{typeof error === 'string'
        ? error
        : 'The daemon isn’t answering. The queue reappears when it does.'}</Empty>}
      <div className="list">
        {pending.map((it) => (
          <div className="li review-item" key={it.id}>
            <span className={`kind kind-${it.kind}`}>{KIND_LABEL[it.kind] || it.kind}</span>
            <span className="msg">{describe(it)}</span>
            <span className="review-actions">
              {it.kind !== 'info' && (
                <button className="btn primary" disabled={busy === it.id} onClick={() => act(it.id, 'accept')}>Accept</button>
              )}
              <button className="btn ghost" disabled={busy === it.id} onClick={() => act(it.id, 'dismiss')}>Dismiss</button>
            </span>
          </div>
        ))}
        {pending.length === 0 && !error && (
          <Empty>Queue is clear. Automations append proposals here for you to accept or dismiss — nothing is applied without you.</Empty>
        )}
      </div>
      {resolved.length > 0 && (
        <div className="list resolved">
          {resolved.map((it) => (
            <div className="li dim" key={it.id}><span className="msg">{plain(it.line)}</span></div>
          ))}
        </div>
      )}
    </Card>
  )
}
