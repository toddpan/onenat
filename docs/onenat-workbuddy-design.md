# OneNat WorkBuddy —— 基于 ONENAT + DSH 的多智能体协作工作台 设计方案

> 对标腾讯 CodeBuddy / WorkBuddy Enterprise 的「对话即工作台 + 智能体编排」体验，
> 完全构建在现有资产之上：**ONENAT 平台**（内网隧道 / 应用 / 技能 / 凭证注册表）
> + **dsh-web-service**（DSH 远程调用 API）+ **dsh-remote-orchestrator**（编排引擎参考实现）。

---

## 0. 调研结论：CodeBuddy / WorkBuddy 是什么，我们学什么

参考：[WorkBuddy Enterprise 产品简介（腾讯云文档）](https://cloud.tencent.cn/document/product/1831/134342)、
[CodeBuddy 产品概览](https://cloud.tencent.com.cn/developer/article/2677101)、
[Craft 开发智能体深度解析](https://cloud.tencent.com.cn/developer/article/2729370)。

腾讯 CodeBuddy（WorkBuddy Enterprise 的核心组件）有三种形态：IDE 插件 / IDE / CLI。
其 CLI 形态（CodeBuddy Code）的关键能力与我们的映射关系：

| CodeBuddy 能力 | 本方案对应 |
|---|---|
| 对话即编程（Chat 模式，每个会话一个聊天窗口） | 任务会话 = 聊天窗口，DSH WEB 同款体验（§7） |
| Craft 智能体：需求规划 → 多文件自主执行 → 反思修复 | 主任务：LLM Planner 拆解 → 子智能体并发执行 → 汇总质检（§6.3） |
| Sub Agent / 任务编排能力 | 子智能体池（绑定 DSH 实体 + 资源），多代理派发（§5、§6.3） |
| MCP 接第三方工具 | ONENAT 资源即工具：端口背后的 SSH / DSH / HTTP 应用，把「连接方式 + 技能文档」注入提示词（§6.2） |
| 混元 / DeepSeek 多模型接入切换 | 每个子智能体独立指定 provider / model / preset / 权限（§5.1） |

差异点（我们要做而 CodeBuddy 不做的）：**执行体与资源分布在多个内网**，
靠 ONENAT 隧道打通，公网端口动态漂移——这是本方案要解决的核心工程问题（§6.1）。

---

## 1. 现状盘点（已有资产）

### 1.1 ONENAT 平台（ngrok v1 魔改，Go，`src/ngrok/server/`）

- 数据面：客户端隧道（tunnel）→ 端口映射（mapping：`proto tcp/http`，`local_ip:local_port → remote_port`）。
  **mapping.id / app.id / tunnel.id 持久化于服务端 store，跨重连稳定；公网端口动态分配**（实测 44425→38405）。
- Web 后台（dashboard 包）：用户/隧道/映射/API Key 管理；应用（App）注册表：
  `app { id, name, type: ssh|http-api|..., internal_url, skills[], credentials }`，映射可绑定应用，支持映射级凭证覆盖（`auth_override`）。
- AI 只读 API（`onk-*` API Key 鉴权，v1 接口在 `dashboard/apps_v1.go`）：

| 接口 | 用途 |
|---|---|
| `GET /api/v1/resources` | 隧道→映射→绑定应用（含 skills[] 带 key 的下载 url）**唯一实时数据源** |
| `GET /api/v1/apps` / `GET /api/v1/apps/:id/skills/:name/content` | 应用目录 / 技能文件下载 |
| `GET /api/v1/apps/:id/credentials`、`GET /api/v1/mappings/:id/credentials` | 凭证（默认 403，需后台为 Key 开启；限速 5 次/分 + 审计） |
| `GET /skill/index.md` | 全量技能 Markdown 索引（快照） |

### 1.2 dsh-web-service（Node，DSH 插件）

DSH 全功能的 REST 封装，前缀 `/api/v1`：`/system/status`、`/workspaces`、`/sessions`（创建可指定
`title / agentPreset / provider / model / reasoningEffort / cwd / workspaceId`）、
`/sessions/:id/prompt`（同步）、`/sessions/:id/prompt-stream`（**SSE：delta / reasoning / tool_call / tool_result / turn_end**）、
`/sessions/:id/history`、`/sessions/:id/cancel`、`/models`、`/presets`、`/chat/completions`（OpenAI 兼容）。
—— 这是子智能体的**算力面标准接口**。

### 1.3 dsh-remote-orchestrator（Node，DSH 插件，本方案的直接前身）

已实现（`src/`）：远程节点池 `RemoteDshAgent`（apiBaseUrl+key+preset+model+systemPrompt）、
SSH 资源池（ssh2 连通测试/远程执行）、`TaskOrchestrator`（静态三段拆解 → 并行派发 → 同步等待超时转轮询 → 汇总）、
5+1 个模型工具、`/dsh-orchestrator` 独立控制台（web-ui.ts）+ DSH GUI 侧栏/中央列接管面板（client/index.ts）、
JSON 文件持久化。

**它与目标差距**：① 节点以裸 URL 标识，端口漂移即失效；② 无任务多轮会话（每子任务一次性会话）；
③ 无资源/技能注入提示词（SSH 资源仅本地用，不下发给远程子智能体）；④ 拆解是静态模板，非 LLM 规划；
⑤ 无任务内聊天窗口（只有子任务抽屉）。这五点正是下面设计要补齐的。

---

## 2. 总体架构

```
                          ┌──────────────────────────────────────────────┐
                          │        OneNat WorkBuddy 插件（本方案新增）      │
                          │   部署在用户日常打开的 DSH 实例（家内 3080）      │
                          │                                              │
  浏览器 ── DSH Web GUI ──▶│  ① 资源目录 ResourceDirectory（ONENAT 同步+解析）│
                          │  ② 子智能体池 SubAgentPool（绑定 DSH 实体+资源）  │
                          │  ③ 任务会话 TaskSession（多轮聊天，每任务一窗口） │
                          │  ④ 编排引擎 Orchestrator（Planner/派发/汇总）    │
                          │  ⑤ SSE 网关（远端流 → 任务聊天窗口）             │
                          └──────┬───────────────────────────┬───────────┘
                                 │                           │
                 ①资源发现/技能/凭证│                           │③④⑤建会话/发Prompt/SSE
                                 ▼                           ▼
                    ┌─────────────────────┐        ┌──────────────────────────┐
                    │ ONENAT 服务 123.57…:18080│        │ 多个 DSH 实例（经 ONENAT 映射）│
                    │ tunnels/mappings/apps │        │ http://123.57…:<动态端口>/api/v1│
                    │ skills/credentials    │        │ （内网：3080 / 其他内网 3080…） │
                    └─────────┬───────────┘        └────────────┬─────────────┘
                              │隧道转发                           │隧道转发
                    ┌─────────▼──────────────────────────────────▼─────────────┐
                    │ 内网环境 A：SSH 主机 + KB 应用        内网环境 B：DSH-3080 …  │
                    └──────────────────────────────────────────────────────────┘
```

**部署形态**：WorkBuddy 以 DSH 插件（hybrid：host 服务 + client 面板）安装在某台「主控 DSH」上，
复用 `dev_scaffold_plugin → dev_build_plugin → dev_inject_plugin` 生产线。v1 **不改动 ONENAT 服务端**
（只消费其既有只读 API）；可选增强见 §10。

**三面分离**：
- **资源面**（ONENAT）：一切内网资源的唯一登记处与公网入口；
- **算力面**（多个 DSH）：智能体运行时，彼此对等，都可被编排；
- **编排面**（WorkBuddy 插件）：面向人的工作台 + 调度大脑，对上只有聊天窗口，对下是资源与算力。

---

## 3. 需求追溯表

| 用户需求 | 方案落点 |
|---|---|
| 1. ONENAT 隧道集成多内网，SSH 管主机、映射端口访问应用 API | §6.1 资源目录：实时同步 `/api/v1/resources`，SSH/HTTP 两类连接方式统一解析 |
| 2. 多个 DSH API 接入 ONENAT 供智能体远程调用 | §6.1 DSH 型应用（`type=http-api`）自动识别为可绑定算力节点 |
| 3. 子智能体绑定 DSH 实体，端口变了 API 地址不受影响；指定模式/模型/提示词 | §6.1 稳定 ID 引用 + 运行时解析器；§5.1 SubAgent 配置模型 |
| 4. 任务（会话）管理：增删任务、多轮会话 | §6.4 TaskSession 模型 + 任务 CRUD API + 每任务持久 remoteSessionId 映射 |
| 5. 会话内挂多个子智能体→主调度主动分发，流程编排；资源（SSH/DSH/其它应用）连接方式+技能注入子智能体提示词 | §6.2 提示词合成引擎；§6.3 编排引擎（Planner 拆解、并发/串行 DAG、汇总）；§8 模型工具 |
| 6. 任务从智能体应用入口发起，每会话一个聊天窗口，同 DSH WEB 页面 | §7 UI：任务侧栏 + 聊天窗口（SSE 流式），DSH GUI 侧栏入口 + 中央列接管（复用 orchestrator 模式） |
| 主任务编排列表 / 拆解与协同派发 | §6.3/§7：看板页复用 orchestrator 任务列表 + 状态机，升级为 LLM Planner |

---

## 4. 核心设计决策（D*x 编号供评审）

- **D1 稳定 ID 解耦端口漂移**：子智能体/资源绑定一律存 ONENAT `mappingId`（或 `appId`），绝不存公网 URL；
  每次派发前经 ResourceDirectory 解析出**当下**的 host:port，并把解析结果写进任务日志（事后可审计当时用的入口）。
- **D2 资源即提示词（Resource-as-Prompt）**：不做 MCP server、不改 DSH——把资源的「入口 + 凭证 + 技能全文」
  在派发瞬间合成为结构化提示词块注入子智能体，让远端 DSH 开箱会用 SSH/HTTP 资源（与 ONENAT 技能机制天然同构）。
- **D3 会话长持**：`(taskId, subAgentId) → remoteSessionId` 持久映射；多轮对话复用同一远端会话，
  追问即续聊（orchestrator 是一次性会话，这里改为任务级长会话）。
- **D4 LLM Planner 替代静态拆解**：主任务派发时用「规划器模型」（可配，默认用主控 DSH 自身 `/chat/completions`）
  按子智能体花名册产出 JSON 子任务图（含并行/串行标记），静态三段模板仅作 Planner 失败兜底。
- **D5 流式双跳**：远端 `prompt-stream` SSE 由插件 host 消费，经本地 SSE 网关（`/api/tasks/:id/stream`）
  转推浏览器聊天窗口；远端不支持 SSE（旧版）时自动降级为 orchestrator 的同步+轮询模式。
- **D6 v1 不动 ONENAT 服务端、不动 dsh-web-service**：全部逻辑收敛在一个新插件里，风险最小、可整体卸载。

---

## 5. 数据模型（新增/改造，TypeScript 语义描述）

### 5.1 子智能体（扩展自 `RemoteDshAgent`）

```ts
interface SubAgent {
  id: string                       // 'agent-xxxxxxxx'
  name: string
  /** DSH 实体引用 —— 稳定 ID，二选一；direct 仅作手工兜底 */
  dshRef:
    | { kind: 'mapping'; mappingId: string }        // ONENAT 映射（推荐，端口动态）
    | { kind: 'app';     appId: string }            // ONENAT 应用（type=http-api）
    | { kind: 'direct';  apiBaseUrl: string; apiKey?: string } // 旧式直连（兼容 orchestrator 数据迁移）
  apiKeyRef?: { source: 'mapping'; mappingId: string } | { source: 'plain'; apiKey: string }
  /* DSH 会话配置 */
  agentPreset?: string             // 模式预设，如 'cordis'
  permission?: 'danger-full-access' | 'workspace-write' | 'read-only'
  provider?: string; model?: string; reasoningEffort?: string
  systemPrompt?: string            // 角色提示词（人设/职责）
  /** 资源绑定：告诉这个子智能体「你还能用哪些内网资源」 */
  resources: AgentResourceBinding[]
  tags?: string[]; description?: string
  enabled: boolean
  lastResolved?: { baseUrl: string; resolvedAt: number }  // 最近一次解析结果（展示用）
  createdAt: number; updatedAt: number
}

interface AgentResourceBinding {
  ref: { kind: 'mapping'; mappingId: string } | { kind: 'app'; appId: string }
  alias?: string                   // 提示词里展示的友好名
  /** 凭证注入策略：inline=把凭证写进提示词；self-fetch=提示词里给 ONENAT 凭证接口让 AI 自取；omit=不给 */
  credentialMode: 'inline' | 'self-fetch' | 'omit'
  /** 技能文件注入：all=全部内联；names=指定清单；none=只给目录让 AI 按需拉取 */
  skillMode: 'all' | { names: string[] } | 'none'
  note?: string                    // 用途说明，注入提示词
}
```

### 5.2 任务会话（Task / Turn / 编排）

```ts
interface WorkTask {
  id: string                       // 'task-xxxxxxxx'
  title: string
  mode: 'chat' | 'orchestrate'     // 挂 1 个子智能体=chat 直通；≥2 或显式选择=orchestrate
  status: 'draft' | 'running' | 'waiting-user' | 'completed' | 'failed' | 'partial_success'
  /** 会话上下文中挂载的子智能体（顺序即优先级） */
  memberAgentIds: string[]
  turns: TaskTurn[]                // 多轮
  sessions: Record<string, {       // key = subAgentId（D3 长持会话）
    remoteSessionId: string; dshRef: ResolvedDshRef; createdAt: number
  }>
  plan?: TaskPlan                  // orchestrate 模式的拆解结果
  summary?: TaskSummary            // 复用 orchestrator 的 TaskSummary 结构
  createdAt: number; updatedAt: number
}

interface TaskTurn {
  id: string; seq: number
  role: 'user' | 'agent' | 'system'
  agentId?: string                 // role=agent 时：哪个子智能体说的
  text: string; reasoning?: string
  streaming?: boolean
  subtaskIds?: string[]            // 该轮派发产生的子任务（chat 窗口里渲染成进度卡片）
  at: number
}

interface TaskPlan {
  strategy: 'parallel' | 'sequential' | 'dag'
  plannerModel?: string
  subtasks: PlanSubtask[]
}
interface PlanSubtask {
  id: string                       // 'sub-xxxxxxxx'
  title: string
  prompt: string                   // Planner 产出的子任务指令（派发时再叠加资源提示词块）
  agentId: string
  dependsOn: string[]              // DAG 依赖（strategy=dag 时生效；parallel 全并行 / sequential 链式）
  status: 'pending' | 'running' | 'completed' | 'failed' | 'skipped'
  remoteSessionId?: string
  result?: { content: string; reasoning?: string }
  error?: string
  logs: { ts: number; level: 'info'|'warn'|'error'|'tool'; msg: string }[]
}
```

### 5.3 资源目录缓存

```ts
interface ResourceSnapshot {        // ResourceDirectory 维护，TTL 30s + 手动刷新
  fetchedAt: number
  tunnels: OnenatTunnel[]          // /api/v1/resources 原样 + 派生字段
  resolveMapping(mappingId): ResolvedEndpoint | undefined
  resolveApp(appId): ResolvedApp | undefined
}
interface ResolvedEndpoint {
  mappingId: string; tunnelName: string; online: boolean
  proto: 'tcp' | 'http'
  host: string; port?: number
  baseUrl?: string                 // tcp 隧道承载 HTTP 时合成 http://host:port（onenat.md §2 规则）
  local: string                    // 127.0.0.1:22 等，用于端口类型推断
  app?: { id, name, type, skills: {name, size, url}[] }
}
```

存储沿用 orchestrator 的 JSON 文件持久化模式（`~/.dsh/onenat-workbuddy/store.json`，原子写 + 启动恢复）。

---

## 6. 核心机制设计

### 6.1 资源目录与「端口漂移免疫」解析器（需求 1/2/3）

`ResourceDirectory` 服务（host 侧）：

1. **同步**：定时（60s）+ 派发前强制刷新拉取 `/api/v1/resources` 与 `/api/v1/apps`；
   构建三条索引：`mappingId → ResolvedEndpoint`、`appId → apps 目录项`、`tunnelId → tunnels`。
2. **解析规则**（全部来自 onenat.md 实测语义）：
   - `online=false` 或无 `public_url` ⇒ 节点不可达，派发时**跳过并告警**，不重试不探测；
   - `proto=tcp` + `local` 指向 web 端口（如 `127.0.0.1:3080`）或绑定 `type=http-api` 应用
     ⇒ 合成 `baseUrl = http://<host>:<port>`（raw TCP 承载 HTTP，路径不变）；
   - `proto=http` ⇒ 直接用 `public_url`；
   - SSH（`local:*:22` 或 `type=ssh`）⇒ 产出 `ssh -p <port> <user>@<host>` 入口；
   - DSH 实体识别：`app.type === 'http-api'` 且技能清单含 `dsh-web-service`（双重校验，防绑错——onenat.md §1 绑定校验规则）。
3. **连通性探活**：对 DSH 端点做 `GET /system/status`（带 Bearer key，10s 超时），
   回填 `name/version/providers` 到子智能体 `lastResolved`，UI 显示「在线 ✓ / 离线 ✗ / 端口已更新 ↻」。
4. **漂移免疫**（D1）：派发链路里任何地方都不允许出现缓存的公网 URL——`SubAgentStore.getResolved(agent)`
   是唯一出口，内部永远走第 2 步实时解析。旧数据迁移：`direct` 引用启动时尝试按 URL 反查 mappingId，查得到就升级。

### 6.2 资源提示词合成引擎（需求 5 前半）

派发子任务时，`PromptComposer.render(subAgent, resolvedResources, credentialsPolicy)` 生成
**资源能力块**，追加在子任务指令之前（沿用 orchestrator `remote-client.ts` 的
`[系统角色与前置指导] / [当前子任务指令]` 分段协议，新增第三段）：

```markdown
[系统角色与前置指导]:
<subAgent.systemPrompt>

[可用资源清单]（由 OneNat 平台注入；公网端口为本次派发时刻实况，勿缓存、失效后重查）:

### 资源1: KB-136 堡垒机 (SSH)  [别名 db-hop]
- 连接: ssh -o StrictHostKeyChecking=accept-new -p 45561 root@123.57.138.43
- 凭证: 密码 `********`（inline 注入）  ※ 不要把密码写入脚本或输出
- 用途: 登录 192.168.30.136 查看服务日志、重启进程
- 技能 usage.md（全文附下）: ……

### 资源2: KB-169 DSH 实例 (HTTP API)  [别名 dsh-169]
- 入口: http://123.57.138.43:39649/api/v1（Authorization: Bearer <key>）
- 凭证: 通过 OneNat 凭证接口自取（限速 5 次/分）:
  curl -H "Authorization: Bearer onk-…" https://onenat.sooncore.com/api/v1/mappings/85a02769/credentials
- 技能 dsh-web-service（全文附下）: ……

[资源使用约定]:
1. 使用任何资源前先读对应技能文件，技能与你的猜测冲突时以技能为准；
2. 连接被拒/超时视为端口可能已漂移，向调度方报告，不要反复重试；
3. 资源仅限本任务使用，不得把凭证转发到第三方。

[当前子任务指令]:
<subtask.prompt>
```

实现要点：
- 技能正文经 `app.skills[].url`（自带 key）抓取，`skillMode` 控制内联范围；单技能超 8KB 截断并附原 url 让 AI 按需下载；
- 凭证按 `credentialMode`：`inline`（host 调映射级凭证接口取到后占位替换）/ `self-fetch` / `omit`；
- 整块在 Web UI「提示词预览」里可见可复制，派发日志记录注入了哪些资源（不含明文凭证）。

### 6.3 编排引擎（需求 5 后半 + 主任务编排列表/拆解派发）

在 orchestrator `TaskOrchestrator` 状态机上升级：

```
用户在任务窗口发消息（挂了 N 个子智能体）
   │
   ├─ N=1（chat 模式）──▶ 直通：复用该 agent 的长持远端会话，prompt-stream 流式回填聊天窗口
   │
   └─ N≥2 或选「协同编排」（orchestrate 模式）
        ① Planner：调规划器模型（D4），输入=任务目标+成员花名册（名称/角色/资源摘要/在线状态）+历史轮摘要，
           输出 JSON：[{title, prompt, agentId, dependsOn}]（校验 agentId∈成员、拓扑无环；失败→静态三段兜底）
        ② 渲染计划卡片到聊天窗口（可改可重派），任务状态=running
        ③ 调度执行（DAG）：
             - parallel：全部就绪子任务同时派发（Promise.all，沿用 orchestrator 的
               「同步等待窗口 < 远端窗口，超时转轮询 waitForSessionResult」机制）
             - sequential/dag：拓扑序推进，上游 completed 后注入「上游产出摘要」再派发下游
             - 每个子任务：解析 DSH 入口 → 组资源提示词块 → (已无会话则)创建远端会话 → 派发
        ④ 汇总：全子任务结束后，规划器模型综合各产出生成 TaskSummary（success/partial_success/failed
           + keyPoints + finalConclusion），以 agent 轮次写回聊天窗口
        ⑤ 多轮：用户继续发消息 → 作为新指令再走 ①（携带上一轮各子任务结果摘要），或 @某个子智能体单聊
```

取消与重试：任务级 `AbortController`（沿袭 orchestrator `activeJobs`）；子任务单项重试 =
复用该子任务远端会话发「重试」指令；删除任务 = abort + 可选关闭远端会话（`DELETE /sessions/:id`）。

### 6.4 任务多轮会话（需求 4）

- 任务 CRUD：`POST /tasks`（传 title + memberAgentIds + 首条消息即发起）、`GET /tasks`（列表+状态）、
  `GET /tasks/:id`（全量轮次+计划+日志）、`DELETE /tasks/:id`、`PATCH /tasks/:id`（标题/成员增删——
  成员变更即时生效：新成员下一轮纳入花名册，其远端会话懒创建）。
- 多轮语义：turn 序列全量持久化；`chat` 模式每轮 = 一次远端 prompt（SSE）；`orchestrate` 模式每轮 =
  一轮「计划→执行→汇总」，各子智能体的远端会话跨轮复用（D3），上下文天然延续。
- 会话窗口与远端真身一致：聊天窗内嵌「查看远端会话」按钮，拉 `/sessions/:id/history` 展示含
  reasoning / tool_call 的完整现场（复用 orchestrator `getSubtaskChat`/`sendFollowupToSubtask` 逻辑）。

---

## 7. UI 设计（需求 6：跟 DSH WEB 页面一样的聊天窗口）

### 7.1 页面结构（独立控制台 `/onenat-workbuddy`，视觉沿用 orchestrator 控制台的暗色体系）

```
┌────────────────────────────────────────────────────────────────────┐
│  ⚡ OneNat WorkBuddy          [资源目录] [子智能体] [任务] [编排看板]    │
├───────────────┬────────────────────────────────────────────────────┤
│ 任务列表(侧栏)  │  聊天窗口（每任务一个，主区）                          │
│ ───────────── │ ┌────────────────────────────────────────────────┐ │
│ + 新建任务     │ │ [任务标题]  模式:chat/orchestrate  成员: @A @B @C │ │
│ ▸ 任务1 ●运行中 │ ├────────────────────────────────────────────────┤ │
│ ▸ 任务2 ✓完成  │ │ (user)  帮我排查 136 上 KB 服务 5xx 飙升原因       │ │
│ ▸ 任务3 …     │ │ (卡片)  计划: ①诊断@ops-136 ②修复@kb-dev 串行     │ │
│               │ │ (agent @ops-136)▋正在流式输出… (思维链/工具折叠)    │ │
│ 子智能体快捷区  │ │ (agent @kb-dev) 已完成，产出摘要……  [查看远端会话] │ │
│ @ops-136 在线  │ ├────────────────────────────────────────────────┤ │
│ @dsh-169 在线  │ │ [输入框──────────────────────────] [@添加成员][发送]│ │
└───────────────┴────────────────────────────────────────────────┴─┘
```

- **聊天体验对齐 DSH WEB**：流式 delta 逐字渲染、reasoning 折叠条、工具调用卡片、停止按钮
  （映射远端 `/sessions/:id/cancel`）、Markdown 渲染；每个 agent 轮次带头像/名字/在线状态。
- **编排看板**：orchestrator 任务列表升级版——主任务卡片 + 子任务泳道（pending/running/completed/failed）
  + 依赖连线（dag 时简单分层布局）+ 工作日志抽屉 + 远端聊天抽屉（全部复用现有实现改造）。
- **DSH GUI 集成**（智能体应用入口）：完全复用 orchestrator `client/index.ts` 模式——
  侧栏「WorkBuddy」入口行 + `sidebar.footer.action` 席位 + 中央列 iframe 接管面板
  （互斥属性 `data-dsh-workbuddy-active`，与 orchestrator/taskboard/ssh 家族互切）；
  点侧栏会话行自动交还中央列。
- **新建任务流**：选模式 → 从资源目录下拉选 DSH 实体（在线状态/模型数实时显示）建/选子智能体 →
  勾选资源绑定（SSH/DSH/HTTP 应用多选，逐个配 credentialMode/skillMode）→ 「提示词预览」确认 → 发起。

### 7.2 Host↔Client 通道

聊天流走本地 SSE（`GET /onenat-workbuddy/api/tasks/:id/stream`，事件：
`turn_delta / turn_end / plan_update / subtask_status / log_append / task_end`）；
控制面走普通 REST。client 包只做壳（入口+iframe），保证热重载与双形态（独立页/GUI 面板）一致。

---

## 8. 模型工具（AI 可自主操作工作台，命名沿用 orchestrator 风格）

| 工具 | 说明 |
|---|---|
| `workbuddy_resource_manage` | 资源目录：list / refresh / resolve（给出入口与在线态）；只读透传 ONENAT |
| `workbuddy_agent_manage` | 子智能体 CRUD + ping + models/presets 拉取（含 dshRef 绑定与资源绑定） |
| `workbuddy_task_manage` | 任务增删查、成员变更、（多轮）发消息——AI 在自己的会话里就能开任务、追问 |
| `workbuddy_task_status` | 任务/子任务进度、日志、计划 |
| `workbuddy_task_chat` | 读子任务远端聊天记录 / 发追问 |
| `workbuddy_task_evaluate` | 触发汇总评估 |

配套 `skills/onenat-workbuddy/SKILL.md`：触发词「WorkBuddy / 任务编排 / 子智能体 / 内网资源」，
内容=以上工具用法 + 资源提示词协议说明，装到 `~/.dsh/skills/`。

---

## 9. REST API 设计（前缀 `/onenat-workbuddy`）

```
# 资源目录
GET  /api/resources                     # 快照（tunnels/mappings/apps/解析结果）
POST /api/resources/refresh             # 强制刷新
GET  /api/resources/mappings/:id/resolve# 单点解析（含 online/BaseUrl 合成）

# 子智能体
GET|POST /api/agents                    # 列表(脱敏) / 新建或更新
DELETE /api/agents/:id
POST /api/agents/:id/ping               # 解析+探活 /system/status
GET  /api/agents/:id/models | /presets  # 远端可用模型/预设（供表单下拉）
GET  /api/agents/:id/prompt-preview     # 资源提示词块预览（凭证打码）

# 任务会话
GET|POST /api/tasks        DELETE /api/tasks/:id   PATCH /api/tasks/:id
GET  /api/tasks/:id                      # 轮次+计划+子任务+日志
POST /api/tasks/:id/messages            # 多轮发言（SSE 流式响应或 202+stream）
POST /api/tasks/:id/cancel
POST /api/tasks/:id/summary             # 重评汇总
GET  /api/tasks/:id/stream              # SSE 网关（聊天流/进度流）
GET  /api/tasks/:id/subtasks/:sid/chat  # 远端聊天记录
POST /api/tasks/:id/subtasks/:sid/followup
POST /api/tasks/:id/subtasks/:sid/retry # 单项重试
```

---

## 10. 安全与边界

1. **凭证最小暴露**：`inline` 仅在用户显式选择时启用；`self-fetch` 借用 ONENAT 既有限速（5 次/分）+审计；
   日志/UI/工具输出一律打码（复用 orchestrator `maskSshResource` 思路 + ONENAT 审计日志）。
2. **只读资源面**：对 ONENAT 仅消费 GET 类接口，创建/修改/删除一律拒绝（与 onenat-skill 行为约定一致）。
3. **漂移与离线**：派发前强刷新；离线成员自动跳过并降级 partial（不再出现「拿着旧端口狂试」）。
4. **超时体系**：本端等待(5min) < 远端窗口(30min)，超时转轮询（orchestrator 实测机制照搬）；
   SSE 网关带 15s 心跳，防隧道/代理断流。
5. **审计**：任务日志记录每次解析到的入口（host:port + mappingId + 时刻），端口漂移可回溯。

---

## 11. 实现计划（4 个里程碑，全部可独立演示）

**代码复用清单**（新插件 `@dsh-external/onenat-workbuddy`，骨架用 `dev_scaffold_plugin hybrid`）：

| 复用自 | 文件 | 改造 |
|---|---|---|
| orchestrator | `store.ts` | 换 schema，存 SubAgent/WorkTask/资源缓存 |
| orchestrator | `remote-client.ts` | +`prompt-stream` SSE 消费、+由 ResolvedEndpoint 构造 client（不再吃裸 agent） |
| orchestrator | `orchestrator.ts` | 状态机保留，接 Planner/DAG/长持会话 |
| orchestrator | `ssh-resources.ts`/`ssh-store.ts` | 保留本地 SSH 池作为「非 ONENAT 资源」补充入口 |
| orchestrator | `web-ui.ts` | 控制台骨架/暗色体系/任务抽屉，重写聊天窗与看板 |
| orchestrator | `client/index.ts` | 改名换肤（workbuddy 属性族/互斥族） |
| orchestrator | `tools.ts`/`router.ts` | 换工具与路由表（§8/§9） |
| dsh-web-service | `streaming.ts` | SSE 解析/事件名对齐参考 |
| ONENAT | `onenat-skill.md` | 资源解析规则即实现规格（§6.1） |

- **M1 资源面（~2 天）**：ResourceDirectory + 解析器 + 探活 + `/api/resources*`；控制台资源目录页。
  验收：端口漂移演练（重启 ONENAT 客户端）后 resolve 返回新端口，旧 URL 零残留。
- **M2 子智能体（~2 天）**：SubAgentStore + dshRef 绑定 + 资源绑定 + PromptComposer + 预览/ping/models。
  验收：建 2 个子智能体（136-DSH、169-DSH），绑定 SSH 资源，提示词预览含实时端口与技能全文。
- **M3 任务聊天（~3 天）**：TaskStore + 多轮 + chat 模式直通 + SSE 网关 + 聊天窗口 UI + GUI 面板入口。
  验收：浏览器在 DSH GUI 里新建任务 → 与远端子智能体流式多轮对话，关闭重开历史不丢。
- **M4 编排（~3 天）**：Planner（JSON 校验+兜底）+ DAG 调度 + 汇总 + 编排看板 + 6 个模型工具 + SKILL。
  验收：一个任务挂 3 个子智能体（规划/实施/质检），自动拆解并发派发、串行依赖生效、聊天窗内看到
  计划卡片→各自流式产出→汇总结论；kill 一个远端节点 → partial_success + 日志可查。
- **后续可选**：ONENAT 服务端增强（http 子域名稳定入口避免端口漂移、映射变更 webhook 推送刷新）、
  原生 slot 版聊天面板替代 iframe、任务模板/定时任务、子智能体结果结构化交接（artifact 协议）。

---

## 12. 风险与对策

| 风险 | 对策 |
|---|---|
| tcp 隧道跑 SSE 长连接被中间层断流 | SSE 网关心跳 + 断线自动 `history` 补齐最后一条回答再继续轮询（orchestrator 降级链路复用） |
| ONENAT 凭证接口 403/限速 | `credentialMode` 默认 `omit`，UI 建绑定时明示；后台未开权限时自动降级 self-fetch 并提示 |
| 绑错应用（技能与端口不符） | 解析器执行 onenat.md §1 双重校验（local 端口段 vs app.type），可疑绑定时 UI 黄条警告 |
| 远端 DSH 版本不一（无 presets/SSE） |能力探测缓存（ping 时记 capabilities），按能力降级（同步 prompt+轮询、隐藏预设下拉） |
| Planner 产出非法/超时 | JSON Schema 校验 + 一次修复重试 + 静态三段兜底，保证任务永不卡死在规划阶段 |
| 任务量大后 JSON 存储膨胀 | turns/logs 按任务分文件 + 保留最近 N 任务全量、其余归档摘要（沿 store.json 目录化） |
```

以上。下一步只需确认后，我即可按 M1 起步：`dev_scaffold_plugin` 生成 `onenat-workbuddy` hybrid 插件骨架并开始实现资源目录与解析器。
