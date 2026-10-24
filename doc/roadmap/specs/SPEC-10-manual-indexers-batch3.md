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

| # | 索引器 | 上游文件 | 机制（初步标注，实现时校正） |
|---|---|---|---|
| 1 | PrivateHD | Definitions/PrivateHD.cs | json-api |
| 2 | Redacted | Definitions/Redacted.cs | json-api |
| 3 | RetroFlix | Definitions/RetroFlix.cs | json-api |
| 4 | RevoPeers | Definitions/RevoPeers.cs | html-login |
| 5 | RuTracker | Definitions/RuTracker.cs | html-login（登录验证文本） |
| 6 | SceneHD | Definitions/SceneHD.cs | json-api |
| 7 | SecretCinema | Definitions/SecretCinema.cs | gazelle api |
| 8 | Shazbat | Definitions/Shazbat.cs | html-login |
| 9 | Simurg | Definitions/Simurg.cs | html |
| 10 | SpeedApp | Definitions/SpeedApp.cs | json-api（token 刷新） |
| 11 | SpeedCD | Definitions/SpeedCD.cs | html-login |
| 12 | SubsPlease | Definitions/SubsPlease.cs | json-api |
| 13 | Toloka | Definitions/Toloka.cs | html-login |
| 14 | TorrentBytes | Definitions/TorrentBytes.cs | html-login |
| 15 | TorrentDay | Definitions/TorrentDay.cs | html-login（分类 URL 模板，同 IPT 系） |
| 16 | TorrentNetwork | Definitions/TorrentNetwork.cs | html-login |
| 17 | TorrentsCSV | Definitions/TorrentsCSV.cs | json-api（自托管） |
| 18 | TorrentSyndikat | Definitions/TorrentSyndikat.cs | html-login |
| 19 | TVStore | Definitions/TVStore.cs | html |
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
