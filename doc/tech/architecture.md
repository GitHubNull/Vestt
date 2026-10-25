# Vestt 技术架构

| 项 | 内容 |
|---|---|
| 文档版本 | 1.0 |
| 上游基线 | Jackett 0.24.2900 |
| 关联文档 | [backend-api.md](backend-api.md)、[frontend.md](frontend.md)、[data-model.md](data-model.md)、[PRD](../product/prd.md) |

---

## 1. 架构总览

```
┌────────────────────────────────────────────────────────────────────┐
│                    vestt（单二进制，go:embed 资源）                  │
│                                                                    │
│  ┌─────────────────────────────┐   ┌─────────────────────────────┐ │
│  │ HTTP Layer (backend/server) │   │ Web UI (frontend/, Vue3)    │ │
│  │  chi router / middleware    │   │  由 server 静态托管          │ │
│  │  ─ admin API                │   └─────────────────────────────┘ │
│  │  ─ torznab / potato API     │                                   │
│  │  ─ manual search API        │                                   │
│  │  ─ dl / img / bh proxy      │                                   │
│  └──────────────┬──────────────┘                                   │
│                 │                                                  │
│  ┌──────────────▼─────────────────────────────────────────────────┐│
│  │  Application Layer (backend/common/services)                   ││
│  │  IndexerManager · CacheService · ConfigService                 ││
│  │  LogCacheService · SecurityService · FlareSolverr              ││
│  └──────────────┬──────────────────────────────┬──────────────────┘│
│                 │                              │                   │
│  ┌──────────────▼──────────────┐  ┌────────────▼─────────────────┐│
│  │ Indexer Framework           │  │ YAML Definition Engine       ││
│  │ (backend/common/indexer)    │  │ (backend/common/engine)      ││
│  │ Indexer 接口 / 注册表        │  │ schema 校验 / 模板 / 选择器   ││
│  │ BaseIndexer / MetaIndexer   │  │ 582 个 .yml（embed）         ││
│  │ 62 个 Go 手写索引器          │  │                              ││
│  └──────────────┬──────────────┘  └──────────────────────────────┘│
│                 │                                                  │
│  ┌──────────────▼─────────────────────────────────────────────────┐│
│  │ Infrastructure                                                  ││
│  │ HTTP Client 栈（限速/重试/CF Cookie/代理） · SQLite (store)      ││
│  │ slog 日志 + 环形缓冲 · bcrypt · go:embed                        ││
│  └────────────────────────────────────────────────────────────────┘│
└────────────────────────────────────────────────────────────────────┘
          │ outbound HTTPS（带 Cookie / UA / 限速）
          ▼
     上游种子站点 / FlareSolverr / OMDb
```

### 1.1 与上游的分层映射

| Jackett（C#） | Vestt（Go） | 说明 |
|---|---|---|
| `Jackett.Common/Models` | `src/backend/common/models` | TorznabQuery/ReleaseInfo/Caps/ServerConfig 等 |
| `Jackett.Common/Indexers` | `src/backend/common/indexer` | 接口、基类、62 个手写索引器 |
| `Jackett.Common/Indexers/Definitions/CardigannIndexer.cs` | `src/backend/common/engine` | YAML 定义引擎 |
| `Jackett.Common/Definitions/*.yml` | `src/backend/common/engine/definitions/`（embed） | 原样复用 |
| `Jackett.Common/Services` | `src/backend/common/services` | 应用服务 |
| `Jackett.Common/Utils/Clients` | `src/backend/common/httpclient` | Web 请求栈 |
| `Jackett.Server/Controllers` | `src/backend/server/api` | HTTP 处理器 |
| `Jackett.Common/Content` | `src/frontend/`（构建产物 embed） | 前端 |

## 2. 仓库布局

