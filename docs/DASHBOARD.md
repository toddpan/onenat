# ngrokd 管理后台（Dashboard）设计方案

> 状态：**已实现**并通过端到端验证（`bash test-dashboard-e2e.sh`，43 项断言全过）。
> 目标：给 ngrokd 服务端加一个 Web 管理页面，实现 **用户管理（管理员/普通用户）**、
> **隧道管理（增删改 + 端口映射 + 生成客户端配置/一键安装脚本）**、
> **远程修改在线客户端配置**。UI 交互参考 PassNAT 截图，保持简单易用，无前端框架。

## 快速使用

```bash
# 1. 编译并准备客户端分发产物
go build -tags debug -o bin/ngrokd ngrok/main/ngrokd
make release-clients                     # 产物输出到 ./dl/ (linux/darwin/windows)

# 2. 启动服务端(带管理后台, 默认 :18080)
bash start-ngrokd.sh                     # 或手动:
# ./bin/ngrokd -domain your.domain -webAddr=:18080 -webData=./ngrokd-dashboard.json \
#             -dlDir=./dl -tunnelAddr=:4443 -httpAddr=:80

# 3. 首次启动会在 stdout/日志打印初始 admin 密码, 浏览器打开 http://server:18080 登录
# 4. Web 端: 创建用户 → 创建隧道 → 添加端口映射 → 详情页复制一键安装命令
# 5. 内网机执行一键安装命令, 客户端自动安装并上线; 之后在 Web 端随时改配置实时生效
```

新增命令行参数：`-webAddr`(默认 `:18080`，空串禁用)、`-webData`、`-webAdminPass`、`-dlDir`。
客户端新命令：`ngrok managed -config=ngrok-managed.yml`（由安装脚本自动生成并注册服务）。

---

## 1. 总体架构

```
 浏览器(管理员/普通用户)
    │  HTTP(独立监听口 -webAddr=:18080, Session Cookie)
    ▼
┌─────────────────────────── ngrokd ────────────────────────────┐
│  dashboard 包                                                  │
│   ├─ Web UI(嵌入静态资源 go:embed) + REST API                   │
│   ├─ Store(用户/隧道/端口映射, JSON 文件持久化)                   │
│   └─ Runtime: 把变更推送给在线客户端 / 标记在线状态               │
│  control/tunnel/registry(现有)                                  │
│   └─ msg 协议扩展: ConfigSync / AckConfig                       │
└───────────────┬───────────────────────────────────────────────┘
                │ TLS 长连接(现有 :4443 隧道口)
                ▼
        ngrok 客户端(新增 managed 模式)
         ├─ 用隧道 KEY 认证
         ├─ 接收 ConfigSync, 按服务端下发的期望配置开/关隧道
         └─ 反代连接(proxy)不变
```

要点：

- 管理后台使用**独立监听端口**（`-webAddr`，默认 `:18080`），与公网 http/https 隧道口、
  客户端隧道口完全隔离，互不影响。
- 服务端是配置的**唯一事实源**：Web 端改了端口映射 → 存库 → 推送给在线客户端 →
  客户端重建对应隧道；客户端离线时改动只落库，客户端下次上线自动应用。
- 复用现有零配置 agent 模式的密钥形态（`ngk-xxxx-xxxx-xxxx-xxxx`），隧道 KEY 即客户端
  认证凭据，一个 KEY 三用：服务端准入、安装脚本取配置、配置文件里的唯一机密。

---

## 2. 概念与数据模型

| 概念 | 说明 |
|---|---|
| **用户 User** | `admin`(管理员) / `user`(普通用户) 两种角色。管理员可管理一切；普通用户只读自己的隧道 + 取安装脚本 |
| **隧道 Tunnel**（后台实体） | 对应截图里的一条"隧道"= 一台受管客户端。含名称、KEY、归属用户、备注、锁定状态、创建时间、在线状态 |
| **端口映射 Mapping** | 隧道下的一条转发规则：协议(tcp/http/https) + 本地 IP:Port → 公网端口(remote_port, 0=自动分配) + 备注。对应 ngrok 运行时的一条 Tunnel |
| **节点** | v1 只有单节点（本 ngrokd），数据模型预留 `node` 字段，UI 显示服务器域名，"更改节点"功能暂不提供 |

存储：**JSON 文件**（`-webData`，默认 `./ngrokd-dashboard.json`），内存加锁 + 变更时
原子写（临时文件 + rename）。不引入数据库，保持零依赖。

