# SPEC-07 聚合 API 层与代理路由

| 项 | 内容 |
|---|---|
| 规模 | M |
| 前置依赖 | SPEC-06 |
| 产出 | ResultsController（torznab/potato/手动搜索）/ IndexerApiController / 缓存服务 / dl·img·bh 代理 / 元索引器全语义 —— 后端功能完整 |

## 1. 目标与范围

把 SPEC-03~06 的能力装配成完整后端 API：协议端点、缓存、代理下载、元索引器。
完成后即可用真实 Torznab 客户端（curl 模拟 Sonarr）全链路验证。

## 2. 上游参考

| 参考文件 | 提取内容 |
|---|---|
| `src/Jackett.Server/Controllers/ResultsController.cs` | 路由/过滤器链（RequiresApiKey/RequiresConfiguredIndexer/RequiresValidQuery）、Torznab 分支、Potato、手动搜索聚合、错误 XML、Retry-After |
| `src/Jackett.Server/Controllers/IndexerApiController.cs` | indexers 列表/Config/UpdateConfig/Test/Delete/Cache + 缓存代理化 |
| `src/Jackett.Server/Controllers/DownloadController.cs`、`BlackholeController.cs`、`ImageController.cs` | 三个代理端点行为 |
| `src/Jackett.Common/Indexers/Meta/`（BaseMetaIndexer/Fallbacks/ResultFilters） | all/aggregate/Tracker[]/CanHandleQuery 裁剪 |
| `src/Jackett.Common/Services/CacheService.cs` | 缓存键/命中/容量/清理语义 |

## 3. 实现步骤与代码结构

### 3.1 缓存服务（`src/backend/common/services/cache.go`）

- 键：`sha1(indexerID + q.GetQueryString() + cats)`（对照上游 CacheService 键构造）。
- 内存 map + SQLite 双层：读先内存后库；写双写（异步批 50ms）。
- 命中判定 TTL（`cache_ttl`）；`query.Cache=false` 跳过；结果数上限 `cache_max_results_per_indexer`。
- `CleanIndexer(id)` / `CleanAll()`；`GetCachedResults()`（Cache 视图，按 FirstSeen 排序）。

### 3.2 ResultsController（`src/backend/server/api/results.go`）

路由 `Route("/api/v2.0/indexers/{indexerId}/results")` + 中间件链 =
`RequireApiKey → RequireConfiguredIndexer → RequireValidQuery`（对齐上游三个 TypeFilter 的错误语义：
401/404/400 与 Torznab 路径的 XML 错误包装）。

1. **Torznab**（`GET .../results/torznab/`）：
   - `t=caps` → 当前索引器 caps XML。
   - `t=indexers` → 仅元索引器；configured 参数过滤；XML 结构见 backend-api.md §3.2。
   - imdbid 归一化 + 能力位校验（错误码 201/203 语义与上游一致）。
   - 查询执行 → 缓存查询 → ReleaseInfo 克隆 → `ConvertToProxyLink`（`/dl/{id}?path=<上游URL>&file=<文件名>`
     与 `/img`、blackhole 时 `/bh`）→ `WriteRSS`。
   - 429：透传 `Retry-After` 头 + 429 + 错误 XML（code 900）。
   - 日志行对齐上游格式（"Torznab search in X => Found N releases (from cache) [Tms]"）。
2. **Potato**（`GET .../results/potato/`）：字段映射（backend-api.md §3.3），
   `size` 单位 MB、`release_name` 追加 `[索引器名]`、`freeleech = DownloadVolumeFactor==0`。
3. **手动搜索**（`GET .../results`）：
   - `Tracker[]`/`Category[]` 解析（csv）；`InteractiveSearch=true`。
   - 元索引器 = ValidIndexers ∩ Tracker[] 过滤；并发扇出（errgroup）+ 单项失败归档。
   - 响应结构 `ManualSearchResult`（backend-api.md §3.1），结果按 PublishDate 倒序。
   - 缓存结果代理化（Link/Poster/BlackholeLink；BlackholeLink 仅配置了 blackholedir）。

