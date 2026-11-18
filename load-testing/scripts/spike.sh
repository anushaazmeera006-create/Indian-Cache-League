#!/usr/bin/env bash
# Quick single-endpoint spike using only curl (no Go toolchain needed).
# Usage: ./spike.sh [requests] [match_id]
N="${1:-1000}"
ID="${2:-104}"
BASE="${BASE:-http://localhost}"
echo "Firing $N requests at $BASE/api/matches/$ID/score ..."
seq 1 "$N" | xargs -P 200 -I{} curl -s -o /dev/null -w "%{http_code}\n" \
  "$BASE/api/matches/$ID/score" | sort | uniq -c