```go
type User struct {
    ID           string
    Username     string
    PassHash     string  // PBKDF2-HMAC-SHA256, 10000 轮, 每用户随机盐
    Role         string  // "admin" | "user"
    CreatedAt    time.Time
}

type Tunnel struct {
    ID         string    // 10 位随机串, 如 "Xw2dophvFo"
    Key        string    // "ngk-" 形态随机密钥, 客户端认证凭据
    Name       string
    Note       string
    OwnerID    string    // 归属用户
    Locked     bool      // 锁定: 不允许客户端连接(截图的"锁定"筛选)
    Node       string    // 预留, 当前=服务器域名
    CreatedAt  time.Time
    Mappings   []*Mapping
}

type Mapping struct {
    ID         string
    Proto      string  // tcp | http | https
    LocalIP    string  // 默认 127.0.0.1
    LocalPort  int
    RemotePort int     // tcp 专用; 0=自动分配
    Subdomain  string  // http/https 专用
    Note       string
}
```

运行时状态（不入库，重启清零）：

```go
type RuntimeStatus struct {
    Online     bool      // controlRegistry 里存在 "tun-<id>"
    LastSeen   time.Time
    ClientVer  string    // 客户端上报的版本
    PublicURLs map[string]string   // mappingID -> 公网地址(tcp://x.x.x.x:port / http://sub.domain)
    MapErrors  map[string]string   // mappingID -> 最近一次开通错误(如端口被占)
    Conns      []ConnRecord        // 最近 N 条连接记录(环形, 内存)
    Traffic    map[string]int64    // 按天统计的字节数(近 7 天, 内存)
}
```

首次启动：库里无用户时自动创建 `admin`，随机密码**打印到日志/stdout** 一次
（可用 `-webAdminPass` 指定初始密码）。

---

## 3. 认证与权限

### 3.1 Web 端（人）

- 登录：`POST /api/login`，校验 PBKDF2 哈希；签发 Session Cookie
  （HttpOnly + SameSite=Lax，HMAC 签名，密钥持久化在数据文件里，重启不掉线）。
- 权限矩阵：

| 能力 | 管理员 | 普通用户 |
|---|---|---|
| 登录/改自己密码 | ✅ | ✅ |
| 隧道列表 | 全部 | 仅自己名下 |
| 创建/编辑/删除隧道、端口映射 | ✅ | ❌ |
| 查看配置文件 / 一键安装命令 | ✅ | ✅（自己名下） |
| 修复隧道 / 重置密钥 / 锁定 | ✅ | ❌ |
| 用户管理 | ✅ | ❌ |

- CSRF：变更一律 POST/JSON + SameSite=Lax；不做 token 表单（内网工具，够用）。

### 3.2 客户端（机器）

现有 `Control.NewControl` 的 token 校验扩展为两级：

1. 先匹配 `-authToken` 静态列表（兼容老客户端，行为不变）；
2. 不中则查 dashboard 库：`User == 某隧道.Key` 且隧道未锁定 → 通过，
   且**强制覆盖 ClientId 为 `tun-<隧道ID>`**。

固定 ClientId 带来两个现成收益：

- 客户端断线重连走现有 `controlRegistry.Add` 的**会话替换**逻辑，平滑接替；
- TCP 端口亲和缓存（`REGISTRY_CACHE_FILE`）按 client-id 命中，重连后**自动归还同一公网端口**。

---

## 4. 协议扩展与客户端 managed 模式

### 4.1 新增消息（`src/ngrok/msg/msg.go`）

```go
// 服务端 → 客户端：期望配置全集(版本号单调递增)
type ConfigSync struct {
    Version int64
    Tunnels []DesiredTunnel
}
type DesiredTunnel struct {
    Name       string  // = mapping.ID
    Protocol   string  // tcp | http | https
    LocalAddr  string  // "127.0.0.1:22"
    RemotePort uint16  // tcp
    Subdomain  string  // http(s)
    Hostname   string
    HttpAuth   string
}

// 客户端 → 服务端：对某版本配置的执行结果回执
type AckConfig struct {
    Version int64
    Tunnels []AckTunnel
}
type AckTunnel struct {
    Name    string
    URL     string // 开通成功的公网地址
    Error   string
}
```

### 4.2 客户端 managed 模式（`ngrok managed -config=ngrok-managed.yml`）

