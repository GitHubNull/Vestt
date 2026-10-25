# Vestt 前端设计（Vue 3 + PrimeVue 4）

| 项 | 内容 |
|---|---|
| 文档版本 | 1.2 |
| 上游基线 | `src/Jackett.Common/Content/{index.html, custom.js, login.html}`（jQuery + Handlebars） |
| 技术栈 | Vue 3.5 + TypeScript + Vite + PrimeVue 4.x + Pinia + Vue Router + vue-i18n + pnpm |
| 接口契约 | [backend-api.md](backend-api.md)（本文不重复字段定义） |

> **v1.2 改版说明（用户决策）**：设置入口收口——索引器与服务器子页全部收进齿轮菜单；
> API Key 并入服务器配置页（不再是独立路由）；搜索框空态显示放大镜、有输入变一键清除；
> 主题支持亮/暗切换（默认暗，localStorage `vestt.theme`）；引入 vue-i18n，**默认简体中文**，
> 可切英文（localStorage `vestt.locale`，中/英两套文案在 `src/i18n/`）。
>
> **v1.1 改版说明（用户决策，有意偏离上游单页 Dashboard 布局）**：搜索表单成为首页
> （Google 式，结果页内展示），索引器/服务器管理拆到独立路由，全局换液态玻璃主题。
> SPEC-11~13 的功能对等项全部保留，仅布局与视觉偏离上游。

---

## 1. 工程结构

```
src/frontend/
├── package.json            # scripts: dev / build / test / lint
├── vite.config.ts          # dev proxy → http://127.0.0.1:9117；base 从后端注入
├── tsconfig.json
├── index.html
└── src/
    ├── main.ts             # 装配 PrimeVue(Aura, 亮/暗) / Pinia / Router / i18n / Toast / DialogService；深链预存
    ├── style.css           # 全局：亮/暗渐变背景（CSS 变量切换）+ .glass-card / .glass-nav 玻璃工具类
    ├── i18n/               # vue-i18n：zh.ts（默认）/ en.ts / 装配与 locale 持久化
    ├── App.vue             # AppShell + Toast + DynamicDialog
    ├── router/
    │   └── index.ts        # 多路由（见 §2）；守卫：深链拦截 + 会话探测
    ├── stores/             # Pinia
    │   ├── auth.ts  config.ts  indexers.ts  search.ts  logs.ts  settings.ts  theme.ts
    ├── services/
    │   ├── api.ts          # fetch 封装（basePath、错误归一化）
    │   ├── types.ts        # 后端 DTO 的 TS 类型（与 backend-api.md 一一对应）
    │   ├── format.ts       # 上游 handlebars 助手移植（jacketTimespan/jacketSize/dateFormat）
    │   ├── searchPresets.ts# 预设存取（localStorage `vestt.presets`，结构化 {name,query,trackers,cats}）
    │   ├── torznabCategories.ts # 搜索表单用的主类别常量（全树在后端）
    │   └── searchHash.ts   # `#search=…` / `#indexers=1&filter=…` 深链解析 + sessionStorage 预存
    ├── utils/
    │   └── hash.ts         # location.hash 通用解析/序列化（键序保持，URI 编解码）
    ├── components/
    │   ├── indexer/        # IndexerTable / IndexerFilters
    │   ├── config/         # IndexerConfigDialog / DynamicForm / DynamicField
    │   ├── search/         # SearchResultTable / PresetBar（SearchDialog 已随改版退役）
    │   ├── settings/       # ServerConfigForm / CacheReleases / LogViewer / ApiKeyPanel（并入服务器配置页）
    │   └── common/         # AppShell / CopyButton / Notify 适配
    └── views/
        ├── HomeView.vue            # 搜索首页（查询框 + 页内结果 + 一键清除）
        ├── IndexersView.vue        # 索引器管理（已配置/未配置 + 筛选 + 配置对话框；入口在齿轮菜单）
        ├── SettingsServerView.vue  # 服务器配置 + API Key 面板
        ├── SettingsCacheView.vue   # 缓存 releases
        ├── SettingsLogsView.vue    # 日志
        └── LoginView.vue
```

## 2. 路由与 basePath

hash 历史路由（后端只在 `{base}/UI/Dashboard` 直出 SPA）：

| 路由 | 视图 | 说明 |
|---|---|---|
| `/` | HomeView | 搜索首页 |
| `/login` | LoginView | 无密码时守卫直接放行 |
| `/indexers` | IndexersView | 索引器管理，`?indexers=1&filter=<json>` 承载筛选深链 |
| `/settings/server` | SettingsServerView | 含 API Key 面板（v1.2 并入） |
| `/settings/cache` | SettingsCacheView | |
| `/settings/logs` | SettingsLogsView | |
| `/dashboard` | → 重定向 `/` | 单页时代书签兼容 |
| `*` | → 重定向 `/` | |

- 除 `/login` 外所有路由 `meta.requiresAuth`，守卫先探测会话（`auth.probe()`），未认证跳
  `/login`；网络错误放行（视图自行报错）。守卫最前拦截裸 `#indexers=1&filter=<json>` 深链
  （main.ts 已预存 sessionStorage），改写为 `/indexers` 路由 query。
