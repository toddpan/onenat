.PHONY: default server client deps fmt clean all release-all assets client-assets server-assets contributors \
        release-client release-server release-clients debug-clients release-assets publish-release \
        check-deps selftest-release-assets

export GOPATH:=$(shell pwd)
export GO111MODULE:=off

GO ?= $(shell if [ -x /Users/tsbj/.toolchains/go1.24/bin/go ]; then echo /Users/tsbj/.toolchains/go1.24/bin/go; else which go; fi)
BUILDTAGS=debug
PYTHON ?= python3

# 发行标识 (GitHub Release 用的 tag)。默认从**单一来源**派生:
#   src/ngrok/server/dashboard/version.go 的 DefaultRelease 常量
# 发版时改那一处即可, 本 Makefile / 发行包名 / 文档都跟着变。
# 需要临时打一个测试版本时覆盖: make release-assets TAG=r2026.11.06-rc1
TAG ?= r$(shell sed -n 's/^[[:space:]]*DefaultRelease = "\(.*\)".*/\1/p' src/ngrok/server/dashboard/version.go)

# Release 资产输出根目录 (资产落在 $(DIST_ROOT)/$(TAG)/)
DIST_ROOT ?= dist/release
# 目标 GitHub 仓库
REPO ?= toddpan/onenat

default: all

# ---------------------------------------------------------------------------
# 第三方依赖 (GOPATH 模式)
#
# 为什么不用 'go get': Go 1.22 起 GOPATH 模式 (GO111MODULE=off) 下的 'go get' 已被移除
# (Go 1.22 Release Notes: "go get is no longer supported outside of a module in the
# legacy GOPATH mode"), 在 Go 1.24 上必然失败 ⇒ make deps / assets / server 全都起不来。
# 改为用 git 把依赖钉到具体 commit 放进 $(GOPATH)/src/ 下 (见 packaging/fetch_gopath_deps.sh)。
# ---------------------------------------------------------------------------
deps: assets
	bash packaging/fetch_gopath_deps.sh

# 只检查依赖是否齐备 (不联网), CI 里当守卫用
check-deps:
	bash packaging/fetch_gopath_deps.sh --check

server: deps
	$(GO) install -tags '$(BUILDTAGS)' ngrok/main/ngrokd

fmt:
	$(GO) fmt ngrok/...

client: deps
	$(GO) install -tags '$(BUILDTAGS)' ngrok/main/ngrok

assets: client-assets server-assets

# go-bindata 工具本体。
# 同样不能用 'go get' (见上), 改用 module 模式的 'go install pkg@version':
# 它与 GO111MODULE 无关, 在任何 Go 1.16+ 上都能跑, 且不受 GOPATH 模式限制。
bin/go-bindata:
	@mkdir -p bin
	GO111MODULE=on GOBIN=$(CURDIR)/bin $(GO) install github.com/jteeuwen/go-bindata/go-bindata@v3.0.7+incompatible \
	  || GO111MODULE=on GOBIN=$(CURDIR)/bin $(GO) install github.com/jteeuwen/go-bindata/go-bindata@latest

# 内嵌资源: go-bindata 按 BUILDTAGS 生成 assets_<tag>.go, 并带上 `// +build <tag>` 约束,
# 所以 debug 与 release 两套资源可以共存于同一目录, 编译时各取所需。
client-assets: bin/go-bindata
	mkdir -p src/ngrok/client/assets
	bin/go-bindata -nomemcopy -pkg=assets -tags=$(BUILDTAGS) \
		-debug=$(if $(findstring debug,$(BUILDTAGS)),true,false) \
		-o=src/ngrok/client/assets/assets_$(BUILDTAGS).go \
		assets/client/...

server-assets: bin/go-bindata
	mkdir -p src/ngrok/server/assets
	bin/go-bindata -nomemcopy -pkg=assets -tags=$(BUILDTAGS) \
		-debug=$(if $(findstring debug,$(BUILDTAGS)),true,false) \
		-o=src/ngrok/server/assets/assets_$(BUILDTAGS).go \
		assets/server/...

release-client: BUILDTAGS=release
release-client: client

release-server: BUILDTAGS=release
release-server: server

