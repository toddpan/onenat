#!/usr/bin/env bash
# oneNat 发行包冒烟测试: 解压 → 启动 → HTTP 验证 → 停止
#
# 改动说明 (命名漂移修正):
#   * 原来写死的 `cd /Users/tsbj/feyanggit/ngrok` 是别人机器上的路径, 换台机器就
#     直接失败 ⇒ 改成按脚本位置定位仓库根目录。
#   * 发行包名原来写死 r2026.09.15 ⇒ 改成从 version.go 的 DefaultRelease 派生
#     (版本号的单一来源), 免得每次发版都要改脚本。
#   * 客户端资产名 `ngrok_linux_amd64` → `onenat-client_linux_amd64`
#     (服务端 /dl/ 端点与安装脚本都只认 onenat-client_ 前缀)。
set -euo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# 发行包名 = oneNat-r<DefaultRelease>, 版本号单一来源在 version.go
REL_VER="$(sed -n 's/^[[:space:]]*DefaultRelease = "\(.*\)".*/\1/p' \
  src/ngrok/server/dashboard/version.go | head -1)"
if [ -z "$REL_VER" ]; then
  echo "无法从 src/ngrok/server/dashboard/version.go 读取 DefaultRelease" >&2
  exit 1
fi
REL="oneNat-r${REL_VER}"

rm -rf /tmp/reltest && mkdir -p /tmp/reltest
tar -C /tmp/reltest -xzf "dist/$REL.tar.gz"
cd "/tmp/reltest/$REL"

echo "=== 启动(空 HTTP_PORT 应关闭 http 入口, 不抢 80):"
TUNNEL_PORT=16450 HTTP_PORT= HTTPS_PORT= WEB_PORT=17198 \
WEB_DATA=/tmp/reltest/dash.json LOGFILE=/tmp/reltest/onenat.log CONSOLE_LOG=/tmp/reltest/console.log \
bash ./start-onenat.sh

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

page = urllib.request.urlopen(base + "/login", timeout=5).read().decode()
print("登录页品牌 oneNat:", "OK" if "oneNat" in page and "ngrokd" not in page else "FAIL")

# 客户端资产名: 服务端 /dl/ 端点只接受 onenat-client_ 前缀的文件名
for path in ["/static/app" + ".js", "/static/style" + ".css",
             "/dl/onenat-client_linux_amd64", "/install" + ".sh", "/install" + ".ps1"]:
    code = urllib.request.urlopen(base + path, timeout=15).status
    print(f"{path}: {code}")
PYEOF

echo "=== 停止:"
bash ./stop-onenat.sh
echo "=== 冒烟通过"
