#!/usr/bin/env bash
# 发布包冒烟测试: 解压 → 启动 → HTTP 验证 → 停止
set -euo pipefail
cd /Users/tsbj/feyanggit/ngrok

rm -rf /tmp/reltest && mkdir -p /tmp/reltest
tar -C /tmp/reltest -xzf dist/ngrokd-r2026.09.01.tar.gz
cd /tmp/reltest/ngrokd-r2026.09.01

echo "=== 启动(空 HTTP_PORT 应关闭 http 入口, 不抢 80):"
TUNNEL_PORT=16450 HTTP_PORT= HTTPS_PORT= WEB_PORT=17198 \
WEB_DATA=/tmp/reltest/dash.json LOGFILE=/tmp/reltest/ngrokd.log CONSOLE_LOG=/tmp/reltest/console.log \
bash ./start-ngrokd.sh

echo "=== HTTP 验证:"
python3 - <<'PYEOF'
import json, urllib.request

base = "http://127.0.0.1:17198"
pwd = ""
for line in open("/tmp/reltest/console.log"):
    if "初始密码:" in line:
        pwd = line.split(":")[-1].strip()
        break
print("初始密码提取:", "OK" if pwd else "FAIL")

req = urllib.request.Request(
    base + "/api/login",
    data=json.dumps({"username": "admin", "password": pwd}).encode(),
    headers={"Content-Type": "application/json"})
print("登录:", urllib.request.urlopen(req, timeout=5).read().decode().strip())

for path in ["/static/app" + ".js", "/static/style" + ".css",
             "/dl/ngrok_linux_amd64", "/install" + ".sh"]:
    code = urllib.request.urlopen(base + path, timeout=15).status
    print(f"{path}: {code}")
PYEOF

echo "=== 端口占用核对(80 应未被本实例占用):"
lsof -nP -i TCP:80 2>/dev/null | grep -c ngrokd || echo "0 (正确: http 入口已关闭)"

echo "=== 停止:"
bash ./stop-ngrokd.sh
echo "=== 冒烟通过"