# ---------------------------------------------------------------------------
# 交叉编译客户端产物到 ./dl/ (管理后台 /dl/ 分发 + GitHub Release 资产用)
#
# ⚠️ 对外分发**必须**用 release tag, 这里的 BUILDTAGS=release 是有意为之:
#   src/ngrok/client/debug.go   (// +build !release) → useInsecureSkipVerify() == true
#   src/ngrok/client/release.go (// +build release)  → 只信任内嵌的 ngrokroot.crt
#   src/ngrok/client/model.go:154 用 useInsecureSkipVerify() 设置 InsecureSkipVerify
# ⇒ 用默认的 debug tag 编出来的客户端会**跳过服务端证书校验** (等于允许中间人),
#   绝不能对外分发 —— GitHub Release、服务端 /dl/ 端点、一键安装脚本都属于对外分发。
#   本地调试确实需要跳过校验时, 用下面的 debug-clients 目标 (产物写 dl-debug/, 不进 dl/)。
# ---------------------------------------------------------------------------
release-clients: BUILDTAGS=release
release-clients: deps
	mkdir -p dl
	GOOS=linux   GOARCH=amd64 $(GO) build -tags '$(BUILDTAGS)' -o dl/onenat-client_linux_amd64      ngrok/main/ngrok
	GOOS=linux   GOARCH=arm64 $(GO) build -tags '$(BUILDTAGS)' -o dl/onenat-client_linux_arm64      ngrok/main/ngrok
	GOOS=linux   GOARCH=arm   $(GO) build -tags '$(BUILDTAGS)' -o dl/onenat-client_linux_arm        ngrok/main/ngrok
	GOOS=darwin  GOARCH=arm64 $(GO) build -tags '$(BUILDTAGS)' -o dl/onenat-client_darwin_arm64     ngrok/main/ngrok
	GOOS=darwin  GOARCH=amd64 $(GO) build -tags '$(BUILDTAGS)' -o dl/onenat-client_darwin_amd64     ngrok/main/ngrok
	GOOS=windows GOARCH=amd64 $(GO) build -tags '$(BUILDTAGS)' -o dl/onenat-client_windows_amd64.exe ngrok/main/ngrok
	GOOS=windows GOARCH=arm64 $(GO) build -tags '$(BUILDTAGS)' -o dl/onenat-client_windows_arm64.exe ngrok/main/ngrok

# 本地调试版客户端 (debug tag ⇒ 跳过服务端证书校验, 仅供自测/联调)
# 刻意写到 dl-debug/ 而不是 dl/: dl/ 是服务端 /dl/ 端点对外下发的目录,
# 绝不能让 insecure 的调试版混进去被用户下载。
debug-clients: BUILDTAGS=debug
debug-clients: deps
	mkdir -p dl-debug
	GOOS=linux   GOARCH=amd64 $(GO) build -tags '$(BUILDTAGS)' -o dl-debug/onenat-client_linux_amd64      ngrok/main/ngrok
	GOOS=linux   GOARCH=arm64 $(GO) build -tags '$(BUILDTAGS)' -o dl-debug/onenat-client_linux_arm64      ngrok/main/ngrok
	GOOS=linux   GOARCH=arm   $(GO) build -tags '$(BUILDTAGS)' -o dl-debug/onenat-client_linux_arm        ngrok/main/ngrok
	GOOS=darwin  GOARCH=arm64 $(GO) build -tags '$(BUILDTAGS)' -o dl-debug/onenat-client_darwin_arm64     ngrok/main/ngrok
	GOOS=darwin  GOARCH=amd64 $(GO) build -tags '$(BUILDTAGS)' -o dl-debug/onenat-client_darwin_amd64     ngrok/main/ngrok
	GOOS=windows GOARCH=amd64 $(GO) build -tags '$(BUILDTAGS)' -o dl-debug/onenat-client_windows_amd64.exe ngrok/main/ngrok
	GOOS=windows GOARCH=arm64 $(GO) build -tags '$(BUILDTAGS)' -o dl-debug/onenat-client_windows_arm64.exe ngrok/main/ngrok

# ---------------------------------------------------------------------------
# GitHub Release 资产
# ---------------------------------------------------------------------------

# 产出 Release 资产 (裸二进制 + .tar.gz/.zip 归档 + sha256 校验清单) 到
# $(DIST_ROOT)/$(TAG)/。前置: release-clients 已把 7 个平台产物放进 dl/。
#   make release-assets                    # 用 version.go 派生的默认 tag
#   make release-assets TAG=r2026.11.06    # 指定 tag
#   make release-assets DRY_RUN=1          # 只看不写
release-assets: release-clients
	$(PYTHON) packaging/release_client.py --tag "$(TAG)" --out-root "$(DIST_ROOT)" $(if $(DRY_RUN),--dry-run,)

# 发到 GitHub Release (需 gh auth login)。先产出资产再发布。
publish-release: release-assets
	bash packaging/publish_release.sh --tag "$(TAG)" --repo "$(REPO)" $(if $(DRY_RUN),--dry-run,) $(if $(YES),--yes,)

# 资产脚本自检 (不需要 Go / 不需要 dl/): 造临时假二进制跑完整流水线并校验
selftest-release-assets:
	$(PYTHON) packaging/release_client.py --selftest

release-all: fmt release-client release-server

all: fmt client server

clean:
	$(GO) clean -i -r ngrok/...
	rm -rf src/ngrok/client/assets/ src/ngrok/server/assets/

contributors:
	echo "Contributors to ngrok, both large and small:\n" > CONTRIBUTORS
	git log --raw | grep "^Author: " | sort | uniq | cut -d ' ' -f2- | sed 's/^/- /' | cut -d '<' -f1 >> CONTRIBUTORS
