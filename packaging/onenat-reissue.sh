#!/bin/bash
# =============================================================================
# oneNat 服务端迁机/换 IP 证书重签脚本
# 位置: /opt/onenat/onenat-reissue.sh
#
# 场景: oneNat 服务端搬迁到新公网 IP(或启用域名)后, 原服务端证书的 SAN
#       只含旧地址, 客户端 TLS ServerName 校验会失败。本脚本用既有 CA
#       (/opt/onenat-pki/ca.key + ca.crt) 重签含新地址的证书并无缝切换;
#       客户端二进制(内置 CA 信任根)无需重编/重装。
#
# 用法:
#   bash onenat-reissue.sh <新IP或域名>[,<更多地址>] [--dry-run]
# 例:
#   bash onenat-reissue.sh 47.98.111.222
#   bash onenat-reissue.sh "47.98.111.222,nat.example.com" --dry-run
#
# 做什么:
#   1. 用既有 CA 重签 10 年期服务端证书 (SAN=新地址)
#   2. 备份旧证书与旧单元 -> /opt/onenat-pki/backup-<时间戳>/
#   3. 重新渲染 systemd 单元: -domain 新地址 + 显式 -tlsCrt/-tlsKey (免重编)
#   4. 重启 onenat 并自检: 监听 / 证书 SAN / deploy 下发的 server_addr
#
# ⚠️ 迁机前必带目录: /opt/onenat-pki (ca.key 是信任根, 丢了就得全量重装客户端)
# =============================================================================
set -euo pipefail

PKI_DIR=/opt/onenat-pki
SVC=/etc/systemd/system/onenat.service
DATA_FILE=/opt/onenat/onenat-dashboard.json
DAYS=3650

err(){ echo -e "\033[31m[ERR]\033[0m $*"; exit 1; }
ok(){ echo -e "\033[32m[OK]\033[0m  $*"; }
info(){ echo -e "\033[36m[..]\033[0m  $*"; }

NEW_ADDR="${1:-}"; DRY="${2:-}"
[ -n "$NEW_ADDR" ] || err "用法: $0 <新IP或域名>[,更多地址] [--dry-run]"
[ -f "$PKI_DIR/ca.key" ] && [ -f "$PKI_DIR/ca.crt" ] || err "缺少 CA: $PKI_DIR/ca.key|ca.crt (迁机时务必携带 /opt/onenat-pki 整个目录)"
[ -f "$SVC" ] || err "未找到 $SVC"

