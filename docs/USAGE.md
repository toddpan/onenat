# ngrok AI 远程运维通道 — 完整使用说明书

> 版本：基于 ngrok v1.7 源码改造 ｜ 适用二进制：本仓库 `bin/ngrokd`、`bin/ngrok`
> 场景：把内网机器的 SSH 和 Web 安全地暴露给公网 AI agent，让它能远程登录、分析、操作这台电脑。

---

## ⚡ 零配置快速上手（推荐，TeamViewer 式）

**内网机器上一条命令，无需任何配置文件：**

```bash
# 本机（SSH 22 + HTTP 80 一起映射出去）
ngrok agent -server=tunnel.example.com:4443

# 内网另一台机器 192.168.1.20 的 SSH(22) + HTTP(80)
ngrok agent -server=tunnel.example.com:4443 192.168.1.20

# 指定那台机器的 sshd 端口是 2222
ngrok agent -server=tunnel.example.com:4443 192.168.1.20:2222

# 刷新机器密钥（旧密钥立即吊销）
ngrok agent -server=tunnel.example.com:4443 -new-key
```

首次运行自动生成**机器密钥** `~/.ngrok.d/machine.key`（形如 `ngk-07542a16-3e14e93f-d06aa23c-95837120`），
此后**永不改变**（除非 `-new-key`）。终端出现连接卡片：

```
┌─────────────────────────────────────────────────────────────┐
│  ngrok agent ONLINE   20:43:46
│  web  ➜  http://xxxx.ngrok.me:80   (公开直连)
│  ssh  ➜  tcp://tunnel.example.com:49769   (需要密钥)
│  密钥 KEY    ngk-07542a16-3e14e93f-d06aa23c-95837120
│  密钥文件    ~/.ngrok.d/machine.key  (机器唯一,不变; -new-key 刷新)
│  说明书      ~/.ngrok.d/remote-manual.{md,json}
│  把 remote-manual.json 发给 AI，它照里面命令自行连接。
│  Ctrl-C 整体下线。
└─────────────────────────────────────────────────────────────┘
```

**把 `~/.ngrok.d/remote-manual.json` 发给 AI，结束。** AI 照说明书里的现成命令自己连：

```bash
# SSH（ProxyCommand 内嵌了密钥握手，复制即用）
ssh -o ProxyCommand='{ printf "AUTH ngk-07542a16-3e14e93f-d06aa23c-95837120\r\n"; cat; } | nc tunnel.example.com 49769' \
    -o StrictHostKeyChecking=accept-new USER@localhost

# Web（公开直连，无需密钥）
curl -s http://xxxx.ngrok.me:80
```

机器密钥一个三用：① 服务端准入（ngrokd 校验）② SSH 入口网关的 AUTH 码 ③ 说明书里的唯一凭据。
服务端用逗号分隔多台机器的密钥即可：`ngrokd -authToken=key1,key2,key3`。

> 以下章节为完整版说明（含传统配置文件模式），零配置用户只需读本节 + 第 12 章故障排查。

---

## ⚡ Web 管理后台（多用户 + 隧道管理 + 一键部署）

ngrokd 内置 Web 管理后台（详见 [docs/DASHBOARD.md](DASHBOARD.md)）：

- **用户管理**：管理员 / 普通用户两级角色；普通用户只读自己名下隧道并获取安装脚本。
- **隧道管理**：Web 端创建隧道、增删改端口映射（tcp/http/https）、在线状态、
  连接记录、近 7 天流量。
- **一键部署**：详情页给出一行命令
  `curl -sSL http://server:18080/install.sh | bash -s -- <隧道ID> <KEY>`，
  在任意内网机执行即完成 客户端下载 + 配置拉取 + systemd/launchd 常驻。
- **远程改配置**：Web 端修改端口映射后，在线客户端**不重启**实时生效
  （新增/改端口/删除/修复隧道均走服务端 ConfigSync 下发）。

启用方式（默认已启用）：