- 配置文件由安装脚本生成，内容极简（只有 `server_addr`、`auth_token=KEY`、
  `tunnel_id` 元数据）。**隧道列表不写在本地**——服务端上线后通过 ConfigSync 下发，
  这样 Web 端改配置不需要碰客户端文件。
- 连接建立、TLS、心跳、proxy 反代全部复用现有 `ClientModel`，仅控制循环里新增：

```
收到 ConfigSync(v):
    diff(期望配置, 当前已开隧道):
        新增/变更 → 逐条发 ReqTunnel(现有消息)
        删除      → 本地 tunnels map 移除即可(服务端已自行关闭)
    回 AckConfig(v, 每条的成功/失败)
```

**删除的语义放在服务端**：Web 端删映射 → dashboard 找到该隧道的 Control →
调用新增的 `Control.RemoveTunnel(url)`（Shutdown 该 Tunnel：关 listener、摘注册表）
→ 再推 ConfigSync 让客户端同步本地视图。这样协议上不需要新增"关隧道"消息，
旧客户端也只会忽略未知消息，兼容性好。

- 非.managed 模式（经典 `ngrok -proto=tcp`、`ngrok agent`）行为完全不变，
  收到 ConfigSync 走现有 `default: ignore` 分支。

### 4.3 在线修改端口映射的完整时序

```
Web(管理员)         dashboard            Control(在线客户端)
   │ PATCH remote_port  │                     │
   │───────────────────►│ 存库                 │
   │                    │ RemoveTunnel(旧url)  │  ← 关旧监听, 立即释放端口
   │                    │─────────────────────►│
   │                    │ ConfigSync(v+1)      │
   │                    │─────────────────────►│
   │                    │                      │ ReqTunnel(new port)
   │                    │◄─────────────────────│
   │                    │ NewTunnel(url)       │
   │                    │◄─────────────────────│
   │                    │ AckConfig(v+1, ok)   │
   │ 200 OK(含新公网地址) │◄────────────────────│
   │◄───────────────────│                      │
```

客户端离线时：PATCH 直接落库返回成功；客户端下次 Auth 上线 → 服务端先推
ConfigSync → 自动应用。UI 上显示"离线（配置将在上线后生效）"。

### 4.4 修复隧道（截图的"修复隧道"）

服务端把该隧道的所有 Tunnel 全部 Shutdown + 推一次 ConfigSync，客户端全量重建。
用于端口被占、本地服务迁移等"卡住"场景。

---

## 5. 管理后台（Web UI + API）

技术：Go `net/http` + `html/template` + 原生 JS（无框架、无构建步骤），
静态资源 `go:embed` 内嵌进 ngrokd 二进制。风格对齐截图：左侧图标栏 + 白底卡片 + 状态徽标。

### 5.1 页面

| 路由 | 页面 | 对应截图 |
|---|---|---|
| `/login` | 登录 | - |
| `/` | 隧道列表：搜索框、"全部/在线/离线/锁定"筛选、状态圆点、协议徽标、节点、创建时间、详情箭头；管理员右上角"+ 创建新隧道" | 截图 1 |
| `/t/{id}` | 隧道详情：名称(可改)、状态、节点、**配置文件信息（点我获取 → 弹窗：配置文本 + 下载 + 复制 + 一键安装命令）**；右侧端口映射表（本地服务/公网连接/备注/状态）+ 添加端口；底部最近连接记录 | 截图 2/3/4 |
| `/t/{id}` 内"编辑隧道"下拉 | 添加端口 / 编辑端口 / 修复隧道 / 删除隧道（更改节点暂缺，单节点） | 截图 3 |
| `/users` | 用户管理（仅管理员）：列表、新建用户(角色)、重置密码、删除 | - |

### 5.2 API

```
POST   /api/login                     登录
POST   /api/logout                    登出
GET    /api/me                        当前用户

GET    /api/tunnels?q=&status=        列表(含在线状态/公网地址)
POST   /api/tunnels                   创建(名称/备注/归属用户) → 生成 ID+KEY
GET    /api/tunnels/{id}              详情(含 mappings + runtime)
PATCH  /api/tunnels/{id}              改名称/备注/锁定/归属
DELETE /api/tunnels/{id}              删除(连带所有映射, 下线客户端)
POST   /api/tunnels/{id}/reset-key    重置 KEY(旧 KEY 立即失效)
POST   /api/tunnels/{id}/repair       修复隧道(全量重建)

POST   /api/tunnels/{id}/mappings     添加映射
PATCH  /api/mappings/{mid}            修改映射(在线客户端实时生效)
DELETE /api/mappings/{mid}            删除映射

GET    /api/users                     用户列表(admin)
POST   /api/users                     新建用户(admin)
PATCH  /api/users/{id}                改角色/重置密码(admin)
DELETE /api/users/{id}                删除用户(admin)
```

