# SPEC-05 YAML 定义引擎 I：加载校验与请求生成

| 项 | 内容 |
|---|---|
| 规模 | L |
| 前置依赖 | SPEC-04 |
| 产出 | .yml 加载与 schema 校验 / IndexerDefinition 结构 / 模板引擎 / login·search 请求生成 / CardigannIndexer 集成为合法 Indexer |
| 伴生 | SPEC-06（响应解析）共同构成完整引擎 |

## 1. 目标与范围

实现 Cardigann 定义引擎的**输入侧**：把 YAML 定义变成可执行的索引器（配置表单、登录、搜索请求生成、
下载请求生成、caps 构造）。响应解析在 SPEC-06。

设计基准：本 SPEC 的每个行为点都指向 `CardigannIndexer.cs` 的具体方法；
实现时必须逐方法对照，禁止凭感觉实现。

## 2. 上游参考（权威规范）

| 参考文件 | 提取内容 |
|---|---|
| `src/Jackett.Common/Indexers/Definitions/CardigannIndexer.cs` | 引擎主体：`Configure/ApplyConfiguration/PerformLogin/GetDownloadForData/GenerateQuery` 等（约 2600 行，行为规范） |
| `src/Jackett.Common/Models/IndexerDefinition.cs` | 定义结构体全集（含各 Block 类型） |
| `src/Jackett.Common/Definitions/schema.json` | 定义文件 JSON Schema（1358 行） |
| `src/Jackett.Common/Definitions/*.yml`（582 份） | 校验对象与联调样本 |
| `src/Jackett.Common/Definitions/IndexerValidations/`（如存在） | 定义级校验规则 |

## 3. 实现步骤与代码结构

### 3.1 定义模型与加载（`src/backend/common/engine/definition.go`）

1. `IndexerDefinition` Go 结构：字段对齐 IndexerDefinition.cs 全集
   （id/replaces/settings/name/description/type/language/encoding/requestDelay/links/legacylinks/
   followredirect/testlinktorrent/certificates/caps/login/ratio/search/download）。
2. `settingsField`：name/type(label/input/checkbox/select/info)/label/default/defaults/options。
3. 加载器：`LoadFS`（embed FS + 本地目录覆盖，便于调试新定义）→ yaml.v3 反序列化 →
   **JSON Schema 校验**（santhosh-tekuri/jsonschema v6 编译 schema.json，复用编译结果）→
   二次校验（id 唯一、links 可解析、caps.categories/categorymappings 冲突检测 = 上游加载校验）。
4. 启动时全量加载 582 份：任一失败记录日志并跳过（对等上游容错），统计 `loaded/skipped`。

### 3.2 模板引擎（`src/backend/common/engine/template.go`）

实现上游 Handlebars 使用子集（仅定义文件实际用到的能力）：

1. 变量替换：`{.Query.Key}`、`{.Config.flaresolverr}`、`{.Settings.xxx}`、`{.Token.*}`（token 由引擎管线注入）。
2. 条件块：`{{#if ...}}`、`{{#unless ...}}`、helper `if_eq/if_in/and/or/eq/ne`（语义对照
   CardigannIndexer.cs 395~409 行与 custom.js helper）。
3. 简单管道允许列表：反序列化时扫描模板使用的 helper，**不在允许列表** → 该定义加载失败并日志告警。
4. 时间变量：`{.Now}`（含日期算术，若定义中使用）。

### 3.3 配置表单生成（`Configure`）

- settings 字段 → ConfigData items：`input`→StringItem（password 名单 `password`/`passkey` → PasswordItem）、
  `checkbox`→BoolItem、`select`→SelectItem(options)、`info`→InfoItem；
  追加引擎基类公共字段（对齐 CardigannIndexer 公共 settings：`username/password/twoFactorAuthentication/
  captcha/cookieheader/useragent/flaresolverr` 等，按定义 settings 是否声明合并）。
- 定义含 `login.form`（非 cookie）时追加 info 提示块（对齐上游 info_flaresolverr/cookie 提示插入规则）。
- captcha 支持：`login.captcha` 块 → `GET config` 时拉取验证码 → `displayimage` 字段 `cardigannCaptcha`
  + 文本字段 `captchaText`（提交时携带）。

### 3.4 登录（`ApplyConfiguration` → `PerformLogin`）

对照 CardigannIndexer `PerformLogin` 全分支：

1. `login.method=form`：构建 POST（`login.path` 模板化、inputs 模板化、`login.form` 字段填充）；
   checkbox inputs 的 "checked" 语义；`submitpath` 可覆盖；响应判定与 `login.error` 选择器；
   成功标志 = 无错误选择器命中（对照上游 errorBlock 处理）。
