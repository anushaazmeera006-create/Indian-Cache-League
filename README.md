# ICL — Indian Cache League

A high-performance **distributed caching and load-balancing system** for cricket
live scores, built to survive IPL-scale traffic spikes — the moment a batsman
hits a six and 100,000 phones refresh at once.

> **Core thesis:** without caching, every refresh hits PostgreSQL. With an
> in-memory cache (2s TTL), request coalescing (singleflight), and an Nginx
> load balancer across 3 Go backends, thousands of simultaneous requests
> collapse into a handful of database queries.

---

## 1. Problem Statement

During a live IPL match, every score update triggers a thundering herd:
thousands of clients poll `/api/matches/104/score` within the same second.
Naive architectures send **one DB query per HTTP request**, overwhelming
PostgreSQL exactly when traffic peaks.

ICL solves this with three cooperating mechanisms:

| Mechanism | What it prevents |
|---|---|
| In-memory cache (Go map + RWMutex, TTL) | Repeated DB reads for hot keys |
| TTL expiry + invalidation on writes | Stale scores |
| Singleflight request coalescing | N simultaneous misses → 1 DB query |
| Nginx least-conn LB across 3 backends | Single-point backend saturation |

## 2. Architecture

```mermaid
flowchart TD
    U[100k Users / browsers] -->|HTTP| N[Nginx :80<br/>reverse proxy + least_conn LB]
    N -->|Layer-7 path routing| B1[backend-1 :8080]
    N --> B2[backend-2 :8080]
    N --> B3[backend-3 :8080]
    B1 & B2 & B3 --> C{In-memory cache<br/>map + RWMutex, TTL=2s}
    C -->|HIT| R[JSON response<br/>X-Cache: HIT]
    C -->|MISS| SF[Singleflight group<br/>per-key call coalescing]
    SF -->|leader only| DB[(PostgreSQL<br/>matches / scores / standings)]
    SF -->|shared result| R2[JSON response<br/>X-Cache: MISS]
    DB -.init.sql.-> DB
    F[React SPA :3000/dev or nginx container] -->|/api/*| N
    ADM[Admin Simulation Panel] -->|real HTTP traffic| N
    MET[/api/stats + /api/cache/stats/] --> DASH[Infrastructure Dashboard<br/>Recharts]
```

### Request lifecycle — `GET /api/matches/104/score`

1. Nginx picks a backend (`least_conn`), forwards request.
2. Backend checks `cache.Get("score:104")`.
   - **HIT** → respond immediately with cached JSON. `X-Cache: HIT`. Zero DB queries.
   - **MISS** → enter `singleflight.Do(ctx, "score:104", fn)`.
3. First caller becomes **leader**: double-checks cache, queries PostgreSQL,
   marshals result, `cache.Set(...)`, returns.
4. All concurrent waiters on the same key receive the leader's result.
   Waiters can cancel via `context` if the HTTP client disconnects.
5. `X-Cache: MISS` + `X-Backend-Instance: backend-N` headers on every response.

### Why singleflight matters (the money shot)

```
5,000 simultaneous requests, cache entry just expired:
  without coalescing  → 5,000 PostgreSQL queries
  with singleflight   →     1 PostgreSQL query  (4,999 waiters share it)
```

Verified live: `POST /api/simulation/spike {"requests":2000}` after cache
invalidation produced **exactly 1 DB query** (see `load-testing/results/`).

## 3. Technology Stack

| Layer | Tech |
|---|---|
| Backend | Go 1.22+ (`net/http`, goroutines, `sync.RWMutex`, atomics) |
| Cache | Custom in-memory store — `map[string]*Entry` + TTL + janitor |
| Coalescing | Custom singleflight (`internal/singleflight`) — per-key call registry, ctx-cancellable |
| Database | PostgreSQL 16 (`lib/pq`) + in-memory dev store for local runs |
| LB / Proxy | Nginx (`least_conn`, `max_fails` passive health checks, path routing) |
| Frontend | React 18 + Vite + Recharts, dark IPL dashboard theme |
| Orchestration | Docker Compose (postgres + 3 backends + frontend + nginx) |
| Testing | `go test` (unit + HTTP integration), custom Go load tester, curl scripts |

> Redis is intentionally **not** required: the pedagogically important cache
> is implemented in Go so hit/miss/coalescing behavior is fully visible and
> instrumented in the codebase.

## 4. Project Structure

