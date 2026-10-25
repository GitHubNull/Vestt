# Vestt 复刻路线图

| 项 | 内容 |
|---|---|
| 文档版本 | 1.0 |
| 目标 | 将 Jackett 0.24.2900 完整复刻为 Golang + Vue 3 实现（范围见 [PRD](../product/prd.md)） |
| 执行者 | AI 编程代理（工作纪律见根目录 `AGENTS.md`） |

---

## 1. 阶段总览与依赖关系

```
阶段 A 基础设施        阶段 B 后端核心                阶段 C 索引器能力              阶段 D 前端            阶段 E 收尾
SPEC-01 ──▶ SPEC-02 ──▶ SPEC-03 ──▶ SPEC-04 ──▶ SPEC-05 ──▶ SPEC-06 ──▶ SPEC-07 ──▶ SPEC-11 ──▶ SPEC-12 ──▶ SPEC-13 ──▶ SPEC-14
 脚手架      后端基础     Torznab模型   索引器框架     YAML引擎I     YAML引擎II    聚合API层     前端I          前端II        前端III       质量发布
                                 │                                   │
                                 └─────────── SPEC-08/09/10 手写索引器移植（可并行于 SPEC-05+ 之后任意时点）─┘
```

- 主线：SPEC-01 → 02 → 03 → 04 → 05 → 06 → 07 → 11 → 12 → 13 → 14。
- 并行支线：SPEC-08/09/10（62 个手写索引器分 3 批）在 SPEC-04 完成后即可开始，
  与 SPEC-05~07 或前端阶段并行推进。
- 前端三份 SPEC 依赖 SPEC-07 提供的真实 API；若后端未就绪可用 fixture，但验收必须以联调为准。

## 2. SPEC 索引与前置依赖

| SPEC | 文件 | 主题 | 前置 | 预估规模 |
|---|---|---|---|---|
| 01 | [specs/SPEC-01-bootstrap.md](specs/SPEC-01-bootstrap.md) | 工程脚手架与构建体系 | 无 | S |
| 02 | [specs/SPEC-02-backend-foundation.md](specs/SPEC-02-backend-foundation.md) | HTTP 框架/SQLite/日志/鉴权/Server Config API | 01 | M |
| 03 | [specs/SPEC-03-torznab-core.md](specs/SPEC-03-torznab-core.md) | TorznabQuery/Caps/Categories/ReleaseInfo/RSS-XML | 02 | M |
| 04 | [specs/SPEC-04-indexer-framework.md](specs/SPEC-04-indexer-framework.md) | Indexer 接口/注册表/HTTP 客户端栈/限速/CF/代理 | 03 | M |
| 05 | [specs/SPEC-05-yaml-engine-requests.md](specs/SPEC-05-yaml-engine-requests.md) | YAML 引擎 I：schema 校验/模板/login/搜索请求 | 04 | L |
| 06 | [specs/SPEC-06-yaml-engine-parsing.md](specs/SPEC-06-yaml-engine-parsing.md) | YAML 引擎 II：选择器/转换器/过滤器/分类映射 | 05 | L |
| 07 | [specs/SPEC-07-aggregate-apis.md](specs/SPEC-07-aggregate-apis.md) | torznab/potato/手动搜索/缓存/代理路由/元索引器 | 06 | M |
| 08 | [specs/SPEC-08-manual-indexers-batch1.md](specs/SPEC-08-manual-indexers-batch1.md) | 手写索引器移植 批次 1（21 个） | 04 | L |
| 09 | [specs/SPEC-09-manual-indexers-batch2.md](specs/SPEC-09-manual-indexers-batch2.md) | 手写索引器移植 批次 2（20 个） | 04 | L |
| 10 | [specs/SPEC-10-manual-indexers-batch3.md](specs/SPEC-10-manual-indexers-batch3.md) | 手写索引器移植 批次 3（21 个） | 04 | L |
| 11 | [specs/SPEC-11-frontend-foundation.md](specs/SPEC-11-frontend-foundation.md) | 前端 I：Vite/PrimeVue/登录页/仪表盘框架 | 07 | M |
| 12 | [specs/SPEC-12-frontend-indexers.md](specs/SPEC-12-frontend-indexers.md) | 前端 II：索引器管理与动态配置表单 | 11 | M |
| 13 | [specs/SPEC-13-frontend-search-settings.md](specs/SPEC-13-frontend-search-settings.md) | 前端 III：手动搜索/缓存视图/设置/日志 | 12 | M |
| 14 | [specs/SPEC-14-quality-release.md](specs/SPEC-14-quality-release.md) | 测试体系/交叉编译/Docker/systemd/nginx分离部署/文档 | 13 | M |