```
Vestt/
├── Makefile                     # 构建/测试/打包入口
├── LICENSE  README.md  AGENTS.md  DISCLAIMER.md
├── doc/                         # 全部工程文档（见 doc/README.md）
├── src/                         # 源码根：前后端分离开发，各自语义化目录
│   ├── backend/                 # Go 后端（module "vestt"，go.mod 位于此）≈ Jackett src/
│   │   ├── go.mod  go.sum
│   │   ├── cmd/vestt/main.go        # 入口：装配依赖、启动 HTTP 服务
│   │   ├── cmd/vestt-import/        # Jackett 配置导入工具（SPEC-14）
│   │   ├── common/                  # ≈ Jackett.Common
│   │   │   ├── models/              # TorznabQuery / ReleaseInfo / Caps / Categories
│   │   │   │   └── config/          # ServerConfig / IndexerConfig 基类
│   │   │   ├── indexer/             # Indexer 接口 / BaseIndexer / 注册表 / 元索引器
│   │   │   │   ├── indexer/manual/  # 手写索引器（批次注册 registry_batchN.go）
│   │   │   │   ├── indexer/shared/  # 共享抽象基类（Gazelle/Avistaz/Brazilian/CouchPotato）
│   │   │   │   ├── meta/            # all / aggregate / fallbacks
│   │   │   │   ├── manual/          # 62 个手写索引器（每文件一个，文件名=站点名）
│   │   │   │   └── shared/          # 手写索引器共用辅助（Gazelle、分页等）
│   │   │   ├── engine/              # YAML 定义引擎
│   │   │   │   ├── definitions/     # *.yml × 582（从上游复制，embed）
│   │   │   │   ├── schema/          # schema.json（embed）+ JSON Schema 校验
│   │   │   │   ├── template.go      # 模板变量与条件 helper
│   │   │   │   ├── converters.go    # 36 种转换器
│   │   │   │   ├── selectors.go     # goquery 选择器 + filters
│   │   │   │   └── cardigann.go     # 引擎主体（登录/搜索/下载流程）
│   │   │   ├── httpclient/          # WebRequest/WebResult/限速/CF/代理/FlareSolverr
│   │   │   ├── services/            # IndexerManager/Cache/Config/LogCache/Security…
│   │   │   ├── store/               # SQLite 访问层（见 data-model.md）
│   │   │   ├── torznab/             # XML 序列化（caps / rss / error）
│   │   │   └── utils/               # ParseUtil/DateTimeUtil/StringUtil…
│   │   ├── server/                  # ≈ Jackett.Server（HTTP 层）
│   │   │   ├── api/                 # 控制器（对齐上游 Controllers）
│   │   │   ├── middleware/          # ApiKey/CORS/BasePath/Logging/Recover
│   │   │   ├── static/              # embed 前端构建产物（dist）+ login.html
│   │   │   └── server.go            # 路由装配与启停
│   │   └── test/
│   │       ├── integration/         # 黑盒 API 集成测试（起服务打端点）
│   │       ├── smoke/               # 构建产物冒烟（SPEC-14）
│   │       └── fixtures/            # 上游 HTML/XML 快照
│   └── frontend/                # Vue 3 前端（pnpm 工程）≈ Jackett Content
│       ├── package.json  vite.config.ts  tsconfig.json
│       ├── index.html
│       └── src/{components,views,stores,services,router,types}
└── tmp/ref/Jackett-0.24.2900/   # 只读参考（gitignore）
```

约定：**前后端分离开发**——`src/backend/` 与 `src/frontend/` 各自独立成工程（后端 Go module、
前端 pnpm），互不感知对方内部结构，仅通过 backend-api.md 契约交互；构建时前端产物同步至
`src/backend/server/static/dist` 由 go:embed 打包（默认形态；同一份产物亦支持外部 nginx 托管，见 §7.1）。Go 包按上述目录归属，**不引入**
`src/backend/common` 依赖 `src/backend/server` 的反向依赖；`engine` 不感知 SQLite；
手写索引器之间禁止互相 import（可 import `indexer/shared`）。

## 3. 技术选型

