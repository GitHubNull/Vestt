# Changelog

本文件记录 Vestt 的所有重要变更。格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## 版本约定

- 版本号格式 `vMAJOR.MINOR.PATCH`；发布标签形如 `v0.1.0`，发布产物命名
  `vestt-v<ver>-<os>-<arch>[.exe]`（前端静态包 `vestt-web-v<ver>.tar.gz`）。
- Vestt 版本线独立于上游：功能面对应复刻基线 Jackett 0.24.2900，但 `0.1.0` 与上游
  `0.24.2900` 是两套版本号，不存在数值对应关系。
- `0.x` 阶段（`0.y.z`）：`MINOR` 表示功能面推进（SPEC 批次落地），`PATCH` 表示缺陷
  修复；进入 `1.0.0` 前不承诺 API 兼容性。

## [0.2.0] - 2026-10-25

搜索结果默认改为 DataView 风格卡片视图（用户决策，有意偏离上游单表格形态）。

### Added

- **卡片视图**：搜索结果默认以 list 卡片展示——大缩略图（无海报时为站点首字母 +
  站点名散列配色的占位块，右下角磁链水印）、分类/站点小字 + 标题 + IMDB/TMDB 等彩色
  label、大小与 S/L pill 徽标、下载/磁链/Blackhole 操作，行内展开详情与表格一致
- **视图切换**：结果工具栏 list/table 切换按钮，选择持久化 localStorage
  `vestt.results.view`（默认 list）；缓存 releases 视图保持纯表格，不出现切换
- **页内懒渲染**：分页器（10/25/50 每页）之外，卡片列表用 IntersectionObserver 哨兵
  按批追加渲染，大结果集不会撑爆 DOM
- **本地二次过滤**：工具栏标题过滤框（带 × 清除），对已加载结果大小写不敏感子串
  过滤，不发后端请求，列表/表格视图同时生效

### Changed

- `frontend.md` 升至 v1.3：组件树、状态表、工程结构回写；`releaseUtils.ts` 抽取
  表格与卡片共享的 label/链接/rowKey/blackhole/过滤纯函数

## [0.1.0] - 2026-10-25

首个可运行版本：Jackett 0.24.2900 的功能对等复刻（Go 后端 + Vue 3 前端 + SQLite 存储），
API 路径与报文与上游保持兼容。

### Added

- **协议核心**：Torznab（`t=caps|search|tvsearch|movie|music|book`，含 imdb/tmdb/tvdb/tvmaze
  全部 ID 参数）与 TorrentPotato（CouchPotato 兼容）
- **索引器**：645+ 内置索引器 —— 582 个 Cardigann YAML 定义（复用上游 `Definitions/*.yml`
  与 `schema.json`，go:embed 内嵌）+ 62 个手写实现（各自独立单测）+ 聚合元索引器（all/aggregate）
- **搜索与缓存**：按 tracker/类别过滤的并发聚合搜索（单站失败不影响整体）；
  结果缓存 TTL + 每索引器容量可配置（SQLite 落盘）
- **代理路由**：带站点会话 Cookie 的 `/dl`（代理下载）、`/img`（图片代理）、`/bh`（Blackhole）
- **FlareSolverr 集成**：Cloudflare 盾站点自动求解
- **管理控制台**（Vue 3 + PrimeVue 4，前端产物内嵌单二进制）：索引器筛选/配置（动态表单、
  验证码）/测试/标签、手动搜索、缓存视图、服务器设置、日志查看
- **安全**：API Key 鉴权（`crypto/rand`）、可选管理密码（bcrypt + 服务端会话 Cookie）、
  CORS 与 BasePath 支持、日志敏感字段脱敏、默认仅绑定 `127.0.0.1`
- **可观测**：匿名 `GET /health`（返回 version/uptime）；slog 结构化日志
- **发布工程**：
  - `Dockerfile` 多阶段镜像（node:24 + corepack/pnpm 构建前端 → golang:1.26 构建后端
    embed → alpine 运行时，非 root、HEALTHCHECK `/health`、镜像 < 60MB）
  - `docker-compose.yml` 示例（端口/卷/healthcheck/FlareSolverr 旁路注释示例）
  - `install/systemd/vestt.service` + `install/install_service_systemd.sh`
    （参数化 `--user/--group/--install-path/--data-dir/--service-name/--port`，
    自动创建专用系统用户，行为对齐上游 Jackett 安装脚本）
  - `install/nginx/vestt.conf`（architecture.md §7.1 形态 B：nginx 托管前端 dist +
    `try_files $uri @backend` 反代 + `X-Forwarded-*` 头）
- **文档基线**：产品需求文档 `doc/product/prd.md`，技术文档 `doc/tech/architecture.md`、
  `backend-api.md`、`data-model.md`、`frontend.md`，路线图 `doc/roadmap/roadmap.md` 与
  `SPEC-01` ~ `SPEC-14` 实现规格，`doc/README.md` 导航

### 基线说明

- 上游参考代码 `tmp/ref/Jackett-0.24.2900/`（只读）；上游已知 bug 默认复刻。
- 运行旗标：`--port`、`--data-dir`、`--basepathoverride`、`--loglevel`
  （flag 为当次运行覆盖，不写回数据库）。

[0.2.0]: https://github.com/GitHubNull/Vestt/releases/tag/v0.2.0
[0.1.0]: https://github.com/GitHubNull/Vestt/releases/tag/v0.1.0
