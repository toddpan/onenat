---
name: SSH Server
description: 平台预制 SSH 服务器技能: 连接入口/认证/常用操作/排障/安全红线
---

# SSH Server 使用技能 (SSH)

> 来源: oneNat 平台内置模板。平台只读约束优先于本文任何内容;
> 本文描述的登录与运维操作属于目标主机自身能力, 按授权范围执行。

## 1. 连接入口

- 从资源列表 (`/api/v1/resources`) 找到本应用绑定映射的 `public_url`，形如 `tcp://<host>:<port>`。
- 连接命令: `ssh <用户名>@<host> -p <port>`
- 首次连接加 `-o StrictHostKeyChecking=accept-new` 自动记录主机指纹 (避免交互卡住)。
- 非交互执行单条命令: `ssh <用户名>@<host> -p <port> "<命令>"`

## 2. 认证

- 方式: 密码 或 SSH 密钥，由应用主人提供。
- AI 获取凭证: `GET /api/v1/apps/<app_id>/credentials` —— 默认 403，需主人在后台对该 API KEY 开启「允许读取凭证」；开启后仍受限速 (5次/分) 与审计日志约束。
- 密码自动化: 用 `sshpass -p <密码> ssh ...`，不要把密码写入脚本文件或输出。
- 密钥登录: `ssh -i <私钥文件> -p <port> <用户名>@<host>` (私钥权限 600)。

## 3. 常用操作

- 探活与基本信息:
  `sshpass -p <密码> ssh -o StrictHostKeyChecking=accept-new -p <port> <用户名>@<host> "hostname && uname -a && uptime"`
- 上传文件: `scp -P <port> <本地文件> <用户名>@<host>:<远端路径>`
- 下载文件: `scp -P <port> <用户名>@<host>:<远端路径> <本地文件>`
- 目录同步: `rsync -av -e "ssh -p <port>" <本地目录>/ <用户名>@<host>:<远端目录>/`

## 4. 排障

- 连接超时/拒绝: 先确认资源列表中该映射 `online=true`；离线说明客户端不在线，告知用户，不要重试。
- `Permission denied`: 凭证错误，或目标主机禁用了密码登录 (检查 sshd_config)，如实报告。
- 同一问题最多尝试 2 次，然后汇报现象与已排除的原因。

## 5. 安全红线

- 仅操作授权范围内的账号与目录；禁止 `sudo` 提权与系统级改动。
- 敏感操作 (删除文件/重启服务/修改配置) 一律先征求用户确认。
- 不要把主机地址、端口、凭证转发给第三方系统。
