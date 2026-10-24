# SPEC-06 YAML 定义引擎 II：响应解析与转换器

| 项 | 内容 |
|---|---|
| 规模 | L |
| 前置依赖 | SPEC-05 |
| 产出 | 选择器解析 / 转换器注册表（全集）/ 行过滤器 / 字段→ReleaseInfo 映射 / 分类映射应用 / 分页链执行 —— 引擎端到端可用 |

## 1. 目标与范围

实现引擎的**输出侧**：HTML/XML/JSON 响应 → `[]models.ReleaseInfo`。
完成后，582 个 YAML 索引器在 Vesstt 上"可配置、可搜索、可下载"，与上游行为对等。

权威规范：`CardigannIndexer.cs` 的 `ParseResponse/ApplyFilters/ApplyConverters/ParseRow` 及相关工具类；
本 SPEC 所有边界行为以该文件为准。

## 2. 上游参考

| 参考文件 | 提取内容 |
|---|---|
| `src/Jackett.Common/Indexers/Definitions/CardigannIndexer.cs`（1120~2450 行为主） | 行解析、字段 switch（download/magnet/infohash/details/title/description/category/categorydesc/size/leechers/seeders/date/files/grabs/downloadvolumefactor/uploadvolumefactor/minimumratio/minimumseedtime/imdb 等）、转换器 switch、rowfilters、andmatch |
| `src/Jackett.Common/Models/IndexerDefinition.cs`（SelectorBlock 等） | selector/optional/attribute/remove/multiple/filters/converters 结构 |
| `src/Jackett.Common/Utils/ParseUtil.cs`、`DateTimeUtil.cs`、`StringUtil.cs` | 数值/时间/字符串解析辅助 |
| `src/Jackett.Common/Models/TorznabQuery.cs` | andmatch 关键词拆分语义 |

## 3. 实现步骤与代码结构

### 3.1 选择器引擎（`src/backend/common/engine/selectors.go`）

```go
type SelectorBlock struct {
    Selector   string;   Optional bool
    Attribute  string;   Remove   string
    Multiple   bool
    Filters    []RowFilter
    Converters []ConverterSpec
    CaseSensitive int    // title 字段大小写规范化开关（上游 3 值语义）
}
```

1. 基于 goquery：`SelectRows(doc, rowsSelector)` → 对每行 `SelectField(row, block)`。
2. `attribute` 缺省 = text；`href/src/data-*` 支持；`remove` 先剔除子树。
3. `multiple`：收集多值（join 规则对照上游，如 categories 多值）。
4. `optional`：零命中返回空串不报错；非 optional 零命中 → 该行跳过（对齐上游容错）。

### 3.2 行过滤器（`src/backend/common/engine/rowfilters.go`）

- `name=selector`：args = 子选择器（+可选 regex），取值过滤。
- `name=text`：args = 字面量/regex 匹配当前值。
- 语义：不匹配则丢弃该行（对照上游 ApplyFilters）。
- `search.paths[].filters`（andmatch 等）：`andmatch` = 用 `q` 的分词对**标题**做 AND 过滤
  （对照 TorznabQuery 关键词拆分 + 上游 andmatch 实现）。

### 3.3 转换器注册表（`src/backend/common/engine/converters.go`）

实现 PRD FR-305 全集，注册表 map[string]Converter，每个转换器独立单测。
语义要点（逐个对照 CardigannIndexer.cs 1120~1270 与既有行为）：

