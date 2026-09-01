# ngrokd 服务端发行包 r2026.09.01

公网隧道服务端 ngrokd + Web 管理后台 + 客户端一键安装分发，开箱即用。

## 包内容

```
ngrokd-r2026.09.01/
├── start-ngrokd.sh      # 启动脚本 (Linux/macOS, 自动选择平台二进制, nohup 常驻)
├── start-ngrokd.bat     # 启动脚本 (Windows, 前台运行, Ctrl+C 停止)
├── stop-ngrokd.sh       # 停止脚本 (Linux/macOS; Windows 直接关窗口或 Ctrl+C)
├── ngrokd.service       # systemd 服务模板 (Linux 生产常驻推荐)
├── bin/
│   ├── ngrokd-linux-amd64         # 服务端 (Linux x86_64)
│   ├── ngrokd-linux-arm64         # 服务端 (Linux arm64)
│   ├── ngrokd-darwin-arm64        # 服务端 (macOS Apple Silicon, 本地试用)
│   └── ngrokd-windows-amd64.exe   # 服务端 (Windows x86_64)
├── dl/                            # 客户端二进制 (管理后台 /dl/ 一键安装分发用)
│   ├── ngrok_linux_amd64 / ngrok_linux_arm64 / ngrok_linux_arm
│   ├── ngrok_darwin_amd64 / ngrok_darwin_arm64
│   └── ngrok_windows_amd64.exe / ngrok_windows_arm64.exe
└── README.md                      # 本文档
```

## 系统要求

- Linux x86_64 / arm64（生产），macOS、Windows Server/10+ 均可运行
- 无任何外部依赖（静态编译，无需安装运行时）
- 开放端口：**4443**（客户端隧道口，必开）、**18080**（Web 管理后台）、
  80（可选，http 隧道入口）、以及给 TCP 映射用的公网端口段

## 快速开始（3 步）

```bash
# 1. 解压
tar xzf ngrokd-r2026.09.01.tar.gz
cd ngrokd-r2026.09.01

# 2. 启动 (默认: 隧道口 4443 / 公网 http 80 / 管理后台 18080)
bash start-ngrokd.sh

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

## Windows 上运行

双击或命令行执行 `start-ngrokd.bat`（前台运行，Ctrl+C 或关窗口停止）：

```bat
rem 默认配置启动 (隧道口 4443 / http 80 / 管理后台 18080)
start-ngrokd.bat

rem 自定义示例 (cmd):
set DOMAIN=ngrok.example.com
set WEB_ADMIN_PASS=YourInitialPass
start-ngrokd.bat
```

- `HTTP_PORT=off` / `WEB_PORT=off` 可分别关闭 http 入口 / 管理后台。
- Windows 常驻建议用任务计划程序（Task Scheduler，"计算机启动时运行"）或
  NSSM 注册为系统服务；初始 admin 密码打印在控制台与 `%LOGFILE%`。
- 注意：Windows 服务端适合本地/内网测试，公网生产推荐 Linux + systemd。

## 常用环境变量（start-ngrokd.sh / start-ngrokd.bat）

| 变量 | 默认 | 说明 |
|---|---|---|
| `DOMAIN` | `127.0.0.1` | 对外域名（隧道展示地址 / 客户端配置里的 server_addr 主机名） |
| `TUNNEL_PORT` | `4443` | 客户端控制/隧道口 |
| `HTTP_PORT` | `80` | 公网 http 隧道入口，空字符串关闭 |
| `HTTPS_PORT` | 关闭 | 公网 https 隧道入口 |
| `TLS_CERT` / `TLS_KEY` | 内嵌自签证书 | 正式 TLS 证书路径（https 隧道用） |
| `AUTH_TOKENS` | 空=不校验 | 静态密钥白名单（逗号分隔）；管理后台的隧道 KEY 始终有效 |
| `WEB_PORT` | `18080` | 管理后台端口，空字符串禁用后台 |
| `WEB_DATA` | `./ngrokd-dashboard.json` | 用户/隧道数据文件 |
| `WEB_ADMIN_PASS` | 随机 | 初始 admin 密码（仅数据文件为空时生效） |
| `DL_DIR` | `./dl` | 客户端二进制分发目录 |
| `LOGFILE` | `./ngrokd.log` | 日志文件 |

示例（生产）：

```bash
sudo DOMAIN=ngrok.example.com HTTP_PORT=80 HTTPS_PORT=443 \
     TLS_CERT=/etc/ssl/ngrok.crt TLS_KEY=/etc/ssl/ngrok.key \
     WEB_ADMIN_PASS='YourInitialPass' bash start-ngrokd.sh
