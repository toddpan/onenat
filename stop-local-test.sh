#!/usr/bin/env bash
# 停止本地测试的全部进程
cd "$(dirname "$0")"
T=/tmp/ngrok-local-test
for f in agent ngrokd sshd web; do
  [ -f "$T/$f.pid" ] && kill "$(cat $T/$f.pid)" 2>/dev/null
done
pkill -f "bin/ngrokd" 2>/dev/null
pkill -f "bin/ngrok " 2>/dev/null
pkill -f sshd_sim.py 2>/dev/null
pkill -f websim.py 2>/dev/null
echo "stopped."