客户端/安装器专用（无 Session，ID+KEY 鉴权，常数时间比较 + 简单限速）：

```
GET /api/deploy?id=<tunnelId>&key=<KEY>   返回该隧道的客户端配置文件文本(YAML)
GET /install.sh                            一键安装脚本(SERVER 地址已烘焙)
GET /dl/ngrok_<os>_<arch>                  客户端二进制分发
```

`/api/deploy` 返回的配置文件内容（managed 模式实际读取的文件）：

```yaml
# ngrok managed client config, generated by dashboard. DO NOT EDIT.
server_addr: your-domain.com:4443
trust_host_root_certs: true
auth_token: ngk-07542a16-3e14e93f-d06aa23c-95837120
inspect_addr: disabled
# metadata (managed 模式识别用)
# tunnel_id: Xw2dophvFo
```

> 注意：**隧道列表和端口映射不写进这个文件**（服务端下发），Web 端改配置对客户端
> 透明，密钥不变就不用重装。

### 5.3 一键安装脚本（核心交付物）

隧道详情页展示一行命令（含隧道 ID 与 KEY）：

```bash
# Linux / macOS
curl -sSL http://your-server:18080/install.sh | bash -s -- Xw2dophvFo ngk-0754-...-9583
```

```powershell
# Windows (由后台 /install.ps1 提供同源 PowerShell 安装器)
powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm 'http://your-server:18080/install.ps1'))) -TunnelId 'Xw2dophvFo' -Key 'ngk-0754-...'"
```

脚本职责（POSIX sh，兼容 bash）：

1. 识别 `uname -s/-m` → 选二进制 `ngrok_linux_amd64 / linux_arm64 / darwin_arm64 / darwin_amd64…`；
2. 从 `/dl/` 下载客户端到 `/usr/local/bin/ngrok`（已存在则覆盖）；
3. `curl /api/deploy?id=&key=` 拉配置 → 写 `/etc/ngrok/ngrok-managed.yml`（0600）；
4. 注册并启动常驻服务：
   - Linux 有 systemd → `ngrok-client.service`（`Restart=always`）；
   - macOS → launchd plist（`KeepAlive=true`）；
   - 其他 → nohup 兜底并提示；
5. 打印结果卡片（在线状态 + 公网地址查询命令）。

KEY 会出现在目标机 shell 历史里——文档注明可用环境变量
`TUNNEL_ID/KEY curl ... | bash` 的替代形态。

二进制分发：ngrokd 从 `-dlDir`（默认 `./dl`）目录按文件名直接 serve；
Makefile 增加 `make release-clients` 交叉编译产物列表，管理员部署时拷进去即可。

---

## 6. 代码改动清单

| 位置 | 改动 |
|---|---|
| `src/ngrok/server/dashboard/`（新） | `store.go`(数据+持久化) `auth.go`(登录/Session/密码哈希) `api.go`(REST) `web.go`(页面路由+模板) `runtime.go`(在线状态/推送/流量) `assets/`(模板+静态资源, go:embed) |
| `src/ngrok/server/cli.go` | 新参数：`-webAddr`(默认 :18080，空串禁用)、`-webData`、`-webAdminPass`、`-dlDir` |
| `src/ngrok/server/main.go` | 启动 dashboard（store 加载、路由注册、监听） |
| `src/ngrok/server/control.go` | token 校验扩展（查隧道 KEY + 强制 ClientId=`tun-<id>`）；新增 `RemoveTunnel(url)`、`RebuildTunnels()`；manager 循环处理 `AckConfig` |
| `src/ngrok/server/tunnel.go` | Tunnel 上挂 mappingID；HandlePublicConnection 里累计流量/最近连接记录 |
| `src/ngrok/msg/msg.go` | 注册 `ConfigSync` / `AckConfig` |
| `src/ngrok/client/config.go` | 新命令 `managed`；配置解析复用现有 YAML |
| `src/ngrok/client/model.go` | 控制循环处理 ConfigSync：diff → ReqTunnel / 删 map → AckConfig；managed 模式不读本地 tunnels |
| `src/ngrok/client/cli.go` | `managed` 命令行入口 |
| `Makefile` / 脚本 | `release-clients` 交叉编译；`install.sh` 模板 |
| `docs/` | 新增 `docs/DASHBOARD.md`（本文件）+ USAGE 补章节 |

