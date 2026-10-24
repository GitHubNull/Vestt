# SPEC-03 Torznab 核心模型与 XML 序列化

| 项 | 内容 |
|---|---|
| 规模 | M |
| 前置依赖 | SPEC-02 |
| 产出 | TorznabQuery / TorznabCategories / TorznabCapabilities / ReleaseInfo / RSS·caps·error XML 序列化（纯库，不含路由） |

## 1. 目标与范围

实现 Torznab 协议的**全部数据结构与 XML 序列化**，字节级对等上游输出（属性顺序不敏感，元素与值必须一致）。
这是元索引器、YAML 引擎、手写索引器共用的协议底座。

不在范围：HTTP 端点（SPEC-07）、能力位与索引器逻辑的连接（SPEC-04）。

## 2. 上游参考（权威规范）

| 参考文件 | 提取内容 |
|---|---|
| `src/Jackett.Common/Models/TorznabQuery.cs` | 查询参数全集、`ToTorznabQuery` 解析、`IsXxxSearch` 判定、`GetQueryString`、`SanitizedSearchTerm`、`CreateMock` |
| `src/Jackett.Common/Models/TorznabCategory.cs`、`TorznabCatType.cs` | 完整类别树（约 60 节点）与 `GetCatDesc/GetNewznabCategories` 映射 |
| `src/Jackett.Common/Models/TorznabCapabilities.cs`、`TorznabCapabilitiesCategories.cs` | 能力位模型与 XML（searching/categories/limits/server/registration） |
| `src/Jackett.Common/Models/ReleaseInfo.cs` | 条目字段全集（含 `Gain`、`GigabytesFromBytes`） |
| `src/Jackett.Common/Models/ResultPage.cs`、`ChannelInfo.cs` | RSS channel/item 结构与 torznab/newznab 属性输出 |
| `src/Jackett.Server/Controllers/ResultsController.cs`（`CreateErrorXML`） | 错误 XML |

## 3. 实现步骤与代码结构

### 3.1 `src/backend/common/models`（包 `models`）

1. `torznab_query.go`：
   ```go
   type TorznabQuery struct {
       QueryType string; Categories []int; Extended int; Limit, Offset int
       RageID, TvdbID, TmdbID, TvmazeID, TraktID, DoubanID *int
       ImdbID string; Season int; Episode string; SearchTerm string
       Album, Artist, Label, Track string; Year *int; Genre string
       Author, Title, Publisher string
       InteractiveSearch, Cache, IsTest bool
   }
   ```
   - `ParseTorznabQuery(url.Values)`：全部参数容错解析（int 失败忽略，等价上游 TryParse 语义）。
   - 判定方法：`IsSearch/IsTVSearch/IsMovieSearch/IsMusicSearch/IsBookSearch/IsRssSearch/IsIdSearch` 等，
     逐行对照 TorznabQuery.cs。
   - `GetQueryString()`：上游同名去抖动格式（标准 dash/引号、多空白折叠、Season/Episode/ID 追加）。
   - `SanitizedSearchTerm`：小写去特殊字符搜索词。
   - `CanHandleQuery(caps)` 逻辑放 caps 包（本 SPEC 提供 `caps.Supports(query)`）。
2. `torznab_category.go`：`Category{ID int, Name string, Subs []*Category}`；
   `ParseTorznabCategories(csv)` / `GetCatDesc(id)` / `GetCatDescByName`；
   **完整类别表**从 `TorznabCatType.cs` 静态转录（以常量列表呈现，逐条对照）。
3. `torznab_capabilities.go`：
   ```go
   type Capabilities struct {
       Searching SearchCaps  // Search/TVSearch/MovieSearch/MusicSearch/BookSearch
       SupportedTVSearchParams, SupportedMovieSearchParams, SupportedMusicSearchParams, SupportedBookSearchParams []string
       Categories *CapsCategories
       LimitsMax, LimitsDefault int
   }
   ```
   - `XML()`/`MustXML()` 生成 `<caps>` 文档（结构顺序：server → limits → registration → searching → categories，
     属性值逐项对照 TorznabCapabilities.cs `ToXml`）。
   - `SupportsCategory / SupportsParam(queryType, param) / SupportsQuery(query)`。
4. `release_info.go`：ReleaseInfo 全字段（backend-api.md §2.6 列表为准）+ `Clone()`（深拷贝集合）+ `Gain()`。

### 3.2 `src/backend/common/torznab`（包 `torznab`）

1. `rss.go`：`WriteRSS(w, channel ChannelInfo, releases []models.ReleaseInfo, serverURL *url.URL)`。
   - 命名空间：`xmlns:torznab="http://torznab.com/schemas/2015/feed"` 与 newznab 命名空间同时声明（对齐上游）。
   - item 属性输出规则（对照 ResultPage.ToXml）：category（多个用逗号拼接的 torznab:attr）、
     seeders/peers/files/grabs、imdb（`tt`+格式）、size、downloadvolumefactor/uploadvolumefactor、
     minimumratio/minimumseedtime、magnet（magnet URI 时 `link` 为磁链 + `magnet` 属性）、
     `enclosure`（length=size, type=application/x-bittorrent）。
   - `pubDate` RFC1123 GMT；时间零值处理与上游一致。
2. `caps.go`：薄封装 models.Capabilities 输出。
3. `error.go`：`ErrorXML(code int, desc string)` —— 与上游 `CreateErrorXML` 完全一致（声明行 + error 元素）。
4. `indexers.go`：`t=indexers` 文档序列化（供 SPEC-07）。

### 3.3 序列化实现要求

- 使用 `encoding/xml` 自定义类型（attr 用 `,chardata`/`,attr` 标签显式控制），禁止依赖 map 顺序。
- 提供对拍工具 `torznab/testdata`：以**上游运行时抓取的样例**为 golden 文件（入库 5~8 份：
  caps/search/tvsearch/movie/book/music/空结果/error）。
- 若无法现场抓取上游输出，以本 SPEC 引用的源码行为准手写 golden，并在文件头注明生成依据。

## 4. 接口定义

本 SPEC 为内部库，无 HTTP 接口。下游使用约定：

```go
q, err := torznab.ParseQuery(r.URL.Query())
caps.SupportsQuery(q) → (ok bool, reason string)
torznab.WriteRSS(w, ch, releases, serverURL)
torznab.ErrorXML(code, desc)
```

## 5. 验收标准与验证方法

1. **类别表完整性**：`TestCategoryTable` 断言节点数与 `TorznabCatType.cs` 中 `TorznabCategory` 初始化条数一致；
   抽查 15 个（1000/2000/2010/2040/3000/3030/4000/4050/5000/5030/5040/6000/7000/7030/8000）desc 一致。
2. **查询解析矩阵**（表驱动 ≥ 30 用例）：q/cat/imdbid（tt 前缀、7/8 位）/tmdbid/tvdbid/tvmazeid/season+ep/
   extended/limit/offset/音乐图书参数/非法值容错；`IsMovieSearch` 等 8 种判定全覆盖。
3. **XML 对拍**：
   - caps golden 对拍（含 unsupported searching 全 no 的变体、categories 树嵌套）。
   - RSS golden 对拍：普通条目 / magnet-only 条目 / freeleech（d v f=0）/ 多类别 / imdb 属性 /
     极大 size / 无 seeders。
   - error XML 对拍 100/201/203/900。
4. `GetQueryString` 与上游一致（含 dash/引号标准化用例 6 条）。
5. `go vet`、`gofmt`、覆盖率：`common/models` 与 `common/torznab` ≥ 85%。
6. 更新 roadmap 进度表。
