# Vestt 文档中心

本目录是 Vestt 项目的全部工程文档。Vestt 是 Jackett 0.24.2900 的功能对等复刻，
技术栈为 Golang + Vue 3 + Vite + PrimeVue 4.x + pnpm，存储层使用 SQLite 单文件库。

## 目录结构

```
doc/
├── README.md            # 本文件：导航索引与阅读顺序
├── product/
│   └── prd.md           # 产品需求文档（PRD）：功能范围、用户流程、验收口径
├── tech/
│   ├── architecture.md  # 技术架构：总体分层、仓库布局、技术选型、并发模型
│   ├── backend-api.md   # 后端 API 契约：全部端点、鉴权、错误码、报文示例
│   ├── frontend.md      # 前端设计：组件架构、状态管理、动态表单、主题
│   └── data-model.md    # 数据模型：SQLite 库表设计、缓存与迁移策略
└── roadmap/
    ├── roadmap.md       # 复刻路线图：阶段划分、依赖关系、进度追踪表
    └── specs/           # SPEC-01 ~ SPEC-14：可独立执行的分步实现规格
```

## 推荐阅读顺序

人类读者（了解项目全貌）：

1. [product/prd.md](product/prd.md) — Vestt 做什么、边界在哪
2. [tech/architecture.md](tech/architecture.md) — 怎么实现
3. [roadmap/roadmap.md](roadmap/roadmap.md) — 按什么顺序落地

AI 编程代理（执行实现）：

1. 仓库根目录 `AGENTS.md` — 工作纪律与命令
2. 本文件 — 文档地图
3. `roadmap/roadmap.md` — 找到当前应执行的 SPEC 编号
4. 对应 `roadmap/specs/SPEC-XX-*.md` — 严格按 SPEC 执行
5. 涉及接口细节时查阅 `tech/backend-api.md`；涉及表结构时查阅 `tech/data-model.md`；
   涉及前端时查阅 `tech/frontend.md`

## 硬性约定

- 上游参考代码位于 `tmp/ref/Jackett-0.24.2900/`（已被 `.gitignore` 排除），只读，禁止修改、禁止提交。
- SPEC 文件编号即执行顺序；不得跳过标注了前置依赖的 SPEC。
- 每个 SPEC 的“验收标准与验证方法”是完成定义（Definition of Done），必须全部通过。
- 文档中引用的上游文件路径均相对 `tmp/ref/Jackett-0.24.2900/`。
- 本项目遵循 GPL-2.0（继承上游许可），见根目录 `LICENSE`；法律边界见 `DISCLAIMER.md`。

## 文档维护规则

- 文档变更必须与代码同步：改接口必改 `backend-api.md`，改表结构必改 `data-model.md`，改范围必改 PRD 与 roadmap。
- roadmap.md 的进度追踪表是唯一进度事实来源，完成 SPEC 后必须更新对应条目。
