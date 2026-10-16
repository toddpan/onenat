# oneNat 凭证代理与 SKILL 去 KEY 化 — 设计文档

> 版本: v1.0 (设计稿) · 日期: 2026-09-21
> 状态: 待评审
> 关联代码: `src/ngrok/server/dashboard/`
> 前置文档: `docs/app-management-design.md` (本设计 = 其 Phase 3「凭证注入代理」的落地细化) · `docs/onenat-workbuddy-design.md` (兼容性影响)

---

## 1. 背景与目标

### 1.1 问题

应用的登录密钥目前通过 SKILL 链路交到 AI 手里，AI 侧存在多条明文泄露面：

| # | 泄露路径 | 代码位置 |
|---|---|---|
| L1 | `/keys` 页安装提示把明文 KEY 拼进 curl 命令与 URL | `web.go` `pageKeys` InstallPrompt |
| L2 | SKILL 文档本身烘入 `{{.Key}}`，AI 每次下载技能都拿到明文 KEY | `web.go` `skillTmpl` 认证节及全部示例 |
| L3 | `?key=` 查询参数鉴权，KEY 随 URL 落入服务器访问日志、浏览器历史、代理日志 | `api.go` `userFromApiKey`、`apps_v1.go` `skillContentURL` |
| L4 | 凭证接口把应用账号/密码/API KEY 原样 JSON 返回给 AI | `apps_v1.go` `apiV1AppCredentials` / `apiV1MappingCredentials` |
| L5 | SKILL 教 AI 用 `sshpass -p <密码>` 把密码放上命令行（ps / shell history 可见） | `skillTmpl` 第 4 节 |

「不要打印密钥」(skillTmpl 第 5 节) 只是提示词软约束——提示注入或 AI 自身失误都可以越过它。**只要明文进过 AI 上下文，就只能靠 AI 自觉。**

### 1.2 目标

1. **应用凭证零下发**（HTTP 类应用）：AI 经平台代理调用上游，认证头由服务端注入，应用凭证从不进入 AI 上下文——提示注入无密钥可偷；
2. **oneNat KEY 脱敏下发**：SKILL 文档、安装提示、资源接口全部不再携带明文 KEY，KEY 由用户放入本地环境变量，AI 只引用变量名；
3. **权限显式化**：新增 KEY 权限位「允许代理调用」，与现有「允许读取凭证」对称，默认关闭，用户按需开启；
4. **SSH/TCP 类应用缩小暴露窗口**（Phase 2）：凭证接口升级为「租约」语义（诚实边界：无法加密吊销，只能最小化重发窗口 + 审计）；
5. **兼容**：WorkBuddy 依赖的映射级凭证接口、存量 KEY、已安装的技能快照均不受破坏。

### 1.3 非目标（本期不做）

- 不做协议级代执行（AI 发指令、平台 SSH 进去执行——等于受限 RCE，风险换风险，列为远期候选）；
- 不做平台托管轮换凭证（需要目标系统配合，如临时账号/证书签发）；
- 不改变数据面：代理仍经由「服务端 → 公网入口 → 隧道 → 客户端 → 上游」既有路径转发。

### 1.4 术语与边界（先说清楚物理极限）

只要 AI 需要**直连**后端，协议上就必须有人出示凭证，区别只是谁来出示：

- **AI 出示**（现状）：凭证进 AI 上下文 → 泄露面即 AI 本身；
- **网关出示**（本设计）：凭证只存在于服务端内存→解密→注入这一个瞬间，AI 永远只见引用。HTTP 类可完全做到；SSH 等 TCP 协议 HTTP 代理注不进去，只能靠「租约」缩小窗口或「平台代执行」（远期）。

另注意：代理解决的是**泄露**，不解决**滥用**——上游应用的写操作，注入凭证后 AI 照样能执行。那是操作级授权问题（AppScopes 收窄 + 审计兜底 + KEY 页面明示风险），不在本设计范围。

---

## 2. 方案总览