```
ICL/
├── backend/
│   ├── cmd/server/main.go          # entrypoint: mux, middleware, graceful shutdown
│   ├── internal/
│   │   ├── api/api.go              # handlers: score hot path, stats, simulation
│   │   ├── cache/cache.go          # TTL cache: Get/Set/Delete/janitor/stats
│   │   ├── config/config.go        # env-var configuration
│   │   ├── database/store.go       # Store iface + Postgres + Memory impls
│   │   ├── metrics/metrics.go      # counters, latency window, p50/p95/p99
│   │   ├── middleware/middleware.go# metrics, instance header, logging
│   │   ├── simulation/simulation.go# real-HTTP traffic generator
│   │   └── singleflight/singleflight.go # request coalescing
│   ├── internal/api/api_test.go    # integration tests (hit/miss/TTL/herd)
│   ├── internal/cache/cache_test.go
│   ├── internal/singleflight/singleflight_test.go
│   ├── Dockerfile
│   └── go.mod
├── frontend/
│   ├── src/pages/{Dashboard,MatchDetail,Leaderboard,Metrics,Admin}.jsx
│   ├── src/services/api.js
│   ├── src/styles.css
│   ├── Dockerfile                  # builds SPA, serves via nginx
│   └── package.json
├── nginx/nginx.conf                # LB + Layer-7 routing
├── database/init.sql               # schema + seed data
├── load-testing/
│   ├── hammer/main.go              # Go load tester w/ percentile report
│   ├── scripts/run-tests.sh        # TEST 1–5 scenarios
│   ├── scripts/spike.sh            # curl-only spike
│   └── results/                    # captured run output
├── docker-compose.yml
├── .env.example
└── README.md
```

## 5. API Reference

All responses are JSON. Errors use `{"error": "..."}` with proper status codes.

| Method | Path | Description |
|---|---|---|
| GET | `/api/matches` | List all matches |
| GET | `/api/matches/{id}` | Match metadata |
| GET | `/api/matches/{id}/score` | **Hot path** — cached live score. Headers: `X-Cache: HIT|MISS`, `X-Backend-Instance` |
| PUT | `/api/matches/{id}/score` | Update score in DB + invalidate cache |
| GET | `/api/leaderboard` | Points table (P/W/L/NRR/pts) |
| GET | `/api/stats` | Combined metrics snapshot (HTTP, DB, singleflight, cache) |
| GET | `/api/cache/stats` | Cache counters only |
| POST | `/api/cache/invalidate/{id}` | Force next read to miss |
| GET | `/api/health` | LB health check (`{"status":"ok","instance":...}`) |
| POST | `/api/simulation/start` | `{users, match_id, duration, target_url}` — real HTTP load |
| POST | `/api/simulation/stop` | Stop generator |
| GET | `/api/simulation/status` | Generator counters |
| POST | `/api/simulation/spike` | `{requests, match_id}` — instant N-way burst |

### Sample

```bash
curl -si http://localhost/api/matches/104/score
# HTTP/1.1 200 OK
# X-Cache: MISS
# X-Backend-Instance: backend-2
# {"match_id":104,"team1":"India","team2":"Australia","runs":142,"wickets":2,...}

curl -si http://localhost/api/matches/104/score   # again → X-Cache: HIT
curl -X PUT http://localhost/api/matches/104/score \
     -d '{"team1":"India","team2":"Australia","runs":143,"wickets":2,"overs":"15.5","status":"live"}'
curl http://localhost/api/stats | jq '.db_queries, .coalesced_requests'
```

## 6. Running the Project

### Prerequisites

- Docker + Docker Compose **or**
- Go 1.22+ and Node 20+ (for local dev without Docker)

### Option A — Docker Compose (full stack, recommended)

```bash
npm --prefix frontend run build        # build SPA assets (or let compose build it)
docker compose up -d --build
```

Services: `postgres` (healthy-gated), `backend-1..3`, `frontend`, `nginx`.

- **Frontend:** http://localhost
- **Nginx/API:** http://localhost/api/...
- **Direct backend (bypass LB):** not exposed by default — exec into the
  compose network or temporarily publish ports for debugging.

### Option B — Local, no Docker

```bash
# terminal 1 — backend (in-memory dev store, no Postgres needed)
cd backend && go run ./cmd/server        # PORT=8080

# terminal 2 — frontend dev server (proxies /api -> :8080)
cd frontend && npm install && npm run dev # http://localhost:3000
```

For PostgreSQL locally, set `DATABASE_URL=postgres://...` — the same binary
switches to the real `PostgresStore` automatically.