| 领域 | 选型 | 理由 / 上游对应 |
|---|---|---|
| HTTP 路由 | `go-chi/chi` v5 | 轻量、路由树清晰，易表达上游 attribute routing |
| HTML 解析 | `PuerkitoBio/goquery` | CSS 选择器，YAML 引擎 selector 的直接等价物（上游 CsQuery） |
| YAML | `gopkg.in/yaml.v3` | 解析 .yml 定义 |
| JSON Schema | `santhosh-tekuri/jsonschema` v6 | 校验 .yml against schema.json（上游 Newtonsoft.Json.Schema） |
| XML | `encoding/xml` + 自定义 marshaler | Torznab caps/RSS 命名空间输出 |
| SQLite | `modernc.org/sqlite` | 纯 Go 无 CGO，交叉编译友好 |
| 日志 | `log/slog` | 结构化日志 + 自定义环形缓冲 handler |
| 密码哈希 | `golang.org/x/crypto/bcrypt` | 管理密码（上游 PBKDF2 语义等价即可，单向哈希） |
| 模板 | 自研受限模板 + `expr` 可选 | 上游 Handlebars 子集（见 engine/template.go） |
| 限速 | 自研 per-indexer 令牌桶 | 上游 RateLimiter 语义 |
| CSS/主题 | PrimeVue 4 + `@primeuix/themes` | 指定栈 |
| 状态 | Pinia | 指定栈 |
| 构建 | Make + Go embed + Vite | 单二进制交付 |

版本策略：Go ≥ 1.23；Node ≥ 20；pnpm ≥ 9；具体版本锁定在 `go.mod` / `package.json`，
升级需同步更新 `tech/architecture.md`。

## 4. 后端设计

### 4.1 依赖装配（main.go）

采用手写构造注入（不引 DI 框架），顺序与上游 `Startup.cs` 等价：

```
SQLite 打开/迁移 → Store 各 DAO
  → SecurityService(bcrypt) → ConfigService(读写 server_config)
  → LogCacheService(ring buffer, 挂 slog handler)
  → HTTPClient 栈(代理/FlareSolverr/限速，cookie 变更回调持久化到 indexer_config.cookies_json)
  → Engine(schema 校验 + 582 yml) → IndexerRegistry(内置注册 62 个手写索引器)
  → IndexerManagerService(实例化、加载已配置索引器、元索引器)
  → CacheService(SQLite + 内存索引)
  → HTTP server(chi) 装配全部 handler
```

（SPEC-05 落地状态：HTTPClient 栈、IndexerManagerService 与 Cardigann 动态工厂
（`engine.LoadAll` → 每定义一个 `engine.NewCardigann` 实例）已在 main.go 装配。
SPEC-08 落地状态：批次 1 的 21 个手写索引器经 `indexer/manual/registry_batch1.go`
init() 登记（main.go blank import 接线）；SPEC-09 批次 2（20 个，registry_batch2.go）
与 SPEC-10 批次 3（21 个，registry_batch3.go）已追加，62 个手写索引器全部落地，
`TestAllManualIndexersRegistered` 锁定注册计数（另含 582 个引擎定义与 2 个元索引器）。

### 4.2 Indexer 接口（上游 IIndexer 等价）

```go
type Indexer interface {
    ID() string
    Name() string
    Description() string
    SiteLink() string
    AlternativeSiteLinks() []string
    Language() string
    Type() string                       // public | semi-private | private
    Tags() []string
    IsConfigured() bool
    LastError() string
    Capabilities() *torznab.Capabilities
    CanHandleQuery(q *models.TorznabQuery) bool
    ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*IndexerResult, error)
    // 管理接口
    ConfigurationForSetup(ctx) (*ConfigData, error)
    ApplyConfiguration(ctx, items []ConfigItem) (ConfigStatus, error)
    LoadFromSavedConfiguration(raw []byte) error
    Unconfigure()
    Test(ctx) error
    // 下载
    Download(ctx, link *url.URL) ([]byte, error)
}
```

- `BaseIndexer` 提供通用实现（FloodLimit、Cookie 持久化、caps 缓存、错误包装）。
- `CardigannIndexer`（engine/cardigann.go）实现同一接口，驱动 YAML 定义。
- `MetaIndexer`（all/aggregate）持有已配置索引器集合，实现扇出与 `t=indexers`。

### 4.3 并发模型

- **请求扇出**：手动搜索/Torznab 元索引器对子索引器并发（`errgroup` + 每请求总超时 120s），
  单个失败按 `IndexerError` 归档进分项状态，不取消其他（对等上游 AggregateException 语义）。
