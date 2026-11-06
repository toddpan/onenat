#!/usr/bin/env bash
# =============================================================================
# oneNat GitHub Release 发布脚本 (本地手工发版用; CI 见 .github/workflows/release-client.yml)
#
# 为什么要有这个脚本:
#   仓库原本**没有任何** Release 自动化, 历史 release 全靠人工拖文件上传, 于是踩过
#   这些坑 —— r2026.09.07 的 tarball 里装的其实还是改名前的 ngrok 二进制、校验清单
#   漏文件、漏平台。本脚本把「校验 → 生成说明 → 上传」固化成一条命令, 让发版可复现。
#
# 前置条件:
#   1) 已产出资产:  make release-assets TAG=r2026.11.06
#      (等价于: make release-clients && python3 packaging/release_client.py --tag r2026.11.06)
#   2) 已登录 GitHub CLI:  gh auth login   (或 export GH_TOKEN=...)
#
# 用法:
#   bash packaging/publish_release.sh --tag r2026.11.06 [选项]
#
# 选项:
#   --tag TAG           发行标签 (必填), 形如 r2026.11.06
#   --repo OWNER/NAME   目标仓库 (默认 toddpan/onenat)
#   --dist-dir DIR      资产目录 (默认 dist/release/<tag>)
#   --notes FILE        自定义 release 说明文件 (默认自动生成到 dist/release/<tag>.notes.md)
#   --title TEXT        release 标题 (默认 "oneNat Release <tag>")
#   --target REF        新建 tag 时指向的 commit/分支 (默认当前 HEAD)
#   --prerelease        标记为预发布
#   --draft             先建草稿 (不公开)
#   --yes               不交互确认
#   --dry-run           只打印将要执行的 gh 命令, 不真的上传
#   --skip-verify       跳过 checksums.txt 校验 (不建议)
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

TAG=""
REPO="toddpan/onenat"
DIST_DIR=""
NOTES_FILE=""
TITLE=""
TARGET=""
PRERELEASE=0
DRAFT=0
ASSUME_YES=0
DRY_RUN=0
SKIP_VERIFY=0

info() { printf '\033[36m[..]\033[0m %s\n' "$*"; }
ok()   { printf '\033[32m[OK]\033[0m  %s\n' "$*"; }
err()  { printf '\033[31m[ERR]\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --tag)         TAG="${2:?--tag 需要取值}"; shift 2 ;;
    --repo)        REPO="${2:?--repo 需要取值}"; shift 2 ;;
    --dist-dir)    DIST_DIR="${2:?--dist-dir 需要取值}"; shift 2 ;;
    --notes)       NOTES_FILE="${2:?--notes 需要取值}"; shift 2 ;;
    --title)       TITLE="${2:?--title 需要取值}"; shift 2 ;;
    --target)      TARGET="${2:?--target 需要取值}"; shift 2 ;;
    --prerelease)  PRERELEASE=1; shift ;;
    --draft)       DRAFT=1; shift ;;
    --yes|-y)      ASSUME_YES=1; shift ;;
    --dry-run)     DRY_RUN=1; shift ;;
    --skip-verify) SKIP_VERIFY=1; shift ;;
    -h|--help)     usage 0 ;;
    *)             err "未知参数: $1 (用 --help 看用法)" ;;
  esac
done

[ -n "$TAG" ] || err "缺少 --tag (例: --tag r2026.11.06)"
if ! printf '%s' "$TAG" | grep -Eq '^r[0-9]{4}\.[0-9]{2}\.[0-9]{2}(-[0-9A-Za-z.]+)?$'; then
  err "发行标签格式应为 rYYYY.MM.DD (可选 -后缀), 实际: $TAG"
fi

[ -n "$DIST_DIR" ] || DIST_DIR="dist/release/$TAG"
[ -n "$TITLE" ]   || TITLE="oneNat Release $TAG"

# 资产是在 DIST_DIR 里 `cd` 之后按 basename 传给 gh 的，而 --notes-file 也在
# 那之后按相对路径解析 —— 两者基准不同，必然一个失败。统一先转成绝对路径。
DIST_DIR="$(cd "$DIST_DIR" 2>/dev/null && pwd || echo "$REPO_ROOT/$DIST_DIR")"

# ---------------------------------------------------------------------------
# 1. 前置检查
# ---------------------------------------------------------------------------
command -v gh >/dev/null 2>&1 || err "找不到 gh (GitHub CLI), 装好并 gh auth login 后重试"

if [ -z "${GH_TOKEN:-}" ]; then
  gh auth status >/dev/null 2>&1 || err "gh 未登录: 先执行 gh auth login, 或 export GH_TOKEN=<token>"
fi

[ -d "$DIST_DIR" ] || err "资产目录不存在: $DIST_DIR
       先产出资产: make release-assets TAG=$TAG"

