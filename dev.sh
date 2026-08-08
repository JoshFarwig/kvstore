#!/bin/sh
set -e

export NODE_ID="${NODE_ID:-dev}"
export CPU_PCT_CAP="${CPU_PCT_CAP:-80}"
export MEM_PCT_CAP="${MEM_PCT_CAP:-80}"

go run .
