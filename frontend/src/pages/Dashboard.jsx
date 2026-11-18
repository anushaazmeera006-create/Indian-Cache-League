import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../services/api.js'

// Home dashboard: live match cards polling the score endpoint.
// Every poll goes through nginx -> backend -> cache -> (on miss) DB + singleflight.
export default function Dashboard() {
  const [matches, setMatches] = useState([])
  const [scores, setScores] = useState({})
  const [cacheStatus, setCacheStatus] = useState({})
  const [err, setErr] = useState('')

  useEffect(() => {
    let alive = true
    async function load() {
      try {
        const ms = await api.matches()
        if (!alive) return
        setMatches(ms)
        const entries = await Promise.all(
          ms.map(async (m) => {
            try {
              const res = await fetch(`/api/matches/${m.id}/score`)
              const hit = res.headers.get('X-Cache')
              const data = await res.json()
              return [m.id, { data, hit }]
            } catch {
              return [m.id, null]
            }
          }),
        )
        if (!alive) return
        const map = {}
        const cm = {}
        for (const [id, v] of entries) {
          if (v) {
            map[id] = v.data
            cm[id] = v.hit
          }
        }
        setScores(map)
        setCacheStatus(cm)
        setErr('')
      } catch (e) {
        if (alive) setErr(e.message)
      }
    }
    load()
    const t = setInterval(load, 2000) // poll at TTL cadence
    return () => { alive = false; clearInterval(t) }
  }, [])

  return (
    <div>
      <h1>Live Matches</h1>
      {err && <div className="error">{err}</div>}
      <div className="grid">
        {matches.map((m) => {
          const s = scores[m.id]
          return (
            <Link to={`/match/${m.id}`} className="card" key={m.id}>
              <div className="card-head">
                <span className="team">{m.team1}</span>
                <span className="vs">vs</span>
                <span className="team">{m.team2}</span>
              </div>
              {s ? (
                <div className="score">
                  <div className="runs">{s.runs}<small>/{s.wickets}</small></div>
                  <div className="meta">
                    {s.overs} ov · {s.status}
                    <span className={`badge ${cacheStatus[m.id] === 'HIT' ? 'hit' : 'miss'}`}>
                      {cacheStatus[m.id] || '…'}
                    </span>
                  </div>
                </div>
              ) : (
                <div className="meta">loading…</div>
              )}
              <div className="venue">{m.venue} · {m.match_date}</div>
            </Link>
          )
        })}
      </div>
    </div>
  )
}