### Environment variables

See `.env.example`. Key ones: `DATABASE_URL`, `CACHE_ENABLED`, `CACHE_TTL`
(default `2s`), `PORT`, `INSTANCE_ID`, `SIM_TARGET_URL` (point at `http://nginx`
in compose so simulated users traverse the load balancer).

## 7. Testing & Load Testing

### Automated tests

```bash
cd backend
go test ./...
```

Coverage includes: cache get/set/TTL/delete/stats/concurrency;
singleflight thundering-herd proof (**500 concurrent callers → 1 execution**);
HTTP integration tests for hit/miss/TTL-expiry/invalidation/cache-disabled.

### Load tests

```bash
# scripted suite (TEST 1–5)
bash load-testing/scripts/run-tests.sh

# or the Go hammer directly (works against any URL)
cd load-testing/hammer && go mod init hammer 2>/dev/null; go run . \
  -url http://localhost/api/matches/104/score -c 2000 -n 20000 \
  -stats http://localhost
```

**Measured result (this machine, Windows, in-memory dev store):**
20,000 requests @ 2,000 concurrency in 1.57s → **12,727 req/s**, P50 64ms,
P95 364ms, **1 DB query total** — the 2s TTL meant almost everything was a
cache hit; misses were coalesced by singleflight. Full output:
`load-testing/results/local-run.txt`.

To reproduce the thundering herd deterministically:

```bash
curl -X POST localhost/api/cache/invalidate/104   # expire the entry
curl -X POST localhost/api/simulation/spike \
     -d '{"requests":5000,"match_id":104}'        # 5000-way burst
curl localhost/api/stats | jq '{db_queries, coalesced_requests, db_queries_saved}'
# → db_queries increases by ~1; coalesced_requests ≈ 4999
```

### Verifying load balancing

```bash
for i in $(seq 1 30); do curl -sI localhost/api/matches/104/score | grep -i x-backend-instance; done | sort | uniq -c
# → requests spread across backend-1/2/3
```

## 8. How the concepts map to Computer Networks

- **TCP connections:** each HTTP request = one TCP connection (or reused
  keep-alive conn). Go's `net/http` spawns a goroutine per connection.
- **Concurrency:** goroutines are multiplexed onto OS threads; blocking I/O
  (DB queries) parks the goroutine, not the thread — thousands of concurrent
  requests fit in a few MB of stack.
- **epoll / file descriptors:** on Linux, Go's netpoller uses `epoll` so one
  thread waits on thousands of sockets; each connection costs one FD
  (`ulimit -n` governs the ceiling — raise it for 100k-connection demos).
- **Latency/RTT:** every cache hit saves a full backend→Postgres round trip;
  nginx→backend is one extra hop vs. direct, traded for distribution.
- **Timeouts:** `ReadHeaderTimeout`/`ReadTimeout`/`WriteTimeout`/`IdleTimeout`
  on `http.Server` prevent slow-loris resource exhaustion; nginx
  `proxy_read_timeout` bounds upstream waits.

## 9. Database Schema

`database/init.sql` creates `matches`, `scores` (live runs/wickets/overs),
and `standings` (P/W/L/NRR/points), seeded with 3 matches and 6 teams.

## 10. Known Limitations & Future Work

- Per-instance caches are independent — a write invalidates only the backend
  that served the PUT. Production would add Redis pub/sub or a write-through
  invalidation bus (Redis is the planned L2).
- Metrics are per-process; a `/api/cluster/stats` aggregator would be needed
  for fleet-wide dashboards.
- Load-test numbers above are from a Windows dev box with the in-memory
  store — rerun under docker-compose on Linux for representative figures.
- WebSocket push (vs 2s polling) is a natural extension.
- Horizontal autoscaling via consistent-hash LB is future work.

## 11. viva cheat-sheet

| Question | Answer |
|---|---|
| Why not query DB every time? | Hot key, 100k readers → DB melts; cache serves RAM-speed reads |
| How fresh is data? | TTL=2s bounds staleness; PUT invalidates immediately |
| How does singleflight work? | `map[key]*call`; first caller leads, others wait on `done` chan, share result; ctx-cancellable |
| What if leader's DB query fails? | All waiters get the same error; nothing is cached — next request retries |
| How does LB pick a backend? | nginx `least_conn` (+ `max_fails` passive health checks); `X-Backend-Instance` proves distribution |
| File descriptor story? | 1 FD per TCP conn; Go netpoller+epoll multiplexes; goroutine per conn |
