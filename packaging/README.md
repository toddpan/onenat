# oneNat 服务端发行包

公网隧道服务端 + Web 管理后台 + 客户端一键安装分发 + AI SKILL，开箱即用。

> **版本号单一来源**：`{{RELEASE}}` 是占位符，打包时由 `packaging/repack.py`
> 自动替换成 `r<版本>`（版本取自 `src/ngrok/server/dashboard/version.go` 的
> `DefaultRelease` 常量）。文档里不再写死版本号，避免出现「包名是旧版本、后台显示
> 新版本」这种自相矛盾的情况。改版本只需要改 `version.go` 一处。

**功能一览**

- 隧道管理: Web 端创建隧道/端口映射(SSH、Web 等), 在线增删改实时生效
- 用户管理: 管理员 / 普通用户两级角色
- 客户端一键安装: Linux/macOS/Windows 各一行命令, 自动下载+配置+常驻
- **AI SKILL**: 用户自建 API KEY, 复制一行提示词给 AI 助手, AI 即可
  只读查询并使用你名下的隧道资源 (SSH/Web 公网入口); 无任何创建/修改权限
- **实例级凭证 (r2026.09.07)**: 同一应用多条映射指向不同机器实例时, 可在
  映射上覆盖独立凭证 (凭证跟实例走); AI 经 `GET /api/v1/mappings/:id/credentials`
  取「映射覆盖 ?? 应用默认」的有效凭证, 开关可控 (KEY 页「允许读取凭证」)、
  限速 5 次/分、逐次审计
- **凭证来源显式化 (r2026.09.15)**: 资源列表新增 `auth_inherited_shared`
  (该映射未配实例凭证、但确有别的映射共用同一应用 ⇒ 现在生效的是应用默认
  凭证, 通常只对其中一台有效); 内置 SSH 技能与平台技能 (onenat.md) 都写明了
  「应用默认 vs 映射实例」的判定与自检方法, 认证失败不再靠猜密码; 映射详情页
  对这种情况显示「继承应用默认」徽标
- 可观测: 在线状态、最近连接记录、近 7 天流量

## 包内容

```
oneNat-{{RELEASE}}/
├── start-onenat.sh      # 启动脚本 (Linux/macOS, 自动选择平台二进制, nohup 常驻)
├── start-onenat.bat     # 启动脚本 (Windows, 前台运行, Ctrl+C 停止)
├── stop-onenat.sh       # 停止脚本 (Linux/macOS; Windows 直接关窗口或 Ctrl+C)
├── onenat.service       # systemd 服务模板 (Linux 生产常驻推荐)
├── bin/
│   ├── ngrokd-linux-amd64         # 服务端 (Linux x86_64)
│   ├── ngrokd-linux-arm64         # 服务端 (Linux arm64)
│   ├── ngrokd-darwin-arm64        # 服务端 (macOS Apple Silicon, 本地试用)
│   └── ngrokd-windows-amd64.exe   # 服务端 (Windows x86_64)
├── dl/                            # 客户端二进制 (管理后台 /dl/ 一键安装分发用)
│   ├── onenat-client_linux_amd64 / onenat-client_linux_arm64 / onenat-client_linux_arm
│   ├── onenat-client_darwin_amd64 / onenat-client_darwin_arm64
│   └── onenat-client_windows_amd64.exe / onenat-client_windows_arm64.exe
└── README.md                      # 本文档
```

> 说明：服务端可执行文件沿用 ngrok 协议系命名（`ngrokd`），产品与包名为 oneNat；
> 两者通过启动脚本和文档衔接。
>
> **客户端二进制已改名为 `onenat-client_*`**（原 `ngrok_*`）。这不是 cosmetic：
> 服务端 `/dl/` 端点的 MD5 清单只收 `onenat-client_` 前缀的文件，一键安装脚本
> 也是按 `BIN_NAME="onenat-client_${os}_${arch}"` 去下载的——名字对不上，
> 烘进安装脚本的校验表就会是空的，导致每次重装都重新下载一遍。

## 系统要求

- Linux x86_64 / arm64（生产），macOS、Windows Server/10+ 均可运行
- 无任何外部依赖（静态编译，无需安装运行时）
- 开放端口：**4443**（客户端隧道口，必开）、**18080**（Web 管理后台）、
  80（可选，http 隧道入口）、以及给 TCP 映射用的公网端口段

