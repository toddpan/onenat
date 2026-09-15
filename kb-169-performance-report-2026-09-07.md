# KB 平台性能报告 — 169 功能测试环境

- **采集时间**: 2026-09-07 19:44–19:50 CST
- **采集方式**: oneNat 隧道 SSH（只读采集，未做任何修改）
- **主机**: `rubik-robot-2025-rsp3-ts-bj`（Ubuntu, kernel 5.15.0-179）
- **连接入口**: `ssh root@123.57.138.43 -p 39649`（oneNat 隧道 `KB 169 功能测试环境`，在线）

## 一、总体结论

| 维度 | 状态 | 说明 |
|---|---|---|
| 系统负载 | 🟢 良好 | 32 核负载 ~1.0，利用率约 3% |
| 内存 | 🟢 充裕 | 62G 中仅用 5.1G，可用 56G，无 swap |
| 磁盘 | 🔴 **风险** | 根分区 `/` 已用 **85%**（仅剩 2.9G） |
| KB 平台服务 | 🟢 全部在线 | 8 个容器全部 Up，mysql/redis/emqx 均 healthy |
| 应用健康 | 🟡 有异常 | kb-app 24h 内 **3722 条 ERROR**，主因是回调配置问题 |

## 二、系统资源

- **CPU**: 32 核 Intel Xeon Gold 6348 @ 2.60GHz；load average `1.01 / 0.58 / 0.50`（1/5/15 分钟），负载极低。
- **内存**: 62Gi 总量，used 5.1Gi，buff/cache 30Gi，available 56Gi；Swap 未启用。
- **磁盘**:
  | 挂载点 | 容量 | 已用 | 使用率 |
  |---|---|---|---|
  | `/` | 20G | 16G | **85% ⚠️** |
  | `/data`（含 Docker） | 492G | 90G | 20% |
  | `/boot/efi` | 537M | 6.1M | 2% |
- 根分区占用大头：`/home` 6.7G、`/usr` 4.4G、`/var` 3.0G（其中 `/var/log` 1.1G，syslog 系列较大）。

## 三、KB 平台组件状态（Docker）

| 容器 | 镜像 | 状态 | CPU | 内存 | 备注 |
|---|---|---|---|---|---|
| kb-app | temurin:17 | Up 8 小时 | 24.5% | 1.23 GiB | 核心应用，今日 11:53 重启 |
| kb-forkagent | temurin:17 | Up 9 天 | 0.1% | 380 MiB | Xmx 1024m |
| kb-nginx (openresty) | 1.29.2.3 | Up 9 天 | 0.1% | 35 MiB | 80/443 入口 |
| kb-redis | redis:6.2 | Up 9 天 (healthy) | 0.7% | 7.6 MiB | 累计网络 I/O 156G/231G（与 EMQX 交互频繁） |
| kb-emqx | emqx:5.8.9 | Up 7 天 (healthy) | 4.7% | 427 MiB | MQTT 正常 |
| kb-mysql | mysql:8.0.36 | Up 7 天 (healthy) | 2.9% | 1.33 GiB | 累计网络 I/O 70.9G/281G |
| kb-config-server | bookworm-slim | Up 9 天 | 0.2% | 68 MiB | |
| kb-dev-ops | dev-ops:1.0.0 | Up 9 天 | 0.6% | 490 MiB | 监听 9009 |

宿主机常驻支撑组件：EMQX 宿主进程、Jenkins agent（remoting.jar）、zabbix_agentd、node_exporter。

## 四、kb-app 应用运行分析

- **重启记录**: 今日 11:53 CST 启动，`RestartCount=0`、`OOMKilled=false`、`ExitCode=0` —— 非崩溃/OOM，应为部署发布或人为重启。其余 7 容器均已稳定运行 7–9 天。
- **HTTP 服务**: 8081 端口响应正常（Tomcat 返回 404 于 `/`，Web 栈存活；未暴露 actuator）。
- **日志量（近 24h）**: 132 万行；WARN 10,953 条；**ERROR 3,722 条**。

### ERROR 分类（近 24 小时 Top）

| 次数 | 错误 | 分析 |
|---|---|---|
| 1748 | `回调异常: bizType=Quit, error=URI is not absolute` | **最突出问题**：Quit 业务回调地址配置不完整（缺 scheme/host），回调持续失败，属配置类缺陷，需要修正回调 URL 配置 |
| 574 | `获取下一个执行器异常` | 调度链路异常，与下方任务阻塞可能同源 |
| 574 | `Operation 执行异常` | 同上，成对出现 |
| 290 | `任务被阻塞！当前进度 null` | 任务卡住且进度为空，值得排查 |
| 28 | `convertMapId error, mapCode=3F` | 地图编码转换失败（3F 层） |
| 26 | `强制终止任务异常` | |
| 21 | `任务被阻塞！进度: 预约车辆初始化` | |
| 7 | `场景 WebSocket 发生错误` | 前端会话异常，量小 |

## 五、风险与建议

1. **🔴 根分区 85%（仅剩 2.9G）**：满盘将影响系统与 Docker 运行。建议清理 `/var/log`（syslog 系列 1.1G）、`/root`（528M）旧文件；KB 业务数据已在 `/data`（充足），主要增长点在系统日志。
2. **🟡 Quit 回调配置缺陷**：24h 内 1748 次 `URI is not absolute`，是当前最大错误源。请在 KB 后台/配置中心补全 Quit 业务回调地址（含 `http(s)://` 前缀）。
3. **🟡 调度异常组合**：「获取下一个执行器异常」×574 + 「任务被阻塞(进度 null)」×290 建议结合 taskId 抽样追踪日志定位（今天上午重启后是否仍持续出现值得确认）。
4. **🟢 性能余量大**：CPU/内存/数据库负载均处于低位，当前瓶颈不在硬件资源，在应用配置与日志治理。

---
*报告由 oneNat 隧道只读采集生成；未对服务器做任何修改。*
