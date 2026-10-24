# Vestt 前端设计（Vue 3 + PrimeVue 4）

| 项 | 内容 |
|---|---|
| 文档版本 | 1.0 |
| 上游基线 | `src/Jackett.Common/Content/{index.html, custom.js, login.html}`（jQuery + Handlebars） |
| 技术栈 | Vue 3.5 + TypeScript + Vite + PrimeVue 4.x + Pinia + Vue Router + pnpm |
| 接口契约 | [backend-api.md](backend-api.md)（本文不重复字段定义） |

---

## 1. 工程结构

```
src/frontend/
├── package.json            # scripts: dev / build / test / lint
├── vite.config.ts          # dev proxy → http://127.0.0.1:9117；base 从后端注入
├── tsconfig.json
├── index.html
└── src/
    ├── main.ts             # 装配 PrimeVue(Aura) / Pinia / Router / Toast / DialogService
    ├── App.vue
    ├── router/
    │   └── index.ts        # /login, /dashboard（hash 参数承载深链）
    ├── stores/             # Pinia
    │   ├── auth.ts  config.ts  indexers.ts  search.ts  logs.ts
    ├── services/
    │   ├── api.ts          # fetch 封装（basePath、错误归一化）
    │   └── types.ts        # 后端 DTO 的 TS 类型（与 backend-api.md 一一对应）
    ├── components/
    │   ├── indexer/        # IndexerTable / IndexerRow / IndexerFilters / TestStateBadge
    │   ├── config/         # IndexerConfigDialog / DynamicForm / DynamicField
    │   ├── search/         # SearchDialog / SearchResultTable / PresetBar
    │   ├── settings/       # SettingsDialog（ServerConfig 表单 / LogViewer / ApiKeyPanel）
    │   └── common/         # CopyButton / Notify 适配
    └── views/
        ├── LoginView.vue
        └── DashboardView.vue
```

## 2. 路由与 basePath

- 上游 SPA 挂载于 `/UI/Dashboard`（服务端直出 index.html），深链通过 **location.hash** 传递：
  `#indexers=1&filter=private`、`#search=test&tracker=all&category=2000`。
  Vestt 保持同一 hash 语义（`SearchDialog` 打开时同步 hash，刷新可恢复）。
- `basePathOverride` 处理：`api.ts` 启动时调用 `GET server/config` 取 `basepathoverride`，
  所有请求前缀化；Vite dev 经 proxy 直连后端无需处理。
- 登录页：管理密码已设置且无有效会话时，后端直出 `login.html` 等价行为由 `LoginView.vue` 承担
  （POST form 到 `/UI/Dashboard`，成功后 302 回 Dashboard）。

## 3. 状态管理（Pinia）

| Store | State | 关键 Actions | 对应后端 |
|---|---|---|---|
| `auth` | passwordSet, authenticated | `login()`, `logout()` | `UI/Dashboard` POST/GET Logout |
| `config` | serverConfig, loading | `load()`, `save(patch)` | `GET/POST server/config` |
| `indexers` | all[], filters{type,tag,state}, testStates{} | `load()`, `configure(id, items)`, `test(id)`, `del(id)`, `addBatch(ids)` | `indexers*` 全套 |
| `search` | visible, query, category[], trackers[], results[], indexerStates[] | `open(preset)`, `run()`, `savePreset()` | `indexers/{id}/results` |
| `logs` | entries[] | `load()`, `poll()` | `GET server/logs` |

原则：API 调用只出现在 stores/services；组件不直接 fetch。

## 4. 组件架构树

```
App.vue
└── RouterView
    ├── LoginView
    └── DashboardView
        ├── AppHeader                # logo、版本、API Key（复制）、设置、注销
        ├── IndexerFilters           # 类型/标签/状态 三维筛选（对等上游 dropdowns）
        ├── Tabs
        │   ├── Tab「已配置」→ IndexerTable(mode=configured)
        │   │     每行：状态徽标(健康/失败) · 名称+图标 · 类型 · 标签 · Caps 摘要
        │   │     操作：搜索 / 测试 / 配置 / 复制 Torznab URL / 删除
        │   └── Tab「未配置」→ IndexerTable(mode=unconfigured)
        │         每行：名称 · 描述 · 类型 · 语言 · 标签 · 操作：配置
        │         支持多选 + 批量添加（addIndexers 循环 POST Config）
        ├── SearchDialog             # 手动搜索
        │     SearchBar（query / tracker 多选 / category 级联）
        │     SearchResultTable（懒加载 DataTable，虚拟滚动 500+ 行）
        │       列：日期 · 标题(链到 Details) · 大小 · 种子/下载数 · 类别 · 站点 · 操作(下载/Blackhole/磁链)
        │       行内展开：海报、infohash、上下传比、最小比率/时间（updateReleasesRow 行为）
        │     PresetBar（保存/加载预设 → localStorage）
        ├── IndexerConfigDialog      # DynamicForm + 测试结果区
        ├── SettingsDialog           # Tab: Server Config / Cached Releases / Log
        └── Toast                    # 全局通知（对等上游 doNotify，类型 info/success/danger）
```

