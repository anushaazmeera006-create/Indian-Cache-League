import { useEffect, useState } from 'react'
import {
  LineChart, Line, BarChart, Bar, PieChart, Pie, Cell,
  XAxis, YAxis, Tooltip, Legend, ResponsiveContainer, CartesianGrid,
} from 'recharts'
import { api } from '../services/api.js'

// Infrastructure monitoring dashboard — every number comes from the live
// /api/stats + /api/cache/stats endpoints (real instrumentation, not samples).
export default function Metrics() {
  const [snap, setSnap] = useState(null)
  const [cache, setCache] = useState(null)
  const [history, setHistory] = useState([])
  const [instances, setInstances] = useState({})

  useEffect(() => {
    let alive = true
    async function poll() {
      try {
        const [s, c, h] = await Promise.all([api.stats(), api.cacheStats(), api.health().catch(() => null)])
        if (!alive) return
        setSnap(s)
        setCache(c)
        if (h?.instance) {
          setInstances((prev) => ({ ...prev, [h.instance]: (prev[h.instance] || 0) + 1 }))
        }
        setHistory((prev) => {
          const next = [...prev, {
            t: new Date().toLocaleTimeString(),
            rps: +(s.requests_per_sec_estimate || 0).toFixed(1),
            hits: c.hits,
            misses: c.misses,
            p95: s.p95_latency_ms,
            dbq: s.db_queries,
          }]
          return next.slice(-60)
        })
      } catch { /* backend may be restarting */ }
    }
    poll()
    const t = setInterval(poll, 1000)
    return () => { alive = false; clearInterval(t) }
  }, [])

  const hitMiss = cache ? [
    { name: 'Hits', value: cache.hits, color: '#2ecc71' },
    { name: 'Misses', value: cache.misses, color: '#e74c3c' },
  ] : []

  return (
    <div>
      <h1>Infrastructure Metrics</h1>
      {snap && (
        <>
          <div className="grid cols-4">
            <div className="stat"><div className="num">{snap.requests_total}</div><div className="lbl">HTTP requests</div></div>
            <div className="stat"><div className="num">{snap.requests_active}</div><div className="lbl">Active now</div></div>
            <div className="stat"><div className="num">{snap.requests_per_sec_estimate?.toFixed(1)}</div><div className="lbl">Req/sec</div></div>
            <div className="stat"><div className="num">{(snap.error_rate * 100).toFixed(2)}%</div><div className="lbl">Error rate</div></div>
            <div className="stat"><div className="num">{snap.p50_latency_ms}ms</div><div className="lbl">P50 latency</div></div>
            <div className="stat"><div className="num">{snap.p95_latency_ms}ms</div><div className="lbl">P95 latency</div></div>
            <div className="stat"><div className="num">{snap.p99_latency_ms}ms</div><div className="lbl">P99 latency</div></div>
            <div className="stat"><div className="num">{snap.avg_latency_ms}ms</div><div className="lbl">Avg latency</div></div>
          </div>

          <h2>Cache</h2>
          {cache && (
            <div className="grid cols-4">
              <div className="stat"><div className="num">{cache.hits}</div><div className="lbl">Hits</div></div>
              <div className="stat"><div className="num">{cache.misses}</div><div className="lbl">Misses</div></div>
              <div className="stat"><div className="num">{(cache.hit_ratio * 100).toFixed(1)}%</div><div className="lbl">Hit ratio</div></div>
              <div className="stat"><div className="num">{cache.entries}</div><div className="lbl">Entries</div></div>
              <div className="stat"><div className="num">{cache.expirations}</div><div className="lbl">Expired</div></div>
              <div className="stat"><div className="num">{cache.memory_bytes}B</div><div className="lbl">Memory</div></div>
              <div className="stat"><div className="num">{cache.ttl_seconds}s</div><div className="lbl">TTL</div></div>
              <div className="stat"><div className="num">{cache.deletes}</div><div className="lbl">Invalidations</div></div>
            </div>
          )}

          <h2>Database</h2>
          <div className="grid cols-4">
            <div className="stat"><div className="num">{snap.db_queries}</div><div className="lbl">Total queries</div></div>
            <div className="stat"><div className="num">{snap.db_queries_per_sec_estimate?.toFixed(1)}</div><div className="lbl">Queries/sec</div></div>
            <div className="stat"><div className="num">{snap.db_avg_latency_ms}ms</div><div className="lbl">Avg query time</div></div>
            <div className="stat"><div className="num">{snap.db_errors}</div><div className="lbl">DB errors</div></div>
          </div>

          <h2>Singleflight (request coalescing)</h2>
          <div className="grid cols-3">
            <div className="stat"><div className="num">{snap.coalesced_requests}</div><div className="lbl">Coalesced requests</div></div>
            <div className="stat"><div className="num">{snap.db_queries_saved}</div><div className="lbl">DB queries saved</div></div>
            <div className="stat"><div className="num">{(snap.singleflight_efficiency * 100).toFixed(1)}%</div><div className="lbl">Efficiency</div></div>
          </div>
        </>
      )}

      <h2>Traffic history (last 60s)</h2>
      <div className="chart">
        <ResponsiveContainer width="100%" height={260}>
          <LineChart data={history}>
            <CartesianGrid strokeDasharray="3 3" stroke="#333" />
            <XAxis dataKey="t" stroke="#888" fontSize={11} />
            <YAxis stroke="#888" fontSize={11} />
            <Tooltip contentStyle={{ background: '#111', border: '1px solid #333' }} />
            <Legend />
            <Line type="monotone" dataKey="rps" name="Req/sec" stroke="#3498db" dot={false} />
            <Line type="monotone" dataKey="p95" name="P95 ms" stroke="#f39c12" dot={false} />
          </LineChart>
        </ResponsiveContainer>
      </div>

      <h2>Cache hits vs misses</h2>
      <div className="grid cols-2">
        <div className="chart">
          <ResponsiveContainer width="100%" height={220}>
            <PieChart>
              <Pie data={hitMiss} dataKey="value" nameKey="name" innerRadius={50} outerRadius={80} label>
                {hitMiss.map((e) => <Cell key={e.name} fill={e.color} />)}
              </Pie>
              <Tooltip />
              <Legend />
            </PieChart>
          </ResponsiveContainer>
        </div>
        <div className="chart">
          <ResponsiveContainer width="100%" height={220}>
            <BarChart data={history.slice(-30)}>
              <CartesianGrid strokeDasharray="3 3" stroke="#333" />
              <XAxis dataKey="t" stroke="#888" fontSize={10} />
              <YAxis stroke="#888" fontSize={11} />
              <Tooltip contentStyle={{ background: '#111', border: '1px solid #333' }} />
              <Legend />
              <Bar dataKey="hits" name="Hits" fill="#2ecc71" />
              <Bar dataKey="misses" name="Misses" fill="#e74c3c" />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </div>

      <h2>Backend instance distribution</h2>
      <p className="meta">Requests seen by this frontend's backend. In the full stack,
      check <code>X-Backend-Instance</code> response headers or query each backend's
      <code>/api/stats</code> to see nginx round-robin distribution.</p>
      <ul>
        {Object.entries(instances).map(([k, v]) => <li key={k}>{k}: {v} health polls</li>)}
      </ul>
    </div>
  )
}