| 转换器 | 要点 |
|---|---|
| `querystring` | name+args → 从 .Query 取值 URL 编码 |
| `timeparse/dateparse/datetimeparse/timeago/reltime/fuzzytime` | args=layout；相对时间（"2 hours ago" 等）/模糊时间解析，输出 RFC3339 或 relative |
| `regexp/re_replace` | 提取/替换（组号、flags） |
| `split` | separator → 取第 N 段 |
| `replace/trim/prepend/append/tolower/toupper` | 字面量 |
| `base64decode/base64encode/urldecode/urlencode/htmldecode/htmlencode` | 编码族 |
| `validfilename` | Windows 非法字符替换 |
| `diacritics` | 变音符剥离 |
| `jsonjoinarray` | JSON 路径数组 join |
| `hexdump/strdump` | 调试输出语义 |
| `validate` | 正则不匹配则整行丢弃 |
| `sizeparse` | "1.5 GB" → 字节数 |
| `newznabcategories` | 类别文本 → ID |
| `regextokens/dateunits/uriappend/translate/map/multiply/divide/multiple` | 对照源码逐个实现 |

### 3.4 字段映射（`src/backend/common/engine/fields.go`）

selector 字段 → ReleaseInfo（对照 CardigannIndexer.cs 2214~2360 switch）：
`download/magnet/infohash/details/title/description/category/categorydesc/size/leechers/seeders/date/files/
grabs/downloadvolumefactor/uploadvolumefactor/minimumratio/minimumseedtime/imdb/tvdbid/tmdbid/tvmazeid/
traktid/doubanid/genre/languages/subs/author/booktitle/publisher/artist/album/label/track/year/poster`。

- 缺失容忍：`download` 与 `magnet` 皆空 → 丢弃该行（无可用下载）。
- `date` 解析失败 → PublishDate = MinValue（上游语义）。
- `seeders/peers`：peers = seeders+leechers 联动规则对照源码。
- `imdb`：剥离 `tt`，转 int64；无效值丢弃。
- `size`：支持 `sizeparse` 已转换或字面量后缀解析。

### 3.5 分类映射应用

- 解析出的 `category` 值 → 经 categorymappings（id→cat 映射 + filters）→ Torznab 类别集合；
- 反向 desc（categorydesc）同步生成；默认类别（default=true）注入。
- 分类过滤：请求 cat 参数与结果类别求交（对照上游 CategoryFilter 语义）。

### 3.6 分页链与整体管线（`src/backend/common/engine/pipeline.go`）

```
query → SPEC-05 请求链 → 逐请求（串行/限速）→ errorblock 检查 → ParseResponse
      → 行解析（3.1~3.5）→ 汇总去重（guid/link/infohash）→ IndexerResult
```

- 分页：`search.paths` 多链 = 请求按序执行，前页结果合并；`progress` 控制页码变量。
- 响应类型：HTML（goquery）/ XML（encoding/xml + goquery 兼容）/ JSON（gjson 风格 path，
  对照上游 JsonSelector 支持）。
- `testlinktorrent`：测试阶段尝试解析首条下载链接（对照上游 Test 行为）。

### 3.7 CardigannIndexer 收口

`ResultsForQuery` 全链路打通；`Download(link)` 走 3.7 下载请求生成 + client 转发；
测试路径（`Test()`）= 空词搜索 + 断言请求成功（结果数不做硬断言，对齐上游）。

## 4. 接口定义

```go
type Converter interface{ Apply(ctx *ParseCtx, value string, args []string) (string, error) }
engine.RegisterConverter(name string, c Converter)
// 管线入口（由 CardigannIndexer 调用）
func parseResponse(resp httpclient.Result, chain *PageChain, q *models.TorznabQuery) ([]models.ReleaseInfo, error)
```

对外仍无新 HTTP 端点；端到端行为经 SPEC-07 的 API 验证。

## 5. 验收标准与验证方法

1. **转换器全覆盖单测**：每个转换器 ≥ 3 用例（正常/边界/异常输入），合计 ≥ 120 用例；
   重点对拍：timeparse 各 layout、timeago（30+ 相对时间样本）、sizeparse（GB/MB/KB/TiB 变体）、
   regexp 组号、validate 丢弃语义。
2. **行解析矩阵**（httptest 假站点 + fixture HTML）≥ 20 用例：text/attribute/remove/multiple/
   optional 缺失/行过滤器丢弃/andmatch/多页合并/去重。
