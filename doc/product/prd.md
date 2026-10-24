# Vestt 产品需求文档（PRD）

| 项 | 内容 |
|---|---|
| 产品名称 | Vestt |
| 文档版本 | 1.0 |
| 上游基线 | Jackett 0.24.2900（参考代码：`tmp/ref/Jackett-0.24.2900/`） |
| 技术栈 | Golang + Vue 3 + Vite + PrimeVue 4.x + pnpm + SQLite |
| 关联文档 | [tech/architecture.md](../tech/architecture.md)、[roadmap/roadmap.md](../roadmap/roadmap.md)、根目录 `DISCLAIMER.md` |

---

## 1. 产品概述

### 1.1 一句话定位

Vestt 是一个自托管的**索引器代理服务器**：把数百个私有/公开种子站的私 有搜索接口统一封装为
标准的 **Torznab / Newznab / TorrentPotato** 协议 API，供 Sonarr、Radarr、Lidarr、Readarr、Prowlarr 等
自动化媒体管理工具直接对接，同时提供一个基于 Web 的管理控制台。

### 1.2 产品背景

Jackett 是该品类事实上的标准实现（GPL-2.0，C#/ASP.NET Core）。Vestt 以 Jackett 0.24.2900 为产品基线，
进行**功能对等的 1:1 复刻**，但在技术栈上更换为 Golang 后端 + Vue 3 前端 + SQLite 存储，以获得
更小的运行时体积、更简的跨平台交付（单二进制）与更现代的管理界面。

### 1.3 与上游的关系与边界

- 功能范围、API 行为、YAML 索引器定义格式与 Jackett 0.24.2900 **保持对等**。
- Vestt 复用上游 GPL-2.0 的 582 个 YAML 索引器定义（`src/Jackett.Common/Definitions/*.yml`）与
  `Definitions/schema.json` 校验文件，因此整体以 GPL-2.0 发行（见 `LICENSE`）。
- Vestt **不托管、不缓存到磁盘**任何种子内容；它只是协议转换与请求转发中间件（详见 `DISCLAIMER.md`）。
- 与上游的差异点：存储层由 JSON 文件改为 SQLite；自更新机制简化为版本检查提示（不做运行时自替换二进制）；
  前端由 jQuery/Handlebars 重写为 Vue 3。其余行为以"上游即规范"。

### 1.4 目标用户

| 角色 | 说明 | 核心诉求 |
|---|---|---|
| 自托管媒体库管理员 | 使用 Sonarr/Radarr 等工具的 NAS/服务器用户 | 一键配置索引器，稳定供 API |
| 追加站点的进阶用户 | 同时在多个 PT/公开站活动的用户 | 聚合搜索、按 tracker 过滤、缓存减压 |
| 私有生态维护者 | Docker/家庭服务器玩家 | 低资源占用、单二进制、无外部依赖 |

### 1.5 产品目标

1. **协议兼容**：Torznab 消费方（Sonarr/Prowlarr 等）无需任何适配即可从 Jackett 迁移到 Vestt。
2. **索引器覆盖率 1:1**：初始即支持全部 582 个 YAML 定义索引器 + 62 个 C# 手写索引器移植版 + 聚合元索引器。
3. **单文件交付**：一个静态二进制内嵌前端资源与索引器定义库，解压即用。
4. **可长期维护**：定义引擎与索引器实现解耦，新增站点优先走 YAML 定义。

## 2. 术语表

| 术语 | 定义 |
|---|---|
| 索引器（Indexer） | 对一个种子站搜索能力的封装实现，含鉴权、搜索、解析、下载 |
| Torznab | Newznab 的 BT 扩展协议，基于 RSS/XML，`t=caps|search|tvsearch|movie|music|book` 查询类型 |
| Newznab | Usenet 索引器标准协议（Vestt 仅作 Torznab 兼容输出，不代理 Usenet） |
| TorrentPotato | CouchPotato 使用的 JSON 搜索协议（仅电影） |
| 元索引器 | `all`（全部已配置）与 `aggregate`（全部未失败已配置）两个虚拟聚合索引器 |
| Cardigann / YAML 定义 | 用 YAML 描述索引器的声明式格式，由定义引擎执行；上游称 Cardigann 定义 |
| 手写索引器 | 上游以 C# 编码的索引器（62 个），Vestt 以 Go 等价移植 |
| API Key | 服务端生成的访问令牌，用于 Torznab/Potato/代理下载的鉴权 |
| FlareSolverr | 第三方 Cloudflare 盾绕过代理服务，可选集成 |
| Blackhole | 将下载链接写为 `.torrent`/`.magnet` 文件到监视目录的交互模式 |
| Release | 一次搜索返回的条目（种子），对应 `ReleaseInfo` 模型 |

