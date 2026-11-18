import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Routes, Route, NavLink } from 'react-router-dom'
import App from './App.jsx'
import Dashboard from './pages/Dashboard.jsx'
import MatchDetail from './pages/MatchDetail.jsx'
import Leaderboard from './pages/Leaderboard.jsx'
import Metrics from './pages/Metrics.jsx'
import Admin from './pages/Admin.jsx'
import './styles.css'

function Layout() {
  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <span className="logo">🏏</span> ICL — Indian Cache League
        </div>
        <nav>
          <NavLink to="/" end>Matches</NavLink>
          <NavLink to="/leaderboard">Leaderboard</NavLink>
          <NavLink to="/metrics">Infrastructure</NavLink>
          <NavLink to="/admin">Simulation</NavLink>
        </nav>
      </header>
      <main>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/match/:id" element={<MatchDetail />} />
          <Route path="/leaderboard" element={<Leaderboard />} />
          <Route path="/metrics" element={<Metrics />} />
          <Route path="/admin" element={<Admin />} />
        </Routes>
      </main>
      <footer className="footer">
        ICL demonstrates in-memory caching, TTL expiry, singleflight request coalescing,
        and load balancing under IPL-scale live-score traffic.
      </footer>
    </div>
  )
}

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <BrowserRouter>
      <Layout />
    </BrowserRouter>
  </React.StrictMode>,
)
