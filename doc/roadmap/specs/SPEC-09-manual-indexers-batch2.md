# SPEC-09 手写索引器移植 批次 2（FileList → PreToMe）

| 项 | 内容 |
|---|---|
| 规模 | L |
| 前置依赖 | SPEC-04；**移植协议沿用 [SPEC-08 §3](SPEC-08-manual-indexers-batch1.md)**（不再重复） |
| 产出 | 批次 2 共 20 个手写索引器的 Go 实现 + 注册 + 单测 |

## 1. 目标与范围

按 SPEC-08 定义的移植协议完成批次 2。对等承诺与批次 1 相同：以参考代码为准。

## 2. 上游参考

同 SPEC-08 §2（每索引器权威源为同名 `.cs`）。

## 3. 批次清单（20 个，按字母序）

| # | 索引器 | 上游文件 | 机制（实现时校正后回填） |
|---|---|---|---|
| 1 | FileList | Definitions/FileList.cs | json-api（HTTP Basic + api.php） |
| 2 | GazelleGamesAPI | Definitions/GazelleGamesAPI.cs | gazelle-api（GGN api.php 全量覆写） |
| 3 | GreatPosterWall | Definitions/GreatPosterWall.cs | gazelle |
| 4 | HDBitsApi | Definitions/HDBitsApi.cs | json-api |
| 5 | HDRTorrent | Definitions/HDRTorrent.cs | html（public Brazilian 基类） |
| 6 | ImmortalSeed | Definitions/ImmortalSeed.cs | html-login |
| 7 | IPTorrents | Definitions/IPTorrents.cs | cookie html（原标 html-login 有误） |
| 8 | Knaben | Definitions/Knaben.cs | json-api |
| 9 | Libble | Definitions/Libble.cs | html-login（含 2FA 字段） |
| 10 | LostFilm | Definitions/LostFilm.cs | html-login（captcha + 两次请求链） |
| 11 | Magnetico | Definitions/Magnetico.cs | json-api（自托管） |
| 12 | MejorTorrent | Definitions/MejorTorrent.cs | html |
| 13 | MTeamTp | Definitions/MTeamTp.cs | json-api（x-api-key） |
| 14 | MyAnonamouse | Definitions/MyAnonamouse.cs | json-api（mam_id cookie 会话） |
| 15 | NebulanceAPI | Definitions/NebulanceAPI.cs | json-api |
| 16 | NorBits | Definitions/NorBits.cs | html-login |
| 17 | Orpheus | Definitions/Orpheus.cs | gazelle-api（token 鉴权 ajax.php） |
| 18 | PassThePopcorn | Definitions/PassThePopcorn.cs | json-api（ApiUser/ApiKey 头） |
| 19 | PixelHD | Definitions/PixelHD.cs | html-login（captcha 登录） |
| 20 | PreToMe | Definitions/PreToMe.cs | html-login（PIN） |

## 4. 批次专项注意

- **Gazelle 系**（GazelleGamesAPI、Orpheus）：抽象公共 Gazelle 客户端进 `indexer/shared/gazelle.go`
  （action=signup 校验、`groupresult` 解析、磁链/imdb 关联、token/rate limit）。
- **json-api 系**（HDBitsApi、MTeamTp、MyAnonamouse、NebulanceAPI、PassThePopcorn）：
  统一使用 `httpclient` JSON 帮助方法；API Key/Uid 类配置（configdata 复用）；
  响应错误码 → IndexerError{Kind: Auth/Config}。
- **PassThePopcorn**：`ApiUser/ApiKey` 头 + IMDb 查询分支；磁链/infohash 混合返回。
- **IPTorrents**：分类 → URL 路径映射表必须与上游 `MapTorznabCatsToTrackers` 一致（单测全表断言）。
- **LostFilm**：`q` 为 S/E 模式时的剧名查找链（两次请求）必须复刻。

## 5. 验收标准与验证方法

1. 与 SPEC-08 §5 相同的通用验收（20 个索引器全绿、注册计数、3 个抽样全链路）。
2. 批次专项：Gazelle 共享客户端单测 ≥ 6 用例；IPTorrents 分类映射全表断言；
   PTP API 头断言；MTeamTp 分页参数断言；MyAnonamouse 搜索参数（搜索类型/语言）断言。
3. `make test` 全绿；机制列校正回填本表；更新 roadmap 进度表。

## 澄清记录

### 机制标注校正（§3 表已回填）
- FileList：原标 html-login——实为 HTTP Basic 认证打 api.php 的 json-api。
- IPTorrents：原标 html-login——实为粘贴 cookie + 可选 UA 覆写的 cookie html。
- GreatPosterWall：原标 html-login——实为 GazelleTracker 系（gazelle，2FA cookie）。
- GazelleGamesAPI / Orpheus：gazelle-api（前者 GGn api.php 全量覆写 group 键控响应；后者 OPSnet ajax.php token 鉴权）。
- PassThePopcorn：原标 "APT 校验头"——实为 ApiUser/ApiKey 自定义头。

