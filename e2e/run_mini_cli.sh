#!/usr/bin/env bash
set -euo pipefail
ROOT="$(dirname "$(dirname "$(realpath "$0")")")"
JBS="$ROOT/bin/jbs"
SERVER="$ROOT/bin/jbs-server"
URL="${JBS_MINI_URL:-http://127.0.0.1:9085}"
TEMP="$(mktemp -d -t jbs-mini-cli-XXXXXX)"
export HOME="$TEMP"
cleanup() { kill "${PID:-}" 2>/dev/null || true; rm -rf "$TEMP"; }
trap cleanup EXIT
"$SERVER" --addr "${URL#http://}" --scripts "$ROOT/testdata/scripts" --static "$ROOT/web/dist" --data "$TEMP/data" > "$TEMP/server.log" 2>&1 &
PID=$!
curl --silent --show-error --fail --retry 30 --retry-delay 0 --retry-connrefused --retry-max-time 10 "$URL/api/health" >/dev/null
"$JBS" --profile host --server "$URL" room create --script mini > "$TEMP/room.json"
ROOM="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["room_id"])' "$TEMP/room.json")"
for PLAYER in a b c; do
  "$JBS" --profile "$PLAYER" --server "$URL" join "$ROOM" --char "$PLAYER" --name "$PLAYER" > /dev/null
done
ENDED=0
for (( ROUND=0; ROUND<50 && ENDED==0; ROUND++ )); do
  PROGRESS=0
  for PLAYER in a b c; do
    "$JBS" --profile "$PLAYER" --server "$URL" state > "$TEMP/state.json"
    read -r ACTION ARG < <(python3 -c '
import json,sys
v=json.load(open(sys.argv[1]))
if v["ended"]:
 print("end -"); sys.exit()
assert v["pid"]==sys.argv[2]
actions={a["type"]:a for a in v["allowed_actions"] if not a.get("optional")}
for action in ["search","say","vote.cast","ready"]:
 if action in actions:
  arg=actions[action]["options"][0]["value"] if action=="search" else "-"
  print(action,arg); break
else: print("wait -")
' "$TEMP/state.json" "$PLAYER")
    case "$ACTION" in
      end) ENDED=1; break ;;
      search) "$JBS" --profile "$PLAYER" --server "$URL" search "$ARG" > /dev/null ;;
      say) "$JBS" --profile "$PLAYER" --server "$URL" say "请各位核对时间线。" > /dev/null ;;
      vote.cast) "$JBS" --profile "$PLAYER" --server "$URL" vote murderer b > /dev/null ;;
      ready) "$JBS" --profile "$PLAYER" --server "$URL" ready > /dev/null ;;
      wait) continue ;;
      *) printf 'Unexpected CLI action: %s\n' "$ACTION" >&2; exit 1 ;;
    esac
    PROGRESS=1
  done
  if [[ "$ENDED" == 0 && "$PROGRESS" == 0 ]]; then printf 'Mini game stalled.\n' >&2; exit 1; fi
done
[[ "$ENDED" == 1 ]]
python3 - "$TEMP/data/rooms/$ROOM/events.jsonl" <<'PY'
import collections,json,sys
es=[json.loads(line) for line in open(sys.argv[1])]
phases=[e["payload"]["id"] for e in es if e["type"]=="phase.enter"]
assert phases==["lobby","reading","intro","search1","discussion","search2","vote","reveal","end"]
speeches=collections.Counter(e["actor"] for e in es if e["type"]=="chat.say")
assert speeches=={"a":2,"b":2,"c":2},speeches
assert len([e for e in es if e["type"]=="vote.cast"])==3
assert any(e["type"]=="game.end" for e in es)
for pid in ["a","b","c"]:
 profile=json.load(open(sys.argv[1].split("/data/rooms/")[0]+"/.jbs/"+pid+".json"))
 assert profile["player_id"]==pid
print("Mini CLI game passed: 3 independent profiles, 9 phases, 6 speeches, 3 votes.")
PY