### 3.3 IndexerApiController（`src/backend/server/api/indexer.go`）

- `GET /api/v2.0/indexers?configured=` → DTO 列表（caps/potatoenabled/last_error/tags/...）。
- `GET/POST /{id}/Config`、`POST /{id}/Test`、`DELETE /{id}` —— 全部委托 IndexerManagerService；
  POST Config 前清该索引器缓存；失败回滚未配置态（SPEC-04 已实现，这里接错误码 400/500）。
- `GET /api/v2.0/indexers/Cache` → 缓存视图（代理化链接 + BlackholeLink 规则同手动搜索）。

### 3.4 代理端点（`src/backend/server/api/proxy.go`）

| 路由 | 行为 |
|---|---|
| `GET /dl/{indexerId}` | 校验 apikey → 索引器存在 → `path` 参数构造上游 URL → `Download(ctx)` → 流式返回；`Content-Disposition: attachment; filename=<file>` |
| `GET /img/{indexerId}` | 同上，透传图片 Content-Type，禁缓存头 |
| `GET /bh/{indexerId}` | 取回字节（或磁链）→ 写 `blackholedir/<file>.torrent|.magnet` → 200 文本回执；目录未配置 → 400 |

- `path` 为上游站点相对/绝对 URL（UTF-8 URL 编码，`ConvertToProxyLink` 生成时保持可逆）。

### 3.5 元索引器收口（`src/backend/common/indexer/meta`）

- 对外只有 `all`（AggregateIndexer，成员为全部注册索引器）；类型过滤
  （`public`/`private`/`semi-public`）与 tag 过滤索引器按 FilterFunc 动态生成。
- `CanHandleQuery`：目标集合中任一支持即支持；扇出前逐个过滤（Fallbacks/ResultFilters 语义对照）。
- `t=indexers`、`Tracker[]`、`configured` 参数全覆盖。

### 3.6 装配与冒烟

main.go 接入全部路由；`make run` 后可用：
```
curl 'http://127.0.0.1:9117/api/v2.0/indexers/all/results/torznab/?apikey=$KEY&t=caps'
curl 'http://127.0.0.1:9117/api/v2.0/indexers?configured=false'   # 管理会话
```

## 4. 接口定义

backend-api.md §2、§3、§4 全部端点在本 SPEC 交付（§1、§5、§6 已在 SPEC-02 交付）。
任何字段级出入一律以 backend-api.md 为准修代码或修文档（记录澄清）。

## 5. 验收标准与验证方法

1. **集成测试**（httptest 假站点注册 3 个假索引器 + 1 个引擎索引器 fixture）：
   - caps / search / tvsearch / movie / book / music 全型请求；
   - 错误矩阵：无 apikey(401 XML code100)、未知索引器(404 XML 201)、未配置(404 XML 201)、
     能力外参数(400 XML 201/203)、上游 429(429 + Retry-After)；
   - 缓存：两次相同请求第二次命中（响应头 + 日志），TTL 过期后回源；
   - 手动搜索聚合：1 失败 + 2 成功 → `indexers[].status` 正确、结果合并倒序；
   - Tracker[] 过滤、Category 过滤、imdbid 归一化（tt0001234/tt1234/非法值）。
   - Potato 字段映射 golden。
   - /dl 流式返回 + Content-Disposition；/bh 写盘断言；/img Content-Type。
2. **协议冒烟**：以真实 curl 打 `all` 的 caps/search（无真实站点联网，走 fixture 索引器），
   输出可被 `xmllint --noout` 校验。
3. **缓存一致性**：配置变更/删除索引器后 `Cache` 视图对应行消失。
4. 性能抽查：fixture 索引器扇出 50 个并发 → P95 < 500ms（本机无网络延迟基线）。
5. `make test` 全绿；更新 roadmap 进度表。

## 澄清记录

以 tmp/ref 源码为准澄清的实现决策（验收过程中逐条核实）：