```
AI Agent (持有: oneNat KEY, 仅此一把)
   │  GET /api/v1/proxy/<mapping_id>/api/scenes      ← 无任何上游凭证
   ▼
oneNat 服务端 dashboard (新增 proxy.go)
   │  ① Bearer KEY 鉴权 + can_proxy 权限位 + 限速/并发
   │  ② mapping 归属校验 (OwnerID) + AppScopes 过滤
   │  ③ 实时解析 PublicEndpoint(mapping_id)  ← 端口动态, 每请求解析
   │  ④ RevealMappingCredential(mapping_id)  ← 复用: 覆盖优先/回退应用默认
   │  ⑤ 按 AuthType 注入认证头 (内存中, 不落日志) + 透传其余头
   ▼
服务端公网入口 (既有数据面, 不改)
   ▼
ngrok 客户端 ──► 上游应用 (KB API / Web / …)
```

三档方案与分期：

| 方案 | 覆盖对象 | AI 侧持有 | 泄露消除程度 | 分期 |
|---|---|---|---|---|
| **A 凭证注入代理** + SKILL 去 KEY 化 | HTTP 类应用 (http-api/web/database) | 仅 oneNat KEY（变量名引用） | 应用凭证 100% 不进 AI；KEY 不进文档/日志 | **P1 本期** |
| **B 凭证租约** | SSH/TCP 类 | 短 TTL 凭证 | 缩小窗口 + 审计，**无法**加密吊销（诚实边界） | P2 可选 |
| **C 服务端代执行 / MCP 工具** | 全部 | 零 | 100%，但等于受限 RCE | 远期候选 |

---

## 3. 数据模型与权限变化

### 3.1 ApiKey 新增权限位

`store.go` `ApiKey` 增加：

```go
// CanProxy 控制能否经 /api/v1/proxy/:mapping/:path 以注入凭证方式调用
// 上游应用。与 CanReadCred 对称: 默认 false, 由用户在后台为 KEY 显式开启。
// 推荐 AI KEY 配置: can_proxy=true + can_read_cred=false ("用而不见")。
CanProxy bool `json:"can_proxy,omitempty"`
```

- **存量 store 文件兼容**：JSON 零值即 false，旧文件读入无需迁移；存量 KEY 权限不变（不会自动获得代理能力）。
- **鉴权点**：`userFromApiKey` 返回后由各 handler 自查权限位（与 `CanReadCred` 现有模式一致）。
- `UpdateApiKeyPerm` 扩展为同时可改 `can_proxy`；`PATCH /api/keys/:id` body 支持 `{"can_proxy": true}`。
- `apiKeyView` / `/keys` 页面增加开关，开关旁固定提示文案：

  > 开启后，持有此 KEY 的 AI 可通过平台代理调用已绑定应用的端口，平台自动附加应用登录凭证，AI 全程接触不到凭证本身。代理调用会逐请求审计。

### 3.2 AppScopes 继续生效

代理目标映射绑定的应用必须通过 `appVisibleToKey(k, a)`，未授权应用一律 404（不泄露存在性，沿用现有约定）。

---

## 4. 代理端点设计（P1 核心）

新增文件 `dashboard/proxy.go`，路由挂载在 `dashboard.go route()`：

```go
case strings.HasPrefix(p, "/api/v1/proxy/"):
    d.apiV1Proxy(w, r)   // GET/HEAD/POST/PUT/PATCH/DELETE 全方法
```

> 注：这是 `/api/v1/*` 第一个非 GET-only 的端点，方法分发在 handler 内部完成（现有 `methodMismatch` 模式），`route()` 只做前缀分发，不引入 method-pattern 路由（规避 ServeMux 1.22 兼容坑）。

### 4.1 路径与请求语义

- 路径形如 `/api/v1/proxy/<mapping_id>/<upstream_path...>`，query 原样透传。
- **转义保真**：从 `r.URL.EscapedPath()` 用 `strings.SplitN` 取前 4 段，余下部分**原样**作为上游路径（不解码再编码，避免二次转义错位）。
- 支持方法：GET / HEAD / POST / PUT / PATCH / DELETE；CONNECT/TRACE/OPTIONS 直接 405。
- 请求体 `io.Copy` 流式转发，Content-Type 原样。

### 4.2 目标解析与 SSRF 防护（安全核心）

