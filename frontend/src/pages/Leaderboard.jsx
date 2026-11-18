import { useEffect, useState } from 'react'
import { api } from '../services/api.js'

export default function Leaderboard() {
  const [rows, setRows] = useState([])
  const [err, setErr] = useState('')

  useEffect(() => {
    api.leaderboard().then(setRows).catch((e) => setErr(e.message))
  }, [])

  return (
    <div>
      <h1>Points Table</h1>
      {err && <div className="error">{err}</div>}
      <table className="table">
        <thead>
          <tr><th>#</th><th>Team</th><th>P</th><th>W</th><th>L</th><th>NRR</th><th>Pts</th></tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={r.team}>
              <td>{i + 1}</td><td>{r.team}</td><td>{r.played}</td>
              <td>{r.won}</td><td>{r.lost}</td>
              <td>{r.nrr > 0 ? '+' : ''}{r.nrr.toFixed(3)}</td>
              <td><strong>{r.points}</strong></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