---

## 7. 新增命令行参数（ngrokd）

| 参数 | 默认 | 说明 |
|---|---|---|
| `-webAddr` | `:18080` | 管理后台监听地址，空串=禁用 |
| `-webData` | `./ngrokd-dashboard.json` | 用户/隧道数据文件 |
| `-webAdminPass` | 随机 | 首次初始化 admin 的密码（仅库为空时生效） |
| `-dlDir` | `./dl` | 客户端二进制分发目录 |

现有参数全部不变；`-authToken` 语义不变（ managed 客户端走隧道 KEY，两条路并存）。

---

## 8. 里程碑（已全部完成）

- **M1 后台骨架**：store + 登录/Session + 用户管理 + 隧道/映射 CRUD（纯库操作）+ 列表/详情/用户页 UI。✅
- **M2 受管客户端**：managed 模式 + ConfigSync/AckConfig + 服务端 KEY 校验/ClientId 绑定 + 在线增删改映射实时生效 + 在线状态展示。✅
- **M3 部署链路**：`/api/deploy`、`install.sh`、`/dl` 分发、配置弹窗 + 一键安装命令 UI、修复隧道/重置密钥。✅
- **M4 可观测**：最近连接记录、近 7 天流量条形图（内存统计，重启清零）。✅

每步都保持 `go build` + `go test ngrok/...` 通过；现有 agent/经典模式零回归。

## 8.1 与设计稿的实现差异（as-built）

- 路由采用**手写分发器**而非 `ServeMux` 方法模式：GOPATH 构建（无 go.mod）下
  net/http 运行在 1.21 兼容模式，`"GET /x/{id}"` 这类模式静默失效。
- 普通用户不可创建隧道（仅管理员），与权限矩阵一致。
- `Control.Online()` 以 `controlRegistry.Get(c.id) == c` 判定；声称 `tun-<id>`
  槽位的连接必须持有该隧道**当前** KEY，防止重置密钥后旧客户端复活槽位。
- TCP 端口亲和缓存按 client-id+协议共享：同一隧道下多条 tcp 映射/修复重建后
  端口可能变为最近一次缓存的端口，需要固定端口就在映射里显式填 `remote_port`。

---

## 9. 测试与验收

- 单测：store 读写/并发、密码哈希、KEY 生成与常数时间比较、ConfigSync diff 逻辑。
- 集成（扩展 `run-local-test.sh`）：
  1. 起 ngrokd（`-webAddr=:18080`）→ curl 登录 → 建用户/建隧道/加映射；
  2. `curl /api/deploy` 拿配置 → 本机起 `ngrok managed` → 列表变"在线"、拿到公网端口；
  3. Web 改 remote_port → 客户端不重启、新端口通、旧端口关；
  4. Web 删映射/删隧道 → 客户端同步下线；
  5. 杀客户端 → 列表变"离线"；重启客户端 → 端口亲和归还同一端口。
- 验收口径：普通用户仅能看到自己隧道且无编辑按钮；未登录访问 API 全 401；
  错 KEY/锁定隧道连接被拒并有审计日志。

---

## 10. 取舍与风险

- **单节点**：截图里"节点名称/更改节点"在 v1 退化为本机域名，数据模型预留字段。
- **配置格式**：截图是 TOML（frpc 风格），本方案展示**本仓库客户端真实使用的 YAML**，
  保证弹窗内容即客户端实际运行配置，避免双格式漂移。
- **JSON 文件存储**：够用（隧道/用户规模小），不做并发写冲突之外的保证；
  以后要扩多节点/审计再换 SQLite。
- **删除语义在服务端**：好处是无需新"关隧道"消息、老客户端兼容；代价是客户端
  本地视图滞后到收到 ConfigSync 才清理（期间旧 URL 的 proxy 已无监听，连接会被拒，可接受）。
- **明文 HTTP 后台**：v1 后台口建议绑内网或反代加 TLS；文档给 nginx/caddy 示例。