1. `MappingByID` → 隧道 OwnerID == KEY 归属用户，否则 **404**；
2. 映射必须绑定应用 (`m.AppID != ""`)，否则 **422**「该映射未绑定应用，无法代理」；
3. 应用必须通过 `appVisibleToKey`，否则 **404**；
4. `proto` 必须为 `http`，或 `tcp` 且应用类型 ∈ {http-api, web, database}（tcp 隧道可承载 HTTP，实测场景）；其余（ssh 等）返回 **422**「该应用类型不支持凭证注入代理 (仅 HTTP 类可代理)，请改用凭证租约 (P2) 或为 KEY 开启凭证读取」；
5. 转发目标 = `PublicEndpoint(m.ID, rt)` **每请求实时解析**（公网端口动态），只取其 host:port；**AI 传入的任何 Host 头一律丢弃**，Host 重写为上游入口；
6. 目标 URL 一律 `http://<host:port>/<upstream_path>?<query>`（隧道出口为明文 HTTP，https 目标不在本期范围——写明非目标）；
7. 不跟随重定向（`CheckRedirect` 返回 `http.ErrUseLastResponse`），3xx 原样回传。

### 4.3 凭证注入规则（对齐 apps.js 既有语义）

`RevealMappingCredential(mappingID)` 解析**有效凭证**（映射覆盖优先、回退应用默认，`resolved_from` 天然正确），按 `auth_type` 注入：

| auth_type | 注入行为 |
|---|---|
| `none` | 不注入 |
| `basic` | `Authorization: Basic base64(username + ":" + password)` |
| `bearer` | `Authorization: Bearer <api_key>` |
| `header` | `<username 字段作为请求头名>: <api_key 字段作为值>`（UI 字段提示同步修正为「请求头名 / 请求头值」） |
| `custom` | 仅注入 ExtraHeaders；无 ExtraHeaders 时返回 **422**「该应用认证方式无法自动注入」 |
| ExtraHeaders | 对所有类型逐条解密后 `Set`（覆盖同名入站头） |

- **注入头覆盖同名入站头**（防 AI 自带低权凭证混淆）；
- hop-by-hop 头（Connection / Keep-Alive / Proxy-* / TE / Upgrade 等 RFC 7230 清单）两侧均剥离；
- 追加 `X-Forwarded-For: <client_ip>`、`X-Forwarded-Proto: https`（按 dashboard 入口实际协议）；
- 响应头原样回传（hop-by-hop 除外），附加 `X-OneNat-Proxy: 1` 便于排障；
- **凭证只在内存中拼装一次即丢弃；日志、审计、错误信息任何位置不出现注入头值**（复用 crypto.go 既有纪律）。

### 4.4 限额与超时

| 项 | 值 | 说明 |
|---|---|---|
| 速率 | 60 次/分/KEY | 新 `proxyLimiter`，与 `credLimiter` 同实现 |
| 并发 | 4 在途/KEY | 信号量，超额 429——防把代理当压测跳板 |
| 总超时 | 30s | `http.Client.Timeout` |
| Dial 超时 | 10s | |
| 响应体上限 | 10 MB | 超限截断并置 `X-OneNat-Truncated: 1`（防内存放大） |

### 4.5 错误语义汇总

| 场景 | 状态码 |
|---|---|
| KEY 无效 | 401 |
| KEY 无 `can_proxy` | 403「该 API KEY 无代理调用权限; 请用户在后台为 KEY 开启『允许代理调用』」 |
| 映射不存在 / 非本账号 / 应用不在 AppScopes | 404 |
| 未绑定应用 / tcp+ssh 类应用 / custom 无 ExtraHeaders | 422 |
| KEY 限速或超并发 | 429 |
| 上游连接失败（隧道离线等） | 502 |
| 上游超时 | 504 |

### 4.6 审计

每个代理请求（含被拒的）记审计，复用 `AuditKey`：

- action=`proxy.request`，target=mapping_id，result=最终 HTTP 状态码；
- detail: `{method, upstream_path(不含 query), app_id, resolved_from, bytes_out, ms}`；
- **query 与注入头值不入审计**（query 可能含业务敏感参数）；
- 403/422/429 单独 result 标记（`denied` / `unsupported` / `rate-limited`），与凭证接口的审计风格一致。

---

## 5. SKILL 文档与资源接口去 KEY 化（P1 核心）

### 5.1 `skillTmpl` 改造（`web.go`）