- 深链语义（与上游 location.hash 兼容）：
  - `#search=<q>&tracker=<id|all>&category=<cat>&filter=<t>`：首页打开并执行搜索，
    刷新重执行；由 `searchHash.ts` 预存 + HomeView 消费还原。
  - `#indexers=1&filter=<json>`：进入 `/indexers` 并应用筛选（守卫改写为路由 query）。
- `basePathOverride` 处理：`api.ts` 启动时调用 `GET server/config` 取 `basepathoverride`，
  所有请求前缀化；Vite dev 经 proxy 直连后端无需处理。
- 登录页：`LoginView.vue` 承担上游 login.html 等价行为（POST 后跳首页 `/`）。

## 3. 状态管理（Pinia）

| Store | State | 关键 Actions | 对应后端 |
|---|---|---|---|
| `auth` | passwordSet, authenticated | `login()`, `logout()` | `UI/Dashboard` POST/GET Logout |
| `config` | serverConfig, loading | `load()`, `save(patch)` | `GET/POST server/config` |
| `indexers` | all[], filters{type,tag,state}, testStates{} | `load()`, `test(id)`, `remove(id)`, `addIndexers(ids)` | `indexers*` 全套 |
| `search` | query, categories[], trackers[], results[], indexerStates[] | `applyParams(preset)`, `run()`, `savePreset()` | `indexers/{id}/results` |
| `logs` | entries[] | `load()`, `poll()` | `GET server/logs` |
| `settings` | draft（服务器配置编辑态） | `open()`, `save()`, `changePassword()` | `GET/POST server/config` |

原则：API 调用只出现在 stores/services；组件不直接 fetch。

`search` store 与 UI 可见性解耦（v1.1）：首页 HomeView 拥有表单与结果渲染，
`applyParams()` 仅写入 query/trackers/categories（深链与行内"搜索"交接用）。

## 4. 组件架构树

```
App.vue
├── AppShell                    # 玻璃顶栏：品牌+版本、导航（搜索）、齿轮菜单（索引器/服务器配置/
│   │                           # 缓存结果/日志）、语言（中/EN）与主题（夜/昼）滑动开关、
│   │                           # 注销（有管理密码时）、外部访问安全警告横幅
│   └── RouterView
│       ├── HomeView            # 搜索首页（居中查询框；执行后切换顶部紧凑模式）
│       │     查询框左图标：空=放大镜；有输入=一键清除（×，点击清空并聚焦）
│       │     高级行（tracker MultiSelect / category Select，可折叠）
│       │     PresetBar（保存/加载预设 → localStorage `vestt.presets`）
│       │     每索引器状态条（OK n [x ms] / Error / Unknown）
│       │     SearchResultTable（DataTable 分页渲染）
│       │       列：日期 · 标题(链到 Details) · 大小 · 种子/下载数 · 类别 · 站点 · 操作(下载/Blackhole/磁链)
│       │       行内展开：海报、infohash、上下传比、最小比率/时间
│       ├── IndexersView        # 筛选栏 + Tabs[已配置/未配置] IndexerTable + IndexerConfigDialog + ConfirmDialog
│       │     已配置行：状态徽标 · 名称+图标 · 类型 · 标签 · Caps · 操作（搜索/测试/配置/复制 Torznab URL/删除）
│       │     未配置行：名称 · 描述 · 类型 · 语言 · 标签 · 多选批量添加
│       ├── SettingsServerView  # ServerConfigForm（进入时 settings.open() 初始化 draft）
│       │                        + ApiKeyPanel（掩码切换 + 复制 + Torznab base URL）
│       ├── SettingsCacheView   # CacheReleases（cache_enabled=false 时显示禁用提示）
│       ├── SettingsLogsView    # LogViewer（客户端分页 + 10s 轮询）
│       └── LoginView
├── Toast                       # 全局通知（对等上游 doNotify）
└── DynamicDialog               # DialogService 挂载点
```

### 4.1 上游行为对等要点（UI 验收时逐项核对）

