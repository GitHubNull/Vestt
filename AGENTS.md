# AGENTS.md — AI 编程代理工作指南

本文件是 AI 编程代理（Claude Code、Copilot Agent 等）在本仓库工作时必须遵守的
上下文规范、工作流与纪律。人类开发者亦可参考。

## 1. 项目上下文速览

- **Vestt** = Jackett 0.24.2900 的 1:1 功能复刻（Go + Vue 3 + PrimeVue 4 + SQLite）。
- 上游参考代码：`tmp/ref/Jackett-0.24.2900/`（**只读**，禁止修改/提交）。
- 文档入口：[doc/README.md](doc/README.md)。
- 技术要点：chi v5 · goquery · yaml.v3 · modernc.org/sqlite · slog · go:embed；
  Vue 3.5 + Vite + Pinia + pnpm。
- 仓库布局：`src/backend/`（Go module，含集成测试）、`src/frontend/`（前端 pnpm 工程）、`doc/`（文档）；全部源码收于 `src/` 下，前后端分离开发。

## 2. 任务工作流（强制顺序）

1. **定位任务**：读 `doc/roadmap/roadmap.md` 进度表 → 找到第一个非完成 SPEC
   （或用户指定 SPEC）。
2. **读 SPEC**：完整阅读 `doc/roadmap/specs/SPEC-XX-*.md`，注意"前置依赖"未满足则停止。
3. **补上下文**：按需读 `doc/tech/backend-api.md`（接口）、`doc/tech/data-model.md`（表结构）、
   `doc/tech/frontend.md`（前端）、上游对应源码（行为规范）。
4. **实现**：按 SPEC"实现步骤"落码；上游行为存疑时**以 tmp/ref 源码为准**，
   并在 SPEC 文件末尾追加"## 澄清记录"。
5. **验收**：逐条执行 SPEC"验收标准与验证方法"；`make test` 全绿。
6. **收口**：更新 roadmap.md 进度表（状态/日期/备注）；文档与实现不一致处回写 tech 文档。

禁止：跳过 SPEC 顺序、跳过验收、在无 SPEC 的情况下开发新功能
（新功能 → 先补 PRD/tech/SPEC 文档，再开发）。

## 3. 常用命令

```bash
make build            # 前端构建 + go build → dist/vestt
make run              # 构建并启动（127.0.0.1:9117）
make test            # go test -C src/backend ./... + go vet
pnpm --dir src/frontend dev   # 前端开发（代理 API 到 9117）
pnpm --dir src/frontend test  # 前端 vitest
pnpm --dir src/frontend build # 前端构建（产物会 embed 进 Go）
```

- Go 代码一律 `gofmt`；提交前 `make test` 必须通过。
- 测试策略：引擎/转换器/模型用表驱动单测；站点解析用 httptest 假站点 + fixture；
  集成测试放 `src/backend/test/integration`；**禁止**在单测中访问真实站点。

## 4. 代码规范

### Go（`src/backend/`）

- 包布局以 `doc/tech/architecture.md §2` 为准；禁止 `common` 反向依赖 `server`。
- 错误：领域内使用 `indexer.IndexerError`（Kind 分类）；对外 API 错误映射见 backend-api.md §0。
- 上下文：所有 IO 函数首参 `ctx context.Context`。
- 依赖注入：main.go 手写构造（无 DI 框架）；新服务在 architecture.md §4.1 的装配链中登记。
- 上游移植代码文件头注释保留对应 C# 文件路径（如 `// Ported from: Definitions/IPTorrents.cs`）。

### Vue/TS（`src/frontend/`）

- 组件树与 store 划分以 `doc/tech/frontend.md` 为准；API 调用只存在于 `services/` 与 stores。
- DTO 类型一律来自 `services/types.ts`（字段名与后端 JSON 原样一致，不得改名）。
- PrimeVue 4 组件 + Aura 主题；文案与上游 UI 一致（英文基线）。

### 文档

- 接口变更 → 同步 `doc/tech/backend-api.md`；表结构变更 → `data-model.md`；
  组件变更 → `frontend.md`；范围变更 → PRD + roadmap。
- 文档内路径引用（相对链接）必须可达。

## 5. 上游参考使用边界

- `tmp/ref/Jackett-0.24.2900/` 只读；以其中源码为行为规范时必须**引用文件路径**写入澄清记录。
- 上游 YAML 定义（`Definitions/*.yml`）与 `schema.json` 直接复制进
  `src/backend/common/engine/definitions/`（保持 GPL-2.0 归属声明）。
- 上游已知 bug 默认复刻；修正需在澄清记录注明依据。

## 6. 提交纪律

- 提交信息：`SPEC-XX: <摘要>`（例：`SPEC-06: add timeparse/reltime converters`）。
- 一个 SPEC 一次（或多次有逻辑分界的）提交；不与无关改动混提交。
- 禁止提交：`dist/`、`src/frontend/node_modules/`、`src/frontend/dist/`、`src/backend/server/static/dist/`、`*.db`、`tmp/`（.gitignore 已覆盖，
  不要用 `-f` 绕过）。
- 未被明确要求时不要执行 `git commit`/`git push`。

## 7. 常见陷阱

- Torznab XML 的字段值格式（imdb 的 `tt` 前缀、category 逗号拼接、pubDate RFC1123）——
  以 SPEC-03 golden 文件为准，勿自行发挥。
- `modernc.org/sqlite` 不支持部分并发写：保持 `SetMaxOpenConns(1)`，写操作走事务。
- 手写索引器间禁止互相 import；共享逻辑进 `indexer/shared`。
- 前端密码字段提交前检查掩码占位 `********`（避免把掩码当真值提交）。
- basePath：所有后端路由在 `basepathoverride` 下需前缀化（middleware 剥离后内部路由不变）。
