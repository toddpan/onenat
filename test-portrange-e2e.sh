#!/usr/bin/env bash
# E2E smoke: server-side -portRange 30000-40000 enforcement
# 运行: bash test-portrange-e2e.sh
set -u
cd "$(dirname "$0")"
T=/tmp/onenat-portrange-test
rm -rf "$T"; mkdir -p "$T/home"
FAIL=0

cleanup() {
  for f in "$T"/*.pid; do [ -f "$f" ] && kill "$(cat "$f")" 2>/dev/null; done
}
trap cleanup EXIT

echo "[0] invalid -portRange must abort with a clear error"
./bin/ngrokd -portRange 40000-30000 -log="$T/bad.log" 2>"$T/bad.err"; RC=$?
ERRMSG=$(cat "$T/bad.err")
if [ "$RC" != 0 ] && echo "$ERRMSG" | grep -q "invalid -portRange"; then
  echo "  ✓ rejected: $ERRMSG"
else
  echo "  ✗ expected non-zero exit + error, got rc=$RC err='$ERRMSG'"; FAIL=1
fi

# ---------- 1. server WITH range 30000-40000 ----------
./bin/ngrokd -domain 127.0.0.1 -httpAddr=":18180" -httpsAddr="" \
  -tunnelAddr=":15543" -webAddr="" -portRange 30000-40000 \
  -log="$T/srv.log" -log-level=INFO &
echo $! > "$T/srv.pid"
sleep 1
kill -0 "$(cat "$T/srv.pid")" 2>/dev/null || { echo "  ✗ server died (port conflict? see $T/srv.log)"; exit 1; }

echo "[1] startup log announces the configured range:"
if grep -q "Public TCP port-mapping range: 30000-40000" "$T/srv.log"; then
  echo "  ✓ logged"
else
  echo "  ✗ missing range log line"; FAIL=1
fi

run_agent() { # $1 log name, $2 remote-port ("" = auto)
  local extra=""
  [ -n "$2" ] && extra="-remote-port $2"
  HOME="$T/home" ./bin/ngrok -log="$T/$1.log" -log-level=INFO \
    agent -server=127.0.0.1:15543 $extra 127.0.0.1:19999 \
    >/dev/null 2>&1 &
  echo $! > "$T/$1.pid"
}

# ---------- 2. in-range requested port 31000 ----------
run_agent inrange 31000
sleep 2
echo "[2] in-range request 31000 should bind:"
if nc -z 127.0.0.1 31000; then echo "  ✓ port 31000 listening"; else echo "  ✗ port 31000 NOT listening"; FAIL=1; fi

# ---------- 3. out-of-range requested port 22022 must be rejected ----------
run_agent outrange 22022
sleep 2
echo "[3] out-of-range request 22022 must be rejected:"
MSG=$(grep -oE "Rejected tunnel tcp [^:]*: Public port 22022 is outside the allowed mapping range \[30000-40000\]" "$T/srv.log" | tail -1)
if [ -n "$MSG" ]; then
  echo "  ✓ server logged: $MSG"
else
  echo "  ✗ no rejection in server log"; FAIL=1
fi

# ---------- 4. auto-assigned port must fall inside the range ----------
run_agent auto ""
sleep 2
echo "[4] auto-assigned port inside [30000,40000]:"
AUTOURL=$(grep -oE "tcp://[0-9.]+:[0-9]+" "$T/auto.log" 2>/dev/null | tail -1)
AUTOPORT=${AUTOURL##*:}
if [ -n "$AUTOPORT" ] && [ "$AUTOPORT" -ge 30000 ] 2>/dev/null && [ "$AUTOPORT" -le 40000 ] 2>/dev/null; then
  echo "  ✓ auto port $AUTOPORT in range ($AUTOURL)"
else
  echo "  ✗ auto port out of range or missing: '$AUTOURL'"; FAIL=1
fi

# ---------- 5. server WITHOUT range keeps old behavior ----------
./bin/ngrokd -domain 127.0.0.1 -httpAddr=":18181" -httpsAddr="" \
  -tunnelAddr=":15544" -webAddr="" \
  -log="$T/srv2.log" -log-level=INFO &
echo $! > "$T/srv2.pid"
sleep 1
HOME="$T/home" ./bin/ngrok -log="$T/norange.log" -log-level=INFO \
  agent -server=127.0.0.1:15544 -remote-port 22022 127.0.0.1:19999 \
  >/dev/null 2>&1 &
echo $! > "$T/norange.pid"
sleep 2
echo "[5] unrestricted server still accepts 22022:"
if grep -q "port-mapping range: unrestricted" "$T/srv2.log" && nc -z 127.0.0.1 22022; then
  echo "  ✓ logged 'unrestricted' and port 22022 listening"
else
  echo "  ✗ unrestricted server misbehaves"; FAIL=1
fi

echo
[ "$FAIL" = 0 ] && echo "ALL PASS" || echo "FAILURES PRESENT"
exit "$FAIL"