```bash
make release-clients                       # 客户端产物进 ./dl/, 供一键安装下载
bash start-onenat.sh                       # WEB_PORT 默认 18080
# 或: ./bin/ngrokd -webAddr=:18080 -webData=./onenat-dashboard.json -dlDir=./dl ...
```

首次启动在日志/stdout 打印初始 `admin` 密码；`-webAddr=""` 可禁用后台。
端到端自测：`bash test-dashboard-e2e.sh`（43 项断言）。

---

1. [系统概览](#1-系统概览)
2. [角色与术语](#2-角色与术语)
3. [编译与产物](#3-编译与产物)
4. [第一步：部署服务端 ngrokd（公网 VPS）](#4-第一步部署服务端-ngrokd公网-vps)
5. [第二步：配置内网机器](#5-第二步配置内网机器)
6. [第三步：启动 agent 模式](#6-第三步启动-agent-模式)
7. [第四步：把说明书交给 AI](#7-第四步把说明书交给-ai)
8. [AI agent 侧：如何连接](#8-ai-agent-侧如何连接)
9. [生命周期：轮换/宽限/吊销/下线](#9-生命周期轮换宽限吊销下线)
10. [客户端配置完整参考](#10-客户端配置完整参考)
11. [命令行参数参考](#11-命令行参数参考)
12. [故障排查](#12-故障排查)
13. [安全模型](#13-安全模型)
14. [自测与验证](#14-自测与验证)
15. [FAQ](#15-faq)

---

## 1. 系统概览

```
 AI Agent (公网)                ngrokd (公网VPS)                 内网机器
    │                              │                              │
    │ ①首行 "AUTH 访问码"           │                              │
    │ ②SSH(端到端加密+密钥认证)      │  ── TLS 隧道 ──────────────► │ ★ngrok 客户端(改造)
    │ ═══════════════════════════► │                              │  ├─ ★网关: 验码后才拼接字节
    │                              │                              │  ├─ ★登录即生成连接卡片+说明书
    │                              │                              │  └─► sshd 127.0.0.1:22
```

一次 AI 连接的字节路径：

```
AI ──► [AUTH 行] ──► ngrokd(纯转发) ──► ngrok 网关(验码) ──┬─ 通过: 剩余字节原样给 sshd, 之后全透明
                                                          └─ 拒绝: 回 ERR 行并断开, sshd 零接触
```

**三层认证**：

| 层 | 在哪 | 机制 | 防什么 |
|---|---|---|---|
| L1 | 服务端 | `-authToken` 校验客户端身份 | 别人蹭你的 VPS 建隧道 |
| L2 | 客户端★ | 入口网关：连接首行必须 `AUTH <访问码>` | 端口扫描/匿名爆破/SSH 0day 暴露 |
| L3 | SSH | 密钥登录（推荐） | 拿到码的陌生人进不了 shell |

---

## 2. 角色与术语

| 术语 | 含义 |
|---|---|
| **ngrokd** | 服务端二进制，跑在公网 VPS 上，负责公网入口和隧道转发 |
| **ngrok** | 客户端二进制，跑在内网机器上，本说明书的核心改造对象 |
| **agent 模式** | `ngrok agent 22`：TCP 隧道 + 入口网关 + 自动生成 AI 说明书 |
| **入口 (entrypoint)** | 公网可达的 `tcp://域名:端口`，AI 从这里连入 |
| **访问码 (access code)** | 形如 `8ae0-1de1` 的 8 位十六进制码，网关的通行证 |
| **连接卡片** | agent 启动后打印在终端的摘要框（入口+码+倒计时） |
| **说明书** | `remote-manual.md` / `remote-manual.json`，给 AI 的自包含连接手册 |

---

## 3. 编译与产物

```bash
export GO111MODULE=off GOPATH=/Users/tsbj/feyanggit/ngrok   # 仓库即 GOPATH
cd /Users/tsbj/feyanggit/ngrok
go build -tags debug -o bin/ngrokd ngrok/main/ngrokd        # 服务端
go build -tags debug -o bin/ngrok  ngrok/main/ngrok         # 客户端
go test ngrok/gate                                          # 网关单测(应全过)
```

- debug 构建：TLS 信任内嵌 snakeoil CA + 系统根证书，自建服务端零配置对接。
- 交叉编译给 Linux 内网机：`GOOS=linux GOARCH=amd64 go build -tags debug -o bin/ngrok-linux ngrok/main/ngrok`（依赖均为纯 Go，可直接交叉）。
- 资源(证书/网页)已改为 go:embed 内嵌，无需 go-bindata。

---

## 4. 第一步：部署服务端 ngrokd（公网 VPS）

### 4.1 前置条件

- VPS 开放端口：**4443**（或自定义的隧道口，客户端连它）；agent 模式不需要 80/443。
- （可选但推荐）一个指向 VPS 的域名 + TLS 证书；不配证书时自动使用内嵌自签证书，
  客户端 debug 构建可直接信任。
- 客户端配置的 `server_addr` 域名必须能解析到 VPS（客户端 TLS SNI 用它做校验名）。

### 4.2 启动命令

```bash
./ngrokd -domain your-domain.com \
         -httpAddr="" -httpsAddr="" \
         -tunnelAddr=:4443 \
         -authToken=secret123 \
         -tlsCrt=/etc/ngrok/tls.crt -tlsKey=/etc/ngrok/tls.key \
         -log=/var/log/ngrokd.log -log-level=INFO
```

### 4.3 服务端参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `-domain` | ngrok.com | 签发隧道用的域名（TCP 隧道体现在展示 URL，实际按端口连） |
| `-tunnelAddr` | :4443 | 客户端控制/代理连接监听口 |
| `-httpAddr` / `-httpsAddr` | :80 / :443 | 公网 HTTP(S) 入口；**纯 SSH 场景建议置空**，减少暴露面 |
| `-authToken` | 空=不校验 | **必配**。客户端 `auth_token` 必须与之完全一致，否则拒绝 |
| `-tlsCrt` / `-tlsKey` | 内嵌自签 | TLS 证书/私钥 |
| `-log` / `-log-level` | stdout / DEBUG | 日志文件与级别（含认证失败审计） |

### 4.4 systemd 常驻（示例）

```ini
# /etc/systemd/system/ngrokd.service
[Unit]
After=network.target
[Service]
ExecStart=/usr/local/bin/ngrokd -domain your-domain.com -httpAddr="" -httpsAddr="" \
          -tunnelAddr=:4443 -authToken=secret123 -log=/var/log/ngrokd.log -log-level=INFO
Restart=always
[Install]
WantedBy=multi-user.target
```

> 注意：客户端断线重连时服务端会**优先归还上次分配的同一个端口**（按 ClientId/IP 亲和缓存，
> 可用环境变量 `REGISTRY_CACHE_FILE=/path/cache 持久化`），便于在防火墙里放行固定端口。
> 若要完全固定端口，见 [FAQ](#15-faq) 的 `-remote-port`。

---

## 5. 第二步：配置内网机器

### 5.1 开启 SSH 服务端

**macOS（本机即目标机时）：**

```bash
sudo systemsetup -setremotelogin on        # 或: 系统设置 → 通用 → 共享 → 远程登录
```

把 AI 侧的公钥加入 `~/.ssh/authorized_keys`（推荐仅密钥登录；
`sudo edit /etc/ssh/sshd_config` 可设 `PasswordAuthentication no`）。

**Linux：** `systemctl enable --now sshd`，同样配好 authorized_keys。

### 5.2 客户端配置文件

默认读取 `~/.ngrok`（YAML），也可 `-config=path` 指定。SSH 场景模板：

```yaml
# ---- 连接服务端 ----
server_addr: your-domain.com:4443     # ngrokd 的隧道口
auth_token: secret123                 # 必须与 ngrokd -authToken 一致
trust_host_root_certs: true           # 服务端用正式证书时

# ---- agent 模式行为 ----
code_ttl: 30m                         # 访问码 30 分钟轮换一次
ssh_user: tsbj                        # 写进说明书, AI 用它登录
ssh_port: 22                          # 本地 sshd 端口
manual_path: /Users/tsbj/.ngrok.d     # 说明书输出目录(默认 ~/.ngrok.d)

# ---- 给 AI 的背景与边界(原样写进说明书) ----
machine_desc: "KB 测试环境宿主机, 已装 docker/kb-cli, 用于联调分析"
rules:
  - 禁止 reboot/poweroff
  - 危险命令先 dry-run
  - 只允许在 /tmp 与 ~/work 下写文件
```

> `inspect_addr: disabled` 可关闭 4040 检查器（纯 SSH 场景用不到）。

---

## 6. 第三步：启动 agent 模式

```bash
ngrok agent 22            # 参数 = 本地 sshd 端口
```

等价于：`-proto=tcp` + `gate: plain` + 网关 + 说明书生成，一条命令全包。

启动成功后终端出现**连接卡片**（每次换码会重新打印一张）：

```
┌─────────────────────────────────────────────────────────┐
│  ngrok agent ONLINE   19:52:58
│  entrypoint   tcp://your-domain.com:55895
│  access code  fe4d-d8a9
│  rotates in   30s (active connections keep working)
│  local target ssh://127.0.0.1:22
│  manual       /Users/tsbj/.ngrok.d/remote-manual.{md,json}
│  give remote-manual.json to the AI agent, it connects by
│  itself following the commands inside. Ctrl-C to go offline.
└─────────────────────────────────────────────────────────┘
```

同时生成两份说明书（内容一致，格式不同）：

- `~/.ngrok.d/remote-manual.json` —— 给 AI/程序读
- `~/.ngrok.d/remote-manual.md` —— 给人看

---

## 7. 第四步：把说明书交给 AI

把 `remote-manual.json` 全文粘给 AI 即可（或在你的产品里作为附件传给它）。
说明书是**自包含**的，AI 不需要其他上下文。结构如下：

```json
{
  "endpoint":  { "gate_host": "your-domain.com", "gate_port": 55895, "proto": "ssh" },
  "gate": {
    "mode": "plain",
    "code": "8ae0-1de1",
    "expires_at": "2026-08-31T20:22:18+08:00",
    "handshake": "First line of every TCP connection must be exactly: AUTH <code> ...",
    "fail_behavior": "5 consecutive failures rate-limit the tunnel for 1m0s."
  },
  "ssh":   { "user": "tsbj", "local_port": 22, "auth": "publickey ...",
             "host_key_algorithm": "ssh-ed25519", "host_key_fingerprint": "SHA256:..." },
  "machine": { "os": "macOS 15.3.1", "desc": "KB 测试环境宿主机 ..." },
  "commands": {
    "shell":   "ssh -o ProxyCommand='{ printf \"AUTH 8ae0-1de1\\r\\n\"; cat; } | nc your-domain.com 55895' -o StrictHostKeyChecking=accept-new tsbj@localhost",
    "oneshot": "ssh ... 'uname -a && df -h'",
    "scp_up":  "scp ... LOCALFILE tsbj@localhost:/tmp/",
    "scp_down":"scp ... tsbj@localhost:/path/file LOCALDIR/",
    "raw_nc_test": "(printf \"AUTH 8ae0-1de1\\r\\n\"; sleep 1) | nc your-domain.com 55895"
  },
  "rules": ["禁止 reboot/poweroff", "危险命令先 dry-run"],
  "troubleshoot": { "connection closed immediately": "access code expired ...", "...": "..." }
}
```

要点：
- `commands.*` 是**可直接复制执行**的完整命令（ProxyCommand 内嵌了握手）。
- `code` 每次轮换后文件会**自动重写**；AI 侧要新码 = 让操作者重新发一次最新说明书。
- `host_key_fingerprint` 供 AI 首次连接时核对主机身份（本机 sshd 未开启时留空并附开启指引）。

---

## 8. AI agent 侧：如何连接

### 8.1 唯一硬规则

**每条 TCP 连接的第一行必须是 `AUTH <访问码>`**（大小写不敏感，CR/LF 结尾），
5 秒内发出。验证通过后连接完全透明——SSH 协议、加密、密钥认证全部端到端不变。
可以把 SSH banner 与 AUTH 行在同一段数据里一次发出（下面的命令就是这么写的）。

### 8.2 现成命令（从说明书 commands 区原样复制）

```bash
# 交互 shell
ssh -o ProxyCommand='{ printf "AUTH fe4d-d8a9\r\n"; cat; } | nc your-domain.com 55895' \
    -o StrictHostKeyChecking=accept-new tsbj@localhost

# 单发命令（AI 最常用形态）
ssh -o ProxyCommand='{ printf "AUTH fe4d-d8a9\r\n"; cat; } | nc your-domain.com 55895' \
    -o StrictHostKeyChecking=accept-new tsbj@localhost 'uname -a && df -h'

# 上传/下载文件
scp -o ProxyCommand='{ printf "AUTH fe4d-d8a9\r\n"; cat; } | nc your-domain.com 55895' \
    local.log tsbj@localhost:/tmp/

# 无 ssh 的冒烟测试（收到 SSH banner 回显 = 网关已放行）
(printf "AUTH fe4d-d8a9\r\n"; sleep 1) | nc your-domain.com 55895
```

### 8.3 行为结果对照

| AI 发出的首行 | 收到 | 说明 |
|---|---|---|
| `AUTH <当前码>` | （静默放行，后续即 SSH） | ✅ 正常 |
| `AUTH <90s 内的旧码>` | 静默放行 | 轮换宽限，兼容刚过期的说明书 |
| `AUTH <错码>` | `ERR ngrok-gate: access denied` + 断开 | 码错/过期 |
| 不说话 / HTTP / SSH banner | `ERR ngrok-gate: bad request, expected: AUTH <code>` + 断开 | 端口扫描或没读说明书 |
| 连续 5 次失败后任何连接 | `ERR ngrok-gate: rate limited, retry in a minute` | 隧道级 60s 冷却 |

---

## 9. 生命周期：轮换/宽限/吊销/下线

| 事件 | 行为 |
|---|---|
| **码轮换**（`code_ttl` 到期，默认 30m） | 生成新码并重写说明书、重打卡片；**已建立的连接不受影响** |
| **宽限期**（换码后 90s） | 旧码仍可用，避免拿着刚过期说明书的 AI 立刻失联 |
| **吊销** | 想立刻换码？重启 agent 进程，或等下个轮换点；旧码宽限后彻底失效 |
| **整体下线** | 终端 `Ctrl-C`：隧道注销、公网端口关闭、说明书作废 |
| **断线重连** | 客户端自动指数退避重连（1s→30s 封顶），服务端通常归还同一公网端口 |
| **会话接替** | 网络闪断重连时用同一 ClientId，服务端平滑替换旧控制会话 |

---

## 10. 客户端配置完整参考

| 键 | 默认 | 说明 |
|---|---|---|
| `server_addr` | 无 | **必填**。`域名:4443`，指向 ngrokd 隧道口 |
| `auth_token` | 空 | 与服务端 `-authToken` 匹配；服务端启用校验时**必填** |
| `trust_host_root_certs` | false | true=信任系统根证书（服务端用正式证书时） |
| `http_proxy` | `$http_proxy` | 客户端出网走 HTTP 代理（CONNECT） |
| `inspect_addr` | 127.0.0.1:4040 | Web 检查器；SSH 场景建议 `disabled` |
| `gate` | off | `plain`=启用入口网关；`agent` 命令自动设 plain |
| `gate_token` | 自动 | **固定**访问码（与 `code_ttl` 互斥，二者只能选一） |
| `code_ttl` | 会话期 | 轮换周期，最小 30s；`30m`/`1h`/`0`=不轮换 |
| `manual_path` | `~/.ngrok.d` | 说明书输出目录（自动创建，权限 0700） |
| `ssh_user` | USER | 说明书中的登录用户 |
| `ssh_port` | 22 | 本地 sshd 端口（说明书+指纹探测用） |
| `machine_desc` | 空 | 机器用途描述，写给 AI |
| `rules` | 空 | AI 行为边界清单，原样进说明书 |
| `tunnels` | - | 经典多隧道配置，与 agent 模式独立 |

---

## 11. 命令行参数参考

### 客户端 ngrok

```
ngrok agent 22                     # ★ AI 远程运维模式（本说明书主线）
ngrok -proto=tcp 22                # 经典 TCP 隧道（无网关/无说明书）
ngrok -remote-port=60022 agent 22  # 请求服务端固定公网端口 60022
ngrok -gate=off -proto=tcp 22      # 明确关闭网关
ngrok -config=/path/x.yml agent 22 # 指定配置文件
ngrok -log=stdout -log-level=DEBUG agent 22   # 前台调试日志（含每次网关拒绝记录）
ngrok start <名字> ... / start-all / list / version / help
```

| flag | 说明 |
|---|---|
| `-config` | 配置文件路径（默认 ~/.ngrok） |
| `-log` / `-log-level` | 日志去向与级别（DEBUG 可审计网关拒绝） |
| `-proto` | http / https / http+https / tcp |
| `-remote-port` | 请求固定服务端 TCP 端口 |
| `-gate` | plain / off，覆盖配置 |
| `-authtoken` | 覆盖配置里的 auth_token |
| `-subdomain` / `-hostname` / `-httpauth` | HTTP 隧道用，SSH 场景不涉及 |

### 服务端 ngrokd

见 [4.3 服务端参数](#43-服务端参数)。

---

## 12. 故障排查

**网关错误（AI 侧可见）**

| 症状 | 原因 | 处理 |
|---|---|---|
| `ERR ngrok-gate: access denied` | 码错或已轮换 | 取最新说明书里的码；轮换自动重写，重发即可 |
| `ERR ngrok-gate: bad request, expected: AUTH <code>` | 首行不是 `AUTH xxx` | 检查 ProxyCommand 引号/`\r\n`；先跑 `raw_nc_test` |
| `ERR ngrok-gate: rate limited, retry in a minute` | ≥5 次失败触发冷却 | 等 60s，用**正确**码重试 |
| 连接 5 秒后被断 | 没在超时内发 AUTH 行 | 检查脚本是否把 AUTH 放在了 sleep 之后 |

**SSH/链路错误**

| 症状 | 原因 | 处理 |
|---|---|---|
| 网关放行后 kex/banner 超时 | 本地 sshd 没跑 | macOS: `sudo systemsetup -setremotelogin on`（说明书会自动提示） |
| `Permission denied (publickey)` | AI 侧公钥未装 | 把 AI 公钥加入 `~/.ssh/authorized_keys` |
| `Host key verification failed` | 主机重装/换钥匙 | 核对新指纹后清 known_hosts 旧行 |
| `Failed to authenticate to server: Invalid authentication token` | 客户端/服务端 token 不一致 | 两边改成同一个值 |
| 客户端 `bind: address already in use`（服务端同理） | 端口被旧进程占用 | `pkill -f ngrokd` / 换端口 |
| `Server failed to allocate tunnel: ...` | 服务端没开对应监听或端口冲突 | 看服务端日志；TCP 用 `-remote-port` 换固定口 |
| 公网口连不上 | VPS 防火墙未放行 4443/隧道端口 | 放行隧道口 + 分配到的 TCP 端口段 |

**定位技巧**：内网机跑 `ngrok -log=stdout -log-level=DEBUG agent 22`，
每次网关拒绝/放行、每条连接都有日志；服务端同理。

---

## 13. 安全模型

- **明文段提醒**：AI → ngrokd 段是普通 TCP，`AUTH <码>` 在该段可见。设计上用**短轮换**
  （默认 30m）+ 90s 宽限把泄露窗口压到最小；SSH 本身端到端加密不受影响。
  若链路上有不可信中间人，请在这段链路前置 WireGuard/VPN。
- **最小暴露**：服务端关闭 `-httpAddr/-httpsAddr`，只留隧道口；VPS 防火墙仅放行所需端口。
- **码失效即弃**：轮换出的新码不落终端历史（卡片只显示当前码）；说明书文件权限 0600。
- **爆破防护**：常量时间比较 + 5 次失败 → 60s 隧道级冷却；扫描者对端口"不可见"。
- **行为边界**：`rules` 写进说明书是给 AI 的软约束；硬约束靠 sshd（仅密钥、限用户）
  与操作系统权限。不要给 AI root，按需建专用账号。
- **审计**：服务端记录认证失败；客户端 DEBUG 日志记录每次网关拒绝与连接。
  需要会话录屏时可在 sshd 侧加 `script`/`tlog` 类方案。

---

## 14. 自测与验证

部署后 3 分钟自检清单：

```bash
# ① 服务端活着
nc -z your-domain.com 4443 && echo tun-port-ok

# ② 内网 agent 在线（终端有卡片，说明书已生成）
ls -la ~/.ngrok.d/remote-manual.* 

# ③ 网关冒烟（把 CODE/PORT 换成说明书里的值；期望回显 SSH banner）
(printf "AUTH $CODE\r\n"; sleep 1) | nc your-domain.com $PORT

# ④ 错码应被拒（期望 access denied）
(printf "AUTH 0000-0000\r\n"; sleep 0.5) | nc your-domain.com $PORT

# ⑤ 真连一次（AI 视角）
ssh -o ProxyCommand='{ printf "AUTH '$CODE'\r\n"; cat; } | nc your-domain.com '$PORT \
    -o StrictHostKeyChecking=accept-new $SSH_USER@localhost 'echo E2E-OK && hostname'
```

预期：③ 回显 `SSH-2.0-...`；④ 拒绝；⑤ 输出 `E2E-OK` + 主机名。

---

## 15. FAQ

**Q1：码轮换了，AI 手里的说明书就废了？**
不是——旧码有 90 秒宽限；之后 AI 重试会收到 `access denied`，此时向操作者要最新说明书即可
（文件在每次轮换时自动重写，重新发送/重新粘贴即可）。

**Q2：想让端口和码都固定长期不变？**
```yaml
gate_token: my-fixed-code-1234     # 固定码（与 code_ttl 互斥）
```
```bash
ngrok -remote-port=60022 agent 22  # 固定公网端口
```
适合调试期；生产建议保留轮换。

**Q3：多台内网机器？**
每台跑一个 agent 实例，各用各的配置与 `manual_path`（说明书互相独立、码互相独立）。
`ssh_user`/`machine_desc` 按机器写清楚，AI 不会串。

**Q4：AI 能不能直接用 HTTP 代理/在受限网络里连？**
AI 侧 `nc` 换成它的出网工具即可（如 `ProxyCommand` 里用 `socat`/`corkscrew`）；
客户端在内网出网要走代理时配 `http_proxy`。

**Q5：想看 AI 连进来干了什么？**
`-log=stdout -log-level=DEBUG` 下客户端记录每次连接与网关判定；
连接级字节审计属 M3 规划（可在 4040 检查器扩展）。

**Q6：为什么验证通过后是"静默"的？**
设计如此：网关只做门卫，验码后立即恢复纯透明管道，不对 SSH 协议做任何解析/改动，
保证与标准 ssh/scp/rsync/端口转发 100% 兼容。

---

*文档对应实现：`src/ngrok/gate/`（网关）、`src/ngrok/client/{config,model,manual,cli}.go`、
`src/ngrok/server/{cli,control}.go`；快速上手另见 `docs/AGENT.md`。*
