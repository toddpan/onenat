#!/usr/bin/env bash
# =====================================================================
#  oneNat 服务端启动脚本 (本地测试 / 生产通用)
#
#  本地测试:  bash start-onenat.sh                 (纯 IP, 免域名免证书)
#  生产部署:  sudo ./ngrokd-domain.com.crt ... 见文末"生产模式"说明
#             DOMAIN=你的域名 bash start-onenat.sh
#
#  常用环境变量:
#    DOMAIN      对外域名 (默认 127.0.0.1, 纯 IP 本地测试)
#    TUNNEL_PORT 客户端隧道口   (默认 4443)
#    HTTP_PORT   公网 http 口   (默认关闭; 若要开启 http 隧道传 HTTP_PORT=80)
#    HTTPS_PORT  公网 https 口  (默认关闭)
#    AUTH_TOKENS 客户端密钥白名单, 逗号分隔 (默认空=不校验)
#    TLS_CERT / TLS_KEY   正式证书路径 (生产 https 需配)
#    LOGFILE     日志路径 (默认 ./onenat.log)
#    WEB_PORT    管理后台端口 (默认 18080; 设为空字符串则禁用)
#    WEB_DATA    管理后台数据文件 (默认 ./onenat-dashboard.json)
#    DL_DIR      客户端二进制分发目录 (默认 ./dl)
#  例:
#    bash start-onenat.sh                                    # 本地最小化
#    DOMAIN=ngrok.me HTTP_PORT=80 AUTH_TOKENS=k1,k2 bash start-onenat.sh
#    DOMAIN=ngrok.me TUNNEL_PORT=14443 HTTP_PORT=18080 bash start-onenat.sh
# =====================================================================
set -euo pipefail
cd "$(dirname "$0")"

# 二进制选择: 优先开发目录 bin/ngrokd, 否则按平台选发布包内 bin/ngrokd-<os>-<arch>
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/' -e 's/armv7l/arm/')
BIN=./bin/ngrokd
if [ ! -x "$BIN" ]; then
  BIN_PKG="./bin/ngrokd-${OS}-${ARCH}"
  if [ -x "$BIN_PKG" ]; then
    BIN="$BIN_PKG"
  else
    echo "未找到服务端二进制 ($BIN 或 $BIN_PKG)"
    echo "开发模式编译: go build -tags debug -o bin/ngrokd ngrok/main/ngrokd"
    exit 1
  fi
fi

DOMAIN="${DOMAIN:-127.0.0.1}"
# 注意 ${VAR-default} 不带冒号: 仅未设置时取默认, 显式设为空串=关闭该功能
TUNNEL_PORT="${TUNNEL_PORT-4443}"
HTTP_PORT="${HTTP_PORT-}"
HTTPS_PORT="${HTTPS_PORT-}"
AUTH_TOKENS="${AUTH_TOKENS-}"
TLS_CERT="${TLS_CERT-}"
TLS_KEY="${TLS_KEY-}"
LOGFILE="${LOGFILE-./onenat.log}"
PIDFILE="./onenat.pid"
CONSOLE_LOG="${CONSOLE_LOG-./onenat-console.log}"
# 管理后台 (web 管理页面): WEB_PORT 为空则禁用
WEB_PORT="${WEB_PORT-18080}"
WEB_DATA="${WEB_DATA-./onenat-dashboard.json}"
DL_DIR="${DL_DIR-./dl}"
WEB_ADMIN_PASS="${WEB_ADMIN_PASS-}"

# 已在运行则先停
if [ -f "$PIDFILE" ] && kill -0 "$(cat $PIDFILE)" 2>/dev/null; then
  echo "oneNat 已在运行 (pid $(cat $PIDFILE)), 先停止..."
  kill "$(cat $PIDFILE)"; sleep 1
fi

ARGS=( -domain "$DOMAIN" -tunnelAddr ":$TUNNEL_PORT" )

if [ -n "$HTTP_PORT" ]; then ARGS+=( -httpAddr ":$HTTP_PORT" ); else ARGS+=( -httpAddr "" ); fi
if [ -n "$HTTPS_PORT" ]; then
  ARGS+=( -httpsAddr ":$HTTPS_PORT" )
  [ -n "$TLS_CERT" ] && ARGS+=( -tlsCrt "$TLS_CERT" )
  [ -n "$TLS_KEY"  ] && ARGS+=( -tlsKey "$TLS_KEY" )
else
  ARGS+=( -httpsAddr "" )
fi
[ -n "$AUTH_TOKENS" ] && ARGS+=( -authToken "$AUTH_TOKENS" )
if [ -n "$WEB_PORT" ]; then
  ARGS+=( -webAddr ":$WEB_PORT" -webData "$WEB_DATA" -dlDir "$DL_DIR" )
  [ -n "$WEB_ADMIN_PASS" ] && ARGS+=( -webAdminPass "$WEB_ADMIN_PASS" )
fi

nohup "$BIN" "${ARGS[@]}" -log "$LOGFILE" -log-level "${LOGLEVEL:-INFO}" \
  > "$CONSOLE_LOG" 2>&1 &
echo $! > "$PIDFILE"
sleep 1

if ! kill -0 "$(cat $PIDFILE)" 2>/dev/null; then
  echo "✗ 启动失败, 日志尾部:"
  [ -f "$CONSOLE_LOG" ] && tail -10 "$CONSOLE_LOG"
  [ -f "$LOGFILE" ] && tail -5 "$LOGFILE"
  exit 1
fi

echo "✓ oneNat 已启动 (pid $(cat $PIDFILE), 日志: $LOGFILE)"
echo "  隧道口     : $DOMAIN:$TUNNEL_PORT   ← 客户端 -server=这里"
echo "  公网 http  : $([ -n "$HTTP_PORT" ] && echo "$DOMAIN:$HTTP_PORT" || echo 关闭)"
echo "  公网 https : $([ -n "$HTTPS_PORT" ] && echo "$DOMAIN:$HTTPS_PORT" || echo 关闭)"
echo "  密钥校验   : $([ -n "$AUTH_TOKENS" ] && echo "开启(白名单)" || echo 关闭)"
if [ -n "$WEB_PORT" ]; then
echo "  管理后台   : http://$DOMAIN:$WEB_PORT"
# 初始密码: stdout 即时打印(onenat-console.log), 文件日志异步刷盘作为兜底
INITIAL_PASS=""
for _i in 1 2 3 4 5 6; do
  sleep 0.5
  INITIAL_PASS=$(grep -oE '初始密码: [A-Za-z0-9]+' "$CONSOLE_LOG" 2>/dev/null | tail -1 | cut -d' ' -f2 || true)
  if [ -z "$INITIAL_PASS" ]; then
    INITIAL_PASS=$(grep -oE 'with password "[^"]*"' "$LOGFILE" 2>/dev/null | tail -1 | cut -d'"' -f2 || true)
  fi
  if [ -n "$INITIAL_PASS" ]; then break; fi
done
if [ -n "$INITIAL_PASS" ]; then
  echo "               初始管理员: admin / $INITIAL_PASS  (仅首次创建打印, 登录后请修改)"
fi
fi

echo
echo "  内网机启动客户端:"
echo "    ngrok agent -server=$DOMAIN:$TUNNEL_PORT"
echo "  停止服务端:"
echo "    bash stop-onenat.sh"
echo
echo "  生产模式(正式证书+标准口):"
echo "    sudo DOMAIN=你的域名 HTTP_PORT=80 HTTPS_PORT=443 TLS_CERT=/path/crt TLS_KEY=/path/key AUTH_TOKENS=k1,k2 bash start-onenat.sh"
