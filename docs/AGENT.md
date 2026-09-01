# ngrok agent — 给公网 AI 的 SSH 远程通道

把内网机器的 SSH 通过 ngrok 暴露给公网 AI agent，带 TeamViewer 式的动态访问码，
并自动生成 AI 可直接执行的远程连接说明书。

## 工作原理

```
AI Agent(公网) ──①首行 "AUTH 访问码"──► ngrokd ──TLS隧道──► ngrok(内网,改造点)
                 ──②透明 SSH──►                          ├─ ★网关: 验码后才拼接字节
                                                          └─ ★登录后生成 remote-manual.{md,json}
                                                                └─► 本地 sshd (127.0.0.1:22)
```

三层认证：

| 层 | 机制 | 防什么 |
|---|---|---|
| L1 | `ngrokd -authToken=xxx` 校验客户端 | 别人蹭你的服务器建隧道 |
| L2 | 客户端网关：连接首行必须是 `AUTH <访问码>`，验码后才把字节拼接给 sshd | 端口扫描/匿名爆破/SSH 暴露 |
| L3 | SSH 自身（推荐仅密钥） | 冒用访问码的陌生人拿到 shell |

访问码默认每 30 分钟轮换（已建立的连接不受影响），旧码有 90 秒宽限；
连续 5 次错码触发该隧道 60 秒冷却。

## 内网机器（macOS）

1. 开启远程登录：`sudo systemsetup -setremotelogin on`
   （或系统设置 → 通用 → 共享 → 远程登录），并把 AI 侧公钥加进 `~/.ssh/authorized_keys`。
2. 写配置 `~/.ngrok`：

```yaml
server_addr: your-server.com:4443
trust_host_root_certs: true      # 自建服务端(非 release 构建对接)时
auth_token: secret123            # 与 ngrokd -authToken 一致
code_ttl: 30m                    # 访问码轮换周期
ssh_user: tsbj                   # 写进说明书
machine_desc: "KB 测试环境宿主机, 可操作 docker/kb-cli"
rules:                           # 给 AI 的行为边界, 原样写进说明书
  - 禁止 reboot/poweroff
  - 危险命令先 dry-run
```

3. 启动：

```bash
ngrok agent 22
```

终端出现连接卡片：

```
┌─────────────────────────────────────────────────────────┐
│  ngrok agent ONLINE   19:52:58
│  entrypoint   tcp://ngrok.me:55895
│  access code  fe4d-d8a9
│  rotates in   30s (active connections keep working)
│  local target ssh://127.0.0.1:22
│  manual       /Users/you/.ngrok.d/remote-manual.{md,json}
│  give remote-manual.json to the AI agent, it connects by
│  itself following the commands inside. Ctrl-C to go offline.
└─────────────────────────────────────────────────────────┘
```

4. 把 `~/.ngrok.d/remote-manual.json`（或 .md）发给公网 AI。完成。

## AI Agent 侧

按说明书 `commands.shell` 原样执行即可，无需安装任何东西：

```bash
# 交互 shell（ProxyCommand 里内嵌了网关握手）
ssh -o ProxyCommand='{ printf "AUTH fe4d-d8a9\r\n"; cat; } | nc ngrok.me 55895' \
    -o StrictHostKeyChecking=accept-new tsbj@localhost

# 单发命令
ssh -o ProxyCommand='{ printf "AUTH fe4d-d8a9\r\n"; cat; } | nc ngrok.me 55895' \
    -o StrictHostKeyChecking=accept-new tsbj@localhost 'uname -a && df -h'

# scp 传文件同样适用
scp -o ProxyCommand='{ printf "AUTH fe4d-d8a9\r\n"; cat; } | nc ngrok.me 55895' \
    local.log tsbj@localhost:/tmp/
```

要点：
- **每条 TCP 连接的第一行**必须是 `AUTH <访问码>`（访问码以说明书当前版本为准，会轮换）。
- 验证通过后连接即完全透明，SSH 的加密与密钥认证端到端不变。
- 可以把 SSH banner 与 AUTH 行在同一段数据里流水线发出（命令里已这么写）。
- 不发码/发错码的连接只会收到 `ERR ngrok-gate: ...` 就被断开，sshd 侧零暴露。

## 服务端（公网 VPS）

```bash
./ngrokd -domain your-domain.com \
         -httpAddr="" -httpsAddr="" \
         -tunnelAddr=:4443 \
         -tlsKey=... -tlsCrt=... \
         -authToken=secret123
```

- 建议关闭 http/https 公网口，只留客户端隧道口，减少暴露面。
- 不配 `-authToken` 时维持旧行为（不校验）。

## 配置参考（客户端）

| 键 | 默认 | 说明 |
|---|---|---|
| `gate` | off | `plain` 启用访问码网关；`agent` 命令自动设为 plain |
| `gate_token` | 自动生成 | 固定访问码（与 `code_ttl` 互斥） |
| `code_ttl` | 会话期内有效 | 轮换周期，≥30s，如 `30m` |
| `manual_path` | `~/.ngrok.d` | 说明书输出目录 |
| `ssh_user` | USER | 说明书里的登录用户 |
| `ssh_port` | 22 | 本地 sshd 端口 |
| `machine_desc` | - | 机器用途描述，写给 AI |
| `rules` | - | AI 行为边界清单，写给 AI |

CLI 覆盖：`-gate=plain`、`-remote-port=60022`（指定服务端 TCP 端口）、`-subdomain`（不适用 tcp）、
`-proto=tcp + 端口` 等价于 `agent` 但不带网关与说明书。

## 安全须知

- AI→ngrokd 段是明文 TCP：plain 模式下访问码在该段可见，短轮换周期即为此设计的止损；
  对链路窃听有强要求时，把 ngrokd 放在与 AI 同一可信出口、或自行加 VPN/WireGuard 前置。
- SSH 建议仅密钥登录，并在 sshd 配置里限制允许的用户。
- Ctrl-C 立即整体下线；换码即吊销旧码（90 秒宽限后完全失效）。
- 所有连接在客户端日志（`-log=stdout -log-level=DEBUG`）可审计。

---

## ⚡ 零配置模式（v2 新增，最简路径）

完全不用配置文件，一条命令：

```bash
ngrok agent -server=host:4443                    # 本机 SSH+Web
ngrok agent -server=host:4443 192.168.1.20       # 远程机器 SSH+Web
ngrok agent -server=host:4443 -new-key           # 刷新机器密钥
ngrok agent -server=host:4443 192.168.1.20:2222  # 远程机器 sshd 在 2222
```

- 首次运行自动生成机器密钥 `~/.ngrok.d/machine.key`，此后不变（`-new-key` 刷新）。
- 同时映射目标的 SSH(22) 和 HTTP(80)：SSH 走密钥网关，Web 公开直连。
- 服务端白名单：`ngrokd -authToken=机器1的key,机器2的key`。
- 卡片 + `~/.ngrok.d/remote-manual.json` 发给 AI 即完成交付。

