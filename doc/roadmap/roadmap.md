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
| 08 | ☐ 未开始 | | |
| 09 | ☐ 未开始 | | |
| 10 | ☐ 未开始 | | |
| 11 | ☑ 完成 | 2026-10-24 | 前端基座全量落地：PrimeVue4 Aura definePreset + Toast/Dialog 服务 + 全局错误钩子、契约层（basePath 推断/锁定、401 归一、14 个命名 API）、5 stores（auth/config/logs 全功能 + indexers/search 骨架）、hash 路由 + 会话探针守卫、LoginView（上游 login.html 对等）、仪表盘壳（AppHeader API Key 遮罩/复制/注销、external 安全警告横幅、三下拉筛选 + 计数徽标、双 Tab 占位表、skeleton）、utils/hash；vitest 26 项全过；后端配套：/UI/assets 静态路由、DashboardGet 直出 SPA；浏览器实测登录全链路；澄清记录 13 条 |
| 12 | ☑ 完成 | 2026-10-24 | 索引器管理全量落地：双表（已配置 Status 徽标/未配置复选批量添加）、三下拉 AND 筛选 + 深度链接（路由 query 同步与恢复）、动态配置表单（9 类字段含 inputtags/验证码图 + alt 链接 + caps 表 + 错误 Alert）、配置/测试/删除/Test All（串行 200ms）全链路 store；vitest 50 项（store 18 + DynamicForm 快照 + CopyButton 2 等）全过；后端联调修复 3 处保真缺陷（sitelink 预填、:not(:contains()) 47 定义、inputtags 契约）均带回归测试；E2E 7 场景真实浏览器全过（0Magnet/0day.kiev 错误/13City cookie/3dtorrents 验证码/批量添加 Toast/筛选深链/删除确认）；澄清记录 11 条 |
| 13 | ☑ 完成 | 2026-10-25 | 搜索/缓存/设置/日志全量落地：SearchDialog（query 回车/Tracker MultiSelect/类别常量树/分项状态条 OK·Error·Unknown/骨架加载/错误态）、SearchResultTable（paginator 50/死种过滤/行展开海报+InfoHash+Ratio/SeedTime+DVF·UVF+FREELEECH 徽标/Details+IMDB·TMDB·TVDB 外链/jacketSize+jacketTimespan/黑hole+磁链+下载操作）、PresetBar（结构化预设 localStorage 同名覆盖）、hash 深链（裸 `#search=` 经 vue-router 斜杠规范化解析/刷新可恢复/打开即执行/restoreSearchHash 无痕还原）、SettingsDialog 四页签（ServerConfigForm 分区表单+PATCH 合并 diff+restart_required 横幅+400 分区 Alert+proxyWarning+密码修改、CacheReleases FirstSeen 视图+禁用提示、LogViewer 轮询开关+级别徽标+"加载更早"、ApiKeyPanel）；stores：search 状态机/日志分页/settings diff 合并与错误归段；vitest 90 项全过；后端契约修正 3 处（信封小写 results/indexers、TrackerCacheResult 自定义 MarshalJSON URL 字符串化、manualSearch 自动附带 apikey）均带回归测试；E2E 真实浏览器全过（深链刷新恢复/预设重放松散 URL 逐字符一致/类别过滤 4000→5 行/端口改→重启横幅/70000→分区 Alert 服务端原文/socks5 空 url→proxyWarning 原文/日志时间线一致/更新检查 toast）；澄清记录 5 节（§7 映射表 9 行逐行勾选） |
| 14 | ☐ 未开始 | | |

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
