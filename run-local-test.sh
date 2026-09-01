#!/usr/bin/env bash
# =====================================================================
#  本地一键测试: 服务端 + 模拟SSH/Web + 客户端 + AI视角连接验证
#  全程纯 IP, 无需域名。运行: bash run-local-test.sh
# =====================================================================
set -e
cd "$(dirname "$0")"
BIN=./bin
TESTDIR=/tmp/ngrok-local-test
SSH_SIM_PORT=2222     # 模拟"内网机器"的 sshd(本机起, 避免 22 权限)
WEB_SIM_PORT=8080     # 模拟"内网机器"的 web
SRV_HTTP=18180        # 服务端公网 http 口 (18080 留给管理后台)
SRV_TUNNEL=14443      # 服务端隧道口

pkill -f "bin/ngrokd" 2>/dev/null || true
pkill -f "bin/ngrok " 2>/dev/null || true
pkill -f sshd_sim.py  2>/dev/null || true
pkill -f websim.py    2>/dev/null || true
sleep 0.5
mkdir -p "$TESTDIR" && rm -rf "$TESTDIR/home" && mkdir -p "$TESTDIR/home"

# ---------- 1. 模拟内网机器上的两个服务 ----------
cat > "$TESTDIR/sshd_sim.py" <<'EOF'
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
cat > "$TESTDIR/websim.py" <<'EOF'
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        b = b"WEB-OK: local test page"
        self.send_response(200); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    def log_message(self, *a): pass
HTTPServer(("127.0.0.1", 8080), H).serve_forever()
EOF
python3 "$TESTDIR/sshd_sim.py" &  echo $! > "$TESTDIR/sshd.pid"
python3 "$TESTDIR/websim.py" &    echo $! > "$TESTDIR/web.pid"

# ---------- 2. 服务端(纯 IP, 不配域名; 本脚本不测管理后台, 显式关闭) ----------
$BIN/ngrokd -domain 127.0.0.1 \
  -httpAddr=":$SRV_HTTP" -httpsAddr="" -tunnelAddr=":$SRV_TUNNEL" \
  -webAddr="" \
  -log="$TESTDIR/ngrokd.log" -log-level=INFO &
echo $! > "$TESTDIR/ngrokd.pid"
sleep 1

# ---------- 3. 客户端 agent 模式(映射本机 2222/ssh + 8080/web) ----------
export HOME="$TESTDIR/home"
$BIN/ngrok -log="$TESTDIR/agent.log" -log-level=INFO \
  agent -server=127.0.0.1:$SRV_TUNNEL -web-port $WEB_SIM_PORT 127.0.0.1:$SSH_SIM_PORT &
echo $! > "$TESTDIR/agent.pid"
sleep 3

KEY=$(cat "$HOME/.ngrok.d/machine.key")
SSHPORT=$(python3 -c "import json;d=json.load(open('$HOME/.ngrok.d/remote-manual.json'));print([s['public_url'] for s in d['services'] if s['kind']=='ssh'][0].split(':')[-1])")

# ---------- 4. AI 视角验证 ----------
echo
echo "================= 本地测试环境已就绪 ================="
echo "服务端日志 : $TESTDIR/ngrokd.log"
echo "客户端日志 : $TESTDIR/agent.log"
echo "机器密钥   : $KEY"
echo "SSH 入口   : tcp://127.0.0.1:$SSHPORT  (需 AUTH 握手)"
echo "Web 入口   : http://127.0.0.1:$SRV_HTTP/  (公开)"
echo "说明书     : $HOME/.ngrok.d/remote-manual.{md,json}"
echo "------------------------------------------------------"
echo "AI/人工验证命令(直接复制):"
echo
echo "  # SSH 网关冒烟(应回显 SSH-2.0-...-SIM banner)"
echo "  (printf \"AUTH $KEY\\\\r\\\\n\"; sleep 1) | nc 127.0.0.1 $SSHPORT"
echo
echo "  # 错码应被拒(应回 ERR ngrok-gate: access denied)"
echo "  (printf \"AUTH ngk-0000-0000-0000-0000\\\\r\\\\n\"; sleep 1) | nc 127.0.0.1 $SSHPORT"
echo
echo "  # Web 直连(应回 WEB-OK; 注意 Agent 模式的 Host 头路由, 非标准口需带 Host)"
echo "  curl -s -H 'Host: 127.0.0.1' http://127.0.0.1:$SRV_HTTP/"
echo
echo "  # 真实 SSH(本机开了远程登录时可用)"
echo "  ssh -o ProxyCommand='{ printf \"AUTH $KEY\\\\r\\\\n\"; cat; } | nc 127.0.0.1 $SSHPORT' \\"
echo "      -o StrictHostKeyChecking=accept-new $(whoami)@localhost"
echo "======================================================"
echo
# 自动冒烟
echo "[自动冒烟]"
R1=$((printf "AUTH $KEY\r\n"; sleep 0.6) | nc 127.0.0.1 $SSHPORT | head -c 20) || true
[[ "$R1" == SSH-2.0-* ]] && echo "  ✓ SSH 网关放行: $R1" || echo "  ✗ SSH 网关异常: '$R1'"
R2=$(curl -s --max-time 2 -H "Host: 127.0.0.1" http://127.0.0.1:$SRV_HTTP/) || true
[[ "$R2" == WEB-OK* ]] && echo "  ✓ Web 直连: $R2" || echo "  ✗ Web 异常: '$R2'"
echo
echo "停止全部: bash stop-local-test.sh"
wait
