---
name: dsh-web-service
description: 通过 RESTful API 管理 DeepSeek Harness (DSH)：工作区、会话、模型设置与 SSE 流式对话。当需要用 HTTP 接口操作 DSH（三方系统集成、OpenAI 兼容调用、自动化脚本）时使用。
whenToUse: 用户要求通过 HTTP/REST API 调用 DSH、集成三方系统、使用 OpenAI 兼容协议对话、或自动化管理工作区与会话时使用。
---

# DSH Web Service API（dsh-web-service）

DSH 已暴露为 Web Service，第三方系统、脚本或任何 OpenAI SDK 均可通过 HTTP 调用。

## 服务地址（二选一）

| 访问路径 | Base URL |
|---|---|
| DSH 本机 / 同内网 | `http://127.0.0.1:3080/api/v1`（DSH 主 webserver 端口，默认 3080） |
| **经 oneNat 隧道** | 从 oneNat `/api/v1/resources` 找 `local=127.0.0.1:3080` 的映射，用其 `public_url` 的 host:port 替换；路径不变 |

- 交互式文档 `<Base>/docs`；OpenAPI 规范 `<Base>/openapi.json`。
- 鉴权：配置了 `apiKey` 时带 `Authorization: Bearer <key>` 或 `X-API-Key: <key>`；未配置免鉴权。
- 经隧道访问前先看该映射 `online`；离线则直接报告，**不要请求**。
- 自检：`curl -s <Base>/system/status`

## 统一响应约定

除 `/chat/completions` 外均返回 `{"ok": true, "data": {...}}` 或 `{"ok": false, "error": "..."}`。
状态码：`200` 成功、`201` 创建、`400` 参数错、`401` 鉴权失败、`404` 不存在、`500` 内部错误、`503` 依赖服务未挂载。

## 1. 工作区管理

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/workspaces` | 列出全部工作区 |
| GET | `/workspaces/:id` | 工作区详情 |
| POST | `/workspaces` | 添加，body `{"path": "本地目录", "title": "标题"}`；目录必须已存在 |
| PUT | `/workspaces/:id` | 重命名，body `{"title": "新标题"}` |
| DELETE | `/workspaces/:id` | 删除绑定（保留文件与会话） |
| GET | `/workspaces/:id/sessions` | 该工作区下的会话 |

## 2. 会话管理

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/sessions` | 列表；`?search=关键词` 搜索、`?workspaceId=` 过滤 |
| GET | `/sessions/:id` | 详情（状态、模型、工作区、cwd、事件数） |
| POST | `/sessions` | 创建，body 可选 `{"workspaceId", "cwd", "title", "provider", "model", "reasoningEffort"}` |
| PUT | `/sessions/:id` | 改标题/模型，body `{"title"?, "provider"?, "model"?, "reasoningEffort"?}` |
| DELETE | `/sessions/:id` | 删除/归档并释放 Agent |
| GET | `/sessions/:id/history` | 历史分页；`?maxMessages=50`、`?beforeSeq=`；返回 `messages[]`（`role`/`content`/`seq`/`time`）与 `hasMore` |
| POST | `/sessions/:id/cancel` | 中止当前轮次 |

取 sessionId（后续对话都要用它）：

```bash
SID=$(curl -s -X POST http://127.0.0.1:3080/api/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"title": "测试", "cwd": "/tmp/my-project"}' | python3 -c "import json,sys;print(json.load(sys.stdin)['data']['sessionId'])")
```

## 3. 模型管理

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/models` | 模型目录：`provider`、`id`、`name`、`reasoning.efforts`、`routeOnly`、`isDefault` |
| GET | `/models/default` | 当前全局默认 `{provider, model, reasoningEffort?}` |
| PUT | `/models/default` | 改全局默认，body `{"provider", "model", "reasoningEffort"?}`；对新会话生效 |
| GET | `/providers` | 提供商清单（各含 `models[]`） |

**选模型前先 `GET /models` 拿合法的 `provider` + `model` 组合**，非法组合报 `no adapter registered for provider ...`。

## 4. 对话（流式 / 同步）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/sessions/:id/prompt-stream` | **SSE 流式**，body `{"prompt", "mode"?: "normal"\|"steer"}` |
| GET | `/sessions/:id/events` | SSE 订阅该会话全部底层事件（按 `seq` 递增） |
| POST | `/sessions/:id/prompt` | 同步阻塞到本轮完成，返回 `{content, reasoning?, toolCalls[]}` |

