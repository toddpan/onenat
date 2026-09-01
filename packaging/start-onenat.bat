@echo off
rem =====================================================================
rem  oneNat Windows 启动脚本 (前台运行, Ctrl+C 停止)
rem  用法: 双击运行, 或在 cmd/PowerShell 里执行 start-onenat.bat
rem
rem  常用环境变量 (与 start-onenat.sh 同名同义):
rem    DOMAIN       对外域名      (默认 127.0.0.1)
rem    TUNNEL_PORT  客户端隧道口  (默认 4443)
rem    HTTP_PORT    公网 http 口  (默认 80; 设为 off 关闭)
rem    HTTPS_PORT   公网 https 口 (默认关闭, 配合 TLS_CERT/TLS_KEY)
rem    AUTH_TOKENS  静态密钥白名单, 逗号分隔
rem    WEB_PORT     管理后台端口  (默认 18080; 设为 off 关闭)
rem    WEB_ADMIN_PASS  初始 admin 密码 (仅数据文件为空时生效)
rem    WEB_DATA / DL_DIR / LOGFILE
rem =====================================================================
setlocal EnableExtensions
cd /d "%~dp0"

if "%DOMAIN%"==""          set DOMAIN=127.0.0.1
if "%TUNNEL_PORT%"==""     set TUNNEL_PORT=4443
if "%WEB_PORT%"==""        set WEB_PORT=18080
if "%WEB_DATA%"==""        set WEB_DATA=onenat-dashboard.json
if "%DL_DIR%"==""          set DL_DIR=dl
if "%LOGFILE%"==""         set LOGFILE=onenat.log
if not defined HTTP_PORT   set HTTP_PORT=80

set EXE=bin\ngrokd-windows-amd64.exe
if not exist "%EXE%" (
  echo 未找到 %EXE% , 请确认在发行包根目录下运行。
  exit /b 1
)

set ARGS=-domain %DOMAIN% -tunnelAddr :%TUNNEL_PORT% -webData %WEB_DATA% -dlDir %DL_DIR%

if /I "%WEB_PORT%"=="off" (
  echo 管理后台: 关闭
) else (
  set ARGS=%ARGS% -webAddr :%WEB_PORT%
)

if /I "%HTTP_PORT%"=="off" (
  set ARGS=%ARGS% -httpAddr ""
  echo 公网 http: 关闭
) else (
  set ARGS=%ARGS% -httpAddr :%HTTP_PORT%
)

if defined HTTPS_PORT if /I not "%HTTPS_PORT%"=="off" (
  set ARGS=%ARGS% -httpsAddr :%HTTPS_PORT%
  if defined TLS_CERT set ARGS=%ARGS% -tlsCrt %TLS_CERT%
  if defined TLS_KEY  set ARGS=%ARGS% -tlsKey %TLS_KEY%
) else (
  set ARGS=%ARGS% -httpsAddr ""
)

if defined AUTH_TOKENS    set ARGS=%ARGS% -authToken %AUTH_TOKENS%
if defined WEB_ADMIN_PASS set ARGS=%ARGS% -webAdminPass %WEB_ADMIN_PASS%

echo.
echo 启动 oneNat (前台运行, Ctrl+C 停止):
echo   %EXE% %ARGS%
echo   管理后台: http://%DOMAIN%:%WEB_PORT%   日志: %LOGFILE%
echo.
"%EXE%" %ARGS% -log %LOGFILE% -log-level INFO

endlocal
