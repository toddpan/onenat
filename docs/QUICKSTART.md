# 三步上手：把内网 SSH/Web 交给公网 AI

> 二进制位置：`/Users/tsbj/feyanggit/ngrok/bin/`（ngrokd=服务端，ngrok=客户端）
> 详细说明见 `docs/USAGE.md`。

---

## 第 1 步：公网服务器上跑服务端（一次性）

在一台有公网 IP 的 VPS 上：

```bash
./ngrokd -domain 你的域名.com \
         -httpAddr="" -httpsAddr="" \
         -tunnelAddr=:4443 \
         -authToken=机器A的密钥,机器B的密钥
```

- `-domain`：随便一个解析到这台 VPS 的域名（客户端要用它连接）
- `-authToken`：白名单。第一次可以先不配（`-authToken` 省略=不校验），拿到各机器密钥后再补上重启
- 防火墙放行 `4443/tcp` 和隧道分配的 TCP 端口段（或用 `-remote-port` 固定端口后只放行那个）

## 第 2 步：内网机器上一条命令

```bash
# 映射本机的 SSH(22) 和 HTTP(80)
ngrok agent -server=你的域名.com:4443

# 或者映射内网另一台机器的（不指定 IP 默认本机）
ngrok agent -server=你的域名.com:4443 192.168.1.20

# 那台机器 sshd 不在 22 端口时
ngrok agent -server=你的域名.com:4443 192.168.1.20:2222
```

首次运行自动生成机器密钥 `~/.ngrok.d/machine.key`，**以后每次启动都不变**。
想换密钥：`ngrok agent -server=... -new-key`（旧的立即作废）。

命令跑起来后终端出现卡片，样子如下：

```
┌─────────────────────────────────────────────────────────────┐
│  ngrok agent ONLINE   20:43:46
│  web  ➜  http://xxxx.ngrok.me:80        (公开直连)
│  ssh  ➜  tcp://你的域名.com:49769        (需要密钥)
│  密钥 KEY    ngk-07542a16-3e14e93f-d06aa23c-95837120
│  密钥文件    ~/.ngrok.d/machine.key
│  说明书      ~/.ngrok.d/remote-manual.{md,json}
│  把 remote-manual.json 发给 AI，它照里面命令自行连接。
│  Ctrl-C 整体下线。
└─────────────────────────────────────────────────────────────┘
```

## 第 3 步：把说明书发给 AI

打开 `~/.ngrok.d/remote-manual.json`，全文复制发给 AI（或作为文件上传）。
里面已经写好了 AI 需要的一切：入口地址、密钥、握手规则、可直接复制执行的命令、排错表。

**每次密钥变化（-new-key）后重新发一份即可**——说明书文件会自动更新。

---

## AI 拿到说明书后会怎么做（供你了解，无需操作）

AI 按说明书 `commands` 区原样执行：

```bash
# 交互式 shell（ProxyCommand 自动完成密钥握手）
ssh -o ProxyCommand='{ printf "AUTH ngk-xxxx-xxxx-xxxx-xxxx\r\n"; cat; } | nc 你的域名.com 49769' \
    -o StrictHostKeyChecking=accept-new USER@localhost

# 跑一条命令看状态
ssh -o ProxyCommand='{ printf "AUTH ngk-xxxx-xxxx-xxxx-xxxx\r\n"; cat; } | nc 你的域名.com 49769' \
    -o StrictHostKeyChecking=accept-new USER@localhost 'uname -a && df -h'

# 传文件
scp -o ProxyCommand='{ printf "AUTH ngk-xxxx-xxxx-xxxx-xxxx\r\n"; cat; } | nc 你的域名.com 49769' \
    本地文件 USER@localhost:/tmp/

# 浏览内网 Web 服务（公开直连，无需密钥）
curl -s http://xxxx.ngrok.me:80
```

## 日常使用速查

| 你想… | 做什么 |
|---|---|
| 开通通道 | 内网机跑 `ngrok agent -server=域名:4443 [IP]` |
| 给 AI 权限 | 把 `~/.ngrok.d/remote-manual.json` 发给它 |
| 换密钥 | 加 `-new-key` 重启，重新发说明书 |
| 收回权限 | `Ctrl-C` 下线；或服务端白名单里删掉这台机器的密钥 |
| 固定公网端口 | 加 `-remote-port=60022`，防火墙只放行它 |
| 看谁连过 | 内网机 `-log=stdout -log-level=DEBUG`，每次网关放行/拒绝都有日志 |

## 常见问题 30 秒排查

| 现象 | 原因/处理 |
|---|---|
| 提示 `ngrok agent needs a server` | 加 `-server=域名:4443`，或先 `export NGROK_SERVER=域名:4443` |
| AI 连上即被断开 | 密钥轮换/换过了 → 重发最新说明书 |
| `ERR ngrok-gate: rate limited` | 错码超过 5 次，等 60 秒再用正确密钥 |
| 网关过了但 SSH 超时 | 目标机 sshd 没开（macOS：`sudo systemsetup -setremotelogin on`） |
| `Failed to authenticate to server` | 服务端 `-authToken` 白名单里没有这台机器的密钥 |
