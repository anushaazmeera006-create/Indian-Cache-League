import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { api } from '../services/api.js'

// Match detail: scorecard + manual score update (triggers cache invalidation).
export default function MatchDetail() {
  const { id } = useParams()
  const [score, setScore] = useState(null)
  const [match, setMatch] = useState(null)
  const [hit, setHit] = useState('')
  const [msg, setMsg] = useState('')

  async function refresh() {
    try {
      const res = await fetch(`/api/matches/${id}/score`)
      setHit(res.headers.get('X-Cache') || '')
      setScore(await res.json())
    } catch (e) { setMsg(e.message) }
  }

  useEffect(() => {
    refresh()
    api.match(id).then(setMatch).catch(() => {})
    const t = setInterval(refresh, 2000)
    return () => clearInterval(t)
  }, [id])

  async function bumpRuns() {
    if (!score) return
    const updated = { ...score, runs: score.runs + 1, overs: score.overs }
    await api.updateScore(id, updated)
    setMsg('Score updated — cache invalidated, next read repopulates from DB')
    refresh()
  }

  async function invalidate() {
    await api.invalidate(id)
    setMsg('Cache entry invalidated manually')
    refresh()
  }

  return (
    <div>
      <p><Link to="/">← All matches</Link></p>
      <h1>Match {id} Scorecard</h1>
      {match && <p className="venue">{match.team1} vs {match.team2} — {match.venue}</p>}
      {score && (
        <div className="card wide">
          <div className="score big">
            <span>{score.team1} {score.runs}/{score.wickets}</span>
            <span className="meta">({score.overs} overs) · {score.status}</span>
          </div>
          <div className="meta">
            Last updated: {new Date(score.last_updated).toLocaleTimeString()} ·
            Cache: <span className={`badge ${hit === 'HIT' ? 'hit' : 'miss'}`}>{hit}</span>
          </div>
          <div className="row">
            <button onClick={bumpRuns}>Simulate boundary (+1 run)</button>
            <button onClick={invalidate}>Invalidate cache</button>
            <button onClick={refresh}>Refresh</button>
          </div>
          {msg && <p className="ok">{msg}</p>}
        </div>
      )}
      {msg && !score && <p className="error">{msg}</p>}
    </div>
  )
}
