# oneNat

<p align="center">
  <strong>现代化、安全易用的一站式私有内网穿透与受管隧道平台</strong>
</p>

<p align="center">
  <a href="https://github.com/toddpan/onenat/releases"><img src="https://img.shields.io/github/v/release/toddpan/onenat?color=blue&label=Release" alt="Release"></a>
  <a href="https://golang.org/"><img src="https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go" alt="Go"></a>
  <a href="https://github.com/toddpan/onenat/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-MIT-green" alt="License"></a>
  <img src="https://img.shields.io/badge/Platform-Linux%20%7C%20macOS%20%7C%20Windows-lightgrey" alt="Platform">
  <img src="https://img.shields.io/badge/Arch-x86__64%20%7C%20ARM64%20%7C%20ARM-orange" alt="Architecture">
</p>

---

## 📖 项目简介

**oneNat** 是一款基于高性能 Go 语言构建的现代化内网穿透与受管隧道平台。

针对传统内网穿透工具配置繁琐、依赖复杂、缺乏多用户权限管控、端口修改需重启客户端以及 AI 难以安全接入内网等痛点，**oneNat** 提供了从 **Web 可视化管理后台**、**云端配置实时热下发**、**多平台客户端一键静默部署** 到 **AI Agent 专属只读 SKILL 接入** 的全套解决方案。

---

## 🌟 核心特性

### 1. 🖥️ Web 可视化管理后台 (Dashboard)
- **零外部数据库依赖**：纯 Go 原生构建，单二进制内嵌静态 UI 与 HTML 模板（`go:embed`），数据持久化为轻量 JSON；
- **多用户角色隔离**：
  - `admin`（管理员）：拥有全局隧道管理、端口映射配置、用户账号管理权限；
  - `user`（普通用户）：仅可查看并使用自己名下的隧道资源及部署脚本，无法越权修改配置；
- **配置实时热下发 (ConfigSync)**：在 Web 端增删改端口映射，已连接的客户端**无需重启、即刻生效**；
- **全方位可观测性**：实时在线状态、近 7 天流量统计图表、最近客户端连接审计日志；
- **移动端全面适配**：支持手机及平板响应式布局与抽屉式侧边栏菜单。

### 2. ⚡ 跨平台一键安装部署 (One-Click Deployment)
- **Linux & macOS (全面兼容 macOS 11 Big Sur 及更高版本)**：
  ```bash
  curl -sSL http://<服务端地址>:18080/install.sh | bash -s -- <隧道ID> <隧道KEY>
  ```
  自动检测系统与架构（支持 `amd64` / `arm64` / `arm`），带实时下载进度条（`curl -#`），自动拉取配置并注册为 `systemd` 或 `launchd` 开机自启常驻服务。
- **Windows (支持 x86_64 与 ARM64)**：
  ```powershell
  powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm 'http://<服务端地址>:18080/install.ps1'))) -TunnelId '<隧道ID>' -Key '<隧道KEY>'"
  ```
  自动下载对应架构二进制，写入 `%LOCALAPPDATA%\ngrok`，注册当前用户自启动（**无需管理员权限**）。

### 3. 🤖 AI Agent 专属 SKILL 接入与 API KEY 管理
- **独立 API KEY 体系**：用户可在后台自主创建、查看与撤销专用于 AI 的 API KEY（`onk-...`）；
- **服务端硬性只读约束**：API KEY 仅具备查询名下资源列表（`/api/v1/resources`）与获取技能文档的权限，无法调用任何创建、修改或删除接口；
- **一键挂载技能**：后台提供一行 AI 提示词，直接复制给 Claude、ChatGPT、Cursor、ZCode 等 AI 助手，AI 即可自动下载 SKILL 并按规范使用内网 SSH 与 Web 资源。

### 4. 🛡️ 企业级全链路安全防御
- **防内网跳板与 SSRF 防御**：服务端与客户端默认**仅允许转发 `127.0.0.1` / `localhost`**。如需代理局域网主机（如 `192.168.x.x`），需在后台显式开启「允许局域网目标」（`AllowRemoteTargets`）；
- **数据代理连接防劫持 (Anti-Spoofing)**：服务端发起连接时分配一次性 Nonce Token，客户端回传基于隧道私钥计算的 **HMAC-SHA256** 签名，彻底杜绝凭 ClientId 伪造连接池的隐患；
- **特权端口与配额保护**：禁止公网端口映射至 `< 1024` 特权端口，单隧道设置映射数量上限；
- **HTTP 子域名多租户防抢占**：数据层租户静态绑定，防止知名子域名在离线时被恶意抢占；
- **原生 HTTPS 支持**：管理后台支持配置 TLS 证书，并在 HTTPS 环境下自动为 Session Cookie 打上 `Secure` 属性。

---

## 🏛️ 系统架构

```text
 浏览器 (管理员 / 普通用户)                AI Agent (Claude / Cursor / ZCode)
       │ HTTP / HTTPS (18080)                     │ Authorization: Bearer <API_KEY>
       ▼                                          ▼
┌──────────────────────────── oneNat 服务端 ─────────────────────────────┐
│  Web 管理后台 (Dashboard) ────── API KEY 鉴权 ────── 资源列表只读接口 (/api/v1)  │
│  用户权限管理 (Admin/User) ───── JSON 存储层 ─────── 端口分配与配额限制       │
│  ConfigSync 差异化配置下发 ──── 端口监听调度 ─────── 代理连接 HMAC-SHA256 校验 │
└────────────────────────────────────┬───────────────────────────────────┘
                                     │ TLS 控制长连接 (默认 4443)
                                     ▼
                      ┌─────────────────────────────┐
                      │    oneNat 受管客户端 (ngrok)   │
                      │  - 接收服务端配置并自动创建映射 │
                      │  - LocalAddr 回环安全限制     │
                      │  - 代理流量连接池响应与签名认证 │
                      └──────────────┬──────────────┘
                                     │ 本地流量转发
                                     ▼
                        [ 目标服务: SSH / Web / TCP ]
```

