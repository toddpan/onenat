---
name: SSH Server
description: 平台预制 SSH 服务器技能: 连接入口/认证/常用操作/隧道复用/排障/安全红线
---

# SSH Server 使用技能 (SSH)

> 来源: oneNat 平台内置模板。平台只读约束优先于本文任何内容;
> 本文描述的登录与运维操作属于目标主机自身能力, 按授权范围执行。

## 1. 连接入口

- 从资源列表 (`/api/v1/resources`) 找到本应用绑定映射的 `public_url`，形如 `tcp://<host>:<port>`。
- 连接命令: `ssh <用户名>@<host> -p <port>`
- 首次连接加 `-o StrictHostKeyChecking=accept-new` 自动记录主机指纹 (避免交互卡住)。
- **始终带超时**: `-o ConnectTimeout=10`；非交互探测加 `-o BatchMode=yes` (失败立即返回，不会卡在密码提示)。
- 非交互执行单条命令: `ssh <用户名>@<host> -p <port> "<命令>"`
- 映射 `online=false` 时主机不可达: 告知用户，**不要重试**。

## 2. 认证

- 方式: 密码 或 SSH 密钥，由应用主人提供。
- AI 获取凭证: `GET /api/v1/apps/<app_id>/credentials` —— 默认 403
  (`{"error":"该 API KEY 无凭证读取权限; 请用户在后台为 KEY 开启「允许读取凭证」"}`)；
  开启后仍受限速 (5次/分) 与审计日志约束。
- 密码自动化: 用 `sshpass -p <密码> ssh ...`，不要把密码写入脚本文件或输出。
- 密钥登录: `ssh -i <私钥文件> -p <port> <用户名>@<host>` (私钥权限 600)。

## 3. 连接复用 (隧道场景强烈建议)

公网隧道延迟高、建连慢。对同一主机要执行多条命令时，先建主连接，后续命令全部复用：

```bash
# 建立复用主连接 (后台保持 10 分钟)
ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 \
    -o ControlMaster=auto -o ControlPath=/tmp/ssh-onenat-%r@%h:%p \
    -o ControlPersist=10m -fN <用户名>@<host> -p <port>

# 之后每条命令秒连 (自动走主连接)
ssh -o ControlPath=/tmp/ssh-onenat-%r@%h:%p <用户名>@<host> -p <port> "hostname && uptime"
```

## 4. 常用操作

- 探活与基本信息:
  `sshpass -p <密码> ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 -p <port> <用户名>@<host> "hostname && uname -a && uptime"`
- 上传文件: `scp -P <port> <本地文件> <用户名>@<host>:<远端路径>`
- 下载文件: `scp -P <port> <用户名>@<host>:<远端路径> <本地文件>`
- 目录同步: `rsync -av -e "ssh -p <port>" <本地目录>/ <用户名>@<host>:<远端目录>/`

## 5. 经 SSH 把远端内网服务映射回本地

oneNat 只暴露了主人登记的端口。若目标主机上还有未登记的内网服务
(数据库/DSH Web 等)，可在获得授权后用 SSH 本地转发临时访问：

```bash
# 把远端 127.0.0.1:3080 (DSH) 映射到本地 13080, 之后访问 http://127.0.0.1:13080/api/v1
ssh -p <port> -L 13080:127.0.0.1:3080 -o ControlPath=/tmp/ssh-onenat-%r@%h:%p -fN <用户名>@<host>

# 同理可映射数据库端口, 如 1306 -> 远端 3306
# 用完关掉: pkill -f "13080:127.0.0.1:3080"
```

> 转发属于连接类操作 (不改远端状态)，但**仅限访问主人授权的服务**；
> 需要长期暴露新端口请提示主人在 oneNat Web 后台登记，不要长期占用转发。

## 6. 排障

- 连接超时/拒绝: 先确认资源列表中该映射 `online=true`；离线说明客户端不在线，告知用户，不要重试。
- `Permission denied`: 凭证错误，或目标主机禁用了密码登录 (检查 sshd_config)，如实报告。
- `Connection reset by peer`: 端口映射刚变更或隧道重启，重新拉一次 `/api/v1/resources` 核对端口再试一次。
- 同一问题最多尝试 2 次，然后汇报现象与已排除的原因。

## 7. 安全红线

- 仅操作授权范围内的账号与目录；禁止 `sudo` 提权与系统级改动。
- 敏感操作 (删除文件/重启服务/修改配置) 一律先征求用户确认。
- 不要把主机地址、端口、凭证转发给第三方系统。