CHECKSUM_FILE="$DIST_DIR/checksums.txt"
[ -f "$CHECKSUM_FILE" ] || err "缺少校验清单: $CHECKSUM_FILE
       资产必须由 packaging/release_client.py 产出 (它会一并写 checksums.txt)"

# 只上传资产文件本身, 不把说明文件/临时文件带上
# 注意: 用 while read 而不是 mapfile —— macOS 自带 bash 是 3.2, 没有 mapfile。
ASSETS=()
while IFS= read -r _f; do
  [ -n "$_f" ] && ASSETS+=("$_f")
done < <(cd "$DIST_DIR" && ls -1 | grep -v '^\.' | grep -v '^RELEASE_NOTES' || true)
[ "${#ASSETS[@]}" -gt 0 ] || err "资产目录是空的: $DIST_DIR"

# ---------------------------------------------------------------------------
# 2. 校验 sha256 (发版前的最后一道闸)
# ---------------------------------------------------------------------------
if [ "$SKIP_VERIFY" = "1" ]; then
  info "已指定 --skip-verify, 跳过 sha256 校验 (不建议)"
elif command -v python3 >/dev/null 2>&1; then
  info "校验 checksums.txt (sha256) ..."
  ( cd "$DIST_DIR" && python3 - <<'PYEOF'
import hashlib, os, re, sys

LINE = re.compile(r"^([0-9a-f]{64})  (\S+)$")
bad = 0
with open("checksums.txt", "r", encoding="utf-8") as fh:
    for raw in fh:
        line = raw.rstrip("\n")
        if not line.strip():
            continue
        m = LINE.match(line)
        if not m:
            print("格式不符 (应为 '<64位hex>  <file>'): %r" % line)
            bad += 1
            continue
        digest, name = m.group(1), m.group(2)
        if not os.path.isfile(name):
            print("清单指向的文件不存在: %s" % name)
            bad += 1
            continue
        h = hashlib.sha256()
        with open(name, "rb") as bf:
            for chunk in iter(lambda: bf.read(1 << 20), b""):
                h.update(chunk)
        if h.hexdigest() != digest:
            print("sha256 不符: %s" % name)
            bad += 1
if bad:
    sys.exit(1)
print("checksums.txt 校验通过")
PYEOF
  ) || err "sha256 校验失败, 已中止发布"
  # 额外拦截: 自检用的假二进制绝不能发到 GitHub Release (那是给用户下载执行的程序)
  if grep -rlq -- "FAKE-ONENAT-CLIENT" "$DIST_DIR" 2>/dev/null; then
    err "$DIST_DIR 里有自检用的假二进制 (FAKE-ONENAT-CLIENT), 拒绝发布"
  fi
elif command -v sha256sum >/dev/null 2>&1; then
  info "校验 checksums.txt (sha256sum -c) ..."
  ( cd "$DIST_DIR" && sha256sum -c checksums.txt ) || err "sha256 校验失败, 已中止发布"
elif command -v shasum >/dev/null 2>&1; then
  info "校验 checksums.txt (shasum -a 256 -c) ..."
  ( cd "$DIST_DIR" && shasum -a 256 -c checksums.txt ) || err "sha256 校验失败, 已中止发布"
else
  err "本机既没有 python3 也没有 sha256sum/shasum, 无法校验 (或显式加 --skip-verify)"
fi

