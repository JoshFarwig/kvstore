#!/bin/sh
set -e

CPUS="${CPUS:-2}"
MEM="${MEM:-1.5g}"
NODE_ID="${NODE_ID:-sn-bench}"
CPU_PCT_CAP="${CPU_PCT_CAP:-60}"
MEM_PCT_CAP="${MEM_PCT_CAP:-60}"
SCRIPT="${1:-sn/latency_bench.js}"
NAME=sn-kv-bench

echo "cpus=$CPUS mem=$MEM node_id=$NODE_ID cpu_pct_cap=$CPU_PCT_CAP mem_pct_cap=$MEM_PCT_CAP script=$SCRIPT"

docker build -t kvstore "$(dirname "$0")/.."
docker run -d --cpus="$CPUS" --memory="$MEM" --memory-swap="$MEM" \
  -e NODE_ID="$NODE_ID" -e CPU_PCT_CAP="$CPU_PCT_CAP" -e MEM_PCT_CAP="$MEM_PCT_CAP" \
  -p 8080:8080 --name "$NAME" kvstore
trap 'docker stop "$NAME" > /dev/null 2>&1; docker rm "$NAME" > /dev/null' EXIT

for i in $(seq 1 20); do
  curl -sf localhost:8080/healthz >/dev/null && break
  sleep 0.5
done

k6 run -e NODE_ID="$NODE_ID" "$(dirname "$0")/$SCRIPT"

if [ "$(docker inspect -f '{{.State.OOMKilled}}' "$NAME")" = "true" ]; then
  echo "FAIL: container was OOM-killed" >&2
  exit 1
fi
