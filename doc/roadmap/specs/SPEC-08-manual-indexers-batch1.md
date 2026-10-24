# SPEC-08 手写索引器移植 批次 1（AlphaRatio → ExoticaZ）

| 项 | 内容 |
|---|---|
| 规模 | L |
| 前置依赖 | SPEC-04（可与 SPEC-05~07 并行） |
| 产出 | 批次 1 共 21 个手写索引器的 Go 实现 + 注册 + 单测 |

## 1. 目标与范围

将上游 63 个 C# 手写索引器中的批次 1（21 个）移植为 Go，实现 SPEC-04 `indexer.Indexer` 接口，
行为与对应 `.cs` 文件对等。本 SPEC 同时定义**全部三个批次共用的移植协议**（§3）。

对等承诺：以参考代码为准（请求构造、解析逻辑、能力位、配置字段、错误文案）；
真实站点当前可用性为 SHOULD（站点可能已改版），不作为验收阻断。

## 2. 上游参考

| 参考文件 | 用途 |
|---|---|
| `src/Jackett.Common/Indexers/Definitions/<Name>.cs` | 每个索引器的唯一权威源 |
| `src/Jackett.Common/Indexers/Definitions/Abstract/` | 部分索引器继承的抽象基类 |
| `src/Jackett.Common/Models/IndexerConfig/ConfigurationData*.cs` | 各配置形态（BasicLogin/Cookie/Passkey/APIKey/PinNumber…） |
| `src/Jackett.Common/Indexers/Feeds/BaseNewznabIndexer.cs` | newznab 聚合源类索引器参考 |

## 3. 移植协议（批次 1~3 共用）

每个索引器一个 Go 文件 `src/backend/common/indexer/manual/<name>.go`，强制流程：

1. **读源定契约**：通读对应 `.cs`（含基类链），提取：
   - 配置数据类（→ SPEC-04 configdata 组合）
   - 能力位构造（分类、支持位、ID 参数）
   - 搜索请求构造（URL/方法/头/体、分页、类别参数）
   - 响应解析（HTML 选择器 / JSON 路径 / 正则，字段 → ReleaseInfo）
   - 特殊机制：登录表单、验证码、Cloudflare、API、RSS/BT 客户端、Gazelle API、
     双因子、 Cookie 校验、磁链构造、ratio 规则
2. **分类标注**：在 Go 文件头注释记录 `mechanism:`（api-key / html-login / cookie / gazelle /
   newznab-feed / json-api）供批次统计与联调排期。
3. **实现**：仅依赖 `indexer`（接口/基类）、`indexer/configdata`、`httpclient`、`models`、`torznab`、
   `indexer/shared`（新增共享辅助需在本 SPEC 澄清记录中登记）。禁止索引器间互相 import。
4. **注册**：`indexer/manual/registry_batch1.go`（每批次一个注册文件）向 registry 登记工厂。
5. **单测**（每个索引器必备，httptest 假站点）：
   - caps 对拍：断言分类表与支持位 = 上游 `.cs` 声明。
   - 配置表单对拍：ConfigData → JSON 与上游字段序列一致。
   - 请求生成：1~3 个代表性查询 → 断言 URL/头/体。
   - 解析：手写 fixture HTML/JSON（**从上游解析逻辑反推**，字段覆盖 title/size/seeders/date/
     category/download/magnet 至少 6 项）→ 断言 ReleaseInfo。
   - 错误路径：登录失败/能力外查询 至少各 1 用例（可测者）。
6. **澄清记录**：源码歧义（如上游 bug 是否复刻）→ 默认**复刻上游行为**并记录；
   若决定修正，必须注明上游 issue/行为依据。

### 3.1 批次 1 清单（21 个，按字母序）

| # | 索引器 | 上游文件 | 机制（初步标注，实现时校正） |
|---|---|---|---|
| 1 | AlphaRatio | Definitions/AlphaRatio.cs | cookie html |
| 2 | AniDUB | Definitions/AniDUB.cs | html-login |
| 3 | Anilibria | Definitions/Anilibria.cs | json-api |
| 4 | AnimeBytes | Definitions/AnimeBytes.cs | html-login（含 2FA） |
| 5 | AnimeZ | Definitions/AnimeZ.cs | html |
| 6 | AudioBookBay | Definitions/AudioBookBay.cs | cookie html |
| 7 | AvistaZ | Definitions/AvistaZ.cs | html-login |
| 8 | BakaBT | Definitions/BakaBT.cs | html-login（pin/captcha 类） |
| 9 | BeyondHDAPI | Definitions/BeyondHDAPI.cs | json-api |
| 10 | BitHDTV | Definitions/BitHDTV.cs | passkey html |
| 11 | BrasilTracker | Definitions/BrasilTracker.cs | html-login |
| 12 | BroadcasTheNet | Definitions/BroadcasTheNet.cs | json-api |
| 13 | BrokenStones | Definitions/BrokenStones.cs | html |
| 14 | CinemaZ | Definitions/CinemaZ.cs | html-login |
| 15 | DesiGaane | Definitions/DesiGaane.cs | html-login |
| 16 | DICMusic | Definitions/DICMusic.cs | html-login |
| 17 | DivxTotal | Definitions/DivxTotal.cs | html |
| 18 | DonTorrent | Definitions/DonTorrent.cs | html |
| 19 | EpubLibre | Definitions/EpubLibre.cs | html-login |
| 20 | EraiRaws | Definitions/EraiRaws.cs | json-api |
| 21 | ExoticaZ | Definitions/ExoticaZ.cs | html-login |

（机制列为移植起点提示；以各自 `.cs` 实际为准修正并回填本表。）

## 4. 接口定义

- 每个索引器实现 SPEC-04 `indexer.Indexer`；注册后经 SPEC-07 API 全部可见。
- 批次交付物：21 个实现文件 + ≥21 个测试文件 + `registry_batch1.go` + 本表机制列校正。

## 5. 验收标准与验证方法

1. `go test -C src/backend ./common/indexer/manual/...` 全绿；21 个索引器各自的 caps/配置/请求/解析单测齐备。
2. `GET /api/v2.0/indexers` 含全部 21 个（id/name/type/language 与上游一致）；
   未配置态下 `t=caps`（经 fixture 假站点或直接 caps 构造）输出正确。
3. 注册表无重名/漏注册：`TestRegistryCount` 断言（总数 = 已完成批次累计 + 引擎 582 + 元索引器 2）。
4. 随机抽 3 个跑完整链路（httptest 假站点）：Configure → Test → search → 拿到 fixture 期望条目。
5. `make test` 全绿；机制列校正回填本表；更新 roadmap 进度表。
