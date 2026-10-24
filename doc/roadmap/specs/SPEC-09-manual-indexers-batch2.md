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

| # | 索引器 | 上游文件 | 机制（初步标注，实现时校正） |
|---|---|---|---|
| 1 | FileList | Definitions/FileList.cs | html-login（JSON 列表页） |
| 2 | GazelleGamesAPI | Definitions/GazelleGamesAPI.cs | gazelle api |
| 3 | GreatPosterWall | Definitions/GreatPosterWall.cs | html-login |
| 4 | HDBitsApi | Definitions/HDBitsApi.cs | json-api |
| 5 | HDRTorrent | Definitions/HDRTorrent.cs | html |
| 6 | ImmortalSeed | Definitions/ImmortalSeed.cs | html-login |
| 7 | IPTorrents | Definitions/IPTorrents.cs | html-login（分类 URL 模板） |
| 8 | Knaben | Definitions/Knaben.cs | json-api |
| 9 | Libble | Definitions/Libble.cs | html-login |
| 10 | LostFilm | Definitions/LostFilm.cs | html-login（系列页解析） |
| 11 | Magnetico | Definitions/Magnetico.cs | json-api（自托管） |
| 12 | MejorTorrent | Definitions/MejorTorrent.cs | html |
| 13 | MTeamTp | Definitions/MTeamTp.cs | json-api |
| 14 | MyAnonamouse | Definitions/MyAnonamouse.cs | json-api（含猫咪搜索参数） |
| 15 | NebulanceAPI | Definitions/NebulanceAPI.cs | json-api |
| 16 | NorBits | Definitions/NorBits.cs | html-login |
| 17 | Orpheus | Definitions/Orpheus.cs | gazelle api |
| 18 | PassThePopcorn | Definitions/PassThePopcorn.cs | json-api（APT 校验头） |
| 19 | PixelHD | Definitions/PixelHD.cs | html-login |
| 20 | PreToMe | Definitions/PreToMe.cs | html-login |

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