## 快速开始（3 步）

```bash
# 1. 解压
tar xzf oneNat-{{RELEASE}}.tar.gz
cd oneNat-{{RELEASE}}

# 2. 启动 (默认: 隧道口 4443 / 公网 http 80 / 管理后台 18080)
bash start-onenat.sh

# 3. 浏览器打开 http://<服务器IP>:18080
#    初始管理员 admin / <随机密码>  — 启动输出会直接打印, 登录后请修改
```

登录后台后：创建用户 → 创建隧道 → 添加端口映射 → 在详情页复制
**一键安装命令**，到内网机器上执行即可完成客户端安装上线：

```bash
# Linux / macOS
curl -sSL http://<服务器IP>:18080/install.sh | bash -s -- <隧道ID> <KEY>
```

```powershell
# Windows (cmd / PowerShell 均可, 无需管理员)
powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm 'http://<服务器IP>:18080/install.ps1'))) -TunnelId '<隧道ID>' -Key '<KEY>'"
```

Windows 版安装到 `%LOCALAPPDATA%\ngrok`，注册**当前用户**登录自启动（注册表
HKCU Run 键），不需要管理员权限；重装/升级重复执行同一条命令即可。

内网机装好后，Web 端改端口映射对在线客户端**实时生效**，无需重装。

## 常用环境变量（start-onenat.sh / start-onenat.bat）

| 变量 | 默认 | 说明 |
|---|---|---|
| `DOMAIN` | `127.0.0.1` | 对外域名（隧道展示地址 / 客户端配置里的 server_addr 主机名） |
| `TUNNEL_PORT` | `4443` | 客户端控制/隧道口 |
| `HTTP_PORT` | `80` | 公网 http 隧道入口，空字符串关闭 |
| `HTTPS_PORT` | 关闭 | 公网 https 隧道入口 |
| `TLS_CERT` / `TLS_KEY` | 内嵌自签证书 | 正式 TLS 证书路径（https 隧道用） |
| `AUTH_TOKENS` | 空=不校验 | 静态密钥白名单（逗号分隔）；管理后台的隧道 KEY 始终有效 |
| `PORT_RANGE` | 空=不限制 | 公网端口映射范围 `min-max`（如 `30000-40000`）；范围外端口创建映射将被拒绝，自动分配端口也落在范围内 |
| `WEB_PORT` | `18080` | 管理后台端口，空字符串禁用后台 |
| `WEB_DATA` | `./onenat-dashboard.json` | 用户/隧道数据文件 |
| `WEB_ADMIN_PASS` | 随机 | 初始 admin 密码（仅数据文件为空时生效） |
| `DL_DIR` | `./dl` | 客户端二进制分发目录 |
| `LOGFILE` | `./onenat.log` | 日志文件 |

示例（生产）：

```bash
sudo DOMAIN=ngrok.example.com HTTP_PORT=80 HTTPS_PORT=443 \
     TLS_CERT=/etc/ssl/ngrok.crt TLS_KEY=/etc/ssl/ngrok.key \
     WEB_ADMIN_PASS='YourInitialPass' bash start-onenat.sh
```

## Windows 上运行

双击或命令行执行 `start-onenat.bat`（前台运行，Ctrl+C 或关窗口停止）：

```bat
rem 默认配置启动 (隧道口 4443 / http 80 / 管理后台 18080)
start-onenat.bat

rem 自定义示例 (cmd):
set DOMAIN=ngrok.example.com
set WEB_ADMIN_PASS=YourInitialPass
start-onenat.bat
```

- `HTTP_PORT=off` / `WEB_PORT=off` 可分别关闭 http 入口 / 管理后台。
- Windows 常驻建议用任务计划程序（Task Scheduler，"计算机启动时运行"）或
  NSSM 注册为系统服务；初始 admin 密码打印在控制台与 `%LOGFILE%`。
- 注意：Windows 服务端适合本地/内网测试，公网生产推荐 Linux + systemd。

## 生产部署：systemd 常驻（推荐）