1. **删除全部 `{{.Key}}` 烘入点**（认证节、两个 curl 示例、总索引链接）。模板数据从 `map[string]string` 改为 struct：

```go
type skillData struct {
    BaseURL      string
    User         string
    HasProxy     bool // k.CanProxy — 控制「代理调用」节是否渲染
    CanReadCred  bool // 控制凭证/租约节是否渲染
    // Key 字段永久删除
}
```

2. 认证节改为：

   > API KEY 由用户放置在环境变量 `ONENAT_KEY`（若未设置，请向用户索取存放位置，**不要**要求用户在对话中粘贴明文）。所有请求使用 `Authorization: Bearer $ONENAT_KEY`。
   > 安全约定：不要把 KEY 写入脚本文件、命令行参数或任何输出；平台不会在文档中下发 KEY。

3. 新增「通过代理调用应用（推荐，HTTP 类）」一节：`$BASE/api/v1/proxy/<mapping_id>/<path>`，要点：
   - 认证由平台自动注入，**不要也不要尝试**自带应用凭证（带了也会被覆盖）；
   - 上游应用的登录密钥你永远接触不到，这是设计使然而非限制；
   - `custom` 认证或 SSH 类应用不可代理，见对应节。
4. 原「凭证」节收缩为「SSH/TCP 专用」，仅 `CanReadCred=true` 时渲染，并注明局限（凭证会进入你的上下文，用后即弃、不要落盘/外传，失败最多重试 2 次等既有约定保留）。

### 5.2 资源接口 (`apiV1Resources`)

- `ResourceMapping` 新增 `proxy_url` 字段（`omitempty`）：仅当「可代理」（4.2 条件全满足）时下发，形如 `{base}/api/v1/proxy/<mapping_id>`——AI 不自行拼路径；
- `ResourceSkill.URL` 与 `skillContentURL` **移除 `?key=` 烘入**，改为纯路径，技能文档教 AI 统一带 `$ONENAT_KEY` 请求头下载；
- `userFromApiKey` **移除 `?key=` 查询参数鉴权**（L3 根除；`/api/deploy` 用的是隧道独立 key，不受影响）。

### 5.3 安装提示改造 (`pageKeys` InstallPrompt)

改为（不再拼 URL 带 key）：

> 请安装 oneNat 技能：先将 API KEY 存入环境变量 `export ONENAT_KEY="<key>"`（或写入你的密钥管理工具），然后执行 `curl -s -H "Authorization: Bearer $ONENAT_KEY" <base>/skill/onenat.md -o onenat-skill.md`，阅读并按其中说明使用我的隧道资源。注意：你只有资源的使用权限，没有创建、修改或删除权限。

### 5.4 存量快照

已安装在用户侧的 `onenat-skill.md` 是旧快照，其中可能残留带 `?key=` 的链接与明文 KEY。技能文档既有约定「隔久了或有变更就重新拉取」，且 `?key=` 失效后旧链接自动 401，**无需迁移动作**；用户重新拉取一次即完成升级。

---

## 6. 凭证租约 — P2（可选，SSH/TCP 专用）

> P1 不实现。此节先固定语义，避免 P2 与 P1 冲突。

- `GET /api/v1/mappings/:id/credentials` 响应增加 `lease_id`、`expires_at`（默认 TTL 5 分钟，服务端内存登记 + 审计）；
- 门禁不变（`CanReadCred` + 5 次/分 + 审计）；
- **诚实边界（文档必须写明）**：oneNat 不持有目标机的账号管理能力，TTL 到期**无法强制吊销**已发出的口令；租约的意义是 (a) 最小化重发窗口 (b) 全程留痕 (c) 为未来「平台托管轮换」留接口——若凭证由平台代管，到期自动轮换才构成真正的吊销。

---

## 7. 兼容性影响

| 受影响方 | 影响 | 处理 |
|---|---|---|
| WorkBuddy（映射级凭证接口、inline/self-fetch 策略） | 端点保留、仅增字段 | 无需改动；远期可为其 `credentialMode` 增补 `proxy` 值 |
| 存量 API KEY | 默认无 `can_proxy`，行为不变 | 用户按需在 /keys 页开启 |
| 存量技能快照 | 旧 `?key=` 链接 401 | 重新拉取即升级（既有约定覆盖） |
| `/api/deploy` 隧道 KEY | 不走 `userFromApiKey` | 不受影响 |
| store 文件 | 新字段零值兼容 | 无迁移 |

