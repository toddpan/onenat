#!/usr/bin/env bash
# =====================================================================
#  Dashboard 端到端测试:
#  服务端(管理后台) → Web CRUD → managed 客户端 → 在线改配置 → 权限
#  运行: bash test-dashboard-e2e.sh
# =====================================================================
set -e
cd "$(dirname "$0")"
BIN=./bin
T=/tmp/ngrok-dash-test
DASH_PORT=18099
DASH=127.0.0.1:$DASH_PORT
TUN_PORT=14449
TUN=127.0.0.1:$TUN_PORT
HTTPPORT=18098
PASS=0; FAIL=0

pkill -f "bin/ngrokd" 2>/dev/null || true
pkill -f "ngrok-managed-e2e" 2>/dev/null || true
pkill -f sshd_sim.py 2>/dev/null || true
pkill -f websim.py 2>/dev/null || true
sleep 0.5
rm -rf "$T" && mkdir -p "$T"

# ---------- JSON helper (path access, no eval; fails soft on bad input) ----------
jget() { python3 -c '
import sys, json
try:
    d = json.load(sys.stdin)
except Exception:
    print(""); raise SystemExit
path = sys.argv[1] if len(sys.argv) > 1 else ""
try:
    if path.startswith("#"):
        for part in filter(None, path[1:].split(".")):
            d = (d[int(part)] if isinstance(d, list) else d.get(part)) if d is not None else None
        print(len(d) if d is not None else 0)
        raise SystemExit
    for part in filter(None, path.split(".")):
        if isinstance(d, list):
            d = d[int(part)]
        elif isinstance(d, dict):
            d = d.get(part)
        else:
            d = None
    print("null" if d is None else (json.dumps(d, ensure_ascii=False) if isinstance(d, (list, dict)) else d))
except Exception:
    print("")
' "$1"; }

ok()   { PASS=$((PASS+1)); echo "  ✓ $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  ✗ $1"; }
check() { # check <desc> <expr-result:0=ok>
  if [ "$2" = "0" ]; then ok "$1"; else bad "$1"; fi
}

# ---------- 1. 模拟内网服务 ----------
cat > "$T/sshd_sim.py" <<'EOF'
import socketserver
class H(socketserver.BaseRequestHandler):
    def handle(self):
        self.request.sendall(b"SSH-2.0-OpenSSH_9.6-SIM\r\n")
        while True:
            d = self.request.recv(1024)
            if not d: break
            self.request.sendall(b"SSHSIM:" + d)
class S(socketserver.ThreadingTCPServer): allow_reuse_address = True
S(("127.0.0.1", 2222), H).serve_forever()
EOF
cat > "$T/websim.py" <<'EOF'
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        b = b"WEB-OK: local test page"
        self.send_response(200); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    def log_message(self, *a): pass
HTTPServer(("127.0.0.1", 8080), H).serve_forever()
EOF
python3 "$T/sshd_sim.py" > /dev/null 2>&1 & echo $! > "$T/sshd.pid"
python3 "$T/websim.py" > /dev/null 2>&1 & echo $! > "$T/web.pid"

# ---------- 2. 启动服务端(带管理后台) ----------
$BIN/ngrokd -domain 127.0.0.1 \
  -httpAddr=":$HTTPPORT" -httpsAddr="" -tunnelAddr=":$TUN_PORT" \
  -webAddr=":$DASH_PORT" -webData="$T/dash.json" -webAdminPass=admin123 \
  -dlDir=./dl \
  -log="$T/ngrokd.log" -log-level=INFO > /dev/null 2>&1 &
echo $! > "$T/ngrokd.pid"
sleep 1.5
kill -0 "$(cat "$T/ngrokd.pid")" 2>/dev/null || { echo "ngrokd 启动失败:"; tail -20 "$T/ngrokd.log"; exit 1; }

req()  { curl -sf --max-time 5 -b "$T/admin.cookies" "$@"; }
reqx() { curl -s --max-time 5 -b "$T/admin.cookies" -o /dev/null -w '%{http_code}' "$@"; }

echo "== [1] 认证与用户管理 =="
R=$(curl -s -c "$T/admin.cookies" -X POST -H 'Content-Type: application/json' \
     -d '{"username":"admin","password":"admin123"}' "$DASH/api/login")
[[ "$R" == *'"ok"'* ]] && ok "管理员登录" || bad "管理员登录: $R"
C=$(reqx "$DASH/api/tunnels"); [ "$C" = "200" ] && ok "登录态访问 API 200" || bad "登录态访问 API: $C"
C=$(curl -s -o /dev/null -w '%{http_code}' "$DASH/api/tunnels"); [ "$C" = "401" ] && ok "未登录访问 API 401" || bad "未登录访问 API: $C"

ALICE_ID=$(req -X POST -H 'Content-Type: application/json' -d '{"username":"alice","password":"alice123","role":"user"}' \
     "$DASH/api/users" | jget id)
[ -n "$ALICE_ID" ] && ok "创建普通用户 alice" || bad "创建普通用户"

echo "== [1.5] 应用注册 (端口背后的能力抽象) =="
# alice 登录 (应用归属创建者; 隧道也归属 alice, 绑定校验要求同主)
curl -s -c "$T/alice.cookies" -X POST -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"alice123"}' "$DASH/api/login" > /dev/null
areq()  { curl -sf --max-time 5 -b "$T/alice.cookies" "$@"; }
areqx() { curl -s --max-time 5 -b "$T/alice.cookies" -o /dev/null -w '%{http_code}' "$@"; }
areap() { curl -s --max-time 5 -b "$T/alice.cookies" -H 'Content-Type: application/json' "$@"; }