## 3. 功能需求（FR）

> 编号规则：FR-<模块号><序号>。MUST/SHOULD 语义遵循 RFC 2119。
> 每条 FR 的实现细节由 tech 文档与 SPEC 承接，本文只规定"产品行为"。

### FR-100 服务器核心

- **FR-101**（MUST）以 HTTP 服务启动，默认监听 `127.0.0.1:9117`，可配置端口、绑定地址、是否允许外部访问。
- **FR-102**（MUST）提供全局 API Key（启动时若不存在则随机生成并持久化），用于 Torznab/Potato/代理下载鉴权。
- **FR-103**（MUST）支持管理后台密码（可选），开启后 UI 与管理 API 需 Cookie 会话（14 天有效）。
- **FR-104**（MUST）支持 `Base Path Override`（反向代理子路径部署）与 `Base URL Override`（对外链接重写）。
- **FR-105**（MUST）支持可配置 CORS 开关（供第三方前端调用）。
- **FR-106**（MUST）提供健康检查端点 `/health`（匿名可访问，返回版本与就绪状态）。
- **FR-107**（MUST）提供 `Shutdown` 管理 API（需 API Key）优雅停机。
- **FR-108**（SHOULD）支持通过环境变量/命令行参数覆盖关键启动项（端口、数据目录、日志级别）。
- **FR-109**（MUST）单实例运行约束：SQLite 库文件级互斥，二次启动给出明确错误。

### FR-200 索引器管理

- **FR-201**（MUST）启动时加载全部内置索引器（582 个 YAML 定义 + 62 个 Go 移植手写索引器）。
- **FR-202**（MUST）管理 API 支持列出全部索引器（含 `configured` 过滤参数），返回字段与上游 DTO 对等
  （id、name、description、type、language、tags、site_link、capabilities、state、last_error 等）。
- **FR-203**（MUST）索引器配置接口：`GET config` 返回动态表单描述（字段类型、默认值、校验），`POST config`
  提交配置；提交后自动执行测试（上游 `RequiresTesting` 语义）。
- **FR-204**（MUST）索引器测试接口：执行一次真实搜索（t=search 或各索引器测试逻辑），返回成功/失败与错误信息。
- **FR-205**（MUST）索引器删除接口：清除配置并回退到未配置态。
- **FR-206**（MUST）每个索引器独立持久化配置（SQLite），支持 Cookie/UA/密码/API Key/Passkey 等配置形态。
- **FR-207**（MUST）索引器状态：`healthy` / `failing` / `unknown`；测试与查询失败记录 `last_error`。
- **FR-208**（MUST）支持索引器标签（tags），用于 UI 筛选。
- **FR-209**（MUST）支持按类型（public/semi-private/private）、标签、状态筛选索引器列表。
- **FR-210**（SHOULD）支持备用站点链接（alternative site links）在配置界面切换。
- **FR-211**（MUST）YAML 定义引擎支持登录表单、Cookie 登录、验证码（captcha）检测——
  返回配置数据中带 captcha 字段时，UI 弹出人工输入通道（上游 `cardigannCaptcha` 行为）。

### FR-300 YAML 定义引擎（Cardigann 兼容）

- **FR-301**（MUST）加载并校验 `Definitions/*.yml`（以 `Definitions/schema.json` 为准）。
- **FR-302**（MUST）支持定义中全部块类型：`settings`、`caps`（categories/categorymappings/modes）、
  `login`（form/get/cookie/captcha/error）、`ratio`、`search`（rows/fields/filters/paths）、
  `download`（selectors/method/headers）、`before`/`error`/`infohash`/`pagetest` 块。
- **FR-303**（MUST）模板变量：`.Query.*`、`.Config.*`、`.Settings.*` 与 Handlebars 风格条件
  （`if_eq`/`if_in`/`and`/`or`/`eq`/`ne`）按上游语义实现。