- 未配置索引器批量添加成功/失败的通知文案与重试逻辑（`addSelectedIndexers*`）。
- 测试状态动画：未测试/进行中/成功/失败四态（`updateTestState`）。
- API Key 掩码切换 + 一键复制（Clipboard API + fallback execCommand）。
- `external=true 且无密码` 首次访问弹出安全警告（可隐藏，localStorage 记忆）。
- 代理类型选择后的 `proxyWarning` 提示。
- 缓存 releases 视图仅在 `cache_enabled` 时可进入；表格列与上游 `updateSearchResultTable` 对齐。
- 预设（presets）：`vestt.presets` localStorage 键，结构与上游 `savedPresets` 等价。
- 索引器行"搜索"：跳首页并预选该 tracker（不写 hash，见 SPEC-13 澄清记录）。

## 5. 动态配置表单（DynamicForm）

渲染器核心：接收 `ConfigItem[]`（backend-api.md §2.2 的 8 种 `type`），输出 PrimeVue 表单：

| type | PrimeVue 组件 | 备注 |
|---|---|---|
| `inputstring` | InputText | `value` 绑定 |
| `password` | Password(toggleMask) | 提交时若为掩码占位 `********` 则跳过该字段 |
| `inputbool` | Checkbox | value 布尔字符串处理 |
| `inputselect` | Select | `options` 对象 → {label, value} |
| `inputcheckbox` | MultiSelect（或 Checkbox 组） | `values` 数组 ↔ `options` |
| `inputtags` | InputChips | Tags 字段；提交为逗号拼接字符串（上游 tagify `originalInputValueFormat`） |
| `displayinfo` | Message(closable=false) | 纯说明 |
| `displayimage` | Image | `value` 为 dataURL（验证码），附可选输入框（`cardigannCaptcha`） |
| `hiddendata` | 不渲染 | 提交时原样回传 |

提交：收集为 `ConfigItem[]`（`{id, value}` / `{id, values}` / `{id, cookie}`），POST §2.3。
错误处理：非 2xx → 表单顶部 Alert 展示服务端消息；引擎 captcha 失败时保留表单并刷新验证码。

## 6. PrimeVue 4 接入与主题（v1.2 亮/暗液态玻璃）

- 主题：`@primeuix/themes` Aura 预设（`definePreset` 覆盖主色为 indigo），
  `app.use(PrimeVue, { theme: { preset, options: { darkModeSelector: '.vestt-dark' } } })`。
  **默认暗色**（main.ts 启动给 `<html>` 加 `vestt-dark`），顶栏为「🌙 [switch] ☀️」左右
  滑动开关（关=夜、开=昼，两侧图标可点），选择持久化在 localStorage `vestt.theme`
  （stores/theme.ts）。
- 玻璃策略：
  - `definePreset` 按色系覆盖 `content.background/hoverBackground/borderColor`：
    暗色为半透明白（`rgba(255,255,255,.045~.10)`）+ `surface.900` 半透明（`surface.950`
    不透明承载文本）；亮色为半透明白（`rgba(255,255,255,.65~.85)`）。DataTable/Dialog/
    表单字段自动继承玻璃感。
  - `style.css`：玻璃颜色全部抽为 CSS 变量，`html.vestt-dark` 切换两套值；body 亮/暗两套
    固定渐变背景（attachment fixed，玻璃模糊的 backdrop）；`.glass-card`（毛玻璃面板）与
    `.glass-nav`（粘性顶栏）工具类，含 `backdrop-filter` 与 `@supports` 回退。
- **i18n（v1.2）**：vue-i18n（composition 模式），`i18n/zh.ts`（默认简体中文）+ `en.ts`；
  顶栏为「中 [switch] EN」左右滑动开关（两种语言，不用下拉），切换并持久化 localStorage
  `vestt.locale`；stores 内通知文案经 `i18n.global.t` 翻译；来自后端/索引器定义文件的
  动态文案（如配置字段名、错误消息）保持原文。
- 必装服务：ToastService、DialogService；样式引入 `primeicons`。
- DataTable 懒加载：手动搜索结果一次取回但分页渲染（与上游一次性渲染对齐，10k 行内不做服务端分页）。
- 表单校验以服务端为准（上游无前端校验），仅做必填提示。

## 7. 与上游 JS 的映射表（供对等性核对）

| 上游 custom.js 函数 | Vestt 归属 |
|---|---|
| loadJackettSettings | `configStore.load` |
| reloadIndexers / configureFilters / display*IndexersList | `indexersStore.load` + IndexerTable computed |
| addIndexer / addIndexers | `indexersStore.addIndexers` |
| testIndexer / updateTestState | `indexersStore.test` + 状态徽标 |
| displayIndexerSetup / newConfigModal / getConfigModalJson | IndexerConfigDialog + DynamicForm |
| showSearch / updateSearchResultTable / updateReleasesRow | `searchStore.run` + HomeView 内嵌 SearchResultTable |
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
