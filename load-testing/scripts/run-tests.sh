#!/usr/bin/env bash
# ICL load-test scenarios. Requires the stack running (docker compose up -d).
# Each scenario snapshots /api/stats before/after to show DB-query absorption.
set -euo pipefail
BASE="${BASE:-http://localhost}"
HAMMER="go run ./load-testing/hammer"

echo "== TEST 1: Normal traffic — 100 users, same match =="
$HAMMER -url "$BASE/api/matches/104/score" -c 100 -n 5000 -stats "$BASE"

echo ""
echo "== TEST 2: Traffic spike — 1000 concurrent, same score =="
$HAMMER -url "$BASE/api/matches/104/score" -c 1000 -n 20000 -stats "$BASE"

echo ""
echo "== TEST 3: Thundering herd — expire cache then 5000 simultaneous =="
curl -s -X POST "$BASE/api/cache/invalidate/104" > /dev/null
$HAMMER -url "$BASE/api/matches/104/score" -c 5000 -n 5000 -stats "$BASE"

echo ""
echo "== TEST 4: Distribution check — hit each backend directly =="
for p in 8081 8082 8083; do
  echo -n "backend on :$p -> "
  curl -s "http://localhost:$p/api/health" || echo "(not exposed — check X-Backend-Instance headers instead)"
done
echo "Tip: 'curl -sI $BASE/api/matches/104/score | grep X-Backend-Instance' repeatedly"
echo "to observe round-robin/least-conn distribution."

echo ""
echo "== TEST 5: Cache disabled comparison =="
echo "Restart backends with CACHE_ENABLED=false and re-run TEST 1,"
echo "then compare db_queries and P95 latency."