- **FR-304**（MUST）字段选择器解析：CSS 选择器（goquery），支持 `selector/attribute/remove/multiple/filters`。
- **FR-305**（MUST）转换器（converters）至少覆盖上游全集：
  `querystring, timeparse, dateparse, regexp, re_replace, split, replace, trim, prepend, append,
  tolower, toupper, base64decode, base64encode, urldecode, urlencode, htmldecode, htmlencode,
  timeago, reltime, fuzzytime, validfilename, diacritics, jsonjoinarray, hexdump, strdump, validate,
  sizeparse, datetimeparse, regextokens, dateunits, uriappend, translate, map, multiply, divide, multiple`
  （以 `CardigannIndexer.cs` 的 switch 分支为权威清单）。
- **FR-306**（MUST）行过滤器（rowfilters）：`selector` / `text` 两类，`andmatch` 搜索路径过滤器。
- **FR-307**（MUST）分类映射（categorymappings）含 `cat` 映射、`desc`、默认值与 filters。
- **FR-308**（MUST）限速：按定义的 `requestDelay` 执行请求间隔。
- **FR-309**（MUST）多搜索路径（paths）串行翻页（`IndexerPageableRequestChain` 语义）。
- **FR-310**（SHOULD）定义引擎单测可离线执行（fixture HTML → 期望 Release 列表）。

### FR-400 Torznab API

- **FR-401**（MUST）端点 `/api/v2.0/indexers/{indexerId}/results/torznab/`，`indexerId` 可为具体索引器或
  元索引器（`all`/`aggregate`）。
- **FR-402**（MUST）查询类型 `t=caps`：输出 `<caps>` XML（server/version/categories/movies-search/
  tv-search/music-search/book-search 及各参数支持位）。
- **FR-403**（MUST）查询类型 `t=search|tvsearch|movie|music|book`：输出 RSS 2.0 XML，`<item>` 字段与上游
  `ResultPage.ToXml` 对等（torznab:newznab 命名空间属性，含 seeders/peers/size/category 等）。
- **FR-404**（MUST）参数支持：`q`、`cat`、`imdbid`、`tmdbid`、`tvdbid`、`tvmazeid`、`season`、`ep`、
  `extended`、`limit`、`offset`、`apikey`；音乐/图书扩展参数（artist/album/label/track/year/genre/
  author/title/publisher）按能力位声明。
- **FR-405**（MUST）能力校验：请求超出索引器能力位时返回 Torznab 错误 XML（code 201/203）。
- **FR-406**（MUST）`t=indexers`（仅元索引器）：XML 列出所含索引器及配置状态。
- **FR-407**（MUST）限流响应：上游站点 429 时透传 `Retry-After` 并返回 429 + Torznab 错误 XML（code 900）。
- **FR-408**（MUST）聚合搜索：对元索引器并发扇出全部目标索引器，单索引器失败不影响整体（错误归入分项状态）。
- **FR-409**（MUST）`imdbid` 归一化（`tt` 前缀补全/位数校验）。
- **FR-410**（MUST）RSS 输出中的链接经下载代理重写（见 FR-801）。

### FR-500 TorrentPotato API

- **FR-501**（MUST）端点 `/api/v2.0/indexers/{indexerId}/results/potato/`，仅支持电影能力索引器。
- **FR-502**（MUST）响应 JSON 结构与上游 `TorrentPotatoResponse` 对等
  （`release_name/torrent_id/details_url/download_url/imdb_id/freeleech/type/size/leechers/seeders/publish_date`）。

### FR-600 手动搜索 API（管理 UI 专用）

- **FR-601**（MUST）端点 `/api/v2.0/indexers/{indexerId}/results/`（JSON），支持 `Tracker[]`、`Category[]`、
  `query`、`imdbid`、`tmdbid`、`tvdbid`、`tvmazeid` 参数。
- **FR-602**（MUST）响应为 `ManualSearchResult`：`results`（合并按发布时间倒序）+ `indexers`（每个目标索引器的
  状态/耗时/结果数/错误）。
- **FR-603**（MUST）结果携带代理化 `link/poster/blackhole_link`（blackhole_link 仅在配置了 Blackhole 目录时）。

### FR-700 缓存

- **FR-701**（MUST）查询结果内存缓存 + SQLite 落盘，TTL 与每索引器最大结果数可配置（默认对等上游：TTL 900s、
  每索引器 1000 条）。
