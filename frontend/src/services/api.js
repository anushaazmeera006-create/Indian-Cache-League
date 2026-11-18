const BASE = import.meta.env.VITE_API_BASE || ''

async function req(path, options = {}) {
  const res = await fetch(BASE + path, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new Error(body.error || `HTTP ${res.status}`)
  }
  return res.json()
}

export const api = {
  matches: () => req('/api/matches'),
  match: (id) => req(`/api/matches/${id}`),
  score: (id) => req(`/api/matches/${id}/score`),
  updateScore: (id, score) =>
    req(`/api/matches/${id}/score`, { method: 'PUT', body: JSON.stringify(score) }),
  leaderboard: () => req('/api/leaderboard'),
  stats: () => req('/api/stats'),
  cacheStats: () => req('/api/cache/stats'),
  invalidate: (id) => req(`/api/cache/invalidate/${id}`, { method: 'POST' }),
  health: () => req('/api/health'),
  simStart: (cfg) => req('/api/simulation/start', { method: 'POST', body: JSON.stringify(cfg) }),
  simStop: () => req('/api/simulation/stop', { method: 'POST' }),
  simStatus: () => req('/api/simulation/status'),
  spike: (n, matchId) =>
    req('/api/simulation/spike', { method: 'POST', body: JSON.stringify({ requests: n, match_id: matchId }) }),
}
