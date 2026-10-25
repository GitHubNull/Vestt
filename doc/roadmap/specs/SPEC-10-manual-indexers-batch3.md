# SPEC-10 手写索引器移植 批次 3（PrivateHD → XSpeeds）

| 项 | 内容 |
|---|---|
| 规模 | L |
| 前置依赖 | SPEC-04；移植协议沿用 [SPEC-08 §3](SPEC-08-manual-indexers-batch1.md) |
| 产出 | 批次 3 共 21 个手写索引器的 Go 实现 + 注册 + 单测 —— 62 个手写索引器全部完成 |

## 1. 目标与范围

按 SPEC-08 移植协议完成批次 3，收口全部 62 个手写索引器（第 63 个 `CardigannIndexer.cs` 为引擎本体，
属 SPEC-05/06）。完成后上游手写索引器清单与 Vestt 注册表 1:1。

## 2. 上游参考

同 SPEC-08 §2（每索引器权威源为同名 `.cs`）。

## 3. 批次清单（21 个，按字母序）

| # | 索引器 | 上游文件 | 机制（已按上游基类校正，2026-10-25） |
|---|---|---|---|
| 1 | PrivateHD | Definitions/PrivateHD.cs | shared.Avistaz（上游 AvistazTracker 基类，json-api） |
| 2 | Redacted | Definitions/Redacted.cs | gazelle api（GazelleTracker 基类） |
| 3 | RetroFlix | Definitions/RetroFlix.cs | SpeedApp 系 json-api（SpeedAppTracker 基类，token 刷新同 SpeedApp） |
| 4 | RevoPeers | Definitions/RevoPeers.cs | html-login |
| 5 | RuTracker | Definitions/RuTracker.cs | cookie html（cap 分支请求顺序） |
| 6 | SceneHD | Definitions/SceneHD.cs | json-api（ConfigurationDataPasskey） |
| 7 | SecretCinema | Definitions/SecretCinema.cs | gazelle api（复用 shared.Gazelle） |
| 8 | Shazbat | Definitions/Shazbat.cs | html-login（自定义配置类 ConfigurationDataShazbat） |
| 9 | Simurg | Definitions/Simurg.cs | gazelle api（GazelleTracker 基类；初标 html 已校正） |
| 10 | SpeedApp | Definitions/SpeedApp.cs | json-api（SpeedAppTracker 基类，token 获取/过期重取） |
| 11 | SpeedCD | Definitions/SpeedCD.cs | html-login |
| 12 | SubsPlease | Definitions/SubsPlease.cs | json-api（无登录，公开 API） |
| 13 | Toloka | Definitions/Toloka.cs | html-login |
| 14 | TorrentBytes | Definitions/TorrentBytes.cs | html-login |
| 15 | TorrentDay | Definitions/TorrentDay.cs | html-login（分类 URL 模板，同 IPT 系逻辑） |
| 16 | TorrentNetwork | Definitions/TorrentNetwork.cs | html-login |
| 17 | TorrentsCSV | Definitions/TorrentsCSV.cs | json-api（自托管，site_link 配置项注入） |
| 18 | TorrentSyndikat | Definitions/TorrentSyndikat.cs | json-api（ConfigurationDataAPIKey；初标 html-login 已校正） |
| 19 | TVStore | Definitions/TVStore.cs | html-login |
| 20 | Uniotaku | Definitions/Uniotaku.cs | html-login |
| 21 | XSpeeds | Definitions/XSpeeds.cs | html-login |

## 4. 批次专项注意

- **json-api 系**（PrivateHD/Redacted/RetroFlix/SceneHD/SpeedApp/SubsPlease/TorrentsCSV）：
  复用 SPEC-09 建立的 JSON 帮助方法；SpeedApp 的登录 token 获取/过期重取逻辑必须单测。
- **Gazelle 系**（SecretCinema）：直接复用 SPEC-09 `indexer/shared/gazelle.go`。
- **TorrentDay**：分类映射表全表断言（与 IPTorrents 同源逻辑但 URL 主机/路径不同）。
- **RuTracker**：登录后欢迎文本校验 + `cap` 分支（需 cookie 会话的请求顺序）。
- **TorrentsCSV/Magnetico（SPEC-09）**：自托管站点，`site_link` 可配置（配置项注入 site_link）。

## 5. 验收标准与验证方法

