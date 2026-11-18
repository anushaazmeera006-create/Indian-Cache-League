import { useEffect, useRef, useState } from 'react'
import { api } from '../services/api.js'

// Admin / simulation panel — every control fires REAL HTTP traffic through
// the stack (nginx -> backend -> cache -> singleflight -> DB).
export default function Admin() {
  const [users, setUsers] = useState(100)
  const [matchId, setMatchId] = useState(104)
  const [duration, setDuration] = useState('10s')
  const [target, setTarget] = useState('')
  const [spikeN, setSpikeN] = useState(1000)
  const [sim, setSim] = useState(null)
  const [msg, setMsg] = useState('')
  const [stats, setStats] = useState(null)
  const [toasts, setToasts] = useState([])
  const lastHits = useRef(0)
  const lastToastAt = useRef(0)

  function pushToast(text) {
    const id = Date.now() + Math.random()
    setToasts((t) => [...t.slice(-4), { id, text }])
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 3500)
    if (typeof Notification !== 'undefined' && Notification.permission === 'granted') {
      try { new Notification('ICL — Live score update', { body: 'Live score update sent — served from cache (X-Cache: HIT)' }) } catch {}
    }
  }

  useEffect(() => {
    const t = setInterval(() => {
      api.simStatus().then((s) => {
        setSim(s)
        const hits = s.cache_hits ?? 0
        if (hits < lastHits.current) lastHits.current = hits // new run reset the counter
        if (s.running && hits > lastHits.current && hits > 0 && Date.now() - lastToastAt.current > 2000) {
          lastToastAt.current = Date.now()
          pushToast('Live score update sent — served from cache (X-Cache: HIT)')
        }
        lastHits.current = hits
        api.stats().then(setStats).catch(() => {})
      }).catch(() => {})
    }, 1000)
    return () => clearInterval(t)
  }, [])

  async function start() {
    if (typeof Notification !== 'undefined' && Notification.permission === 'default') {
      Notification.requestPermission().catch(() => {})
    }
    try {
      const body = { users, match_id: matchId, duration }
      if (target) body.target_url = target
      const s = await api.simStart(body)
      setSim(s)
      setMsg('Simulation started')
    } catch (e) { setMsg('Error: ' + e.message) }
  }

  async function stop() {
    try { setSim(await api.simStop()); setMsg('Simulation stopped') }
    catch (e) { setMsg('Error: ' + e.message) }
  }

  async function fireSpike() {
    setMsg(`Firing ${spikeN} simultaneous requests at match ${matchId}...`)
    try {
      const r = await api.spike(spikeN, matchId)
      setMsg(`Spike done: ${r.succeeded}/${r.requests} OK in ${r.duration_ms}ms`)
    } catch (e) { setMsg('Error: ' + e.message) }
  }

  async function expireCache() {
    await api.invalidate(matchId)
    setMsg(`Cache for match ${matchId} invalidated — next read will miss and hit the DB once (singleflight)`)
  }

  return (
    <div>
      <h1>Simulation &amp; Load Testing</h1>
      <div className="grid cols-2">
        <div className="card">
          <h3>Traffic generator</h3>
          <label>Concurrent users
            <input type="number" value={users} min={1} max={5000}
              onChange={(e) => setUsers(+e.target.value)} />
          </label>
          <label>Match ID
            <input type="number" value={matchId} min={1}
              onChange={(e) => setMatchId(+e.target.value)} />
          </label>
          <label>Duration
            <input type="text" value={duration} placeholder="10s"
              onChange={(e) => setDuration(e.target.value)} />
          </label>
          <label>Target URL (optional — defaults to nginx)
            <input type="text" value={target} placeholder="http://localhost"
              onChange={(e) => setTarget(e.target.value)} />
          </label>
          <div className="row">
            <button onClick={start} disabled={sim?.running}>Start</button>
            <button onClick={stop} disabled={!sim?.running}>Stop</button>
          </div>
          {sim && (
            <div className="meta">
              Status: {sim.running ? 'RUNNING' : 'idle'} · sent {sim.requests_sent ?? 0} ·
              ok {sim.successes ?? 0} · fail {sim.failures ?? 0} ·
              cache hits {sim.cache_hits ?? 0}
            </div>
          )}
          {sim && (sim.cache_hits ?? 0) > 0 && (
            <p className="ok">Live score update sent — served from cache (X-Cache: HIT)</p>
          )}
        </div>

        <div className="card">
          <h3>Thundering herd trigger</h3>
          <label>Simultaneous requests
            <input type="number" value={spikeN} min={1} max={20000}
              onChange={(e) => setSpikeN(+e.target.value)} />
          </label>
          <div className="row">
            <button onClick={expireCache}>Expire cache entry</button>
            <button onClick={fireSpike}>Fire spike</button>
          </div>
          <p className="meta">
            Expire the cache, then fire the spike: singleflight should collapse
            thousands of simultaneous misses into ~1 database query.
            Watch <strong>coalesced requests</strong> and <strong>DB queries saved</strong>
            {' '}on the Infrastructure page.
          </p>
        </div>
      </div>

      <div className="toasts">
        {toasts.map((t) => <div key={t.id} className="toast">{t.text}</div>)}
      </div>

      {msg && <p className="ok">{msg}</p>}

      {stats && (
        <div className="card">
          <h3>Live counters (this backend)</h3>
          <div className="grid cols-4">
            <div className="stat"><div className="num">{stats.db_queries}</div><div className="lbl">DB queries</div></div>
            <div className="stat"><div className="num">{stats.coalesced_requests}</div><div className="lbl">Coalesced</div></div>
            <div className="stat"><div className="num">{stats.db_queries_saved}</div><div className="lbl">DB saved</div></div>
            <div className="stat"><div className="num">{stats.requests_total}</div><div className="lbl">Requests</div></div>
          </div>
        </div>
      )}
    </div>
  )
}