```bash
sudo cp bin/ngrokd-linux-amd64 /usr/local/bin/ngrokd
sudo mkdir -p /opt/onenat && sudo cp -r dl onenat.service /opt/onenat/
sudoedit /opt/onenat/onenat.service     # 修改 WorkingDirectory 与 -domain
sudo cp /opt/onenat/onenat.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now onenat
```

初始密码查看：`journalctl -u onenat | grep password`，或按 unit 里配置的
日志路径 grep `with password`。

## 防火墙放行清单

| 端口 | 用途 | 是否必开 |
|---|---|---|
| 4443 (或自定义 TUNNEL_PORT) | 客户端 TLS 隧道口 | 必开 |
| 18080 | Web 管理后台 | 必开（建议仅内网或反代加 TLS） |
| 80 / 443 | http/https 隧道入口 | 用 http 隧道才需要 |
| TCP 映射端口段 | 客户端 TCP 隧道的公网入口 | 按需放行一段（如 20000-50000） |

## 客户端侧（内网机器）

- **一键安装**（推荐）：管理后台隧道详情页复制一行命令执行，自动下载
  `dl/` 里对应平台的客户端、拉取配置并注册 systemd/launchd 常驻。
- **手动运行**：`onenat-client managed -config=onenat-client.yml`
  （配置文件由后台生成，内容仅含服务器地址与隧道 KEY）。
- **旧式 agent 模式**（不依赖后台）：`onenat-client agent -server=<域名>:4443`。
- **Windows 客户端**：下载 `dl/onenat-client_windows_amd64.exe`（x86_64）或
  `dl/onenat-client_windows_arm64.exe`（ARM 设备），放到固定目录后手动运行：
  `onenat-client_windows_amd64.exe -config=onenat-client.yml managed`
  （配置文件从管理后台 `/api/deploy?id=&key=` 获取，或直接复制详情页弹窗内容）。

## 客户端 GitHub Release 资产清单与 sha256 校验

客户端二进制除了随发行包分发（`dl/`），还发布在 GitHub Release 上
（<https://github.com/toddpan/onenat/releases>）。**两种命名并存**，各有用途：

| 形态 | 文件名 | 说明 |
|---|---|---|
| 裸二进制 | `onenat-client_<os>_<arch>`（Windows 带 `.exe`） | 与 `dl/` 同名。下载后 `chmod +x` 直接跑，**无需解包** —— 下游工具（如 dsh-onenat 插件）消费的就是这个形态 |
| 归档 | `onenat-client-<os>-<arch>.tar.gz`（Windows 是 `.zip`） | 连字符命名，兼容历史 release 约定，便于人工下载。内部单文件 `onenat-client`（Windows 是 `onenat-client.exe`） |
| 校验清单 | `checksums.txt` | sha256，格式 `<hex>  <文件名>`（**两个空格**分隔） |

覆盖平台（7 个）：`linux/amd64`、`linux/arm64`、`linux/arm`、`darwin/arm64`、
`darwin/amd64`、`windows/amd64`、`windows/arm64`。

校验方法：

```bash
# 下载资产与 checksums.txt 到同一目录后
sha256sum -c checksums.txt        # Linux
shasum -a 256 -c checksums.txt    # macOS
```

> ⚠️ **对外分发的客户端必须用 `-tags release` 编译**。
> `src/ngrok/client/debug.go`（`// +build !release`）会把
> `useInsecureSkipVerify()` 置为 `true`，即**跳过服务端证书校验**（等于允许
> 中间人）；只有 `src/ngrok/client/release.go`（`// +build release`）才只信任
> 内嵌的 `ngrokroot.crt`。仓库的 `make release-clients` 已经强制用 release tag；
> 本地调试请用 `make debug-clients`（产物写 `dl-debug/`，**不会**进 `dl/`）。

## 发版流程（客户端 Release）

```bash
# 0. 依赖 (GOPATH 模式; Go 1.22+ 已移除 GOPATH 下的 'go get', 故用脚本拉取)
bash packaging/fetch_gopath_deps.sh

# 1. 交叉编译 7 个平台客户端 (-tags release) 到 dl/
make release-clients

# 2. 产出 Release 资产 (裸二进制 + 归档 + checksums.txt) 到 dist/release/<tag>/
make release-assets TAG=r2026.11.06        # 不传 TAG 则取 version.go 里的 DefaultRelease

# 3. 发布到 GitHub Release (需 gh auth login)
bash packaging/publish_release.sh --tag r2026.11.06

# 只想看会产出什么, 不落盘:
python3 packaging/release_client.py --tag r2026.11.06 --dry-run
# 脚本自检 (不需要 Go / 不需要 dl/):
python3 packaging/release_client.py --selftest
```