## 3. 里程碑

| 里程碑 | 内容 | 退出条件（全部满足） |
|---|---|---|
| M1 骨架可运行 | SPEC-01~02 | `make build` 产出二进制；`/health` 200；SQLite 迁移可重复执行 |
| M2 协议层就绪 | SPEC-03~04 | caps XML 与上游一致；索引器注册表加载 582 yml（未解析）+ 62 手写占位 |
| M3 引擎贯通 | SPEC-05~06 | 抽样 20 个 YAML 定义离线解析对拍通过；登录/搜索/下载链路可执行 |
| M4 API 对等 | SPEC-07 + 08~10 | torznab/potato/手动搜索端到端可用；62 手写索引器全部移植并单测通过 |
| M5 界面对等 | SPEC-11~13 | 管理 UI 全功能可联调；上游 custom.js 行为映射表逐项核对 |
| M6 交付 | SPEC-14 | 测试全绿；三平台二进制 + Docker 产物可跑通冒烟；前端静态包与 nginx 配置示例随 release 交付 |

## 4. 进度追踪表（唯一进度事实来源）

> AI 代理完成一份 SPEC 后：勾选状态列、填写完成日期与备注（含偏差说明）。

| SPEC | 状态 | 完成日期 | 备注 |
|---|---|---|---|
| 01 | ☑ 完成 | 2026-10-24 | 脚手架全量落地：Go 模块+目录骨架、/health、Vite+PrimeVue4 前端、Makefile embed 链路；偏差见 SPEC-01 澄清记录 |
| 02 | ☑ 完成 | 2026-10-24 | 存储层/日志/鉴权/Server Config API/UI 路由全量落地；黑盒集成测试 7 项通过；偏差见 SPEC-02 澄清记录 |
| 03 | ☑ 完成 | 2026-10-24 | Torznab 模型库全量落地：71 类别表/查询解析/能力位/ReleaseInfo + RSS·caps·error·indexers XML 序列化；golden 对拍 9 份全过，覆盖率 models 93.5%/torznab 94.4%；偏差见 SPEC-03 澄清记录 |
| 04 | ☑ 完成 | 2026-10-24 | Indexer 接口+Base 模板（Filter/Fix/去重/健康窗口/下载）、configdata 9 类 item+golden、httpclient 栈（限速/自研 jar/代理 socks4|5/CF+FlareSolverr/重试）、注册表+IndexerManager 生命周期+meta 骨架；main.go 装配入库；验收 1-7 全过，澄清记录 11 条 |
| 05 | ☑ 完成 | 2026-10-24 | Cardigann 引擎 I 全量落地：定义模型+582 份 yml 加载+schema 校验、模板引擎（and/or/eq/ne/if/range/过滤器两阶段解析）、配置表单（settings→configdata 9 类映射）、登录全矩阵（form/post/cookie/get/oneurl+captcha+multipart）、caps 构造（desc→自定义类别 id≥100000）、搜索/下载请求生成（.Query 变量集/keyword tokens/intersect+"!"/$raw/POST 双编码/windows-1251 转码/magnet infohash/before+pathselector/testlinktorrent）；模板单测 37、登录矩阵 12、请求 golden 17 定义+单测 15、真实 yml config 形状 5；验收 1-8 全过，澄清记录 9 条 |
| 06 | ☑ 完成 | 2026-10-24 | Cardigann 引擎 II 全量落地：handleSelector 全分支（selector/remove/case/attribute/text+rows/field 级）、27 滤镜（regexp2 .NET 正则语义）、.NET 布局翻译+FromUnknown 模糊解析、分类映射（keywordsfilters+filter 声明式+case）、XML/JSON 行解析与 rows/keywordfields/multiple/after/before/andmatch/filters/relogin；20 真实定义 e2e 对拍全过（filter 126 例+rows 24 例+sample 20 定义）；验收 1-5 全过，澄清记录 7 条 |
| 07 | ☑ 完成 | 2026-10-24 | 缓存服务（SHA-256(query JSON) 键/TTL 秒/Tracker 字段缓存层填充/300·3000 上限）、meta 收口（all+FilterFunc 类型/tag 过滤）、ResultsController（torznab 6 型/potato golden/手动搜索聚合 zip 状态整数）、IndexerApiController（DTO/Config/Test/Delete/Cache 视图双变体）、代理三端点（dl bencode 规范化重编码/img 状态透传/bh .NET Ticks 文件名+IOException GUID 回退）、DownloadActionFilter &amp;-fallback；fixtures 假站点+3 手写 fake+fakecardigann 覆盖 def；集成测试 17 项全过；验收 1-5 全过，澄清记录 15 条 |
| 08 | ☑ 完成 | 2026-10-25 | 批次 1 全 21 个手写索引器移植落地（+4 共享抽象基类 gazelle/avistaz/brazilian/couchpotato 于 indexer/shared，33 用例）：每索引器 caps/配置表单 JSON/请求生成/fixture 解析（≥6 字段）/错误路径单测齐备，manual 包全量绿；registry_batch1.go 登记 21 工厂 + main.go blank import；TestRegistryCount（静态 21；引擎 582 见 engine 测试；元索引器经 manager 不经 registry）；identity_test 21 组 id/name/type/language 对拍上游；spotcheck_test 全链路抽检 3 个（Anilibria=json-api/AlphaRatio=gazelle/DivxTotal=html：Configure→Test→search→fixture）；机制表 11 处校正回填（AnimeBytes 实为 user-passkey json-api 无 2FA、BitHDTV 实为 cookie、AudioBookBay 实为 public html、EraiRaws 实为 rss-feed、AnimeZ 等 4 个 Avistaz 系实为 json-api、Gazelle 系 4 个实为 gazelle）；上游 bug 忠实复刻并注明依据（GazelleTracker.cs:513 null!=0 全量 usetoken、AnimeBytes "Parse error" 转换、AnimeZ System.Byte[] 插值、DonTorrent 三 bug、ISO-8859-8 有损折叠、.NET lookbehind 等价扫描）；make test 全绿；澄清记录 5 节 |
| 09 | ☑ 完成 | 2026-10-25 | 批次 2 全 20 个手写索引器移植落地：manual 包 41 工厂全量绿（单测约 178 例新增）；registry_batch2.go 登记 + TestRegistryCount=41；identity 对拍 41 组；spotcheck 全链路抽检 6 个（批次 2 抽 FileList=json-api/Orpheus=gazelle-api/IPTorrents=cookie html）；机制表 5 处校正（FileList→json-api、IPTorrents→cookie html、GPW→gazelle、GGn/Orpheus→gazelle-api、PTP→ApiUser/ApiKey 头）；专项断言齐备（IPTorrents 84 行分类映射全表双向、PTP 头+IMDb 分支+磁链/infohash 混合、MAM 搜索类型/语言参数全序、MTeamTp STJson 逐字节分页体、LostFilm S/E 两次请求链逐请求断言）；上游 bug 复刻（Orpheus usetoken 恒在、NorBits 无空格日期格式、MejorTorrent ParseTags no-op、GGn group 键控）；make test 全绿；澄清记录 5 节 |
| 10 | ☑ 完成 | 2026-10-25 | 批次 3 全 21 个手写索引器移植落地、62 全量收口：manual 包 62 工厂全量绿（批次 3 新增约 150 例单测）；registry_batch3.go 登记 + TestAllManualIndexersRegistered=62（引擎 582、元索引器 2 各在所属包断言）；identity 对拍 62 组；机制列 3 处实质校正（PrivateHD→shared.Avistaz、Simurg→gazelle、TorrentSyndikat→ConfigurationDataAPIKey）+2 细化（RetroFlix→SpeedAppTracker 基类、SceneHD→Passkey）；5 波代理并行移植 + 首测 50 失败逐条对拍上游裁决（绝大多数为测试期望错误、实现忠实；真实偏差 4 处已修：TorrentNetwork error 字段走 OnParseError/缺空 Instructions 项、Shazbat data-timestamp 属性存在性门槛、SpeedCD `*` 不编码）；spotcheck_batch3 全链路抽检 3 个（RuTracker=cookie 会话+cap 分支请求顺序、SpeedApp=json-api token 过期重取、TorrentDay=cookie+分类映射全表）；上游 bug 忠实复刻并注明依据（CleanTitle 双点、`\b[S|E]\d+\b` 字符类、hdtv 源组 5030 死代码分支、FilterResults 类目交集、配置 id 名称派生、BasicLogin 空 Instructions 项等，澄清记录逐条列出）；make test 全绿；澄清记录 3 节 |
| 11 | ☑ 完成 | 2026-10-24 | 前端基座全量落地：PrimeVue4 Aura definePreset + Toast/Dialog 服务 + 全局错误钩子、契约层（basePath 推断/锁定、401 归一、14 个命名 API）、5 stores（auth/config/logs 全功能 + indexers/search 骨架）、hash 路由 + 会话探针守卫、LoginView（上游 login.html 对等）、仪表盘壳（AppHeader API Key 遮罩/复制/注销、external 安全警告横幅、三下拉筛选 + 计数徽标、双 Tab 占位表、skeleton）、utils/hash；vitest 26 项全过；后端配套：/UI/assets 静态路由、DashboardGet 直出 SPA；浏览器实测登录全链路；澄清记录 13 条 |
| 12 | ☑ 完成 | 2026-10-24 | 索引器管理全量落地：双表（已配置 Status 徽标/未配置复选批量添加）、三下拉 AND 筛选 + 深度链接（路由 query 同步与恢复）、动态配置表单（9 类字段含 inputtags/验证码图 + alt 链接 + caps 表 + 错误 Alert）、配置/测试/删除/Test All（串行 200ms）全链路 store；vitest 50 项（store 18 + DynamicForm 快照 + CopyButton 2 等）全过；后端联调修复 3 处保真缺陷（sitelink 预填、:not(:contains()) 47 定义、inputtags 契约）均带回归测试；E2E 7 场景真实浏览器全过（0Magnet/0day.kiev 错误/13City cookie/3dtorrents 验证码/批量添加 Toast/筛选深链/删除确认）；澄清记录 11 条 |
| 13 | ☑ 完成 | 2026-10-25 | 搜索/缓存/设置/日志全量落地：SearchDialog（query 回车/Tracker MultiSelect/类别常量树/分项状态条 OK·Error·Unknown/骨架加载/错误态）、SearchResultTable（paginator 50/死种过滤/行展开海报+InfoHash+Ratio/SeedTime+DVF·UVF+FREELEECH 徽标/Details+IMDB·TMDB·TVDB 外链/jacketSize+jacketTimespan/黑hole+磁链+下载操作）、PresetBar（结构化预设 localStorage 同名覆盖）、hash 深链（裸 `#search=` 经 vue-router 斜杠规范化解析/刷新可恢复/打开即执行/restoreSearchHash 无痕还原）、SettingsDialog 四页签（ServerConfigForm 分区表单+PATCH 合并 diff+restart_required 横幅+400 分区 Alert+proxyWarning+密码修改、CacheReleases FirstSeen 视图+禁用提示、LogViewer 轮询开关+级别徽标+"加载更早"、ApiKeyPanel）；stores：search 状态机/日志分页/settings diff 合并与错误归段；vitest 90 项全过；后端契约修正 3 处（信封小写 results/indexers、TrackerCacheResult 自定义 MarshalJSON URL 字符串化、manualSearch 自动附带 apikey）均带回归测试；E2E 真实浏览器全过（深链刷新恢复/预设重放松散 URL 逐字符一致/类别过滤 4000→5 行/端口改→重启横幅/70000→分区 Alert 服务端原文/socks5 空 url→proxyWarning 原文/日志时间线一致/更新检查 toast）；澄清记录 5 节（§7 映射表 9 行逐行勾选） |
| 14 | ☑ 完成 | 2026-10-25 | 质量/发布/文档收口全量落地：质量门禁 make test 全绿（golangci-lint 全后端 0 issues[errcheck/staticcheck/govet/ineffassign]，覆盖率 common 80.33%/engine 82.18%/models+torznab 94.98%，前端 typecheck+eslint 0+vitest 95/95）；上游文案 ST1005 冲突全部经 tmp/ref 逐条核实后 nolint+出处保留（engine 31 条/httpclient/models 等全表见 SPEC-14 澄清记录）；覆盖率补测期间裁决并修复 engine 两处真实移植偏差（JSON rows 过滤链全丢行、_JsonSelectorRegex 嵌套括号截断，手写扫描器复刻 (?=:|\z) 前瞻）+ 首次移植上游 CardigannIndexerJsonTests 端到端 fixture（78 releases 全字段对拍）；make release 6 平台二进制+web 包+SHA256SUMS；Docker 镜像 49MB<60MB、non-root、HEALTHCHECK，CMD 带 --listenpublic 新卷开箱即达（对等 Jackett 官方镜像；--listenpublic/--listenprivate 旗标对齐上游 -x/-z 语义实测）；systemd 单元沙箱 verify 通过；vestt-import 导入工具全流程单测；文档同步（README 迁移章节/make test 说明、AGENTS.md、data-model.md cookies 事实、frontend.md 服务清单）；PRD §9 五条验收证据逐条出具（真实工具联调=需人工验证，见 SPEC-14 验收记录）；里程碑 M6 完成；澄清记录 7 节 |

