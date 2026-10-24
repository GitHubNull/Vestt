# SPEC-01 工程脚手架与构建体系

| 项 | 内容 |
|---|---|
| 规模 | S |
| 前置依赖 | 无 |
| 产出 | 可构建、可运行、可测试的空工程骨架（Go + Vue 双端 + Makefile + embed 链路） |

## 1. 目标与范围

建立仓库骨架，使后续所有 SPEC 在统一结构上增量开发。本 SPEC 不实现任何业务功能，
但要求 `/health` 端点与前端空页面可联调。

范围：Go module、目录骨架、Makefile、Vite 前端工程、静态资源 embed、基础开发命令。
不在范围：路由内容、SQLite、日志框架选型落码（SPEC-02）。

## 2. 上游参考

| 参考文件 | 用途 |
|---|---|
| `src/Jackett.Server/Program.cs` | 启动参数语义（port/basepathoverride 等） |
| `src/Jackett.Server/Startup.cs` | 中间件顺序与静态文件服务的装配位置（后续 SPEC 复用） |
| `src/Jackett.Common/Jackett.Common.csproj` | 资源（Content）随库打包的方式 → 对应 go:embed |

## 3. 实现步骤与代码结构

### 3.1 Go 模块（`src/backend/`）

1. `cd src/backend && go mod init vestt`（Go ≥ 1.23），`go.work` 不引入（单 module）。
2. 创建目录（空包可含 `doc.go` 占位以保证提交有效）：
   ```
   src/backend/cmd/vestt/main.go
   src/backend/common/{models,models/config,indexer,indexer/meta,indexer/manual,indexer/shared}
   src/backend/common/{engine,services,store,torznab,utils,httpclient}
   src/backend/server/{api,middleware,static}
   ```
3. `src/backend/server/static/embed.go`：
   ```go
   package static
   import "embed"
   //go:embed all:dist
   var Dist embed.FS   // 构建前由 Makefile 将 src/frontend/dist 同步至此；先提交 .gitkeep 占位说明文件
   ```
4. `src/backend/cmd/vestt/main.go`：
   - flags：`--port`（默认 9117）、`--data-dir`、`--basepathoverride`、`--loglevel`（info|debug）。
   - 打印启动横幅（版本 = `debug.ReadBuildInfo` + `-ldflags -X` 注入的 `vesttVersion`）。
   - 启动 `net/http` 临时 server：仅 `GET /health` 返回 `{"status":"ok","version":...}`。
   - `SIGINT/SIGTERM` → `server.Shutdown(ctx)` 优雅退出。

### 3.2 前端（`src/frontend/`）

1. `pnpm create vite src/frontend --template vue-ts`；`pnpm --dir src/frontend install`。
2. `src/frontend/package.json` scripts：`dev`、`build`、`preview`、`typecheck`、`lint`。
3. 安装并最小接线：`primevue @primeuix/themes primeicons pinia vue-router`（完整装配在 SPEC-11，
   本步只要求 `main.ts` 能挂载空 `App.vue` 显示 "Vestt" 占位）。
4. `vite.config.ts`：
   ```ts
   export default defineConfig({
     base: './',
     server: {
       proxy: {
         '/api': 'http://127.0.0.1:9117',
         '/UI': 'http://127.0.0.1:9117',
         '/dl|/img|/bh|/health': 'http://127.0.0.1:9117',
       },
     },
     build: { target: 'es2020', outDir: 'dist' },
   });
   ```
   （proxy 键值形式按 Vite 文档修正为合法配置；目标是 dev 时 API 请求直通后端。）

前端产物 `src/frontend/dist/` 双用途：既作 embed 源（Makefile 同步进 `src/backend/server/static/dist`），
也可直接交外部静态服务器（nginx）托管（部署形态见 architecture.md §7.1，配置随 SPEC-14 交付）。

### 3.3 Makefile（仓库根）

