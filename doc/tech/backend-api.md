# Vestt 后端 API 契约

| 项 | 内容 |
|---|---|
| 文档版本 | 1.0 |
| 上游基线 | Jackett 0.24.2900（`Jackett.Server/Controllers/*`） |
| 基地址 | `http://127.0.0.1:9117`（含 basePath 时所有路径前缀化） |

> 字段命名严格跟随上游序列化结果（小写 snake/连字符保持原样）。本文件是前端
> `services/api.ts` 与后端 handler 的唯一契约来源；实现与本文冲突时，先修文档对齐上游，再修代码。

## 0. 鉴权模型

| 机制 | 适用 | 载体 | 行为 |
|---|---|---|---|
| API Key | Torznab/Potato/手动搜索/代理下载/Shutdown | 查询串 `apikey`（兼容别名 `passkey`） | 不匹配 → 401（Torznab 路径返回 code=100 XML） |
| 管理会话 | 管理 API + UI 页面 | Cookie `vestt_session`（HttpOnly, SameSite=Lax） | 未启用管理密码时全部放行；启用后未登录访问管理 API → 401 |
| 匿名 | `/health`、`/UI/Login`、`/UI/TestCookie`、静态资源 | — | — |

Torznab 错误响应统一为（HTTP 状态恒为 200，仅上游 429 例外——整包 429 + `Retry-After`）：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<error code="100" description="Invalid API Key" />
```

| code | 语义 |
|---|---|
| 100 | Invalid API Key |
| 201 | Indexer is not specified / not supported / not configured；或请求参数越出索引器能力 |
| 203 | Function Not Available（能力位不满足，如 tvsearch 带 imdbid） |
| 900 | 通用内部错误（description 含详情）；上游 429 时整包 429 + `Retry-After` 头 |

---

## 1. 服务器 API（`/api/v2.0/server/*`）

### 1.1 `GET /api/v2.0/server/config`

返回运行配置与版本信息。

响应 200：

```json
{
  "notices": [],
  "port": 9117,
  "external": false,
  "local_bind_address": "127.0.0.1",
  "cors": false,
  "api_key": "9m8n7b6v5c4x3z2a1s0d9f8g7h6j5k4l",
  "blackholedir": "",
  "updatedisabled": false,
  "prerelease": false,
  "password": "********",
  "logging": false,
  "basepathoverride": "",
  "baseurloverride": "",
  "cache_enabled": true,
  "cache_ttl": 2100,
  "cache_max_results_per_indexer": 1000,
  "flaresolverrurl": "",
  "flaresolverr_maxtimeout": 60000,
  "omdbkey": "",
  "omdburl": "",
  "app_version": "0.1.0",
  "can_run_netcore": true,
  "proxy_type": "-1",
  "proxy_url": "",
  "proxy_port": null,
  "proxy_username": "",
  "proxy_password": ""
}
```

- `password`：已设置时返回上游等价的掩码占位（上游为 DPAPI 保护串；Vestt 返回 `********`，
  提交 `********` 表示不修改）。
- `proxy_type`：`-1`=disabled，`0`=http，`1`=socks4，`2`=socks5（上游 ProxyType 枚举值语义）。
- `can_run_netcore`：为兼容上游 DTO 保留，恒为 `true`。

### 1.2 `POST /api/v2.0/server/config`

请求体 = 1.1 响应同构 JSON（可仅包含变更字段）。服务端逐项校验：

| 字段 | 校验 | 副作用 |
|---|---|---|
| port | 1-65535 且不在受限列表 | `restart_required: true` |
| local_bind_address | 必须为回环地址 | `restart_required: true` |
| external | bool | `restart_required: true` |
| basepathoverride | 空 或以 `/` 开头 | `restart_required: true` |
| baseurloverride | 空或绝对 http(s) URL | 立即生效 |
| flaresolverrurl | 空或绝对 http(s) URL | 立即生效 |
| flaresolverr_maxtimeout | ≥ 5000ms | 立即生效 |
| proxy_* | port 1-65535；url 为空时 type 强制 disabled | 清空结果缓存；`restart_required: true` |
| blackholedir | 空或目录必须存在 | 立即生效 |
| cache_enabled/ttl/max_results_per_indexer | 数值 | 立即生效 |
| logging | bool | 切换 slog 级别 Info/Debug |

响应 200：同 1.1 结构，另加 `"restart_required": true|false`。
校验失败 → 400 + 纯文本错误消息（前端 toast 直接展示）。

### 1.3 `POST /api/v2.0/server/adminpassword`

请求体：JSON 字符串（原始字符串字面量，如 `"newpass"`）。空串/`null` 清除密码。
响应 204。设置后所有管理请求与会话需重新登录。

### 1.4 `GET /api/v2.0/server/logs`

响应 200：

```json
[ { "level": "INFO", "message": "Manual search in Example => Found 12 releases [230ms]", "when": "2026-10-24T12:00:00Z" } ]
```

环形缓冲，最新在前，默认容量 1000 条（`CachedLog` 三字段：`level/message/when`）。

### 1.5 `POST /api/v2.0/server/update`

触发版本检查（对等上游 UpdateService；Vestt 仅检查并记录日志，不自动替换二进制）。响应 204。

### 1.6 `POST /api/v2.0/server/shutdown`

需 API Key（查询串）。响应 200 `{"ShuttingDown": true}`，随后优雅停机。

---

## 2. 索引器管理 API（`/api/v2.0/indexers/*`）

### 2.1 `GET /api/v2.0/indexers?configured=false`

响应 200，数组（`DTO.Indexer`）：

```json
[
  {
    "id": "exampletracker",
    "name": "Example Tracker",
    "description": "This is an example tracker",
    "type": "private",
    "configured": false,
    "site_link": "https://example.com/",
    "alternativesitelinks": ["https://alt.example.com/"],
    "language": "en-US",
    "tags": ["movies"],
    "last_error": null,
    "potatoenabled": false,
    "caps": [ { "ID": "2000", "Name": "Movies" }, { "ID": "2020", "Name": "Movies/SD" } ]
  }
]
```

`caps` 为该索引器声明的 Torznab 类别（含子类），`ID/Name` 首字母大写跟随上游 DTO。

### 2.2 `GET /api/v2.0/indexers/{indexerId}/Config`

返回动态配置表单描述：**JSON 数组**（上游 `ConfigurationData.ToJson` = JArray）。

```json
[
  { "id": "username", "type": "inputstring", "name": "Username", "value": "" },
  { "id": "password", "type": "password", "name": "Password", "value": "" },
  { "id": "authentication", "type": "inputselect", "name": "Authentication method",
    "value": "form", "options": { "form": "Form", "cookie": "Cookie" } },
  { "id": "categories", "type": "inputcheckbox", "name": "Categories",
    "values": ["2000"], "options": { "2000": "Movies", "3000": "Audio" } },
  { "id": "cookieheader", "type": "inputstring", "name": "Cookie header" },
  { "id": "flaresolverr", "type": "displayinfo", "name": "This site uses Cloudflare..." },
  { "id": "cardigannCaptcha", "type": "displayimage", "name": "Captcha", "value": "data:image/png;base64,..." }
]
```

字段类型全集：`inputstring | password | hiddendata | inputbool | inputcheckbox | inputselect | inputtags | displayinfo | displayimage`
（`inputtags` 为 Tags 字段，带 `separator/pattern/delimiters` 元数据，提交值为逗号拼接字符串；
上游 `ConfigurationData.ToJson` 另有 `recaptcha` 类型，后端不发射、前端不渲染）。

### 2.3 `POST /api/v2.0/indexers/{indexerId}/Config`

请求体：`ConfigItem[]`（与 2.2 的 id 对应）：

```json
[
  { "id": "username", "value": "user" },
  { "id": "password", "value": "pass" },
  { "id": "categories", "values": ["2000", "2050"] },
  { "id": "cookie", "cookie": "uid=1; pass=abc" }
]
```

- `values`：多选数组；`cookie`：Cookie 免密登录通道（`ConfigurationDataCookie` 形态）。
- 服务端执行：清该索引器缓存 → 应用配置 →（引擎定义含自动测试时）执行测试。
- 成功 204；登录失败等 → 500 + 错误消息；失败时回滚该索引器为未配置态（上游 ResetBaseConfig 语义）。

### 2.4 `POST /api/v2.0/indexers/{indexerId}/Test`

执行一次真实搜索验证。成功 204（`last_error` 清空）；失败 500 + 错误消息并写入 `last_error`。

### 2.5 `DELETE /api/v2.0/indexers/{indexerId}`

清除配置、会话 Cookie 与缓存。响应 204。

### 2.6 `GET /api/v2.0/indexers/Cache`

返回缓存 releases（`TrackerCacheResult[]`，字段 = ReleaseInfo 全集 + 下方扩展）：

```json
[
  {
    "Title": "Example Movie 2020 1080p BluRay",
    "Guid": "https://example.com/t/123",
    "Link": "http://127.0.0.1:9117/dl/exampletracker?path=...&apikey=...",
    "Details": "https://example.com/t/123",
    "PublishDate": "2026-10-20T18:30:00Z",
    "Category": [2000, 2040],
    "Size": 14680064000,
    "Seeders": 88,
    "Peers": 95,
    "DownloadVolumeFactor": 0.0,
    "UploadVolumeFactor": 1.0,
    "MinimumRatio": null, "MinimumSeedTime": null,
    "Imdb": 1234567, "TVDBId": null, "TMDb": null,
    "Description": null, "Files": null, "Grabs": null,
    "Languages": [], "Subs": [], "Genres": [],
    "Poster": "http://127.0.0.1:9117/img/exampletracker?path=...&apikey=...",
    "InfoHash": null, "MagnetUri": null,
    "FirstSeen": "2026-10-24T10:00:00Z",
    "Tracker": "Example Tracker", "TrackerId": "exampletracker", "TrackerType": "private",
    "CategoryDesc": "Movies/HD",
    "BlackholeLink": null
  }
]
```

> JSON 序列化风格：ReleaseInfo 字段为 PascalCase（跟随上游 Newtonsoft 默认），手动搜索与
> 缓存视图直接透传该结构。**实现以对拍上游输出为准。**

---

## 3. 结果 API（`/api/v2.0/indexers/{indexerId}/results/*`，需 API Key）

### 3.1 手动搜索：`GET /api/v2.0/indexers/{indexerId}/results`

查询参数：`query`、`Tracker[]`（csv，仅元索引器有效）、`Category[]`（csv int）、
`imdbid`、`tmdbid`、`tvdbid`、`tvmazeid`。`indexerId` 可为具体索引器、`all`（聚合全部）
或类型/tag 过滤 id（`public`/`private`/`semi-public`/tag 名）。

响应 200（`ManualSearchResult`）：

```json
{
  "results": [ { "...TrackerCacheResult 全集...": null } ],
  "indexers": [
    { "ID": "exampletracker", "Name": "Example Tracker", "Status": 2,
      "Results": 42, "ElapsedTime": 630, "Error": null },
    { "ID": "brokenone", "Name": "Broken One", "Status": 1,
      "Results": 0, "ElapsedTime": 0, "Error": "Exception: login failed..." }
  ]
}
```

`Status` 为整数：0=Unknown / 1=Error / 2=OK；`results` 按发布时间倒序合并。

### 3.2 Torznab：`GET /api/v2.0/indexers/{indexerId}/results/torznab/`

参数：`t`（必填）、`apikey`、`q`、`cat`、`imdbid`、`tmdbid`、`tvdbid`、`tvmazeid`、`season`、`ep`、
`extended`、`limit`、`offset`，音乐/图书扩展参数（artist/album/label/track/year/genre/author/title/publisher）。

**t=caps** 响应（application/rss+xml）：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<caps><server title="Vestt" /><limits default="100" max="100" /><searching><search available="yes" supportedParams="q" /><tv-search available="yes" supportedParams="q,season,ep,imdbid,tvdbid,tmdbid" /><movie-search available="yes" supportedParams="q,imdbid,tmdbid" /><music-search available="no" supportedParams="q" /><audio-search available="no" supportedParams="q" /><book-search available="no" supportedParams="q" /></searching><categories><category id="2000" name="Movies"><subcat id="2010" name="Movies/Foreign" />...<subcat id="2040" name="Movies/HD" /></category></categories></caps>
```

> 结构逐项对齐上游 `TorznabCapabilities.ToXml`：仅 `server title="Vestt"` 单属性（无
> version/strapline/registration——backend-api.md 早期草案有误，SPEC-03 已按上游修正）；
> limits 属性序 default→max；searching 恒含 6 个模式（含上游的 `audio-search`，
> 与 music-search 同步）；不可用模式仍为 `available="no" supportedParams="q"`；
> 空类别树输出自闭合 `<categories />`。golden 对拍见 `common/torznab/testdata/`。

**t=search|tvsearch|movie|music|book** 响应（application/rss+xml）：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel><atom:link href="http://127.0.0.1:9117/api/v2.0/indexers/example/results/torznab/" rel="self" type="application/rss+xml" /><title>Example Tracker</title><description>This is an example tracker</description><link>https://example.com/</link><language>en-US</language><category>search</category><item><title>Example Movie 2020 1080p BluRay</title><guid>https://example.com/t/123</guid><jackettindexer id="example">Example Tracker</jackettindexer><type>private</type><comments>https://example.com/t/123</comments><pubDate>Tue, 20 Oct 2026 18:30:00 +0000</pubDate><size>14680064000</size><grabs>42</grabs><description>A movie</description><link>http://127.0.0.1:9117/dl/example?path=t%2F123&amp;file=Example.Movie.2020&amp;apikey=...</link><category>2000</category><category>2040</category><enclosure url="..." length="14680064000" type="application/x-bittorrent" /><torznab:attr name="category" value="2000" /><torznab:attr name="category" value="2040" /><torznab:attr name="imdb" value="0123456" /><torznab:attr name="imdbid" value="tt0123456" /><torznab:attr name="author" value="" /><torznab:attr name="booktitle" value="" /><torznab:attr name="publisher" value="" /><torznab:attr name="artist" value="" /><torznab:attr name="album" value="" /><torznab:attr name="label" value="" /><torznab:attr name="track" value="" /><torznab:attr name="seeders" value="88" /><torznab:attr name="peers" value="95" /><torznab:attr name="infohash" value="ABC123" /><torznab:attr name="downloadvolumefactor" value="0.5" /><torznab:attr name="uploadvolumefactor" value="1" /></item></channel></rss>
```

> 逐项对齐上游 `ResultPage.ToXml`（早期草案中的 newznab 命名空间、`rss version="1.0"`、
> category 逗号拼接均与上游不符，SPEC-03 已修正）：命名空间为 atom+torznab；
> channel 首元素 `atom:link rel="self"`；item 含 `jackettindexer`/`type`；
> 类别同时以 `<category>` 元素与逐条 `torznab:attr` 输出；`imdb` 为 7 位零填充数字、
> `imdbid` 为 `tt` 前缀；上游对未设置的 author/booktitle/publisher/artist/album/label/track
> 恒输出空值属性（`value=""`），对拍以 golden 文件为准。

> 属性名/取值细节（imdb 格式、category 拼接、日期 RFC822、magnet 时 link=磁链）以
> `ResultPage.ToXml` + `TorznabQuery` 上游源码为规范，fixture 对拍验收。

**t=indexers**（仅元索引器：`all` 及类型/tag 过滤索引器）：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<indexers>
  <indexer id="exampletracker" configured="true">
    <title>Example Tracker</title>
    <description>...</description>
    <link>https://example.com/</link>
    <language>en-US</language>
    <type>private</type>
    <!-- caps 节点同 t=caps 内容 -->
  </indexer>
</indexers>
```

### 3.3 TorrentPotato：`GET /api/v2.0/indexers/{indexerId}/results/potato/`

参数：`searchtype`、`imdbid`、`search`（上游映射为 `imdbid` + `q` 语义）。响应 200：

```json
{
  "results": [
    {
      "release_name": "Example Movie 2020[Example Tracker]",
      "torrent_id": "https://example.com/t/123",
      "details_url": "https://example.com/t/123",
      "download_url": "http://127.0.0.1:9117/dl/exampletracker?...",
      "imdb_id": "tt1234567",
      "freeleech": true,
      "type": "movie",
      "size": 14000,
      "leechers": 7,
      "seeders": 88,
      "publish_date": "2026-10-20T18:30:00"
    }
  ]
}
```

`size` 单位 MB（上游整除 1024³）；`imdb_id` 为 `tt` + 7 位短格式；`publish_date` ISO8601 秒级，可为 null。

---

## 4. 代理端点

| 端点 | 参数 | 行为 |
|---|---|---|
| `GET /dl/{indexerId}` | `path`、`file`、`apikey` | 携带索引器 Cookie 转发下载；`file` 用于 Content-Disposition 文件名 |
| `GET /img/{indexerId}` | `path`、`apikey` | 图片代理，透传 Content-Type |
| `GET /bh/{indexerId}` | `path`、`file`、`apikey` | 取回 `.torrent`/`.magnet` 内容写入 `blackholedir`，响应 200 文本说明 |

全部校验 `apikey`；`path` 为索引器侧相对 URL（base64/URI 编码原样透传）。

---

## 5. UI 路由与静态资源

| 路径 | 方法 | 行为 |
|---|---|---|
| `/` | GET | 302 → `/UI/Dashboard` |
| `/UI/Login` | GET | Cookie 预检重定向链（`TestCookie`）→ `login.html`（未登录）或 302 Dashboard |
| `/UI/TestCookie` | GET | 校验 Cookie 支持，缺失 → 400 "Cookies required" |
| `/UI/Dashboard` | GET | 直出 `index.html`（Vue SPA）；会话由前端探测（管理 API 401 → `#/login`）；`?logout=true` → 302 Logout |
| `/UI/Dashboard` | POST | form 字段 `password` 校验 → 建立会话（14 天）→ 302 Dashboard |
| `/UI/Logout` | GET | 注销会话 → 302 Login |

SPA 资源（`/js/*`、`/css/*` 等）由静态 handler 服务于 embed 的 `src/backend/server/static/dist`（构建时由 Makefile 从 `src/frontend/dist` 同步）。

分离部署（可选）：上述静态资源亦可由外部 nginx 托管 —— `root` 指向前端构建产物，
`try_files $uri @backend` 将未命中请求反代至后端端口；本节全部 `/UI/*` 路由与后文 API 行为不变，
同域反代无需启用 `allow_cors`（跨域直连才需要）。示例见 SPEC-14 `install/nginx/vestt.conf`。

---

## 6. 健康检查

`GET /health` → 200：

```json
{ "status": "ok", "version": "0.1.0", "go_version": "go1.23.4", "uptime_seconds": 3600 }
```

---

## 7. 分页、排序与缓存语义

- Torznab `limit/offset`：`limit<=0` 时按索引器最大结果；`offset` 下发上游分页（引擎 paths 翻页）。
- 缓存键：`SHA-256(JSON(TorznabQuery))`（仅查询对象；空 `SearchTerm` 归一为 `""`）；TTL 秒级（`cache_ttl`）。
- 缓存命中响应头：`X-Vestt-Cache: hit`（新增提示头，不影响上游兼容）。
- 手动搜索强制 `InteractiveSearch=true`（对等上游），即绕过 `CanHandleQuery` 的 q 必填约束。

## 8. 与上游的显式差异清单

| 项 | 上游 | Vestt |
|---|---|---|
| 进程自重启 | 配置变更后自动重启 webhost | 返回 `restart_required`，提示用户手动重启 |
| DPAPI/数据保护 | Windows DPAPI + Mono crypto | 无保护层；密码 bcrypt 哈希，索引器密钥字段原样入库（SQLite 文件即信任边界） |
| 自更新 | 下载替换二进制 | 仅版本检查（日志+UI 提示） |
| `can_run_netcore` | Mono 迁移提示 | 恒 `true`（DTO 兼容保留） |