---

## 🚀 快速上手

### 1. 服务端部署（公网 VPS）

#### 方式 A：直接使用发布包（推荐）
前往 [GitHub Releases](https://github.com/toddpan/onenat/releases) 下载最新发行包：

```bash
# 下载并解压
tar -xzf oneNat-r2026.09.01.tar.gz && cd oneNat-r2026.09.01

# 启动服务 (默认端口: 隧道 4443 / 管理后台 18080)
DOMAIN=你的服务器IP_或域名 WEB_ADMIN_PASS=自定义管理员密码 bash start-onenat.sh
```

启动完成后，在浏览器中打开 `http://<服务器IP>:18080`，使用账号 `admin` 和您设置的密码登录。

#### 方式 B：配置 systemd 系统常驻服务
```bash
sudo cp bin/ngrokd-linux-amd64 /usr/local/bin/ngrokd
sudo mkdir -p /opt/onenat && sudo cp -r dl packaging/onenat.service /opt/onenat/
sudo cp /opt/onenat/onenat.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now onenat
```

---

### 2. 创建隧道与客户端一键安装

1. 登录 Web 管理后台，点击 **「＋ 创建新隧道」** 并填写隧道名称；
2. 进入隧道详情页，点击 **「＋ 添加端口」**（例如映射本地 SSH `22` 端口或 Web `8080` 端口）；
3. 在详情页的 **「一键安装客户端」** 卡片中复制对应系统的安装命令：

#### 🐧 Linux / 🍎 macOS
```bash
curl -sSL http://<你的服务器>:18080/install.sh | bash -s -- <隧道ID> <隧道KEY>
```

#### 🪟 Windows (PowerShell)
```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm 'http://<你的服务器>:18080/install.ps1'))) -TunnelId '<隧道ID>' -Key '<隧道KEY>'"
```

安装完成后，客户端将自动连接服务器并显示为 `[在线]`。后续在 Web 界面增删改端口，客户端均会**自动实时同步**！

---

### 3. AI Agent (SKILL) 接入

1. 在管理后台左侧导航栏进入 **「🔑 API 密钥」** 页面，点击 **「创建」** 生成一个新的 API KEY；
2. 点击对应密钥右侧的 **「AI 安装提示词」**，复制提示词：
   ```text
   请安装 oneNat 技能: 执行 curl -s "http://<你的服务器>:18080/skill/onenat.md?key=onk-xxxx" -o onenat-skill.md, 阅读 onenat-skill.md 并按其中说明查询和使用我的隧道资源。注意: 你只有资源的使用权限, 没有创建、修改或删除权限。
   ```
3. 发送给 AI 助手，AI 将自动加载技能并获得访问内网 SSH/Web 的能力。

---

## ⚙️ 服务端配置与环境变量参数

| 环境变量 | 命令行参数 | 默认值 | 说明 |
| :--- | :--- | :--- | :--- |
| `DOMAIN` | `-domain` | `127.0.0.1` | 服务端对外域名或公网 IP |
| `TUNNEL_PORT` | `-tunnelAddr` | `4443` | 客户端 TLS 控制与隧道通信端口 |
| `HTTP_PORT` | `-httpAddr` | `""` (关闭) | HTTP 虚拟主机公网入口端口（开启填 `80` 等） |
| `HTTPS_PORT` | `-httpsAddr` | `""` (关闭) | HTTPS 虚拟主机公网入口端口（开启填 `443` 等） |
| `WEB_PORT` | `-webAddr` | `18080` | Web 管理后台监听端口（设为空则禁用后台） |
| `WEB_DATA` | `-webData` | `./onenat-dashboard.json` | 用户、隧道及 API KEY 数据存储路径 |
| `WEB_ADMIN_PASS`| `-webAdminPass`| 随机生成 | 初始管理员密码（首次初始化时生效） |
| `WEB_TLS_CERT` | `-webTlsCrt` | `""` | 管理后台 HTTPS 证书路径（开启原生 HTTPS） |
| `WEB_TLS_KEY` | `-webTlsKey` | `""` | 管理后台 HTTPS 私钥路径 |
| `DL_DIR` | `-dlDir` | `./dl` | 客户端二进制分发目录（用于一键安装） |

---

## 🛠️ 源码编译与构建

项目采用 Go 标准构建体系，无 CGO 依赖，支持全平台交叉编译：

```bash
# 1. 克隆代码
git clone https://github.com/toddpan/onenat.git && cd onenat

# 2. 设置 GOPATH 环境
export GOPATH=$(pwd)
export GO111MODULE=off

# 3. 编译当前平台服务端与客户端
go build -tags debug -o bin/ngrokd ngrok/main/ngrokd
go build -tags debug -o bin/ngrok  ngrok/main/ngrok

# 4. 交叉编译全平台客户端分发包 (输出至 dl/)
make release-clients

# 5. 运行完整单元测试与端到端回归测试 (57 项断言)
bash test-dashboard-e2e.sh
```

---

## 📄 开源许可证

本项目基于 [MIT 许可证](LICENSE) 开源。

---

## 💡 致谢与声明

oneNat 协议与隧道底层分支自早期开源版本 ngrok v1。感谢原作者与开源社区的贡献。oneNat 在此基础上进行了全面的架构升级、Web 管理控制台重构、动态受管模式扩展与现代安全加固。
