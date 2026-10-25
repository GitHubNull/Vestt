BACKEND  := src/backend
WEB_DIR  := src/frontend
DIST_DIR := $(WEB_DIR)/dist
BIN_DIR  := dist
VERSION  ?= 0.1.0-dev
RELEASE_DIR := $(BIN_DIR)/release
GOLANGCI ?= $(shell command -v golangci-lint 2>/dev/null || echo $(HOME)/go/bin/golangci-lint)
LDFLAGS  := -s -w -X main.vesttVersion=$(VERSION)

.PHONY: web build test cover lint release smoke clean

web:            ## 安装依赖并构建前端
	pnpm --dir $(WEB_DIR) install --frozen-lockfile
	pnpm --dir $(WEB_DIR) build
	rsync -a --delete $(DIST_DIR)/ $(BACKEND)/server/static/dist/

build: web      ## 产出单二进制
	go build -C $(BACKEND) -trimpath -ldflags "$(LDFLAGS)" -o ../../$(BIN_DIR)/vestt ./cmd/vestt

test:           ## SPEC-14 质量门禁：后端单测(含覆盖率) + vet + golangci-lint + 覆盖率阈值 + 前端 typecheck/eslint/vitest
	@mkdir -p $(BIN_DIR)
	go test -C $(BACKEND) -coverprofile=../../$(BIN_DIR)/cover.out ./...
	go vet -C $(BACKEND) ./...
	@test -x "$(GOLANGCI)" || { echo "golangci-lint not found at '$(GOLANGCI)'"; echo "install: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; exit 1; }
	cd $(BACKEND) && $(GOLANGCI) run ./...
	scripts/check-coverage.sh $(BIN_DIR)/cover.out
	pnpm --dir $(WEB_DIR) typecheck
	pnpm --dir $(WEB_DIR) lint
	pnpm --dir $(WEB_DIR) test

lint:
	@files=$$(gofmt -l $(BACKEND)); if [ -n "$$files" ]; then echo "$$files"; exit 1; fi

release: web    ## SPEC-14 发布矩阵：6 平台二进制 + SHA256SUMS + 前端静态包
	@mkdir -p $(RELEASE_DIR)
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -C $(BACKEND) -trimpath -ldflags "$(LDFLAGS)" -o ../../$(RELEASE_DIR)/vestt-v$(VERSION)-linux-amd64 ./cmd/vestt
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -C $(BACKEND) -trimpath -ldflags "$(LDFLAGS)" -o ../../$(RELEASE_DIR)/vestt-v$(VERSION)-linux-arm64 ./cmd/vestt
	GOOS=linux   GOARCH=arm   GOARM=7 CGO_ENABLED=0 go build -C $(BACKEND) -trimpath -ldflags "$(LDFLAGS)" -o ../../$(RELEASE_DIR)/vestt-v$(VERSION)-linux-arm ./cmd/vestt
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -C $(BACKEND) -trimpath -ldflags "$(LDFLAGS)" -o ../../$(RELEASE_DIR)/vestt-v$(VERSION)-windows-amd64.exe ./cmd/vestt
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -C $(BACKEND) -trimpath -ldflags "$(LDFLAGS)" -o ../../$(RELEASE_DIR)/vestt-v$(VERSION)-darwin-amd64 ./cmd/vestt
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -C $(BACKEND) -trimpath -ldflags "$(LDFLAGS)" -o ../../$(RELEASE_DIR)/vestt-v$(VERSION)-darwin-arm64 ./cmd/vestt
	tar -czf $(RELEASE_DIR)/vestt-web-v$(VERSION).tar.gz -C $(DIST_DIR) .
	cd $(RELEASE_DIR) && sha256sum vestt-v$(VERSION)-* > SHA256SUMS
	@echo "release artifacts in $(RELEASE_DIR)"

smoke:          ## SPEC-14 L3 冒烟：构建产物启动 → /health → caps → 假站点搜索 → 下载代理
	go test -C $(BACKEND) ./test/smoke/ -count=1

run: build
	$(BIN_DIR)/vestt --port 9117

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR) $(BACKEND)/server/static/dist
