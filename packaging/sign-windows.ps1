# oneNat Windows 二进制 Authenticode 签名工具
# 用途: 分发前对 dl/ngrok_windows_*.exe (及服务端 bin/ngrokd-windows-*.exe) 代码签名,
#       显著降低 SmartScreen / Defender 误报率 (典型误报: Behavior:Win32/Persistence.A!ml,
#       命中"下载落地 + Run 自启动 + 隐藏窗口"行为组合的未签名 exe)。
#
# 用法:
#   正式证书(PFX):  pwsh packaging/sign-windows.ps1 -Pfx .\codesign.pfx -PfxPass '<密码>' -Targets dl\ngrok_windows_amd64.exe, dl\ngrok_windows_arm64.exe
#   按指纹选证书:   pwsh packaging/sign-windows.ps1 -Thumbprint <证书指纹> -Targets dl\ngrok_windows_amd64.exe
#   内网自签(免费): pwsh packaging/sign-windows.ps1 -SelfSigned -Targets dl\ngrok_windows_amd64.exe
#
# 自签说明: 只消除"未知发布者"提示, 目标机需导入公钥才信任发布者:
#   Export-Certificate -Cert Cert:\CurrentUser\My\<指纹> -FilePath onenat-codesign.cer
#   目标机(管理员): Import-Certificate -FilePath .\onenat-codesign.cer -CertStoreLocation Cert:\LocalMachine\TrustedPublisher
# 公网分发建议购买 OV/EV 代码签名证书; EV 证书可直接获得 SmartScreen 云端信誉。
param(
  [Parameter(Mandatory = $true)] [string[]]$Targets,
  [string]$Pfx,
  [string]$PfxPass,
  [string]$Thumbprint,
  [switch]$SelfSigned,
  [string]$SelfSubject = "oneNat Client",
  [string]$TimestampUrl = "http://timestamp.digicert.com"
)
$ErrorActionPreference = "Stop"

function Resolve-CodeCert {
  if ($Thumbprint) {
    $c = Get-ChildItem Cert:\CurrentUser\My, Cert:\LocalMachine\My -ErrorAction SilentlyContinue |
      Where-Object { $_.Thumbprint -eq $Thumbprint } | Select-Object -First 1
    if (-not $c) { throw "未找到指纹为 $Thumbprint 的证书" }
    return $c
  }
  if ($Pfx) {
    $p = (Resolve-Path $Pfx).Path
    $pass = ConvertTo-SecureString $PfxPass -AsPlainText -Force
    return Import-PfxCertificate -FilePath $p -CertStoreLocation Cert:\CurrentUser\My -Password $pass |
      Where-Object { $_.HasPrivateKey } | Select-Object -First 1
  }
  if ($SelfSigned) {
    $old = Get-ChildItem Cert:\CurrentUser\My -ErrorAction SilentlyContinue |
      Where-Object { $_.Subject -eq "CN=$SelfSubject" -and $_.HasPrivateKey } | Select-Object -First 1
    if ($old) { Write-Host ">> 复用已有自签证书: $($old.Thumbprint)"; return $old }
    $c = New-SelfSignedCertificate -Subject "CN=$SelfSubject" -Type CodeSigningCert `
      -KeyAlgorithm RSA -KeyLength 3072 -HashAlgorithm SHA256 `
      -NotAfter (Get-Date).AddYears(5) -CertStoreLocation Cert:\CurrentUser\My
    Write-Host ">> 已生成自签代码签名证书: CN=$SelfSubject"
    Write-Host "   指纹: $($c.Thumbprint)"
    Write-Host "   导出公钥: Export-Certificate -Cert Cert:\CurrentUser\My\$($c.Thumbprint) -FilePath onenat-codesign.cer"
    return $c
  }
  throw "必须指定 -Pfx + -PfxPass、-Thumbprint、-SelfSigned 三者之一"
}

$cert = Resolve-CodeCert
Write-Host ">> 签名证书: $($cert.Subject) [$($cert.Thumbprint)]"
$fail = 0
foreach ($t in $Targets) {
  if (-not (Test-Path $t)) { Write-Warning "跳过不存在的文件: $t"; continue }
  $set = Set-AuthenticodeSignature -FilePath $t -Certificate $cert -TimestampServer $TimestampUrl -HashAlgorithm SHA256
  if ($set.Status -eq "Valid") {
    Write-Host "   [OK] $t"
  } else {
    Write-Host "   [FAIL] $t -> $($set.Status) $($set.StatusMessage)"
    $fail++
  }
}
if ($fail -gt 0) { exit 1 }
Write-Host ">> 全部签名完成。重新打包分发后, 客户机安装不再触发未签名告警。"