# ---------------------------------------------------------------------------
# 3. 生成 release 说明
# ---------------------------------------------------------------------------
if [ -z "$NOTES_FILE" ]; then
  NOTES_FILE="dist/release/$TAG.notes.md"
  mkdir -p "$(dirname "$NOTES_FILE")"
  {
    echo "# oneNat Release $TAG"
    echo
    echo "oneNat 客户端 (隧道代理客户端) 发行版。客户端在远端机器运行, 连到 oneNat 服务端"
    echo "建立反向隧道, 把本机服务暴露成公网入口。"
    echo
    echo "## 📦 客户端资产 (GitHub Release Assets)"
    echo
    echo "| 文件名 | 形态 | 平台 | sha256 |"
    echo "| :--- | :--- | :--- | :--- |"
    while read -r digest name; do
      [ -n "$name" ] || continue
      case "$name" in
        onenat-client_*)
          kind="裸二进制"
          rest="${name#onenat-client_}"; rest="${rest%.exe}"
          os="${rest%%_*}"; arch="${rest#*_}" ;;
        onenat-client-*)
          kind="归档"
          base="${name#onenat-client-}"; base="${base%.tar.gz}"; base="${base%.zip}"
          os="${base%%-*}"; arch="${base#*-}" ;;
        checksums.txt)
          kind="校验清单"; os="-"; arch="-" ;;
        *)
          kind="其它"; os="-"; arch="-" ;;
      esac
      printf '| `%s` | %s | %s/%s | `%s` |\n' "$name" "$kind" "$os" "$arch" "$digest"
    done < "$CHECKSUM_FILE"
    echo '| `checksums.txt` | 校验清单 | 全部资产 | (本清单不含自身) |'
    echo
    echo "## 🔐 校验"
    echo
    echo '```bash'
    echo "# 下载并校验 (sha256, 格式 \"<hex>  <file>\")"
    echo "sha256sum -c checksums.txt          # Linux"
    echo "shasum -a 256 -c checksums.txt      # macOS"
    echo '```'
    echo
    echo "## 📥 使用"
    echo
    echo "- **裸二进制** (下划线命名): 下载后 \`chmod +x\` 即可直接运行, 无需解包 ——"
    echo "  下游工具 (如 dsh-onenat 插件) 就是按这个形态消费的。"
    echo "- **归档** (连字符命名): 兼容历史 release 命名, 便于人工下载; 内部单文件"
    echo "  (\`onenat-client\` / Windows 为 \`onenat-client.exe\`)。"
    echo
    echo "## 🔐 客户端是用 \`-tags release\` 编译的"
    echo
    echo "release tag 的客户端**只信任内嵌的 ngrokroot.crt**, 不会跳过服务端证书校验"
    echo "(\`src/ngrok/client/debug.go\` 的 debug tag 版本会跳过校验, 绝不对外分发)。"
  } > "$NOTES_FILE"
  info "release 说明已生成: $NOTES_FILE"
elif [ ! -f "$NOTES_FILE" ]; then
  err "说明文件不存在: $NOTES_FILE"
fi

# ---------------------------------------------------------------------------
# 4. 确认 + 上传
# ---------------------------------------------------------------------------
if [ -z "$TARGET" ]; then
  TARGET="$(git rev-parse HEAD 2>/dev/null || echo '')"
fi

info "目标仓库 : $REPO"
info "发行标签 : $TAG"
info "资产目录 : $DIST_DIR"
info "资产个数 : ${#ASSETS[@]}"

# gh 会在 DIST_DIR 里执行，--notes-file 却按当前工作目录解析 —— 基准不同必然失败。
# 先绝对化，dry-run 打印出来的也是真实会用的路径。
case "$NOTES_FILE" in
  /*) ;;
  *) NOTES_FILE="$REPO_ROOT/$NOTES_FILE" ;;
esac
[ -f "$NOTES_FILE" ] || err "release 说明文件不存在: $NOTES_FILE"

if [ "$DRY_RUN" = "1" ]; then
  echo
  echo "===== DRY-RUN: 以下命令不会被执行 ====="
  if gh release view "$TAG" --repo "$REPO" >/dev/null 2>&1; then
    echo "gh release upload $TAG ${ASSETS[*]} --repo $REPO --clobber"
  else
    echo "gh release create $TAG ${ASSETS[*]} --repo $REPO --title \"$TITLE\" \\"
    echo "    --notes-file $NOTES_FILE${TARGET:+ --target $TARGET}"
    [ "$PRERELEASE" = "1" ] && echo "    --prerelease"
    [ "$DRAFT" = "1" ] && echo "    --draft"
  fi
  echo "========================================"
  exit 0
fi

if [ "$ASSUME_YES" != "1" ]; then
  printf '确认发布 %s 到 %s ? 输入 yes 继续: ' "$TAG" "$REPO"
  read -r answer
  [ "$answer" = "yes" ] || err "已取消"
fi

if gh release view "$TAG" --repo "$REPO" >/dev/null 2>&1; then
  info "release $TAG 已存在, 覆盖上传同名资产 ..."
  ( cd "$DIST_DIR" && gh release upload "$TAG" "${ASSETS[@]}" --repo "$REPO" --clobber )
  ok "资产已覆盖上传"
else
  info "新建 release $TAG ..."
  create_args=( "$TAG" --repo "$REPO" --title "$TITLE" --notes-file "$NOTES_FILE" )
  [ -n "$TARGET" ] && create_args+=( --target "$TARGET" )
  [ "$PRERELEASE" = "1" ] && create_args+=( --prerelease )
  [ "$DRAFT" = "1" ] && create_args+=( --draft )
  ( cd "$DIST_DIR" && gh release create "${create_args[@]}" "${ASSETS[@]}" )
  ok "release 已创建"
fi

ok "发布完成: https://github.com/$REPO/releases/tag/$TAG"
echo
echo "下游可直接下载 (示例):"
echo "  https://github.com/$REPO/releases/download/$TAG/onenat-client_linux_amd64"
echo "  https://github.com/$REPO/releases/download/$TAG/checksums.txt"
