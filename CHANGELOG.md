# Changelog

本文件记录 Vestt 的所有重要变更。格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [0.1.0] - 2026-10-24

首个版本：项目文档基线与复刻路线图（尚未包含运行时代码）。

### Added

- 产品需求文档 `doc/product/prd.md`：功能范围、用户流程与验收口径
- 技术文档：`doc/tech/architecture.md`（分层与仓库布局）、`backend-api.md`（API 契约）、
  `data-model.md`（SQLite 表设计与迁移策略）、`frontend.md`（组件与状态管理）
- 复刻路线图 `doc/roadmap/roadmap.md` 与 `SPEC-01` ~ `SPEC-14` 共 14 份可独立执行的实现规格
- 文档导航 `doc/README.md`、项目说明 `README.md`、AI 编程代理工作指南 `AGENTS.md`
- `.gitignore`：排除构建产物、运行数据与上游只读参考代码 `tmp/`
- GPL-2.0 许可证 `LICENSE` 与使用边界声明 `DISCLAIMER.md`

[0.1.0]: https://github.com/GitHubNull/Vestt/releases/tag/v0.1.0