---

## 8. 安全分析

**缓解（对 T1 提示注入外传凭证 / T2 AI 失误回显 / T3 日志泄露）：**

- HTTP 类应用凭证 100% 不进 AI 上下文——注入发生在服务端内存，提示注入再强也无密钥可偷（这是相对「提示词约束」的根本性提升）；
- oneNat KEY 不再出现在文档、URL、服务器访问日志；KEY 泄露面收敛为「用户本机环境变量 + AI 上下文中变量名的使用过程」；
- 逐请求审计 + 限速 + 并发上限 + AppScopes，滥用可发现、可定位、可吊销（KEY 粒度）。

**不缓解（明示）：**

- AI 仍持有 oneNat KEY 本身：KEY 在其作用域内（resources/proxy/skills）的活动无法阻止，只能审计；
- 上游应用的写操作照常可达：授权问题，靠 AppScopes 收窄 + KEY 页面明示；
- SSH/TCP 凭证在 P1 维持现状（P2 租约缩小窗口）；
- 上游返回内容仍进 AI 上下文：内容级提示注入仍在，但已无凭证可窃取。

---

## 9. 测试计划（追加 `test-dashboard-e2e.sh`，新增约 12 例）

用本地 HTTP echo 服务充当上游（测试客户端把映射指向它，上游断言收到的请求头/路径/方法）：

1. KEY 开 `can_proxy` → 经代理请求上游，**上游断言收到注入的 `Authorization: Basic …`**，路径与 query 保真；
2. POST 带体转发：上游收到一致 body 与 Content-Type；
3. 注入头覆盖：AI 自带错误 `Authorization` 被服务端头覆盖；
4. `can_proxy=false` → 403；KEY 无效 → 401；
5. 映射不存在 / 非本账号 / 应用不在 AppScopes → 404；
6. ssh 类型映射 → 422；未绑定应用 → 422；
7. 隧道离线 → 502；
8. 限速触发 → 429（快速连发 > 60）；
9. `GET /skill/onenat.md` 响应 **grep 不含明文 KEY**、含 `$ONENAT_KEY`、含 `/api/v1/proxy/`；
10. `can_read_cred=false` 的 KEY 拉取 SKILL：凭证节不渲染；
11. `/api/v1/resources`: `proxy_url` 存在且不含 key；skill URL 不含 `?key=`；
12. `?key=` 鉴权已移除：带 `?key=` 的请求 → 401。

---

## 10. 实施分期

| 阶段 | 内容 | 涉及文件 |
|---|---|---|
| **P1（本期）** | ApiKey.CanProxy + PATCH/UI 开关；`proxy.go`（路由/注入/限速/审计）；skillTmpl 去 KEY 化 + 代理节；resources `proxy_url`；InstallPrompt 改造；移除 `?key=` 鉴权与烘入；E2E 12 例 | `store.go` `api.go` `apps_v1.go` `web.go` `proxy.go`(新) `dashboard.go` `assets/static/*.js` `test-dashboard-e2e.sh` |
| P2（可选） | 凭证租约语义 | `apps_v1.go` `store.go` |
| P3（远期候选） | MCP 工具形态 / 服务端代执行 | 另立设计 |

P1 预估：代码 ~600 行 + 测试 ~300 行；不改数据面，不迁移数据。

---

## 11. 开放问题（请评审拍板）

1. **代理方法范围**：本设计放行 GET/HEAD/POST/PUT/PATCH/DELETE 全方法（审计兜底 + AppScopes 收窄）。备选：再拆一个 `can_proxy_write` 二级权限位，默认只读。推荐前者（位越多越没人配对）。
2. **限速值**：60 次/分/KEY、并发 4 是否合适（对照现有凭证 5 次/分）？
3. **响应体上限**：10 MB 截断是否满足现有应用（KB API 单响应场景）？
4. **P2 租约是否随 P1 一并做**：推荐延后——SSH 场景收益有限且语义必须诚实（无法吊销），避免给用户虚假安全感。