- **限速**：每索引器一个限速器（`requestDelay` 间隔 + 最小突发 1），引擎与手写索引器统一经
  `httpclient` 发请求，限速在 client 层按索引器 ID 隔离。
- **SQLite 并发**：`database/sql` 连接池（`SetMaxOpenConns(1)` 写串行 + WAL 模式），缓存写入异步批量化。
- **优雅停机**：http.Server.Shutdown + 上下文取消扇出，Blackhole 写盘与缓存落盘在退出前 flush。

### 4.4 HTTP 请求栈（httpclient）

分层 RoundTripper（由内向外）：

1. `proxy`：http/socks4/socks5 代理（配置驱动）。
2. `flareSolverr`：检测 CF 盾（403 + `cf-mitigated` / `__cf_chl` 特征）→ 调 FlareSolverr → 注入 Cookie/UA 重放。
3. `rateLimit`：per-indexer 令牌桶。
4. `cookieJar`：每索引器持久化 Cookie（SQLite `indexer_config.cookies`）。
5. `logging`：Debug 级记录 URL/状态/耗时（脱敏 apikey/password）。

超时：连接 30s / 总 100s（可被 FlareSolverr 配置拉长）；UA 可按索引器覆盖。

### 4.5 错误处理

```go
type IndexerError struct {
    IndexerID string
    Kind      ErrKind // Config | Parse | Auth | RateLimit | Network | Internal
    Message   string
    RetryAfter time.Duration // RateLimit 时有效
}
```

- Torznab 端点失败 → 错误 XML（code 900 / 429+Retry-After），与上游一致。
- 手动搜索失败 → 分项 `indexers[].status=Error`。
- 日志统一 `slog`，LogCache handler 同步进环形缓冲。

## 5. YAML 定义引擎设计（engine）

引擎是复刻的核心，行为规范=上游 `CardigannIndexer.cs`（约 2600 行）：

```
加载：definitions/*.yml ──JSON Schema(schema.json) 校验──▶ IndexerDefinition
配置：settings 字段 ──▶ ConfigData（动态表单）
登录：login.form / login.get / login.cookie ──▶ 会话 Cookie（含 captcha/error 块）
搜索：search.paths[] × query 模板 ──▶ HTTP 请求 ──▶ rows 选择器 ──▶ fields 选择器
      ──▶ converters ──▶ rowfilters ──▶ categorymappings ──▶ ReleaseInfo[]
下载：download.selectors / magnet / infohash ──▶ 真实下载链接
```

- 模板替换覆盖 `{.Query.*}` `{.Config.*}` `{.Settings.*}` 与条件块 `{{ if_eq }}` 等（受限实现，不引完整 Handlebars）。
- 转换器注册表 map[string]func(...map[string]any)，清单见 PRD FR-305，逐个以上游行为为准。
- 失败语义：字段缺失按上游"空值容忍"，行级失败跳过该行；整体解析错误包装 `IndexerError{Kind: Parse}`。
- 单测策略：每个转换器/过滤器的表驱动单测 + 抽样 20 个 .yml 的 fixture 解析对拍（HTML 快照进 `test/fixtures`）。

## 6. 前端设计（概要，详见 frontend.md）

- Vue 3 + TypeScript + Vite + PrimeVue 4（Aura 主题）+ Pinia + Vue Router。
- 页面：Login / Dashboard（索引器表）/ 设置（Server Config / 日志 / API Key）；
  弹窗：索引器配置（动态表单）、手动搜索（结果表）。
- 服务层 `services/api.ts` 封装全部后端调用（与 backend-api.md 一一对应），basePath 由运行时配置注入。

## 7. 构建与交付

```
make web            # pnpm --dir src/frontend install && pnpm --dir src/frontend build
make build          # go build -C src/backend -o dist/vestt ./cmd/vestt（embed 前端产物 + definitions）
make test           # go test ./src/backend/...（含集成测试包，编译后黑盒）
make release        # goreleaser 风格交叉编译：linux amd64/arm64/armv7, win amd64, mac amd64/arm64
make docker         # 多阶段镜像（golang → alpine/distroless）
```