1. 与 SPEC-08 §5 相同的通用验收（21 个索引器全绿、注册计数、3 个抽样全链路）。
2. **收口断言**：`TestAllManualIndexersRegistered` —— 62 个手写索引器 + 582 引擎定义 + 2 元索引器，
   注册表总数一致且无重复 id。
3. 批次专项：SpeedApp token 刷新单测；TorrentDay 分类全表断言；RuTracker 会话顺序单测；
   TorrentsCSV 自定义 site_link 生效断言。
4. 机制列校正回填本表；`make test` 全绿；更新 roadmap 进度表。

## 澄清记录

### 机制列校正（2026-10-25，基于上游基类核实）

实现前逐一对照上游 `.cs` 的基类声明，批次清单初标 3 处实质修正：

1. **PrivateHD**：初标 json-api → 实为 `AvistazTracker` 基类
   （PrivateHD.cs 类声明 `: AvistazTracker`），与 AvistaZ/CinemaZ/ExoticaZ 同族，
   复用 SPEC-08 `indexer/shared/avistaz.go`。
2. **Simurg**：初标 html → 实为 `GazelleTracker` 基类
   （Simurg.cs 类声明 `: GazelleTracker`），复用 `indexer/shared/gazelle.go`。
3. **TorrentSyndikat**：初标 html-login → 实为 `ConfigurationDataAPIKey`
   （TorrentSyndikat.cs 配置类），json-api + API key 鉴权。
4. 其余校正为细化：RetroFlix 为 `SpeedAppTracker` 基类（与 SpeedApp 共享
   token 获取/刷新逻辑）；SceneHD 为 `ConfigurationDataPasskey`；
   Shazbat/Toloka/TVStore/Uniotaku/SpeedCD 使用各自自定义配置类（仍为 html-login）；
   SubsPlease/TorrentsCSV 无登录（公开 json-api）。

### 波次分工

| 波 | 代理 | 索引器 |
|---|---|---|
| w1 | spec10-w1-rutracker | RuTracker（1889 行，单独成波） |
| w2 | spec10-w2-jsonapi | PrivateHD/RetroFlix/Redacted/SpeedApp/SceneHD/TorrentsCSV |
| w3 | spec10-w3-mid | SecretCinema/Simurg/Uniotaku/TorrentBytes/RevoPeers |
| w4 | spec10-w4-login | TorrentDay/SubsPlease/TorrentNetwork/SpeedCD/Shazbat |
| w5 | spec10-w5-tail | TorrentSyndikat/TVStore/XSpeeds/Toloka |

### 装配裁决（2026-10-25，50 例失败逐条对拍上游后收口）

全包首测 50 失败 / 17 文件，逐条以上游 `.cs` 为准裁决后：绝大多数为测试期望
错误（实现忠实），少量为真实实现偏差（已修）。以下为裁决确认、值得记录的
上游行为与移植决策：

**通用基类行为（影响多数索引器）**

- `BaseIndexer.FilterResults`（BaseIndexer.cs:174-183）按"查询类目 ∩ 释放类目
  非空"过滤：仅当查询携带类目时生效；空类目查询（浏览）保留全部。查询携带
  子类目（如 2030）不展开兄弟节点，仅顶级父类目展开到全部子类目
  （TorznabCapabilitiesCategories.cs:131-153）。
- `MapTorznabCapsToTrackers` 返回所有映射到查询类目的 tracker 类目（多对多
  全量随行，TorznabCapabilitiesCategories.cs:76-83）：TorrentDay
  `t.json?11;5;32;7;q=`、SpeedApp `categories[]=8&categories[]=29`。
- 配置项 ID 由显示名派生 `Name.Replace(" ","").ToLower()`
  （ConfigurationData.cs:105），代码里的 AddDynamic key 仅为查找键：如
  TorrentDay "freeleech" → id `searchfreeleechonly`、TorrentNetwork →
  `filterfreeleechonly`、Shazbat → `showpagesfetchlimit(sub-requestswhensearching)`。
- `ConfigurationDataBasicLogin` 无条件携带一个空名 Instructions
  DisplayInfoConfigurationItem（ConfigurationDataBasicLogin.cs:13-21；cookie
  变体 ConfigurationDataCookie.cs:13-22 同理）→ 表单 id 列表中的 `""` 项。
- 空 `QueryType` 的查询被 `CanHandleQuery` 拒绝（BaseIndexer.cs:275-311），
  返回空结果而非错误——上游真实请求总带 `t=search`（API 层注入）。

**逐索引器怪癖（复刻上游 bug/特殊行为）**