```make
BACKEND  := src/backend
WEB_DIR  := src/frontend
DIST_DIR := $(WEB_DIR)/dist
BIN_DIR  := dist
VERSION  ?= 0.1.0-dev

.PHONY: web build test run clean lint

web:            ## 安装依赖并构建前端
	pnpm --dir $(WEB_DIR) install --frozen-lockfile
	pnpm --dir $(WEB_DIR) build
	rsync -a --delete $(DIST_DIR)/ $(BACKEND)/server/static/dist/

build: web      ## 产出单二进制
	go build -C $(BACKEND) -trimpath -ldflags "-s -w -X main.vesttVersion=$(VERSION)" -o ../$(BIN_DIR)/vestt ./cmd/vestt

test:
	go test -C $(BACKEND) ./...
	go vet -C $(BACKEND) ./...

run: build
	$(BIN_DIR)/vestt --port 9117

lint:
	gofmt -l $(BACKEND) | tee /dev/stderr | (! read)

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR) $(BACKEND)/server/static/dist
```

（`rsync` 不可用时以 `cp -a` 等价实现；跨平台细节在 SPEC-14 完善。）

### 3.4 版本注入

- `src/backend/cmd/vestt/version.go`：`var vesttVersion = "dev"`，Makefile `-ldflags -X` 覆盖。
- 后续所有 API 的 `app_version` 字段读取此值（对齐 backend-api.md）。

## 4. 接口定义

本 SPEC 仅暴露：

| 端点 | 契约 |
|---|---|
| `GET /health` | backend-api.md §6 |

## 5. 验收标准与验证方法

1. `make build` 一次成功产出 `dist/vestt`，`file dist/vestt` 为可执行 ELF。
2. `./dist/vestt --port 9199` 后：`curl -fsS http://127.0.0.1:9199/health` 返回 JSON 且含版本号；
   `Ctrl+C` 进程以 0 退出（无 goroutine 泄漏告警）。
3. `pnpm --dir src/frontend typecheck && pnpm --dir src/frontend build` 成功；`src/frontend/dist` 同步进 `src/backend/server/static/dist`。
4. `go vet` / `gofmt` 无输出；`make test` 通过（当前为占位测试包）。
5. 目录结构与 [architecture.md §2](../../tech/architecture.md) 完全一致（复核清单走查）。
6. 更新 `doc/roadmap/roadmap.md` 进度表（SPEC-01 → 完成）。

## 澄清记录

1. **Makefile `-o` 路径修正**：`go build -C src/backend` 之后 `-o` 相对路径基于
   `src/backend/`，原 `-o ../$(BIN_DIR)/vestt` 会产出到 `src/dist/`；已改为
   `-o ../../$(BIN_DIR)/vestt`。
2. **lint 目标 POSIX 兼容**：原 `gofmt -l $(BACKEND) | tee /dev/stderr | (! read)`
   在 make 默认 `/bin/sh`（dash）下报 `read: arg count`；改为等价的
   `files=$$(gofmt -l ...); [ -n ] && exit 1` 判断，语义不变（有未格式化文件即失败）。
3. **vite proxy 配置合法化**：原 `'/dl|/img|/bh|/health': '...'` 单键不是 Vite
   合法 proxy key（不会按正则拆分）；拆为 `/dl`、`/img`、`/bh`、`/health` 四个独立键，
   目标一致。
4. **.gitkeep 例外**：`.gitignore` 忽略整个 `src/backend/server/static/dist/`，
   追加 `!src/backend/server/static/dist/.gitkeep` 使占位文件可提交（SPEC 要求）。
5. **依赖版本锁定**：`primevue` 锁定 4.x（`^4.5.5`，对齐文档基线 PrimeVue 4）；
   `@primeuix/themes` 走独立版本线装 `^3.1.0`；`vite@8`、`vue-router@5`、`pnpm@10`
   为 2026-10 当前主版本，与文档"Node ≥ 20、pnpm ≥ 9"约束兼容。