```

## 生产部署：systemd 常驻（推荐）

```bash
sudo cp bin/ngrokd-linux-amd64 /usr/local/bin/ngrokd
sudo mkdir -p /opt/ngrokd && sudo cp -r dl ngrokd.service /opt/ngrokd/
sudoedit /opt/ngrokd/ngrokd.service     # 修改 WorkingDirectory 与 -domain
sudo cp /opt/ngrokd/ngrokd.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ngrokd
```

初始密码查看：`grep "with password" /var/log/...`（按 unit 里配置的日志路径），
或直接用 journalctl：`journalctl -u ngrokd | grep password`。

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
- **手动运行**：`ngrok managed -config=ngrok-managed.yml`
  （配置文件由后台生成，内容仅含服务器地址与隧道 KEY）。
- **旧式 agent 模式**（不依赖后台）：`ngrok agent -server=<域名>:4443`。
- Windows 客户端：下载 `dl/ngrok_windows_amd64.exe`（x86_64）或
  `dl/ngrok_windows_arm64.exe`（ARM 设备），放到固定目录后手动运行：
  `ngrok_windows_amd64.exe -config=ngrok-managed.yml managed`
  （配置文件从管理后台 `/api/deploy?id=&key=` 获取，或直接复制详情页弹窗内容）。

## 升级与卸载

- **升级服务端**：`bash stop-ngrokd.sh` → 替换 `bin/` 下二进制 → 重新启动；
  `ngrokd-dashboard.json`（用户/隧道数据）保留即无缝升级。
- **升级客户端**：管理后台"重置密钥"后重发一键安装命令，或在客户端重跑安装
  命令（会覆盖二进制并重启服务）。
- **卸载**：`bash stop-ngrokd.sh && rm -rf <解压目录>`；systemd 方式另执行
  `sudo systemctl disable --now ngrokd && sudo rm /etc/systemd/system/ngrokd.service`。

## 安全建议

1. **改初始密码**：admin 初始密码为随机值，登录后立即重置。
2. **管理后台不要裸奔公网**：用防火墙限制 18080 来源，或由 nginx/caddy 反代并加
   TLS + BasicAuth；后台本身当前为 HTTP。
3. **固定 TCP 端口**：需要固定公网端口的映射，在后台映射里显式填写
   `remote_port`；不填则自动分配（重连尽量归还原端口，但不保证）。
4. **数据备份**：定期备份 `ngrokd-dashboard.json`（含用户与隧道 KEY，权限 0600）。
5. 隧道 KEY 即客户端凭据，泄露后在后台"重置密钥"即可吊销。

## 版本信息

- 发行标识：r2026.09.01
- 协议版本：ngrok v1.x（Proto 2 / 1.7 系改造版，含 agent 网关与管理后台扩展）
- 已知说明：流量统计与连接记录为内存态（服务重启清零）；管理后台数据为
  JSON 文件存储，适合中小规模（数十用户/数百隧道）。

## 常见问题

| 现象 | 处理 |
|---|---|
| 客户端连不上 4443 | 检查防火墙/安全组；`server_addr` 域名需解析到本机 |
| 后台打不开 | `WEB_PORT` 是否被占用/禁用；`ss -tlnp | grep 18080` |
| 一键安装提示二进制不存在 | `dl/` 目录与 `-dlDir` 是否一致；确认客户端平台有对应产物 |
| 登录密码忘了 | 停服 → 删除/备份 `ngrokd-dashboard.json` → 重启（数据清空重建）或手工替换 users 段的密码哈希 |
| TCP 隧道端口被占 | 后台映射换一个 remote_port，或点"修复隧道"重建 |
