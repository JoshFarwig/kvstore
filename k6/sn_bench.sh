#!/bin/sh
set -e

CPUS="${CPUS:-2}"
MEM="${MEM:-1g}"
NAME=sn-kv-bench

docker build -t kvstore "$(dirname "$0")/.."
docker run -d --rm --cpus="$CPUS" --memory="$MEM" --memory-swap="$MEM" -p 8080:8080 --name "$NAME" kvstore
trap 'docker stop "$NAME" > /dev/null' EXIT

for i in $(seq 1 20); do
  curl -sf localhost:8080/healthz >/dev/null && break
  sleep 0.5
done

k6 run "$(dirname "$0")/sn_bench.js"