- 资源 embed：`src/backend/server/static`（前端构建产物）、`src/backend/common/engine/definitions`（yml + schema.json）。
- 前端产物双托管：同一份 `src/frontend/dist` 既由后端 embed（默认，对等上游单二进制），
  亦可交外部 nginx 托管（可选，静态流量卸载），见 §7.1。
- 运行数据目录：`--data-dir`（默认 `~/.local/share/vestt`，Windows `%APPDATA%/vestt`）。
- Docker：HEALTHCHECK 调 `/health`；`VESTT_PORT`/`VESTT_DATA` 环境变量。

### 7.1 部署形态：前端静态资源双托管

| 形态 | 静态资源托管方 | 适用场景 | 说明 |
|---|---|---|---|
| A（默认） | 后端 go:embed | 单二进制交付、Docker | 对等上游；`/UI/Dashboard` 直出 index.html，静态 handler 服务 `/js/*`、`/css/*` |
| B（可选） | 外部 nginx | 已有 nginx 体系 / 静态流量卸载 | `root` 指向前端 dist；`try_files $uri @backend`：命中文件由 nginx 直出，未命中一律反代后端 —— `/UI/*`（含 logout 302）、`/api/*`、`/dl` `/img` `/bh`、`/health` 行为与形态 A 完全一致 |

```nginx
# 形态 B 示例（完整版随 SPEC-14 交付：install/nginx/vestt.conf）
server {
    listen 80;
    root /var/www/vestt;                    # = src/frontend/dist 构建产物
    location / {
        try_files $uri @backend;            # 静态命中直出；未命中反代后端
    }
    location @backend {
        proxy_pass http://127.0.0.1:9117;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;   # 供 Secure Cookie / 协议推断
    }
}
```

约定：形态 B 不改变任何 API 路径、鉴权与 Cookie 语义；同域反代无需启用 `allow_cors`
（仅前端跨域直连后端时启用）。后端始终内嵌前端产物作为回退，nginx 仅是静态卸载层。

## 8. 安全设计要点

- API Key 生成：`crypto/rand` 32 字节 hex。
- 管理会话：服务端随机 session token 存 SQLite，Cookie HttpOnly + SameSite=Lax；Secure 由请求协议推断。
- 日志脱敏：apikey、password、cookie 值在 LogCache 中以 `***` 呈现（对等上游 JackettProtectedAttribute 意图）。
- 默认绑定 `127.0.0.1`；`external=true` 时绑定 `0.0.0.0` 并在 UI 提示（对等上游警告文案）。

## 9. 架构决策记录（ADR 摘要）

| 编号 | 决策 | 备选 | 理由 |
|---|---|---|---|
| ADR-01 | SQLite 统一存储 | JSON 文件（上游）/ bbolt | 用户确认；事务与缓存查询更简单；纯 Go 驱动保交叉编译 |
| ADR-02 | 复用上游 .yml + schema.json | 自定义格式 | 生态兼容、迁移零成本；GPL 传染已由 LICENSE 顶层选择覆盖 |
| ADR-03 | goquery 实现选择器 | rod/浏览器方案 | 上游为 HTTP+CSS 选择器模型，无头浏览器只经 FlareSolverr 外挂 |
| ADR-04 | chi 而非 gin/echo | gin/echo/net-http | 中间件组合与上游 action-filter 语义映射最直接 |
| ADR-05 | 会话用服务端 token 而非 JWT | JWT | 对等上游 Cookie 会话；可主动吊销 |
| ADR-06 | 手写受限模板而非嵌 Handlebars | handlebars/go 包 | 引擎仅用上游模板子集，行为可控、易测 |
| ADR-07 | 全部源码收于 src/：backend/ 与 frontend/ 语义化分离 | 前端置于顶层 web/ 目录 | 用户约定；前后端工程边界清晰、单一源码根，构建期才经 embed 汇合 |
| ADR-08 | 前端静态资源双托管：后端 embed 默认 + 外部 nginx 可选卸载 | 仅 embed / 纯 SPA 与纯 API 分包 | 用户约定；单二进制保上游对等，nginx try_files 未命中全反代保 API 行为完整，避免分包维护 |
