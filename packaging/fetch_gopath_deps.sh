#!/usr/bin/env bash
# =============================================================================
# 拉取 oneNat (GOPATH 模式) 编译所需的第三方依赖到 $(GOPATH)/src/ 下
#
# 为什么不用 'go get'
# -------------------
# 本仓是 **GOPATH 模式** 工程 (Makefile 里 export GOPATH=$(pwd) / GO111MODULE=off,
# 源码在 src/ 下, 用的是 ngrok v1 时代的目录布局)。而 Go 1.22 起, GOPATH 模式下的
# 'go get' 已经被移除 —— Go 1.22 Release Notes 原文:
#
#   "go get is no longer supported outside of a module in the legacy GOPATH mode
#    (that is, with GO111MODULE=off)."
#
# 也就是说, 在 Go 1.22+ (含本仓 README 要求的 1.24) 上, Makefile 里原本的
#   $(GO) get -tags '$(BUILDTAGS)' -d -v ngrok/...
# 与
#   $(GO) get github.com/jteeuwen/go-bindata/go-bindata
# 都会直接失败, `make deps` / `make assets` / `make server` 全部起不来。
#
# ⇒ 改用 git 把依赖直接放到 $(GOPATH)/src/<import path> 下 —— 这正是 GOPATH 模式
#   编译器找包的方式。版本全部**钉死到具体 commit**, 保证构建可复现。
#
# 幂等: 已存在且 commit 一致的目录直接跳过; commit 不一致则 fetch + checkout。
#
# 用法:
#   bash packaging/fetch_gopath_deps.sh            # 拉齐所有依赖
#   bash packaging/fetch_gopath_deps.sh --force    # 强制重新对齐到钉死的 commit
#   bash packaging/fetch_gopath_deps.sh --check     # 只检查, 不联网 (缺依赖时报错)
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# GOPATH 默认取仓库根目录, 与 Makefile 的 `export GOPATH:=$(shell pwd)` 保持一致
GOPATH="${GOPATH:-$REPO_ROOT}"
export GOPATH

FORCE=0
CHECK_ONLY=0
for a in "$@"; do
  case "$a" in
    --force) FORCE=1 ;;
    --check) CHECK_ONLY=1 ;;
    -h|--help) sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "未知参数: $a" >&2; exit 2 ;;
  esac
done

info() { printf '\033[36m[..]\033[0m %s\n' "$*"; }
ok()   { printf '\033[32m[OK]\033[0m  %s\n' "$*"; }
err()  { printf '\033[31m[ERR]\033[0m %s\n' "$*" >&2; exit 1; }

command -v git >/dev/null 2>&1 || err "找不到 git"

# 依赖表: <import path>|<clone url>|<commit sha>
# (sha 由 `git ls-remote <url> <ref>` 核对过, 全部指向已存在的历史 commit)
DEPS="
github.com/gorilla/websocket|https://github.com/gorilla/websocket|ce903f6d1d961af3a8602f2842c8b1c3fca58c4d
github.com/inconshreveable/mousetrap|https://github.com/inconshreveable/mousetrap|4e8053ee7ef85a6bd26368364a6d27f1641c1d21
github.com/nsf/termbox-go|https://github.com/nsf/termbox-go|2ff630277754813b198ae96036e28e254d2c72bf
github.com/rcrowley/go-metrics|https://github.com/rcrowley/go-metrics|65e299d6c5c92718e672a9d2bc7f96e5b687eef8
gopkg.in/inconshreveable/go-update.v0|https://gopkg.in/inconshreveable/go-update.v0|d8b0b1d421aa1cbf392c05869f8abbc669bb7066
gopkg.in/yaml.v1|https://gopkg.in/yaml.v1|9f9df34309c04878acc86042b16630b0f696e1de
github.com/alecthomas/log4go|https://github.com/alecthomas/log4go|d146e6b86faab5f7e4d135cc63b749d74bf0d4a6
github.com/inconshreveable/go-vhost|https://github.com/inconshreveable/go-vhost|2bfe96c3175a7062ea73e65477a47aae030b550d
github.com/kardianos/osext|https://github.com/kardianos/osext|2bc1f35cddc0cc527b4bc3dce8578fc2a6c11384
github.com/kr/binarydist|https://github.com/kr/binarydist|88f551ae580780cc79d12ab4c218ba1ca346b83a
github.com/mattn/go-runewidth|https://github.com/mattn/go-runewidth|14e809f6d78fcf9f48ff9b70981472b64c05f754
"

missing=0
while IFS='|' read -r import_path url sha; do
  [ -n "$import_path" ] || continue
  dest="$GOPATH/src/$import_path"

  if [ -d "$dest/.git" ]; then
    cur="$(git -C "$dest" rev-parse HEAD 2>/dev/null || echo '')"
    if [ "$cur" = "$sha" ]; then
      info "跳过 (已是 $sha): $import_path"
      continue
    fi
    if [ "$CHECK_ONLY" = "1" ]; then
      printf '\033[33m[!!]\033[0m  %s: 当前 %s, 应为 %s\n' "$import_path" "${cur:0:12}" "${sha:0:12}" >&2
      missing=1
      continue
    fi
    if [ "$FORCE" = "1" ]; then
      info "更新: $import_path -> ${sha:0:12}"
      git -C "$dest" fetch --quiet --tags origin || true
      git -C "$dest" checkout --quiet "$sha"
    else
      info "已存在但版本不同 (当前 ${cur:0:12}, 期望 ${sha:0:12}), 加 --force 可对齐: $import_path"
    fi
    continue
  fi

  if [ "$CHECK_ONLY" = "1" ]; then
    printf '\033[33m[!!]\033[0m  缺失依赖: %s\n' "$import_path" >&2
    missing=1
    continue
  fi

  info "拉取 $import_path ..."
  mkdir -p "$(dirname "$dest")"
  rm -rf "$dest"
  git clone --quiet "$url" "$dest"
  git -C "$dest" checkout --quiet "$sha"
  # 回读 HEAD 确认真的落在钉死的 commit 上
  # (注解 tag 的 sha 与 commit sha 不同, 这里统一按 commit 钉, 避免静默跑偏)
  got="$(git -C "$dest" rev-parse HEAD)"
  [ "$got" = "$sha" ] || err "$import_path checkout 后 HEAD=$got, 期望 $sha"
  ok "就绪: $import_path @ ${sha:0:12}"
done <<EOF
$DEPS
EOF

if [ "$CHECK_ONLY" = "1" ]; then
  if [ "$missing" = "1" ]; then
    # 注意用单引号: 双引号里的反引号会被 shell 当成命令替换执行
    err '依赖不完整 (见上面 [!!] 行); 执行 `bash packaging/fetch_gopath_deps.sh` 补齐'
  fi
  ok "GOPATH 依赖齐全: $GOPATH/src"
  exit 0
fi

ok "GOPATH 依赖就绪: $GOPATH/src"