APPID=$(areap -X POST -d '{"name":"KB API","type":"http-api","description":"仓储AGV系统接口","internal_url":"http://127.0.0.1:8080","auth_type":"basic","username":"kbadmin","password":"kbpass88"}' \
  "$DASH/api/apps" | jget app.id)
[ ${#APPID} -ge 10 ] && ok "alice 注册应用 KB API (id=$APPID)" || bad "注册应用: $APPID"

# 凭证加密落盘: 数据文件中不得出现明文密码
grep -q "kbpass88" "$T/dash.json" && bad "凭证明文泄露到存储!" || ok "凭证 AES-GCM 加密落盘 (dash.json 无明文)"
# 详情视图打码
MASKED=$(areq "$DASH/api/apps/$APPID" | jget app.password_mask)
[[ "$MASKED" == ••••* ]] && ok "凭证打码显示 ($MASKED)" || bad "凭证打码: $MASKED"
# 按类型自动生成技能骨架
NSK=$(areq "$DASH/api/apps/$APPID/skills" | jget "#skills")
[ "$NSK" = "1" ] && ok "http-api 类型自动生成技能骨架" || bad "技能骨架数: $NSK"
# 跨用户隔离: alice 读不到 admin 的应用 (统一 404 不泄露存在性)
ADMINAPP=$(curl -s --max-time 5 -b "$T/admin.cookies" -H 'Content-Type: application/json' \
  -X POST -d '{"name":"admin-only","type":"ssh","auth_type":"none"}' "$DASH/api/apps" | jget app.id)
C=$(areqx "$DASH/api/apps/$ADMINAPP")
[ "$C" = "404" ] && ok "跨用户应用读取被拒(404)" || bad "跨用户应用: $C"
echo "== [2] 隧道与端口映射 CRUD =="
TUNID=$(req -X POST -H 'Content-Type: application/json' -d "{\"name\":\"kb\",\"note\":\"e2e\",\"owner_id\":\"$ALICE_ID\"}" \
     "$DASH/api/tunnels" | jget id)
[ ${#TUNID} = 10 ] && ok "创建隧道 (id=$TUNID)" || bad "创建隧道: $TUNID"

KEY=$(req "$DASH/api/tunnels/$TUNID" | jget key)
[[ "$KEY" == ngk-* ]] && ok "自动生成隧道 KEY" || bad "隧道 KEY: $KEY"

MID=$(req -X POST -H 'Content-Type: application/json' \
  -d "{\"proto\":\"tcp\",\"local_ip\":\"127.0.0.1\",\"local_port\":2222,\"remote_port\":0,\"note\":\"ssh\",\"app_id\":\"$APPID\"}" \
  "$DASH/api/tunnels/$TUNID/mappings" | jget mapping.id)
[ -n "$MID" ] && ok "添加 TCP 端口映射 (mapping=$MID)" || bad "添加 TCP 映射"

MWEB=$(req -X POST -H 'Content-Type: application/json' \
  -d "{\"proto\":\"http\",\"local_ip\":\"127.0.0.1\",\"local_port\":8080,\"subdomain\":\"kb\",\"note\":\"web\",\"app_id\":\"$APPID\"}" \
  "$DASH/api/tunnels/$TUNID/mappings" | jget mapping.id)
[ -n "$MWEB" ] && ok "添加 HTTP 端口映射" || bad "添加 HTTP 映射"

C=$(curl -s --max-time 5 -b "$T/admin.cookies" -X POST -H 'Content-Type: application/json' -d "{\"proto\":\"tcp\",\"local_port\":1,\"remote_port\":99999,\"app_id\":\"$APPID\"}" \
  "$DASH/api/tunnels/$TUNID/mappings" -o /dev/null -w '%{http_code}')
[ "$C" = "400" ] && ok "非法映射参数被拒绝(400)" || bad "非法映射参数: $C"

# 安全边界: 默认拒绝非本地 IP (防内网跳板与 SSRF)
C=$(curl -s --max-time 5 -b "$T/admin.cookies" -X POST -H 'Content-Type: application/json' \
  -d "{\"proto\":\"tcp\",\"local_ip\":\"192.168.1.100\",\"local_port\":8080,\"app_id\":\"$APPID\"}" "$DASH/api/tunnels/$TUNID/mappings" -o /dev/null -w '%{http_code}')
[ "$C" = "400" ] && ok "默认拒绝内网非本机目标(400)" || bad "默认内网 IP: $C"

# 安全边界: 拒绝特权端口 < 1024
C=$(curl -s --max-time 5 -b "$T/admin.cookies" -X POST -H 'Content-Type: application/json' \
  -d "{\"proto\":\"tcp\",\"local_port\":8080,\"remote_port\":80,\"app_id\":\"$APPID\"}" "$DASH/api/tunnels/$TUNID/mappings" -o /dev/null -w '%{http_code}')
[ "$C" = "400" ] && ok "拒绝公网特权端口 < 1024(400)" || bad "特权端口: $C"

echo "== [3] 一键部署链路 =="
DEPLOY=$(curl -s -o /dev/null -w '%{http_code}' "$DASH/api/deploy?id=$TUNID&key=WRONGKEY")
[ "$DEPLOY" = "403" ] && ok "错误 KEY 拉取配置被拒(403)" || bad "错误 KEY: $DEPLOY"
curl -sf "$DASH/api/deploy?id=$TUNID&key=$KEY" -o "$T/ngrok-managed.yml"
grep -q "server_addr: 127.0.0.1:14449" "$T/ngrok-managed.yml" && ok "部署配置含 server_addr" || bad "部署配置内容异常"
grep -q "tunnel_id: $TUNID" "$T/ngrok-managed.yml" && ok "部署配置含 tunnel_id" || bad "配置缺 tunnel_id"
SZ=$(curl -sf "$DASH/dl/ngrok_linux_amd64" | wc -c)
[ "$SZ" -gt 1000000 ] && ok "客户端二进制分发 /dl ($(du -h dl/ngrok_linux_amd64 | cut -f1))" || bad "/dl 分发: $SZ 字节"
curl -sf "$DASH/install.sh" | head -2 | grep -q "install" && ok "install.sh 可获取" || bad "install.sh"
# MD5 幂等: 内嵌校验值与 dl 目录真实 MD5 一致, 且含跳过/校验逻辑
EXPECT_MD5=$(md5 -q dl/ngrok_linux_amd64)
INSTALL_SH=$(curl -sf "$DASH/install.sh")
echo "$INSTALL_SH" | grep -q "$EXPECT_MD5" && ok "install.sh 内嵌 dl 二进制真实 MD5" || bad "install.sh MD5 缺失/不符"
echo "$INSTALL_SH" | grep -q "跳过下载" && ok "install.sh 含 MD5 幂等跳过逻辑" || bad "install.sh 缺跳过逻辑"
echo "$INSTALL_SH" | grep -q "下载校验失败" && ok "install.sh 含下载后 MD5 校验" || bad "install.sh 缺下载校验"
sh -n <(echo "$INSTALL_SH") 2>/dev/null && ok "install.sh 语法合法 (sh -n)" || bad "install.sh 语法错误"
C=$(curl -s --max-time 5 -o /dev/null -w '%{http_code}' "$DASH/install.ps1")
[ "$C" = "200" ] && ok "install.ps1 可获取(Windows)" || bad "install.ps1: $C"
curl -s --max-time 5 "$DASH/install.ps1" | grep -q "TunnelId" && ok "install.ps1 含参数定义" || bad "install.ps1 内容异常"

echo "== [4] managed 客户端上线 =="
$BIN/ngrok -log="$T/client.log" -log-level=INFO -config="$T/ngrok-managed.yml" managed > /dev/null 2>&1 &
echo $! > "$T/client.pid"
sleep 3

ONLINE=$(req "$DASH/api/tunnels/$TUNID" | jget online)
[ "$ONLINE" = "True" ] && ok "隧道显示在线" || bad "隧道未在线: $ONLINE"
SSHURL=$(req "$DASH/api/tunnels/$TUNID" | jget "runtime.active.$MID")
SSHPORT=${SSHURL##*:}
[ -n "$SSHPORT" ] && [ "$SSHPORT" != "null" ] && ok "SSH 公网入口: $SSHURL" || bad "无公网入口: $SSHURL"

R1=$((printf "x"; sleep 0.4) | nc 127.0.0.1 "$SSHPORT" | head -c 20 || true)
[[ "$R1" == SSH-2.0-* ]] && ok "SSH 反代连通 ($R1)" || bad "SSH 反代: '$R1'"
R2=$(curl -s --max-time 3 -H "Host: kb.127.0.0.1:$HTTPPORT" "http://127.0.0.1:$HTTPPORT/")
[[ "$R2" == WEB-OK* ]] && ok "HTTP 反代连通 (Host 路由)" || bad "HTTP 反代: '$R2'"

echo "== [5] 在线修改端口映射(不重启客户端) =="
CLIENT_PID=$(cat "$T/client.pid")
M2=$(req -X POST -H 'Content-Type: application/json' \
  -d "{\"proto\":\"tcp\",\"local_ip\":\"127.0.0.1\",\"local_port\":2222,\"remote_port\":0,\"note\":\"ssh2\",\"app_id\":\"$APPID\"}" \
  "$DASH/api/tunnels/$TUNID/mappings" | jget mapping.id)
sleep 2.5
URL2=$(req "$DASH/api/tunnels/$TUNID" | jget "runtime.active.$M2")
P2=${URL2##*:}
[ -n "$P2" ] && [ "$P2" != "null" ] && ok "在线添加映射 M2 → $URL2 (客户端无需重装)" || bad "在线添加映射: $URL2"

# 修改 M2 为固定端口 24733 → 旧端口关闭, 新端口开通
req -X PATCH -H 'Content-Type: application/json' \
  -d "{\"proto\":\"tcp\",\"local_ip\":\"127.0.0.1\",\"local_port\":2222,\"remote_port\":24733,\"note\":\"ssh2\",\"app_id\":\"$APPID\"}" \
  "$DASH/api/mappings/$M2" > /dev/null
sleep 2.5
R3=$((printf "x"; sleep 0.4) | nc 127.0.0.1 24733 | head -c 20 || true)
[[ "$R3" == SSH-2.0-* ]] && ok "改端口后新口 24733 连通" || bad "新口 24733: '$R3'"
R4=$(nc -z -G 1 127.0.0.1 "$P2" 2>/dev/null && echo OPEN || echo CLOSED)
[ "$R4" = "CLOSED" ] && ok "旧口 $P2 已关闭" || bad "旧口 $P2 仍开着: $R4"

ERR2=$(req "$DASH/api/tunnels/$TUNID" | jget "runtime.errors.$M2")
[ "$ERR2" = "null" ] && ok "映射无错误状态上报" || bad "M2 错误: $ERR2"

echo "== [6] 删除映射 / 修复隧道 =="
req -X DELETE "$DASH/api/mappings/$M2" > /dev/null
sleep 2
R5=$(nc -z -G 1 127.0.0.1 24733 2>/dev/null && echo OPEN || echo CLOSED)
[ "$R5" = "CLOSED" ] && ok "删除映射后公网口关闭" || bad "删除映射后 24733 仍开放"
R6=$((printf "x"; sleep 0.4) | nc 127.0.0.1 "$SSHPORT" | head -c 20 || true)
[[ "$R6" == SSH-2.0-* ]] && ok "其余映射不受影响" || bad "M1 被误伤: '$R6'"

req -X POST "$DASH/api/tunnels/$TUNID/repair" > /dev/null
sleep 3
ONLINE=$(req "$DASH/api/tunnels/$TUNID" | jget online)
# 重建后从 API 重新读取当前公网地址 (亲和缓存按 client+协议共享, 端口可能变化)
SSHURL=$(req "$DASH/api/tunnels/$TUNID" | jget "runtime.active.$MID")
SSHPORT2=${SSHURL##*:}
R7=$((printf "x"; sleep 0.4) | nc 127.0.0.1 "$SSHPORT2" | head -c 20 || true)
[ "$ONLINE" = "True" ] && [[ "$R7" == SSH-2.0-* ]] && ok "修复隧道: 全量重建成功 ($SSHURL)" || bad "修复隧道: $ONLINE '$R7'"

echo "== [7] 普通用户权限 =="
curl -s -c "$T/alice.cookies" -X POST -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"alice123"}' "$DASH/api/login" > /dev/null
areq()  { curl -sf --max-time 5 -b "$T/alice.cookies" "$@"; }
areqx() { curl -s --max-time 5 -b "$T/alice.cookies" -o /dev/null -w '%{http_code}' "$@"; }

N=$(areq "$DASH/api/tunnels" | jget "#tunnels")
[ "$N" = "1" ] && ok "alice 只看到自己的 1 条隧道" || bad "alice 可见隧道数: $N"
C=$(areqx -X POST -H 'Content-Type: application/json' -d '{"name":"hack"}' "$DASH/api/tunnels")
[ "$C" = "403" ] && ok "alice 创建隧道被拒(403)" || bad "alice 创建隧道: $C"
C=$(areqx -X POST -H 'Content-Type: application/json' -d '{"proto":"tcp","local_port":1}' "$DASH/api/tunnels/$TUNID/mappings")
[ "$C" = "403" ] && ok "alice 加映射被拒(403)" || bad "alice 加映射: $C"
C=$(areqx -X DELETE "$DASH/api/tunnels/$TUNID")
[ "$C" = "403" ] && ok "alice 删隧道被拒(403)" || bad "alice 删隧道: $C"
C=$(areqx "$DASH/api/tunnels/$TUNID/config")
[ "$C" = "200" ] && ok "alice 可获取自己隧道配置(200)" || bad "alice 取配置: $C"
C=$(curl -s -b "$T/alice.cookies" -o /dev/null -w '%{http_code}' "$DASH/users")
[ "$C" = "403" ] && ok "alice 访问用户管理被拒" || bad "alice 访问 /users: $C"

echo "== [8] 锁定与重置密钥 =="
req -X PATCH -H 'Content-Type: application/json' -d '{"locked":true}' "$DASH/api/tunnels/$TUNID" > /dev/null
sleep 0.3
C=$(curl -s -o /dev/null -w '%{http_code}' "$DASH/api/deploy?id=$TUNID&key=$KEY")
[ "$C" = "403" ] && ok "锁定后 deploy 拒绝(403)" || bad "锁定后 deploy: $C"
req -X PATCH -H 'Content-Type: application/json' -d '{"locked":false}' "$DASH/api/tunnels/$TUNID" > /dev/null

NEWKEY=$(req -X POST "$DASH/api/tunnels/$TUNID/reset-key" | jget key)
[ "$NEWKEY" != "$KEY" ] && [[ "$NEWKEY" == ngk-* ]] && ok "重置密钥成功" || bad "重置密钥: $NEWKEY"
sleep 1.5
ONLINE=$(req "$DASH/api/tunnels/$TUNID" | jget online)
[ "$ONLINE" = "False" ] && ok "旧密钥客户端被断开下线" || bad "旧密钥客户端仍在线: $ONLINE"
C=$(curl -s -o /dev/null -w '%{http_code}' "$DASH/api/deploy?id=$TUNID&key=$KEY")
[ "$C" = "403" ] && ok "旧 KEY 拉配置被拒" || bad "旧 KEY: $C"
curl -sf "$DASH/api/deploy?id=$TUNID&key=$NEWKEY" -o "$T/ngrok-managed2.yml" && ok "新 KEY 拉配置成功" || bad "新 KEY 拉配置"
grep -q "$NEWKEY" "$T/ngrok-managed2.yml" && ok "新配置含新密钥" || bad "新配置密钥不符"

echo "== [9] 新密钥重装上线 =="
$BIN/ngrok -log="$T/client2.log" -log-level=INFO -config="$T/ngrok-managed2.yml" managed > /dev/null 2>&1 &
echo $! > "$T/client2.pid"
sleep 3
ONLINE=$(req "$DASH/api/tunnels/$TUNID" | jget online)
SSHURL=$(req "$DASH/api/tunnels/$TUNID" | jget "runtime.active.$MID")
SSHPORT3=${SSHURL##*:}
R8=$((printf "x"; sleep 0.4) | nc 127.0.0.1 "$SSHPORT3" | head -c 20 || true)
[ "$ONLINE" = "True" ] && [[ "$R8" == SSH-2.0-* ]] && ok "新密钥客户端上线且连通 ($SSHURL)" || bad "新客户端: $ONLINE '$R8'"

echo "== [10] 离线状态 =="
kill "$(cat "$T/client2.pid")" 2>/dev/null || true
sleep 2
ONLINE=$(req "$DASH/api/tunnels/$TUNID" | jget online)
[ "$ONLINE" = "False" ] && ok "客户端断开后显示离线" || bad "客户端断开后仍显示在线"

echo "== [11] APIKEY 与 SKILL =="
AK=$(req -X POST -H 'Content-Type: application/json' -d '{"name":"e2e-ai"}' "$DASH/api/keys" | jget key.key)
[[ "$AK" == onk-* ]] && ok "创建 API KEY" || bad "创建 API KEY: $AK"
C=$(curl -s --max-time 5 -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $AK" "$DASH/api/v1/resources")
[ "$C" = "200" ] && ok "API KEY 查询资源列表 200" || bad "资源列表: $C"
C=$(curl -s --max-time 5 -o /dev/null -w '%{http_code}' -H "Authorization: Bearer onk-wrong" "$DASH/api/v1/resources")
[ "$C" = "401" ] && ok "错误 API KEY 被拒(401)" || bad "错误 KEY: $C"
C=$(curl -s --max-time 5 -X POST -H 'Content-Type: application/json' -H "Authorization: Bearer $AK" \
  -d '{"name":"hack"}' "$DASH/api/tunnels" -o /dev/null -w '%{http_code}')
[ "$C" = "401" ] && ok "API KEY 无法创建隧道(401)" || bad "API KEY 创建隧道: $C"
SKILL=$(curl -s --max-time 5 "$DASH/skill/onenat.md?key=$AK")
echo "$SKILL" | grep -q "oneNat 隧道资源使用技能" && ok "SKILL 文档可下载" || bad "SKILL 内容异常"
echo "$SKILL" | grep -q "$AK" && ok "SKILL 内嵌 API KEY" || bad "SKILL 未含 KEY"
C=$(curl -s --max-time 5 -o /dev/null -w '%{http_code}' "$DASH/skill/onenat.md")
[ "$C" = "401" ] && ok "无 KEY 下载 SKILL 被拒(401)" || bad "无 KEY SKILL: $C"
echo "$SKILL" | grep -q "获取应用技能" && echo "$SKILL" | grep -q "credentials" && ok "SKILL 含应用技能获取指引" || bad "SKILL 缺技能获取指引"
echo "$SKILL" | grep -q '"app"' && echo "$SKILL" | grep -q "动态维护" && ok "SKILL 说明隧道↔应用绑定与动态性" || bad "SKILL 缺绑定/动态性说明"
KID=$(req "$DASH/api/keys" | jget keys.0.id)
C=$(req -X DELETE "$DASH/api/keys/$KID" -o /dev/null -w '%{http_code}')
[ "$C" = "200" ] && ok "撤销 API KEY" || bad "撤销 API KEY: $C"

echo "== [11.5] 应用技能与凭证安全 =="
# 技能上传 (JSON 文本)
areap -X POST -d '{"name":"kb-api.md","content":"# KB API 使用说明\n## 认证: Bearer Token"}' \
  "$DASH/api/apps/$APPID/skills" > /dev/null && ok "上传技能文件 kb-api.md" || bad "上传技能"
NSK=$(areq "$DASH/api/apps/$APPID/skills" | jget "#skills")
[ "$NSK" = "2" ] && ok "技能列表 (骨架+上传=2)" || bad "技能数量: $NSK"
# 磁盘内容存在 (skills/<appID>/)
ls "$T/skills/$APPID/"*.md > /dev/null 2>&1 && ok "技能内容落盘 skills/<appID>/" || bad "技能磁盘内容缺失"
# 路径穿越拒绝
C=$(areqx -X POST -H 'Content-Type: application/json' -d '{"name":"../../evil.md","content":"x"}' "$DASH/api/apps/$APPID/skills")
[ "$C" = "400" ] && ok "路径穿越文件名被拒(400)" || bad "穿越文件名: $C"
# 可执行扩展名拒绝
C=$(areqx -X POST -H 'Content-Type: application/json' -d '{"name":"evil.sh","content":"x"}' "$DASH/api/apps/$APPID/skills")
[ "$C" = "400" ] && ok "可执行扩展名被拒(400)" || bad "扩展名白名单: $C"
# 技能下载 (web 会话, markdown)
CD=$(areq -o /dev/null -w '%{content_type}' "$DASH/api/apps/$APPID/skills/$(areq "$DASH/api/apps/$APPID/skills" | jget skills.0.id)/download")
echo "$CD" | grep -q "text/markdown" && ok "技能下载 Content-Type markdown" || bad "技能下载头: $CD"
# 在线修改 → 版本递增
SKID=$(areq "$DASH/api/apps/$APPID/skills" | jget skills.0.id)
V2=$(areap -X PUT -d '{"content":"# v2 修改后的技能"}' "$DASH/api/apps/$APPID/skills/$SKID" | jget skill.version)
[ "$V2" = "2" ] && ok "在线修改技能 (version→2)" || bad "技能修改: $V2"
# reveal: 明文返回 + 审计
PLAIN=$(areap -X POST "$DASH/api/apps/$APPID/reveal")
echo "$PLAIN" | grep -q "kbpass88" && ok "reveal 返回明文 (owner)" || bad "reveal: $PLAIN"
grep -q "cred.reveal" "$T/onenat-audit.jsonl" && ok "reveal 写入审计日志" || bad "审计日志缺失"

# ---- AI 侧 (KEY 归属应用主人 alice, AI 只能看到 alice 自己的资源) ----
AK2=$(areap -X POST -d '{"name":"ai-app"}' "$DASH/api/keys" | jget key.key)
RES=$(curl -s --max-time 5 -H "Authorization: Bearer $AK2" "$DASH/api/v1/resources")
echo "$RES" | grep -q '"app"' && ok "resources 内嵌应用信息" || bad "resources 应用缺失"
echo "$RES" | grep -q "kb-api.md" && ok "resources 内嵌技能下载链接" || bad "resources 技能缺失"
echo "$RES" | grep -q "kbpass88" && bad "resources 泄露凭证!" || ok "resources 不含任何凭证"
C=$(curl -s --max-time 5 -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $AK2" "$DASH/api/v1/apps/$APPID/credentials")
[ "$C" = "403" ] && ok "AI KEY 默认无凭证读取权(403)" || bad "AI 凭证读取默认: $C"
SKC=$(curl -s --max-time 5 -H "Authorization: Bearer $AK2" "$DASH/api/v1/apps/$APPID/skills/kb-api.md/content")
echo "$SKC" | grep -q "v2 修改后的技能" && ok "AI 按名字下载技能内容" || bad "AI 技能下载: $SKC"
IDX=$(curl -s --max-time 5 "$DASH/skill/index.md?key=$AK2")
echo "$IDX" | grep -q "KB API" && ok "平台应用总索引 /skill/index.md" || bad "index.md: $IDX"

# ---- 页面 ----
C=$(reqx "$DASH/apps"); [ "$C" = "200" ] && ok "应用管理页 200" || bad "应用页: $C"
C=$(reqx "$DASH/apps/$APPID"); [ "$C" = "200" ] && ok "应用详情页 200" || bad "应用详情页: $C"
C=$(reqx "$DASH/static/apps.js"); [ "$C" = "200" ] && ok "静态 apps.js 200" || bad "apps.js: $C"

# ---- 删除保护 ----
C=$(areqx -X DELETE "$DASH/api/apps/$APPID")
[ "$C" = "409" ] && ok "有绑定映射时删除被拒(409)" || bad "绑定删除保护: $C"
C=$(areqx -X DELETE "$DASH/api/apps/$APPID?force=1")
[ "$C" = "200" ] && ok "force 解绑并删除应用" || bad "force 删除: $C"

echo "== [12] Web 页面渲染 =="
C=$(req -o /dev/null -w '%{http_code}' "$DASH/");           [ "$C" = "200" ] && ok "隧道列表页 200" || bad "列表页: $C"
C=$(req -o /dev/null -w '%{http_code}' "$DASH/t/$TUNID");   [ "$C" = "200" ] && ok "隧道详情页 200" || bad "详情页: $C"
C=$(req -o /dev/null -w '%{http_code}' "$DASH/users");      [ "$C" = "200" ] && ok "用户管理页 200" || bad "用户页: $C"
C=$(req -o /dev/null -w '%{http_code}' "$DASH/static/app.js")
[ "$C" = "200" ] && ok "静态 app.js 200 (前端交互依赖)" || bad "静态 app.js: $C"
C=$(req -o /dev/null -w '%{http_code}' "$DASH/static/style.css")
[ "$C" = "200" ] && ok "静态 style.css 200 (页面样式)" || bad "静态 style.css: $C"
C=$(curl -s -o /dev/null -w '%{http_code}' -L "$DASH/")
[ "$C" = "200" ] && ok "未登录访问 / 重定向到登录页" || bad "未登录 /: $C"

echo
echo "======================================================"
echo "  结果: PASS=$PASS FAIL=$FAIL"
echo "  日志: $T/ngrokd.log  $T/client.log"
echo "  数据: $T/dash.json   后台: http://$DASH (admin/admin123)"
echo "======================================================"

# 清理后台进程(交互式查看时改用 KEEP=1)
if [ "${KEEP:-0}" != "1" ]; then
  pkill -f "bin/ngrokd" 2>/dev/null || true
  pkill -f "ngrok.*managed" 2>/dev/null || true
  pkill -f sshd_sim.py 2>/dev/null || true
  pkill -f websim.py 2>/dev/null || true
  exit $([ "$FAIL" = "0" ] && echo 0 || echo 1)
fi
[ "$FAIL" = "0" ] || exit 1
wait