CI 版：`.github/workflows/release-client.yml`（手动触发 + 推送 `r*` 标签自动触发），
流程与上面一致，只是把 `gh release create` 换成用 `GITHUB_TOKEN` 跑同一个脚本。

## 升级与卸载

- **升级服务端**：`bash stop-onenat.sh` → 替换 `bin/` 下二进制 → 重新启动；
  `onenat-dashboard.json`（用户/隧道数据）保留即无缝升级。
- **升级客户端**：管理后台"重置密钥"后重发一键安装命令，或在客户端重跑安装
  命令（会覆盖二进制并重启服务）。
- **卸载**：`bash stop-onenat.sh && rm -rf <解压目录>`；systemd 方式另执行
  `sudo systemctl disable --now onenat && sudo rm /etc/systemd/system/onenat.service`。

## 安全防护与最佳实践

1. **改初始密码**：admin 初始密码为随机值，登录后立即重置。
2. **防内网穿透与 SSRF（默认开启）**：
   - 服务端与客户端默认**仅允许转发 127.0.0.1 / localhost 本地服务**，任何非本地 IP 均会被服务端及客户端双重拦截；
   - 如确实需要将公网流量转发给内网其他局域网主机（如 192.168.x.x），可在管理后台隧道编辑菜单中显式开启「允许局域网目标」，或客户端传入 `--allow-remote-targets` 参数。
3. **代理连接签名（Anti-Spoofing）**：
   - 客户端建立数据代理连接（Proxy Connection）时，强制校验服务端签发的一次性随机 Token 与基于隧道密钥的 HMAC-SHA256 签名，防止第三方伪造 ClientId 劫持流量连接池。
4. **子域名多租户所有权绑定**：
   - HTTP 子域名已在服务端数据层实施租户静态所有权保护，防止其他租户抢占已存在的知名子域名。
5. **特权端口保护**：
   - 公网 TCP 端口映射禁止绑定系统特权端口（< 1024）。
6. **管理后台启用 HTTPS**：
   - 可直接通过环境变量传入证书：`WEB_TLS_CERT=/path/to/cert.pem WEB_TLS_KEY=/path/to/key.pem bash start-onenat.sh`；
   - 启用 HTTPS 后系统会自动将 Session Cookie 标记为 `Secure`；
   - 亦可在生产环境前端通过 Nginx/Caddy 进行反向代理并配置 HTTPS。
7. **数据备份**：定期备份 `onenat-dashboard.json`（含用户与隧道 KEY，权限 0600）。
8. 隧道 KEY 即客户端凭据，泄露后在后台"重置密钥"即可吊销。

## 版本信息

- 发行标识：{{RELEASE}}（单一来源：`src/ngrok/server/dashboard/version.go` 的
  `DefaultRelease`；打包时由 `packaging/repack.py` 替换占位符）
- 协议版本：ngrok v1.x（Proto 2 / 1.7 系改造版，含 agent 网关与管理后台扩展）
- 已知说明：流量统计与连接记录为内存态（服务重启清零）；管理后台数据为
  JSON 文件存储，适合中小规模（数十用户/数百隧道）。

## 常见问题

| 现象 | 处理 |
|---|---|
| 客户端连不上 4443 | 检查防火墙/安全组；`server_addr` 域名需解析到本机 |
| 后台打不开 | `WEB_PORT` 是否被占用/禁用；`ss -tlnp \| grep 18080` |
| 一键安装提示二进制不存在 | `dl/` 目录与 `-dlDir` 是否一致；确认客户端平台有对应产物 |
| 登录密码忘了 | 停服 → 删除/备份 `onenat-dashboard.json` → 重启（数据清空重建）或手工替换 users 段的密码哈希 |
| TCP 隧道端口被占 | 后台映射换一个 remote_port，或点"修复隧道"重建 |