- SpeedApp/RetroFlix（共享 SpeedAppTracker 基类）：CleanTitle 仅删除
  `[REQUEST(ED)?]` 标记、不折叠残留的点（SpeedAppTracker.cs:234-236）→
  `Movie.Name.2024.1080p..BluRay` 双点为上游正确行为；token 过期 401 → 重登录
  → 单次重试（SpeedAppTracker.cs:126-130）；`categories[]` 键名不编码（值编码
  而键不编码，StringUtil.cs:155-158）。
- XSpeeds：config id `sortby`/`order` 为名称派生（ConfigurationData.cs:105）；
  登录 captcha 走 download 解析回调链；登录失败错误消息与 Kind 按上游原文。
- SpeedCD：登录 token 正则匹配 JSON 内嵌 HTML 的字面反斜杠引号
  （`name=\"a\" value=\"…\"`，SpeedCD.cs:140）；上游 `AllowAutoRedirect=false`
  的 302 Set-Cookie 读取语义在 Go 透明跟随栈下由最终响应等价承载
  （HttpWebClient.cs:85,220-238；SpeedCD.cs:162）；本地 `scdUrlEncode` 对 `*`
  放行（WebUtility 安全集，SpeedCD.cs:216/228）；CleanTitle 不折叠空格
  （SpeedCD.cs:295）；imdb 查询不带类目参数。
- TorrentNetwork：API 响应 `error` 字段经 OnParseError 走 "Parse error"
  （KindParse）而非原样抛出（TorrentNetwork.cs:233-234,291-294；
  BaseIndexer.cs:836-842）——**实现偏差已修**；会话过期重认证 =
  auth+curuser+browse 三请求（TorrentNetwork.cs:224-225）；配置表单补齐
  空 Instructions 项——**实现偏差已修**。
- Shazbat：`data-timestamp` 属性缺失（非空值）才回落到运行时刻
  （Shazbat.cs:230-233 的 null 门槛）——**实现偏差已修**；发布时刻每页
  ParseResults 重新初始化（Shazbat.cs:207）；`\b[S|E]\d+\b` 字符类怪癖导致
  S02E03 不被剥离（Shazbat.cs:275）；重登录后不复查重试响应
  （Shazbat.cs:292-296）。
- SubsPlease：`query.Season = 0` 在克隆后的查询上生效，调用方查询不受影响
  （SubsPlease.cs:151；BaseIndexer.cs:355-357 克隆语义）。
- Toloka：登录失败消息从 `table.forumline table span.gen` 提取
  （Toloka.cs:232-234）；重登录时 SiteLink 从配置项回落
  （BaseIndexer.cs:122-138）。
- TVStore：`1080i-HDTV` 命中 hdtv 源组且非 720p/1080p → TVSD（5030），
  RawHdRegex 的 `1080i[-_. ]HDTV` 分支对 HDTV 输入不可达（TvCategoryParser.cs
  :25,59-69）——上游死代码怪癖，实现正确。
- TorrentBytes/Toloka/TVStore 等同受 FilterResults 交集过滤约束（见通用节）。
- SceneHD：2040→{1,4}、5040→{5,7}（SceneHD.cs:66-72）；custom 1:1 类目
  随行使 caps 断言须用 contains 而非相等。
- Simurg/SecretCinema/Redacted（GazelleTracker 族）：ApplyConfiguration 自测
  合法执行空 PerformQuery（GazelleTracker.cs:184-211）→ 首请求无 `searchstr`。
- TorrentSyndikat：MultiSelectConfigurationItem.Values 为字符串数组
  （configdata/items.go:279-286 对齐上游）；发布时间按上游 `ToLocalTime()`
  语义（TorrentSyndikat.cs:195,212），断言以 Unix 时刻为准。
- TorrentsCSV：上游 `$"search?q={title}"` 原样插值，Go `url.URL` 在 RawQuery
  保留字面空格（仅在 String() 观测，与上游 wire 行为一致）。
- PrivateHD：持久 401 为普通异常 → KindInternal 而非 KindAuth
  （Abstract/AvistazTracker.cs 异常路径）。
- RevoPeers：`rows.Skip(1)` 使单行 fixture 成为表头 → 不报 parse error
  （RevoPeers.cs:181）；imdb href 带尾斜杠时 `Split('/').Last()` 为空 →
  锚定正则取 null（ParseUtil.cs:14）。

（波次代理自检未跑绿即交付的教训与裁决流程见 .scratch/spec10-assembly-plan.md。）