- **FR-702**（MUST）缓存查询键 = 索引器 + 归一化后的查询；`Cache=false` 请求绕过。
- **FR-703**（MUST）提供 `/api/v2.0/indexers/Cache` 查看端点（UI"已缓存 releases"页）。
- **FR-704**（MUST）索引器配置变更/删除时清理该索引器缓存；服务器配置变更可全量清理。

### FR-800 下载代理与 Blackhole

- **FR-801**（MUST）`/dl/{indexerId}?path=...&file=...&apikey=...` 代理下载种子文件（携带索引器会话 Cookie）。
- **FR-802**（MUST）`/img/{indexerId}?path=...&apikey=...` 图片代理（海报）。
- **FR-803**（MUST）`/bh/{indexerId}?path=...&file=...&apikey=...` 将 `.torrent`/`.magnet` 写入 Blackhole 目录。
- **FR-804**（MUST）代理请求强制 API Key 校验。

### FR-900 FlareSolverr 集成

- **FR-901**（MUST）可配置 FlareSolverr URL 与最大超时（默认 60000ms，最小 5000ms）。
- **FR-902**（MUST）索引器遇到 Cloudflare 盾时按上游语义经 FlareSolverr 解析（获取 Cookie/UA）后重放请求。

### FR-1000 服务器配置

- **FR-1001**（MUST）`GET/POST /api/v2.0/server/config`，配置项全集与上游 ServerConfig DTO 对等：
  port、local_bind_address、external、cors、blackholedir、updatedisabled、prerelease、logging、
  basepathoverride、baseurloverride、cache_enabled、cache_ttl、cache_max_results_per_indexer、
  omdbkey、omdburl、flaresolverrurl、flaresolverr_maxtimeout、proxy_*（类型 http/socks4/socks5/disabled）、
  password（管理密码，哈希存储）。
- **FR-1002**（MUST）端口/绑定地址变更触发的校验（范围 1-65535、绑定地址必须回环）与重启提示
  （Vestt 返回 `restart_required` 标志，由用户手动重启，替代上游进程自重启）。
- **FR-1003**（MUST）`GET /api/v2.0/server/adminpassword` 等管理端点遵循上游语义；密码以 bcrypt 哈希存储。
- **FR-1004**（MUST）提供版本信息（应用版本、.NET 运行时对等字段以 `go_version` 呈现）。

### FR-1100 日志

- **FR-1101**（MUST）日志缓存环形缓冲（默认 1000 条）经 `GET /api/v2.0/server/logs` 暴露。
- **FR-1102**（MUST）日志级别 Info/Debug（`logging` 开关），Debug 模式记录完整请求 URL（脱敏后）。
- **FR-1103**（MUST）文件日志写入数据目录，滚动按大小（对等上游行为）。

### FR-1200 安全

- **FR-1201**（MUST）API Key 仅查询串传递校验（`apikey` 或 `passkey` 参数）。
- **FR-1202**（MUST）管理会话 Cookie：HttpOnly、SameSite=Lax、Secure 由请求协议决定。
- **FR-1203**（MUST）登录尝试含 Cookie 可用性预检（上游 `TestCookie` 流程的等价保护）。
- **FR-1204**（MUST）配置文件中敏感字段（密码、代理凭据）不以明文日志输出。

### FR-1300 元索引器

- **FR-1301**（MUST）`all`：全部索引器聚合；`aggregate`：全部已配置且未失败的索引器聚合。
- **FR-1302**（MUST）`t=indexers`、`Tracker[]` 过滤、按 `CanHandleQuery` 裁剪参与索引器集合。
- **FR-1303**（SHOULD）OMDb API Key 配置后支持 `imdbid` 电影搜索的标题增强（上游 ImdbResolver 行为）。

### FR-1400 交付与运维

- **FR-1401**（MUST）跨平台构建：linux/amd64、linux/arm64、linux/arm/v7、windows/amd64、macos/amd64、macos/arm64。
- **FR-1402**（SHOULD）提供 Dockerfile（multi-arch）与 docker-compose 示例。
- **FR-1403**（SHOULD）提供 systemd unit 与安装脚本。
- **FR-1404**（MUST）数据目录布局：`<data>/vestt.db`、`<data>/Logs/`；数据目录可迁移。
- **FR-1405**（MUST）版本检查 API（对等上游 UpdateService 的"检查并提示"部分；不做运行时自替换）。
- **FR-1406**（SHOULD）前端静态资源双托管：后端 go:embed 默认托管；同时提供 nginx 分离部署支持
  （前端构建产物独立发布 + `install/nginx/vestt.conf` 反代示例，API 行为不变，见 architecture.md §7.1）。