> **UI 改版备注（2026-10-25，非 SPEC 变更，用户决策）**：
> ① 搜索首页化（Google 式查询框 + 页内结果，SearchDialog 退役）；② 管理功能拆分为
> `/indexers` + `/settings/{server,cache,logs}` 多路由（SettingsDialog/AppHeader 退役，改
> AppShell），v1.2 进一步收口：索引器入口并入齿轮菜单、API Key 并入服务器配置页；③ 液态
> 玻璃主题，v1.2 起支持亮/暗切换（默认暗）；④ v1.2 引入 vue-i18n，默认简体中文、可切英文。
> SPEC-11~13 的功能对等项全部保留（含上游深链兼容），仅布局/视觉/语言有意偏离上游；
> 详见 frontend.md v1.2 改版说明。下方 ☑ 状态不变。

状态取值：`☐ 未开始` / `◐ 进行中` / `☑ 完成` / `✗ 阻塞（备注注明原因）`。

## 5. 阶段进入/退出通用纪律

1. **进入**：前置 SPEC 状态为 `☑ 完成`；工作树干净（无未提交半成品）。
2. **执行中**：严格按 SPEC 的"实现步骤"落码；接口以 [tech/backend-api.md](../tech/backend-api.md) 为准；
   表结构以 [tech/data-model.md](../tech/data-model.md) 为准；上游行为存疑时读 `tmp/ref/` 对应源码，
   并在 SPEC 文件末尾追加"澄清记录"。
3. **退出**：SPEC"验收标准与验证方法"逐条通过；`make test` 全绿；更新本表后才能进入下一个 SPEC。
4. **发现文档错误**：优先修文档（对齐上游源码），再继续编码；文档与代码同步是硬约束。

## 6. 风险与缓冲

| 风险 | 影响 | 缓解 |
|---|---|---|
| YAML 引擎边界行为与上游有细微差异 | 部分站点解析错位 | SPEC-05/06 的 fixture 对拍 + 首批联调站点清单（每 SPEC 列出） |
| 上游站点结构性改版 | 移植后不可用 | 手写索引器 SPEC 只承诺"行为对等"（以参考代码为准），真实站点可用性为 SHOULD |
| modernc/sqlite 性能 | 缓存吞吐 | 读路径加内存索引；必要时连接池调优（data-model.md §5） |
| Torznab XML 细节（命名空间/日期格式） | Sonarr 拒收 | SPEC-03 用上游 XML 快照做逐字节对拍（属性顺序除外） |
| 62 个手写索引器工作量 | 主线延期 | 批次化（08~10）+ 与引擎阶段并行；单批次内可再按字母推进 |
