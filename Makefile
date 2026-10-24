BACKEND  := src/backend
WEB_DIR  := src/frontend
DIST_DIR := $(WEB_DIR)/dist
BIN_DIR  := dist
VERSION  ?= 0.1.0-dev

.PHONY: web build test run lint clean

web:            ## 安装依赖并构建前端
	pnpm --dir $(WEB_DIR) install --frozen-lockfile
	pnpm --dir $(WEB_DIR) build
	rsync -a --delete $(DIST_DIR)/ $(BACKEND)/server/static/dist/

build: web      ## 产出单二进制
	go build -C $(BACKEND) -trimpath -ldflags "-s -w -X main.vesttVersion=$(VERSION)" -o ../../$(BIN_DIR)/vestt ./cmd/vestt

test:
	go test -C $(BACKEND) ./...
	go vet -C $(BACKEND) ./...

run: build
	$(BIN_DIR)/vestt --port 9117

lint:
	@files=$$(gofmt -l $(BACKEND)); if [ -n "$$files" ]; then echo "$$files"; exit 1; fi

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR) $(BACKEND)/server/static/dist