## 4. 用户流程

### 4.1 首次使用与索引器配置（主流程）

```
启动 Vestt → 打开 http://host:9117
  → [可选] 设置管理密码 → 重新登录（Cookie 会话）
  → 仪表盘"未配置索引器"列表 → 搜索/筛选目标站点
  → 点击"配置" → 弹出动态表单（用户名/密码/Cookie/API Key…）
      ├─ 含 captcha 字段 → 显示验证码图片 + 输入框（人工识别）
      └─ 提交 → POST /indexers/{id}/Config
            → 服务端尝试登录 → 成功：自动测试 → 索引器移入"已配置"列表
            → 失败：表单回显错误
  → 复制 API Key → 在 Sonarr/Radarr/Prowlarr 中以 Torznab Custom Indexer 对接：
      URL = http://host:9117/api/v2.0/indexers/{id}/results/torznab/
      API Key = <仪表盘展示的 Key>
```

### 4.2 手动搜索流程

```
仪表盘 → 搜索按钮 → 弹窗输入关键词/类别/tracker 多选
  → GET /api/v2.0/indexers/{id|all}/results/?query=...&Tracker[]=a&Tracker[]=b
  → 结果表实时渲染（标题/大小/做种/下载/类别/站点）→ 点击下载
      ├─ 浏览器直下 .torrent（经 /dl 代理）
      └─ 有 Blackhole 配置 → 显示 BlackholeLink
  → 支持"保存搜索预设"（localStorage，等价上游 savedPresets）
```

### 4.3 聚合消费流程（自动化工具）

```
Prowlarr/Sonarr 定时轮询 /results/torznab/?apikey=...
  → Vestt 命中缓存则直接返回
  → 未命中 → 扇出索引器 → 汇总 RSS → 记录缓存
  → 下载阶段 Prowlarr 回调 /dl/{id} → Vestt 附带站点 Cookie 转发真实下载链接
```

## 5. 非功能需求（NFR）

| 编号 | 需求 | 目标 |
|---|---|---|
| NFR-01 | 性能 | 聚合搜索 50 索引器扇出 P95 < 15s（受上游站点限制）；缓存命中 P95 < 50ms |
| NFR-02 | 资源 | 空载内存 < 80MB；Docker 镜像 < 60MB |
| NFR-03 | 可靠性 | 单索引器异常（超时/解析失败/封禁）绝不拖垮整体请求，错误结构化记录 |
| NFR-04 | 并发 | 索引器间并行、索引器内串行/分页受控；每索引器独立限速器（请求间隔 + 突发上限） |
| NFR-05 | 兼容性 | Torznab XML 字节级字段对等（命名空间、属性顺序不敏感，但元素齐全） |
| NFR-06 | 安全 | API Key/密码明文不落日志；默认仅监听回环地址 |
| NFR-07 | 可维护性 | YAML 引擎与手写索引器共用统一 `Indexer` 接口；新增站点优先 YAML |
| NFR-08 | 可测试性 | 引擎解析/转换器/分类映射/XML 序列化均可用离线 fixture 单测 |
| NFR-09 | 国际化 | UI 文案与上游一致（英文基线），数据多语言（索引器 language 字段）透传 |

## 6. 对外兼容性承诺

1. **URL 兼容**：所有上游端点路径、参数、HTTP 方法 1:1 保留（见 `tech/backend-api.md`）。
2. **报文兼容**：Torznab/Potato/手动搜索 JSON 的字段名与类型与上游序列化结果对等（包括小写字段名风格，
   如 `manual search` 的 `TrackerCacheResult` 字段）。
3. **YAML 定义兼容**：可直接加载上游 582 个 `.yml`，不要求修改定义文件。
4. **配置兼容（SHOULD）**：提供从 Jackett 数据目录导入索引器配置的工具（读 `Jackett/Definitions` 与
   索引器 JSON 配置并转换入库）。

## 7. 范围外（Non-Goals）