1. **缓存键与 TTL**（上游 `CacheService.cs`）：键 = `SHA-256(JSON(TorznabQuery))`
   （仅查询对象，不含索引器 id/类别参数）；JSON 序列化时空 `SearchTerm` 归一为 `""`；
   TTL 单位为**秒**（`CacheTTL=2100`）。SPEC §1.1 草稿的 sha1(id+query+cats)/分钟口径作废。
2. **Tracker 字段在缓存层填充**：`GetCachedResults` 时由 TrackerCache 条目写入
   `Tracker/TrackerId/TrackerType`（CacheResults 时从索引器对象捕获），API 层不填；
   `TrackerCacheResult` 不含 Languages/Subs（上游 DTO 即如此）。
3. **torznab 路径错误语义**：一律 HTTP 200 + XML 错误文档（code 100 无 apikey /
   201 参数与配置类 / 203 能力不支持），仅上游 429 → HTTP 429 + `Retry-After` +
   XML code 900。SPEC §5 验收矩阵写的 401/404/400 状态码作废。
4. **未知索引器在过滤器链内抛异常 → HTTP 500**（results/admin API 全部）；
   仅 `/dl` 捕获后 404、`/img` 捕获后 500、`/bh` 走 JSON error。SPEC §5 的 404 口径作废。
5. **`POST /{id}/Config` 失败 → 500**（上游 action 无错误映射，异常冒泡）。
   backend-api.md §2 的 400 口径已回写为 500。
6. **手动搜索 `Indexers[].Status` 序列化为整数**：0=unknown / 1=error / 2=OK；
   状态条目与请求 tracker 列表 zip（不信任 result.Indexer）。backend-api.md §3.1
   的字符串 "OK" 口径已回写为整数。
7. **无 `aggregate` 这个 id**：上游 `GetWebIndexer` 只特判 `"all"`
   （IndexerManagerService.cs:312）+ FilterFunc 动态过滤索引器。SPEC §3.5 已修正。
8. **`t=indexers` 在非元索引器上 → code 201**（`RequiresValidQuery` 先跑
   `CanHandleQuery`，BaseIndexer.cs:275 对 "indexers" 查询类型返回 false）；
   "this isn't a meta indexer"（203）分支对基础索引器不可达，仅为上游原样保留。
9. **两个 ConfigureCacheResults 变体**：IndexerApiController（Cache 视图）**无**
   MagnetUri 回退，blackhole 门控用**原始** Link 的 scheme != magnet；
   ResultsController（手动搜索）**有** MagnetUri 回退并检查**代理后**的 Link。
   两者 `file = MakeValidFileName(title, '_', false)`。
10. **bencode 重编码**：BencodeNET 的 Parse/EncodeAsBytes 行为移植为
    `common/utils/bencode` 规范编码器（字典键 bytewise 排序）；解析失败 →
    记录内容日志 + 404（上游 `DownloadController.cs`）。
11. **`MakeValidFileName` 的非法字符集取 .NET Unix 口径**（`Path.GetInvalidFileNameChars()`
    = NUL 与 `/`，见 stringutil.go:12）：空格等保留，文件名可出现空格；
    代理端点写入前另有防御性清洗。
12. **`UncleanLink`/downloadUrlBase 未移植**：上游 `DownloadImage` 的链接反清洗
    依赖 Jackett 全局保护服务，Vestt 无此层；`Base.DownloadImage` 直接
    `client.Do`（Referer=SiteLink、索引器 Cookie、EmulateBrowser）。
13. **Potato 代理用 `CurrentIndexer.Id`**（非 release 来源索引器）；potato 动作
    无错误映射——失败直接 500。`size` 单位 MiB（Size/1024/1024），
    `leechers = (Peers ?? -1) - (Seeders ?? 0)`。
14. **chi 路由注册**：上游 ASP.NET `{ignored?}` 容忍尾斜杠，故 torznab/potato/
    dl/img/bh 均注册 `/x` 与 `/x/`（+ `{ignored}`）两种拼写。
15. **429 路径**：httpclient 遇站点 429 返回 `TooManyRequests{RetryAfter}`，
    Base 包装为 `IndexerError{KindRateLimit}`，torznab 动作映射为
    429 + Retry-After + XML code 900。
