#!/usr/bin/env bash
set -euo pipefail
ROOT="$(dirname "$(dirname "$(realpath "$0")")")"
JBS="${JBS_BIN:-$ROOT/bin/jbs}"
SERVER="${JBS_SERVER:-$ROOT/bin/jbs-server}"
URL="${JBS_URL:-http://127.0.0.1:9080}"
LLM="${JBS_LLM:-qoder}"
TIMEOUT="${E2E_TIMEOUT:-30m}"
PROFILE="e2e-host-$$"
ARTIFACTS="$ROOT/e2e/artifacts"
if [[ "${1:-}" == --mixed && "${START_SERVER:-1}" == 1 ]]; then
  printf 'Mixed mode needs an externally started server: set START_SERVER=0.\n' >&2
  exit 1
fi
mkdir -p "$ROOT/bin" "$ARTIFACTS"
if [[ ! -x "$JBS" || ! -x "$SERVER" ]]; then
  GOPROXY="${GOPROXY:-https://goproxy.cn,direct}" go -C "$ROOT" build -o "$JBS" ./cmd/jbs
  GOPROXY="${GOPROXY:-https://goproxy.cn,direct}" go -C "$ROOT" build -o "$SERVER" ./cmd/jbs-server
fi
if [[ "${START_SERVER:-1}" == 1 ]]; then
  "$SERVER" --addr "${URL#http://}" --scripts "$ROOT/scripts" --static "$ROOT/web/dist" --data "$ROOT/data" > "$ARTIFACTS/server-$$.log" 2>&1 &
  PID=$!
  trap 'kill "$PID" 2>/dev/null || true' EXIT
fi
curl --silent --show-error --fail --retry 30 --retry-delay 0 --retry-connrefused --retry-max-time 15 "$URL/api/health" >/dev/null
AI_SEATS="han,hong,lin,xiu,zhang"
if [[ "${1:-}" == --mixed ]]; then AI_SEATS="han,hong,lin"; fi
"$JBS" --server "$URL" --profile "$PROFILE" room create --script eastern-star --ai-seats "$AI_SEATS" --llm "$LLM" > "$ARTIFACTS/room.json"
ROOM="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["room_id"])' "$ARTIFACTS/room.json")"
if [[ "${1:-}" == --mixed ]]; then
  printf 'Room %s at %s. Join xiu and zhang with separate CLI profiles.\n' "$ROOM" "$URL"
  exit 0
fi
set +e
"$JBS" --server "$URL" --profile "$PROFILE" wait --until-phase end --timeout "$TIMEOUT" > "$ARTIFACTS/final-$ROOM.json"
CODE=$?
set -e
if [[ "$CODE" != 3 ]]; then printf 'Game did not complete, CLI exit=%s\n' "$CODE" >&2; exit 1; fi
E2E_ROOM="$ROOM" E2E_DATA="$ROOT/data" E2E_LLM="$LLM" E2E_AI_SEATS="$AI_SEATS" go -C "$ROOT" test -tags e2e ./e2e -v -count=1
printf 'Completed %s (%s).\n' "$ROOM" "$LLM"
