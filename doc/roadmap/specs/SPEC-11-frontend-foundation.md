# SPEC-11 前端 I：工程、鉴权与仪表盘框架

| 项 | 内容 |
|---|---|
| 规模 | M |
| 前置依赖 | SPEC-07（后端 API 可联调） |
| 产出 | Vue 3 工程完整装配 / api 服务层 / 登录流程 / 仪表盘布局骨架（含导航、通知、版本、API Key 面板） |

## 1. 目标与范围

把 SPEC-01 的最小 Vite 工程升级为完整前端基座：PrimeVue 4 主题体系、Pinia stores、Router、
`services/api.ts` 契约层、登录页、仪表盘壳。索引器表格内容与配置弹窗留给 SPEC-12，
搜索与设置弹窗留给 SPEC-13。

上游对照：`Content/index.html`（整体结构与导航）、`custom.js` 的
`loadJackettSettings/bindUIButtons`（首屏行为）、`login.html`。

## 2. 上游参考

| 参考文件 | 提取内容 |
|---|---|
| `src/Jackett.Common/Content/index.html` | 仪表盘信息架构：顶栏（logo/API Key/设置/注销）、筛选区、双 Tab 列表 |
| `src/Jackett.Common/Content/login.html` | 登录表单行为 |
| `src/Jackett.Common/Content/custom.js`（§loadJackettSettings、doNotify） | 首屏装载顺序与通知样式 |

## 3. 实现步骤与代码结构

### 3.1 应用装配（`src/frontend/src/main.ts`）

1. `createApp(App)` + `Pinia` + `Router`。
2. PrimeVue 4：`@primeuix/themes` Aura `definePreset`（主色可调，默认靛蓝）、`primeicons` 样式、
   `ToastService`、`DialogService`、全局 `Dialog` 组件。
3. 全局错误钩子：未处理 Promise rejection → Toast(danger)。

### 3.2 契约层（`src/frontend/src/services`）

1. `types.ts`：全部 DTO 类型（backend-api.md §1.1/§2.1/§2.2/§2.3/§2.6/§3.1 一一对应，字段名原样）。
2. `api.ts`：
   - 基地址解析：首请求 `GET /api/v2.0/server/config` 前以当前 `location.pathname` 推断 basePath；
     取到 `basepathoverride` 后固化。
   - `request()` 封装：JSON 头、401 → 跳登录、非 2xx → 抛 `{status, message}`（message 取响应文本）。
   - 命名函数：`getServerConfig / postServerConfig / setAdminPassword / getLogs / shutdown /
     getIndexers / getIndexerConfig / postIndexerConfig / testIndexer / deleteIndexer /
     getCache / manualSearch / logout`。
3. 单测：baseUrl 组合、401 跳转、错误归一化（vitest + msw 或 fetch mock）。

### 3.3 状态（`src/frontend/src/stores`）

按 [frontend.md §3](../../tech/frontend.md) 建 5 个 store；本 SPEC 交付
`auth/config/logs` 全功能 + `indexers/search` 的骨架（state + load）。

### 3.4 路由与登录（`src/frontend/src/views/LoginView.vue`）

- 路由：`/login`、`/dashboard`（默认重定向 `/login` → 校验会话后进 dashboard）。
- 登录页：密码输入 + 提交（`POST /UI/Dashboard` form-encoded）→ 成功 302 → `/dashboard`；
  失败 → 后端重定向回登录页（与上游一致，无需本地错误状态）。
- 会话探测：进入 dashboard 前调一个管理 API（`getServerConfig`），401 → 跳 `/login`。
- 未设置管理密码时：服务端直接放行，登录页仅闪现即跳过（对齐上游 Login 页行为）。

### 3.5 仪表盘壳（`src/frontend/src/views/DashboardView.vue`）

1. `AppHeader`：
   - 标题 + `app_version`。
   - API Key 面板：展示（遮罩切换）+ 复制按钮（Clipboard API，fallback execCommand）。
   - 设置按钮（占位 Dialog，SPEC-13 实现）、注销按钮（`GET /UI/Logout`）。
   - `external && !password` 时首访安全警告 Toast + "不再提示"（localStorage
     `vestt.externalAccessWarningHidden`）。
2. `IndexerFilters`（组件先交付，数据接线在 SPEC-12）：类型（public/semi-private/private）、
   标签（由索引器集合聚合）、状态（healthy/failing/untested）三个下拉 + 计数徽标。
3. Tabs：「已配置」「未配置」两个 `IndexerTable` 占位（空态插画 + 行数）。
4. Toast 通知适配器 `components/common/notify.ts`：`info/success/danger` 三型（对齐上游 doNotify）。
5. 加载骨架：首屏并发 `getServerConfig + getIndexers`，skeleton 屏。

### 3.6 basePath 深链

- hash 语义（frontend.md §2）本 SPEC 先建 `utils/hash.ts`（parse/serialize），
  search 深链在 SPEC-13 消费。

## 4. 接口定义

消费 backend-api.md：§1.1（首屏配置）、§5（登录/登出）、§6（health 不直接用）。
新增前端内部约定见 `services/api.ts` 导出清单（§3.2）。

## 5. 验收标准与验证方法

1. **联调冒烟**（真实后端）：
   - 未设密码：`/login` 自动跳 `/dashboard`；顶栏显示 API Key 与版本。
   - 设置密码后：访问管理 API 401 → 跳登录；错误密码 → 回登录页；正确密码 → 进入。
   - 注销 → 回登录页；重启浏览器仍保持会话（Cookie 14 天）。
2. **单元测试**（vitest）：api 层 6+ 用例；hash 工具 4+ 用例；`config/auth` store 4+ 用例。
3. **视觉走查**：PrimeVue Aura 主题生效（按钮/输入/表格风格统一）；Toast 三型正确。
4. `pnpm --dir src/frontend typecheck && pnpm --dir src/frontend test && pnpm --dir src/frontend build` 全绿；
   `make build` 后单二进制自带新 UI（`/UI/Dashboard` 直出）。
5. 更新 roadmap 进度表。
