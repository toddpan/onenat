#!/usr/bin/env bash
# =====================================================================
#  oneNat 停止脚本: 读取 start-onenat.sh 写入的 onenat.pid 并优雅停止
#  用法: bash stop-onenat.sh
# =====================================================================
set -euo pipefail
cd "$(dirname "$0")"

PIDFILE="${PIDFILE:-./onenat.pid}"

if [ ! -f "$PIDFILE" ]; then
  echo "未找到 $PIDFILE (服务未通过 start-onenat.sh 启动?)"
  exit 0
fi

PID=$(cat "$PIDFILE")
if ! kill -0 "$PID" 2>/dev/null; then
  echo "pid $PID 已不存在, 清理 pidfile"
  rm -f "$PIDFILE"
  exit 0
fi

kill "$PID"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  kill -0 "$PID" 2>/dev/null || break
  sleep 0.5
done
if kill -0 "$PID" 2>/dev/null; then
  echo "优雅停止超时, 强制结束 $PID"
  kill -9 "$PID"
fi
rm -f "$PIDFILE"
echo "✓ oneNat 已停止"