2. `login.method=get`：带 selector inputs（解析页面取 hidden input）后提交。
3. `login.method=cookie`：`cookieheader`/`cookie` 字段解析入库。
4. `login.cookies`：登录后必须存在的 Cookie 白名单校验。
5. 两步验证：`twoFactorAuthentication` settings 字段透传到 form inputs。
6. 登录请求经过 client 栈（限速/CF/代理自动生效）；CF 盾拦截时走 FlareSolverr 后重试（SPEC-04 机制）。

### 3.5 caps 构造

- `caps.categories` / `caps.categorymappings` → `models.Capabilities`：
  - categorymapping：`{id, cat, desc, default}`；`cat` 支持逗号多映射与**新分类定义**（id 非标准段
    → 挂载到 8000 其他/自定义段，对照上游 categorymappings 处理）。
  - `caps.modes` → searching 支持位与 supportedParams（含 `allowrawsearch`/`allowtvsearchimdb`）。
- 构造结果缓存于索引器实例。

### 3.6 搜索请求生成（`GetAllTorrentUrls` 输入侧）

对照 `search` 块：

1. `search.paths[]` → `IndexerPageableRequestChain`：
   - path 模板变量：`.Query.*`（q/season/ep/imdbid/tmdbid/…分类参数）、`.Config.*`、`.Settings.*`。
   - `search.keywords` / `search.keywordsfilters`（对 q 的预处理，含 `keywordsall` 语义）。
   - category → path 参数：`categories` 模板字段（如 `?cat={category}`）与 categorymappings 的 cats 展开。
   - 分页：`page` 变量 + `search.paths[i].progress` 起始页与递增语义。
2. method/inputs/headers/query separator（`queryseparator` 默认 `;` → `&`）逐项对照。
3. `before` 块：搜索前执行一次登录或指定 path 请求（beforeblock 语义）。
4. `search.errorblock`：响应命中错误选择器 → IndexerError{Kind: Parse, Message=错误文本}。
5. 输出 `[]IndexerRequest`，由 SPEC-06 的解析器消费。

### 3.7 下载请求生成（`GetDownloadForData` 输入侧）

- `download.method=GET/POST`、`download.headers`、`download.selectors`（selector/attribute 取值）、
  `download.usesBeforeResponse`；无 download 块时默认下载 path 即详情页链接规则（对照上游默认行为）。
- magnet/infohash 块处理骨架（INFOHASH 块 → 先请求页面提取 infohash 生成磁链）。

### 3.8 CardigannIndexer 集成

`engine/cardigann.go` 实现 SPEC-04 的 `indexer.Indexer`：
- `ID()` = 定义 id；`Type/Language/Tags`（tags 取定义 settings 的 info 字段？否——上游 tags 来自
  `settings` 的 tag 声明缺失时为空；以 IndexerDefinition 为准）。
- `ResultsForQuery` = 3.6 生成请求 → 逐请求执行 → 交给 SPEC-06 解析器（本 SPEC 先返回空集 + TODO 接口，
  但**请求生成单测必须完备**）。
- `AlternativeSiteLinks`：定义 links[1:]；`followredirect/testlinktorrent/certificates` 传入 client 配置。

## 4. 接口定义

```go
engine.New(ctx, deps, def *IndexerDefinition) (*CardigannIndexer, error)
engine.LoadAll() (map[string]*IndexerDefinition, error)   // embed + 校验
CardigannIndexer 实现 indexer.Indexer（SPEC-04 §3.1）
```

对外无新 HTTP 端点；`GET /indexers`（SPEC-07）将展示引擎实例（含 582 个）。

## 5. 验收标准与验证方法

1. **全量加载**：`LoadAll()` 返回 582 份定义；失败清单为空（若有上游本身损坏的 yml，记录进
   `engine/testdata/known_bad.yml` 白名单并在验收报告注明）。
2. **Schema 校验**：注入 6 份变异 yml（缺必填/枚举越界/未知块）全部被拒。
3. **模板单测** ≥ 25 用例：变量替换/条件块/and/or/eq/ne/if_in/转义/缺失变量容错（空串替换）。
4. **登录矩阵**（httptest 假站点）≥ 8 用例：form 成功/表单校验失败/两步验证/cookie 登录/
   error selector 命中/captcha 提交流程/headers 注入/submitpath。
5. **请求生成**：选取上游代表定义 ≥ 12 个（覆盖 tvsearch/movie/search/music/book、
   category 模板、分页、keywordsfilters、before 块、queryseparator）→ 断言生成的 URL/Method/Body/Headers
   与预期（golden 文件）一致。
6. **caps**：10 个定义的 caps 输出对拍（categories 树 + searching 支持位 + imdb/tmdb 位）。
7. 抽样 5 个真实 yml 走 `Configure → GET config`：字段顺序/类型/默认值与上游 UI 展示一致。
8. `make test` 全绿；更新 roadmap 进度表。