SSE 事件（prompt-stream）：`connected`（连接建立）、`delta`/`reasoning`（文本/思考增量）、`tool_call`/`tool_result`（工具调用与结果）、`turn_end`（本轮结束）、`done`（`[DONE]` 终止）、`error`。

```bash
curl -N -X POST http://127.0.0.1:3080/api/v1/sessions/$SID/prompt-stream \
  -H "Content-Type: application/json" -d '{"prompt": "介绍一下你自己"}'
```

## 5. OpenAI 兼容接口

`POST /chat/completions`，标准 OpenAI 客户端直接可用：

```bash
# 非流式；加 "stream": true 即流式（SSE chunks，data: [DONE] 结束）
curl -s -X POST http://127.0.0.1:3080/api/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"messages": [{"role": "user", "content": "你好"}]}'
```

- 可传 `sessionId` 延续指定会话；不传自动新建。响应带 `sessionId` 供后续多轮。
- 可传 `model`，格式 `"provider/model"`（如 `"zai-coding-cn/glm-5.3"`）。
- 三方集成优先走此接口（标准协议，免协调会话生命周期）。

Python SDK：

```python
from openai import OpenAI
client = OpenAI(base_url="http://127.0.0.1:3080/api/v1", api_key="none")
r = client.chat.completions.create(model="none", messages=[{"role": "user", "content": "你好"}])
print(r.choices[0].message.content)
```

## 6. 设置

`GET /settings` 全部命名空间（redacted 值 + schema）；`PATCH /settings/:namespace` 合并写入（body 为 JSON patch）。常用：`agent-default-model`、`locale`、`ui-theme`、`permission`。

## 排错速查

| 现象 | 处理 |
|---|---|
| 连接拒绝 | DSH 未启动或端口非 3080；先查 `system/status` |
| 经隧道超时/拒绝 | 隧道离线：查 oneNat resources 里该映射 `online`，离线报告用户，不要重试 |
| 401 | 配置了 `apiKey`：带 `Authorization: Bearer <key>` |
| 模型报 no adapter | `provider/model` 非法：先 `GET /models` 校验 |
| 新会话模型不对 | 全局默认被改过：查 `GET /models/default`，或创建时显式传 `provider/model` |
| 流式空回复 | 会话刚创建需 Agent 就绪：改用 `POST /sessions/:id/prompt` 同步等待，或先 `GET /events` 确认事件在流动 |
| 首帧迟迟不来 | 模型排队/长思考，首个增量实测可达 5~8 分钟：先 `GET /sessions/:id` 看状态再决定，勿急于判死重发 |
| 连续 401/403 | API key 失效或已轮换：**停止循环重试**，刷新 key 配置后再试一次，仍失败如实报告 |
| prompt 被拒 `agent-busy` | 会话正在跑：先 `POST /sessions/:id/cancel` 或改 `mode: "steer"` 追加 |

## 调用策略

- 一切以**实测 curl** 为准，不要凭记忆猜测端点或字段。
- 批量操作前先查列表拿真实 id（workspace/session id 均为 UUID）。
- 长任务用 `prompt-stream` 或 `events` 监听，避免同步阻塞超时。

## 自动化调用守则（定时任务 / 子智能体 / 脚本必读）

- **禁止交互等待**：无人值守上下文不得调用 `ask_user_question` 等等待人工答复的工具。参数不全（如收件群/收件人无法解析）时取保守默认执行，并在产出中显式说明假设；有人值守的会话才允许提问。
- **首帧慢 ≠ 失败**：大 prompt + 模型排队时首个增量可达 5~8 分钟，SSE 静默先查 `GET /sessions/:id` 会话状态；10 分钟内不要判定失败重发——重复 prompt 会把整轮任务重跑一遍。
- **401 即停**：同一接口连续 2 次 401/403 即停止重试；先刷新 API key / 凭证来源再重试一次，仍失败如实报告，不要拿旧凭证反复敲接口。
- **禁止自派发**：任务指令里提到的 `@某智能体` /「远程 DSH」若经核对（baseUrl、hostname 一致）就是当前节点或当前会话，直接本地执行；不要再经本 API 新建会话把任务派回自己，白绕一跳且易嵌套死循环。