# ---- SAN 构造 (自动识别 IP/域名, 支持逗号分隔多地址) ----
SAN=""
IFS=',' read -ra items <<< "$NEW_ADDR"
for a in "${items[@]}"; do
  a="$(echo "$a" | xargs)"; [ -n "$a" ] || continue
  if [[ "$a" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then SAN+="IP:$a,"; else SAN+="DNS:$a,"; fi
done
SAN="${SAN}IP:127.0.0.1,DNS:localhost,DNS:onenat.local"
PRIMARY="$(echo "$NEW_ADDR" | cut -d, -f1 | xargs)"

echo "=============================================="
echo " oneNat 服务端证书重签"
echo "   新地址 : $NEW_ADDR"
echo "   主地址 : $PRIMARY"
echo "   SAN    : $SAN"
echo "   有效期 : $DAYS 天"
echo "   模式   : ${DRY:+DRY-RUN }实签"
echo "=============================================="

WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT

# ---- 1. 签发 ----
info "生成服务端密钥与 CSR..."
openssl genrsa -out "$WORK/snakeoil.key" 2048 2>/dev/null
openssl req -new -key "$WORK/snakeoil.key" -subj "/CN=$PRIMARY/O=oneNat" -out "$WORK/snakeoil.csr"
printf "subjectAltName=%s\nbasicConstraints=CA:FALSE\nextendedKeyUsage=serverAuth\n" "$SAN" > "$WORK/san.ext"
info "用既有 CA 签发证书..."
openssl x509 -req -in "$WORK/snakeoil.csr" -CA "$PKI_DIR/ca.crt" -CAkey "$PKI_DIR/ca.key" \
  -CAcreateserial -days "$DAYS" -out "$WORK/snakeoil.crt" -extfile "$WORK/san.ext" 2>/dev/null
openssl verify -CAfile "$PKI_DIR/ca.crt" "$WORK/snakeoil.crt" >/dev/null || err "新证书链校验失败"
ok "新证书签发并校验通过: $(openssl x509 -in "$WORK/snakeoil.crt" -noout -subject)"

if [ "$DRY" = "--dry-run" ]; then
  info "DRY-RUN: 未落地。将更新 systemd 关键行为:"
  echo "    DOMAIN=$PRIMARY"
  echo "    -domain $PRIMARY"
  echo "    -tlsCrt $PKI_DIR/snakeoil.crt"
  echo "    -tlsKey $PKI_DIR/snakeoil.key"
  exit 0
fi

# ---- 2. 备份 + 落地 ----
TS=$(date +%Y%m%d-%H%M%S)
BAK="$PKI_DIR/backup-$TS"; mkdir -p "$BAK"
cp -a "$PKI_DIR/snakeoil.crt" "$BAK/" 2>/dev/null || true
cp -a "$PKI_DIR/snakeoil.key" "$BAK/" 2>/dev/null || true
cp -a "$SVC" "$BAK/onenat.service.bak"
ok "旧证书与单元已备份: $BAK"

install -m 600 "$WORK/snakeoil.key" "$PKI_DIR/snakeoil.key"
install -m 644 "$WORK/snakeoil.crt" "$PKI_DIR/snakeoil.crt"
ok "新证书已写入 $PKI_DIR/"

# ---- 3. 重渲染 systemd 单元 (保留原密码/路径, 换 domain + 显式 tls 参数) ----
PASS=$(grep -oP '(?<=-webAdminPass )\S+' "$SVC" | head -1 || true)
[ -n "$PASS" ] || err "无法从旧单元提取 -webAdminPass"
cat > "$SVC" <<EOF
[Unit]
Description=oneNat tunnel server (with web dashboard) - prod $PRIMARY
Documentation=https://github.com/toddpan/onenat
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/onenat
Environment=DOMAIN=$PRIMARY
ExecStart=/usr/local/bin/ngrokd \\
  -domain $PRIMARY \\
  -tunnelAddr :4443 \\
  -httpAddr "" \\
  -httpsAddr "" \\
  -webAddr :18080 \\
  -webData /opt/onenat/onenat-dashboard.json \\
  -webAdminPass $PASS \\
  -dlDir /opt/onenat/dl \\
  -tlsCrt $PKI_DIR/snakeoil.crt \\
  -tlsKey $PKI_DIR/snakeoil.key \\
  -log /var/log/onenat/onenat.log \\
  -log-level INFO
Restart=always
RestartSec=3
StandardOutput=journal
StandardError=journal
LimitNOFILE=65535
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF
ok "systemd 单元已更新 (domain=$PRIMARY, 显式 tlsCrt/tlsKey)"

systemctl daemon-reload
info "重启 onenat..."
systemctl restart onenat
sleep 3
systemctl is-active --quiet onenat || { journalctl -u onenat --no-pager -n 20; err "服务重启失败(已回滚提示: $BAK/onenat.service.bak)"; }
ok "onenat 服务已重启 (active)"

# ---- 4. 自检 ----
echo "---------------- 自检 ----------------"
CERT_SUBJ=$(echo | openssl s_client -connect 127.0.0.1:4443 2>/dev/null | openssl x509 -noout -subject 2>/dev/null || true)
CERT_SAN=$(echo | openssl s_client -connect 127.0.0.1:4443 2>/dev/null | openssl x509 -noout -ext subjectAltName 2>/dev/null | tail -1 || true)
CERT_EXP=$(echo | openssl s_client -connect 127.0.0.1:4443 2>/dev/null | openssl x509 -noout -enddate 2>/dev/null || true)
echo "  4443 证书: $CERT_SUBJ"
echo "  SAN      : $CERT_SAN"
echo "  到期     : $CERT_EXP"
HTTP=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/login || true)
echo "  18080    : HTTP $HTTP"
if [ -f "$DATA_FILE" ]; then
  TID=$(python3 -c "import json;print(json.load(open('$DATA_FILE'))['tunnels'][0]['id'])" 2>/dev/null || true)
  TKEY=$(python3 -c "import json;print(json.load(open('$DATA_FILE'))['tunnels'][0]['key'])" 2>/dev/null || true)
  if [ -n "${TID:-}" ] && [ -n "${TKEY:-}" ]; then
    SA=$(curl -s "http://127.0.0.1:18080/api/deploy?id=$TID&key=$TKEY" | grep server_addr || true)
    echo "  下发配置 : $SA"
  fi
fi
echo "--------------------------------------"
cat <<'TIP'

后续 (客户端侧, 二进制无需重装):
  1. 每台已装客户端重跑一次安装命令即可切换到新地址:
     curl -sSL http://<新地址>:18080/install.sh | bash -s -- <隧道ID> <隧道KEY>
  2. 或手动修改客户端 /etc/ngrok/ngrok-managed.yml 的 server_addr 为 <新地址>:4443
     并执行 systemctl restart ngrok-client
TIP