### 上游行为忠实复刻（各文件头注释注明依据）
- Orpheus usetoken 恒在：上游在赋值 DVF 之前算 canUseToken，所有下载链接都带 `&usetoken=1`
  （freeleech 亦然，与 SPEC-08 GazelleTracker.cs:513 同一上游 bug）。
- NorBits：PublishDate 格式 `yyyy-MM-ddHH:mm:ss`（日期与时间间无空格，NorBits.cs:285）；
  ConfigureIfOK 先存响应 cookie 再被组合值覆盖（NorBits.cs:196）。
- MejorTorrent：ParseTags 按值传参实为 no-op（MejorTorrent.cs:609-630）；分类比较大小写敏感
  （"series" 命中、"Series"→Otro）。
- MTeamTp：请求体按上游 STJson 设置（WriteIndented 两空格/camelCase/null 省略/枚举小写）逐字节复刻。
- Knaben：wire 字段名混用 PascalCase + `[JsonProperty]` camelCase，DTO 照此对齐。
- HDBitsApi：请求体按键插入序序列化（有序 JSON builder 保 JObject 字节序）。
- LostFilm：meta refresh 的 `Substring(IndexOf("url=")+5)` 逐字复刻（含尾引号行为）；
  俄语月份显式表移植，失败回退 time.Now() 与上游一致。
- GGn：点号前置替换、artistcheck 数字过滤；Remaster/Trumpable 标签、freeleech 系数。
- IPTorrents：84 行分类映射全表双向对拍（tracker↔torznab↔URL 路径段）。

### Go 平台适配（非行为偏差）
- 重定向不可观测：Go client 自动跟随重定向，上游 IsRedirect/RedirectingTo 分支合并为
  等价检测（Libble/PreToMe/PixelHD，与 SPEC-08 shared/gazelle.go 同一近似）。
- goquery 不支持 `:has()`/`:contains()` 伪选择器——手动 descendant/文本过滤等价实现
  （PixelHD/ImmortalSeed）。
- .NET Unicode 类正则：`\w`→`[^\p{L}\p{N}_]+`、`\W`→`[^\p{L}\p{N}_]+`（MAM/NorBits/MejorTorrent）；
  `\uXXXX` 字符类→`\x{0100}-\x{FFFF}`（语义相同）。
- API URL 硬编码改为未导出可覆盖字段（默认与上游一致）作 httptest 接缝：Knaben api.knaben.org、
  MTeamTp api.<domain>、PTP searchBase（BeyondHDAPI 先例）。
- Replaces 元数据（GPW seals / MTeamTp mteamtp2fa）：Go Base 无该字段，丢弃（SPEC-08 AnimeZ 先例）。
- GGn JSON 遍历序：Go map 无序，group/torrent 按键数字升序遍历（最终按 PublishDate 倒序，无观察差异）。
- 登录 cookie 捕获：Go client 只把最终响应写入 jar，登录成功时合并响应 Set-Cookie 与 jar
  （沿用 shared/gazelle.go 方法）。
- MejorTorrent 复用同包 DivxTotal 的 `divxCheckTitleMatchWords`（上游两文件该函数逐字节相同，
  非跨索引器 import，属包内共享）。

### 验证期修复的实现 bug（代理自修，记录备查）
- MejorTorrent：goquery `Slice(1,-1)` Python 语义丢末行→`Slice(1,len)`；
  Pages 校验改 `strconv.Atoi` 复刻 Convert.ToInt32 的 FormatException。
- IPTorrents：query string 键未编码→按上游 UrlEncode 语义修正（`filter_cat[1]`→`filter_cat%5B1%5D`）。

### 注册与验收（中央装配）
- `registry_batch2.go` init() 登记 20 工厂；TestRegistryCount 断言静态工厂 41（21+20）。
- identity 对拍 20 组 id/name/type/language；spotcheck 全链路抽检 3 个（FileList=json-api、
  Orpheus=gazelle-api、IPTorrents=cookie html）。

### Magnetico 补漏交付（编排核对时漏派，单独补移植）
- Basic auth 编码：上游 ISO-8859-1 取字节（不可映射字符变 `?`），Go 取 UTF-8 原始字节——
  ASCII 凭证完全一致，注释注明。
- 上游默认 `sort="discovered"` 用**标签值**而非 option key（上游怪癖，注释注明）。
- ApplyConfiguration 上游不要求结果非空即置 configured（与 FileList/Anilibria 不同），忠实复刻。