3. **抽样端到端对拍**：20 个真实定义（清单固化在 `engine/testdata/samples.txt`，覆盖
   public/private、HTML/JSON/XML 响应、freeleech/leech 标记、分页站点）。
   用 fixture 页面跑 `ResultsForQuery`，断言 ReleaseInfo 关键字段与人工预期一致。
4. **全量定义可执行性**：582 个定义全部能构造 caps + 生成搜索请求（SPEC-05 请求单测在
   引擎收口后自动扩展到全量：跑一遍"请求生成"断言无 panic/无空 URL）。
5. 引擎回归时长 < 60s（CI 可承受）；`make test` 全绿；更新 roadmap 进度表。

## 澄清记录

以 tmp/ref 源码为准澄清的实现决策（验收过程中逐条核实）：

1. **date 字段解析失败即整行丢弃（HTML）/请求失败（JSON）** —— 上游
   `CardigannIndexer.cs:2331-2334` 的 `case "date":` 无 try/catch，
   `DateTimeUtil.FromUnknownParse` 抛异常向上传播。本实现令 `parseFields`
   返回 error：HTML 行解析时丢弃该行，JSON 解析时使请求失败（与上游一致）。
2. **`.NET` 日期布局翻译**：Go `strings.NewReplacer` 对同起点并列项按**参数顺序**
   取先出现者，故 token 对按最长优先排列（`yyyy`→`2006` 在 `yy` 之前、`MMMM`→`January`
   在 `MMM`→`Jan` 之前），否则 `yyyy`/`MMMM` 会被 `yy`/`MMM` 抢匹配。
   单字母 `d`/`h` 同时补 `2`/`3`（Go 布局无等价单字母日/时 token）。
3. **无时区冒号的偏移量**（如 `+0200`）：`Z07:00` 布局拒绝无冒号形式，故
   `ParseDateTimeGoLang` 在含 `Z07:00` 布局失败时回退以 `-0700` 重试。
4. **JSON 字段中的日期字符串**（Newtonsoft `DateParseHandling.DateTime`）：
   JSON.NET 将 ISO-8601 形字符串转为 DateTime token，插值时按不变区域性输出
   `MM/dd/yyyy HH:mm:ss`（如 `"2019-03-31T00:00:00+02:00"` →
   `"03/31/2019 00:00:00"`）。本实现以 `jsonIsoDateRe` 识别并
   `tryDotnetDateToken` 模拟（多布局尝试）。
5. **`:contains()` 选择器组合**：AngleSharp 原生支持 `:contains`，goquery/cascadia
   不支持，故 `querySelectorAll` 先按顶层逗号分组（`splitSelectorGroups`），
   每组内逐复合选择器过滤、按节点去重取并集；组内 `>`/`+`/`~` 组合子退化为
   后代匹配（近似，已用 20 定义对拍确认无回归）。
6. **`.NET` 正则语义**：Go RE2 不支持后行断言/Unicode `\w`/`.NET` 组引用替换，
   故 `regexp`/`re_replace` 滤镜改用 `dlclark/regexp2` 获得 .NET 兼容语义。
   上游 `regexp` 滤镜恒取 `Groups[1].Value`（`CardigannIndexer.cs:1141-1153`），
   无捕获组时 .NET 抛 ArgumentOutOfRange → 数据置空；`re_replace` 以
   .NET `Regex.Replace` 语义执行替换。
   副作用：`.NET` `\w` 覆盖 Unicode（含西里尔字母），mazepa golden 中
   乌克兰语关键词被 `keywordsfilters` 以 `+词` 连接——与上游输出逐字符一致，
   golden 已按此更新。
7. **filters 文档表与上游 27 个滤镜名对齐**（上游 `CardigannIndexer.cs:1114-1275`）；
   实现全覆盖，文档表为准。
