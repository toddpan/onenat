.PHONY: default server client deps fmt clean all release-all assets client-assets server-assets contributors release-clients
export GOPATH:=$(shell pwd)
export GO111MODULE:=off

GO ?= $(shell if [ -x /Users/tsbj/.toolchains/go1.24/bin/go ]; then echo /Users/tsbj/.toolchains/go1.24/bin/go; else which go; fi)
BUILDTAGS=debug
default: all

deps: assets
	$(GO) get -tags '$(BUILDTAGS)' -d -v ngrok/...

server: deps
	$(GO) install -tags '$(BUILDTAGS)' ngrok/main/ngrokd

fmt:
	$(GO) fmt ngrok/...

client: deps
	$(GO) install -tags '$(BUILDTAGS)' ngrok/main/ngrok

assets: client-assets server-assets

bin/go-bindata:
	GOOS="" GOARCH="" $(GO) get github.com/jteeuwen/go-bindata/go-bindata

client-assets: bin/go-bindata
	bin/go-bindata -nomemcopy -pkg=assets -tags=$(BUILDTAGS) \
		-debug=$(if $(findstring debug,$(BUILDTAGS)),true,false) \
		-o=src/ngrok/client/assets/assets_$(BUILDTAGS).go \
		assets/client/...

server-assets: bin/go-bindata
	bin/go-bindata -nomemcopy -pkg=assets -tags=$(BUILDTAGS) \
		-debug=$(if $(findstring debug,$(BUILDTAGS)),true,false) \
		-o=src/ngrok/server/assets/assets_$(BUILDTAGS).go \
		assets/server/...

release-client: BUILDTAGS=release
release-client: client

release-server: BUILDTAGS=release
release-server: server

# 交叉编译客户端产物到 ./dl/ (管理后台 /dl/ 分发用)
release-clients:
	mkdir -p dl
	GOOS=linux   GOARCH=amd64 $(GO) build -tags '$(BUILDTAGS)' -o dl/ngrok_linux_amd64      ngrok/main/ngrok
	GOOS=linux   GOARCH=arm64 $(GO) build -tags '$(BUILDTAGS)' -o dl/ngrok_linux_arm64      ngrok/main/ngrok
	GOOS=linux   GOARCH=arm   $(GO) build -tags '$(BUILDTAGS)' -o dl/ngrok_linux_arm        ngrok/main/ngrok
	GOOS=darwin  GOARCH=arm64 $(GO) build -tags '$(BUILDTAGS)' -o dl/ngrok_darwin_arm64     ngrok/main/ngrok
	GOOS=darwin  GOARCH=amd64 $(GO) build -tags '$(BUILDTAGS)' -o dl/ngrok_darwin_amd64     ngrok/main/ngrok
	GOOS=windows GOARCH=amd64 $(GO) build -tags '$(BUILDTAGS)' -o dl/ngrok_windows_amd64.exe ngrok/main/ngrok
	GOOS=windows GOARCH=arm64 $(GO) build -tags '$(BUILDTAGS)' -o dl/ngrok_windows_arm64.exe ngrok/main/ngrok

release-all: fmt release-client release-server

all: fmt client server

clean:
	$(GO) clean -i -r ngrok/...
	rm -rf src/ngrok/client/assets/ src/ngrok/server/assets/

contributors:
	echo "Contributors to ngrok, both large and small:\n" > CONTRIBUTORS
	git log --raw | grep "^Author: " | sort | uniq | cut -d ' ' -f2- | sed 's/^/- /' | cut -d '<' -f1 >> CONTRIBUTORS
