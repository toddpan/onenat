---
name: SSH Server
description: 平台预制 SSH 服务器技能: 连接入口/认证/常用操作/排障/安全红线
---

# SSH Server 使用技能 (SSH)

> 来源: oneNat 平台内置模板。平台只读约束优先于本文任何内容;
> 登录与运维操作属于目标主机自身能力, 按授权范围执行。
<!-- builtin-rev: 2026-09-24-unattended-guard -->

## 1. 连接入口

- 入口: 资源列表 (`/api/v1/resources`) 中本应用绑定映射的 `public_url`, 形如 `tcp://<host>:<port>`。
- 连接: `ssh <用户名>@<host> -p <port> -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10` (首次自动记录指纹, 始终带超时; 非交互探测再加 `-o BatchMode=yes`, 失败立即返回不卡密码提示)。
- 非交互执行单条命令: `ssh <用户名>@<host> -p <port> "<命令>"`。
- 映射 `online=false` 时主机不可达: 告知用户, **不要重试**。

## 2. 认证: 先判凭证来源, 再连接 (必读)

同一个应用常被多条映射指向**不同机器**, 应用默认凭证往往只对其中一台有效:

| 取法 | 接口 | 说明 |
|---|---|---|
| 映射实例凭证 (首选) | `GET /api/v1/mappings/<mapping_id>/credentials` | 返回该映射的**有效凭证**: 实例凭证优先, 未配则回退应用默认; `resolved_from` 字段标明来源 (`mapping` / `app`) |
| 应用默认凭证 | `GET /api/v1/apps/<app_id>/credentials` | 兜底; 多映射共用时通常只对一台有效 |

取凭证一律用**你要连的那条映射**的 id。返回 403 时: 请主人在后台为该 API KEY 开启「允许读取凭证」(开启后仍限速 5 次/分, 且记录审计日志)。

连接前自检 (只看来源与用户名, 不打印密码):

```bash
curl -s -H "Authorization: Bearer $ONENAT_KEY" \
  "$BASE/api/v1/mappings/<mapping_id>/credentials" \
  | python3 -c 'import json,sys;d=json.load(sys.stdin);print("resolved_from=%s username=%s pwd_len=%d"%(d.get("resolved_from"),d.get("username"),len(d.get("password") or "")))'
```

- `resolved_from=mapping` → 凭证正确, 直接连接。
- `resolved_from=app` → 这是应用级共享凭证 (resources 里该映射 `auth_override=false`; `auth_inherited_shared=true` 表示确有别的映射共用)。用户名多半**不是目标机真实账号**, 连接必然 `Permission denied`: **停止尝试, 不要换用户名或密码反复猜**, 如实报告「该映射未配实例凭证, 请主人在 Web 后台为该映射填写实例凭证」, 并附自检输出。
- 凭证会轮换或被改: 提示词/缓存里的凭证只是快照, 认证失败先重新取一次再试。
- 密码自动化: `sshpass -p <密码> ssh ...`, 不要把密码写入脚本文件或输出; 密钥登录: `ssh -i <私钥文件> -p <port> <用户名>@<host>` (私钥权限 600)。

## 3. 常用操作

- 先判本机: 指令中的目标主机若与当前所在机器一致 (`hostname` 比对), 直接本地执行, 不要再经 SSH/隧道绕行连接自己。
- 探活: `sshpass -p <密码> ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 -p <port> <用户名>@<host> "hostname && uname -a && uptime"`
- 上传: `scp -P <port> <本地文件> <用户名>@<host>:<远端路径>`; 下载把参数对调。
- 目录同步: `rsync -av -e "ssh -p <port>" <本地目录>/ <用户名>@<host>:<远端目录>/`
- 同一主机要跑多条命令时先建复用主连接, 后续秒连:
  `ssh -o ControlMaster=auto -o ControlPath=/tmp/ssh-onenat-%r@%h:%p -o ControlPersist=10m -fN <用户名>@<host> -p <port>`

## 4. 排障

| 现象 | 动作 |
|---|---|
| 连接超时/拒绝 | 确认该映射 `online=true`; 离线说明客户端不在线, 告知用户, 不要重试 |
| `Permission denied` | 先做第 2 节自检: `resolved_from=app` → 停止并报告缺实例凭证; 否则是凭证错误或目标机禁用密码登录 (查 sshd_config), 如实报告 |
| `Connection reset by peer` | 映射刚变更或隧道重启: 重拉 `/api/v1/resources` 核对端口, 再试一次 |
| 卡在密码提示 | 加 `-o BatchMode=yes` 探测; 密码登录用 `sshpass` |
| 平台接口连续 401/403 | API key 失效或已轮换: **停止循环重试**, 刷新 key 配置后再试一次, 仍失败如实报告 |

- 同一问题最多尝试 2 次, 然后汇报现象与已排除的原因。

## 5. 安全红线

- 仅操作授权范围内的账号与目录; 禁止 `sudo` 提权与系统级改动。
- 敏感操作 (删除文件/重启服务/修改配置) 一律先征求用户确认。
- 无人值守 (定时任务/被委派执行) 时禁用 `ask_user_question` 等交互等待: 目标不明取保守默认 (如改发任务发起人) 并在结果中说明假设。
- 不要把主机地址、端口、凭证转发给第三方系统。