### 4.1 上游行为对等要点（UI 验收时逐项核对）

- 未配置索引器批量添加成功/失败的通知文案与重试逻辑（`addSelectedIndexers*`）。
- 测试状态动画：未测试/进行中/成功/失败四态（`updateTestState`）。
- API Key 一键复制（Clipboard API + fallback execCommand）。
- `external=true 且无密码` 首次访问弹出安全警告（可隐藏，localStorage 记忆）。
- 代理类型选择后的 `proxyWarning` 提示。
- 缓存 releases 视图仅在 `cache_enabled` 时可进入；表格列与上游 `updateSearchResultTable` 对齐。
- 预设（presets）：`vestt.presets` localStorage 键，结构与上游 `savedPresets` 等价。

## 5. 动态配置表单（DynamicForm）

渲染器核心：接收 `ConfigItem[]`（backend-api.md §2.2 的 8 种 `type`），输出 PrimeVue 表单：

| type | PrimeVue 组件 | 备注 |
|---|---|---|
| `inputstring` | InputText | `value` 绑定 |
| `password` | Password(toggleMask) | 提交时若为掩码占位 `********` 则跳过该字段 |
| `inputbool` | Checkbox | value 布尔字符串处理 |
| `inputselect` | Select | `options` 对象 → {label, value} |
| `inputcheckbox` | MultiSelect（或 Checkbox 组） | `values` 数组 ↔ `options` |
| `displayinfo` | Message(closable=false) | 纯说明 |
| `displayimage` | Image | `value` 为 dataURL（验证码），附可选输入框（`cardigannCaptcha`） |
| `hiddendata` | 不渲染 | 提交时原样回传 |

提交：收集为 `ConfigItem[]`（`{id, value}` / `{id, values}` / `{id, cookie}`），POST §2.3。
错误处理：非 2xx → 表单顶部 Alert 展示服务端消息；引擎 captcha 失败时保留表单并刷新验证码。

## 6. PrimeVue 4 接入要点

- 主题：`@primeuix/themes` Aura 预设（`definePreset` 覆盖主色），`app.use(PrimeVue, { theme })`。
- 必装服务：ToastService、DialogService；样式引入 `primeicons`。
- DataTable 懒加载：手动搜索结果一次取回但分页渲染（与上游一次性渲染对齐，10k 行内不做服务端分页）。
- 表单校验以服务端为准（上游无前端校验），仅做必填提示。

## 7. 与上游 JS 的映射表（供对等性核对）

| 上游 custom.js 函数 | Vestt 归属 |
|---|---|
| loadJackettSettings | `configStore.load` |
| reloadIndexers / configureFilters / display*IndexersList | `indexersStore.load` + IndexerTable computed |
| addIndexer / addIndexers | `indexersStore.add / addBatch` |
| testIndexer / updateTestState | `indexersStore.test` + TestStateBadge |
| displayIndexerSetup / newConfigModal / getConfigModalJson | IndexerConfigDialog + DynamicForm |
| showSearch / updateSearchResultTable / updateReleasesRow | `searchStore.run` + SearchDialog |
| getSavedPresets / setSavedPresets | PresetBar + localStorage service |
| bindUIButtons | 各组件内联事件（无全局绑定） |
| doNotify / doErrorNotify | Toast 适配器 `common/notify.ts` |

## 8. 构建与部署（前端静态资源双托管）

- `pnpm --dir src/frontend build` → `src/frontend/dist/`。同一份产物支持两种托管形态（详见 architecture.md §7.1）：
  - **后端 embed（默认，对等上游）**：Makefile 同步至 `src/backend/server/static/dist` 由 `embed` 打包，`/UI/Dashboard` 直出。
  - **nginx 托管（可选卸载）**：nginx `root` 指向 dist，`try_files $uri @backend` —— 静态文件 nginx 直出，
    未命中一律反代后端（`/UI/*` 的 302 语义、`/api/*`、`/dl|/img|/bh`、`/health` 全部保真）；
    示例配置见 SPEC-14 `install/nginx/vestt.conf`。
- `vite.config.ts`：`build.target='es2020'`、`base: './'`（相对路径，同时兼容 basePathOverride 与 nginx 子路径托管）。
- 同域反代（nginx → 9117）无需 CORS；仅前端跨域直连后端时才启用 `allow_cors`。
- dev 模式：`pnpm --dir src/frontend dev` + Vite proxy `/api|/UI|/dl|/img|/bh|/health → :9117`。