- 不做 Usenet/Newznab 索引源代理（保持上游语义：Vestt 输出 Torznab）。
- 不做运行时自更新（替换二进制）；仅版本检查提示。
- 不做上游的 Windows 托盘程序（Tray）与 Service 安装器 UI 等价物（以 systemd/Docker 文档替代）。
- 不做私有数据的云同步；一切数据本地 SQLite。

## 8. 技术规格附录

### 8.1 Torznab 请求参数表

| 参数 | 类型 | 说明 | 适用 t |
|---|---|---|---|
| t | string | caps/search/tvsearch/movie/music/book/indexers | 全部 |
| q | string | 搜索词（URL 编码） | search/tvsearch/movie/music/book |
| cat | csv[int] | Torznab 类别 ID | 全部搜索类 |
| imdbid | string | `tt`+7/8 位数字 | movie/tvsearch（按能力位） |
| tmdbid | int | TMDB ID | movie/tvsearch（按能力位） |
| tvdbid | int | TVDB ID | tvsearch（按能力位） |
| tvmazeid | int | TVMaze ID | tvsearch（按能力位） |
| season / ep | int / string | 剧集季/集 | tvsearch |
| extended | 0/1 | 输出扩展参数 | 全部搜索类 |
| limit / offset | int | 分页 | 全部搜索类 |
| apikey | string | 访问令牌 | 全部（或 passkey 别名） |

音乐扩展：`artist`、`album`、`label`、`track`、`year`、`genre`；图书扩展：`author`、`title`、`publisher`。

### 8.2 主分类（TorznabCategories 主干）

1000 Console / 2000 Movies / 3000 Audio / 4000 PC / 5000 TV / 6000 XXX / 7000 Books / 8000 Other；
子分类以 `TorznabCatType` 全表为准（实现时以 `TorznabCatType.cs` 为权威清单，约 60 个）。

### 8.3 上游错误码语义

| HTTP | code | 场景 |
|---|---|---|
| 401 | 100 | Invalid API Key |
| 404 | 201 | Indexer not specified / not supported / not configured |
| 400 | 201 | 不支持的查询参数（能力外请求） |
| 400 | 203 | Function Not Available（能力位不满足） |
| 203 | — | XML `t=indexers` 用于非元索引器 |
| 400/429 | 900 | 通用错误 / 上游限流（带 Retry-After） |

### 8.4 配置表单字段类型（前端动态渲染）

| 类型 | 上游类 | 渲染 |
|---|---|---|
| input | TextField(text/password) | PrimeVue InputText / Password |
| checkbox | CheckboxField | PrimeVue Checkbox |
| select | SelectField | PrimeVue Select（options） |
| multi-select | SelectField(multiple) | PrimeVue MultiSelect |
| info | InfoField / DisplayInfo | 静态说明区（info_category_8000 / info_cookie / info_flaresolverr / info_useragent 特殊文案） |
| hidden | HiddenField | 不渲染 |
| stringlist | StringListField | 多行/可增删输入列表 |

### 8.5 手动搜索响应 DTO（关键字段）

```
ManualSearchResult {
  results: TrackerCacheResult[]   // Title, Guid, Link, Details, PublishDate, Category[], Size,
                                  // Seeders, Peers, DownloadVolumeFactor, UploadVolumeFactor,
                                  // Tracker, TrackerId, TrackerType, Poster, InfoHash, MagnetUri,
                                  // BlackholeLink, MinimumRatio, MinimumSeedTime, Description...
  indexers: ManualSearchResultIndexer[]  // ID, Name, Status(OK/Error/Unknown), Results, ElapsedTime, Error
}
```

## 9. 验收口径（产品级）

1. 上游 582 个 YAML 定义 100% 通过 schema 校验并被引擎加载（不含 `broken` 标记的站点可测试通过）。
2. 62 个手写索引器 Go 移植后，`t=caps` 输出与上游一致、fixture 搜索解析结果一致。
3. Sonarr/Prowlarr 以自定义 Torznab 方式添加 Vestt 后可完成：能力读取 → 搜索 → 下载 全链路。
4. 管理 UI 覆盖 PRD 全部 FR-2xxx/6xxx/1xxx 的用户可见行为。
5. 全部 SPEC 验收标准通过（见 `roadmap/roadmap.md` 追踪表）。
