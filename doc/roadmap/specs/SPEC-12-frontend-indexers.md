# SPEC-12 前端 II：索引器管理与动态配置表单

| 项 | 内容 |
|---|---|
| 规模 | M |
| 前置依赖 | SPEC-11 |
| 产出 | 双列表索引器表格 / 筛选联动 / 配置弹窗（DynamicForm 8 种字段） / 测试状态 / 批量添加 / 删除 / 标签 |

## 1. 目标与范围

完成仪表盘的核心交互：浏览、筛选、配置、测试、删除索引器。
上游对照：`custom.js` 的 `reloadIndexers / displayFilteredIndexersList / displayConfiguredIndexersList /
displayUnconfiguredIndexersList / addIndexer(s) / testIndexer / updateTestState / prepareDeleteButtons /
displayIndexerSetup / newConfigModal / getConfigModalJson`；`index.html` 的表格与弹窗骨架。

## 2. 上游参考

| 参考文件 | 提取内容 |
|---|---|
| `Content/index.html` | 表格列结构、操作按钮组、配置弹窗布局（caps 链接、描述、替代站点链接） |
| `Content/custom.js`（§3 函数族） | 上述行为语义 |
| `Jackett.Common/Models/IndexerConfig/ConfigurationData.cs` | 字段类型语义（前端渲染依据） |

## 3. 实现步骤与代码结构

### 3.1 IndexerTable（`components/indexer/IndexerTable.vue`）

1. **已配置列表**列：状态徽标（healthy 绿 / failing 红 / untested 灰，含 last_error tooltip）、
   名称+站点链接、类型、语言、标签 chips、caps 数、Torznab URL 复制按钮、
   操作列（搜索 / 测试 / 配置 / 删除）。
2. **未配置列表**列：名称、描述（title 属性全文）、类型、语言、标签、操作（配置）、
   行首复选框（批量添加）。
3. 排序：默认名称字母序（对齐上游），状态优先可选。
4. 空态与计数：`showing X of Y` 文案。
5. 删除：ConfirmDialog（文案对齐上游确认行为）→ `deleteIndexer` → 成功 Toast + 列表刷新。
6. 测试：按钮进入 loading 态（updateTestState 四态：idle/running/ok/fail）→ 完成更新徽标 + Toast
   （失败含 last_error 摘要）。批量"测试全部已配置"按钮（顺序执行，间隔 200ms，防 UI 卡死）。

### 3.2 筛选联动（IndexerFilters ↔ indexers store）

- 三个筛选正交（AND）：type / tag / state；URL hash 同步 `#indexers=1&filter=<json>`。
- 标签下拉项 = 全部索引器 tags 去重（大小写不敏感合并，对齐上游 tag_filter）。
- 未配置 Tab 默认隐藏已配置项（对齐上游 unconfigured 列表语义）。

### 3.3 DynamicForm（`components/config/DynamicForm.vue` + `DynamicField.vue`）

渲染规则按 [frontend.md §5](../../tech/frontend.md) 映射表：

1. `displayinfo` → Message 组件；`displayimage` → Image + 可选输入框（`cardigannCaptcha` 场景：
   图片 + `captchaText` 输入 + 提交时以 `cardigannCaptcha=<value>&captchaText=<text>` 提交，
   对照上游 getConfigModalJson 的 captcha 通道）。
2. `inputcheckbox`（多选）→ MultiSelect，`options` 对象转选项数组。
3. `password`：显示掩码占位；提交时值 === `********` 则该字段不提交（backend-api.md §2.2 备注）。
4. 提交 payload 组装：`{id, value}` / `{id, values}`；cookie 免密通道：若字段集合含 `cookieheader`
   提示块且用户选择 cookie 登录，则以 `{id: <cookieFieldId>, cookie: <header>}` 提交。
5. 错误呈现：非 2xx → 弹窗顶部 Alert（服务端文本）+ 保留输入；400 时表单不重置。
6. 成功：Toast + 弹窗关闭 + 列表刷新（新增者出现在已配置 Tab 并自动触发一次 Test，
   对齐上游 RequiresTesting 自动测试）。

### 3.4 IndexerConfigDialog

1. 打开：`getIndexerConfig(id)` → loading → DynamicForm。
2. 头部：名称（链 site_link）、描述、语言、类型、caps 数（点击展开 caps 列表）。
3. `alternativesitelinks`：存在时渲染下拉（写入站点链接选择，提交为 `siteLink` 字段，
   对照上游 Setup 弹窗行为）。
4. 重试与刷新验证码按钮（captcha 场景重新 GET config）。

### 3.5 批量添加

- 多选未配置索引器 → "添加所选" → 循环 `postIndexerConfig`（空配置直提，供免配置站点）
  → 逐项 Toast 结果（对齐 addSelectedIndexersSuccess/Error 文案语义）→ 刷新列表。

## 4. 接口定义

消费 backend-api.md §2.1~2.5（indexers 列表/Config/Config POST/Test/Delete）。
无新后端接口；如需新接口必须先改 backend-api.md。

## 5. 验收标准与验证方法

1. **端到端（真实后端）**：
   - 配置一个免密公共 YAML 索引器（fixture 假站点）→ 出现在已配置 → 自动测试变绿。
   - 配置一个需登录索引器（假站点 form）→ 错误密码 → Alert 文本；正确 → 成功。
   - captcha 索引器（假站点）→ 验证码图显示 → 输入后成功。
   - cookie 登录索引器 → cookieheader 通道成功。
   - 批量添加 5 个免密索引器 → 全部成功 Toast。
2. **渲染对拍**：8 种字段类型各一个 fixture Config JSON → 快照测试（组件渲染输出）。
3. **筛选**：type/tag/state 组合 ≥ 6 用例（store 单测）。
4. **操作列**：复制 Torznab URL（剪贴板 mock）；删除后列表消失且后端 DELETE 已调用（msw 断言）。
5. `pnpm --dir src/frontend typecheck && test && build` 全绿；联调记录写入 SPEC 澄清记录；更新 roadmap 进度表。

## 澄清记录

### 上游行为差异（以 SPEC 为准处）

1. **删除确认**：上游 `index.html` 删除按钮直接调用 `deleteIndexer` 无任何确认；本 SPEC 明确要求
   ConfirmDialog，已实现（PrimeVue ConfirmDialog，"Delete indexer / Are you sure you want to delete
   the indexer <name>?"，accept 按钮 danger）。上游依据：tmp/ref/Jackett-0.24.2900/Jackett.Web/src/custom.js
   `deleteIndexer()`（无确认框）。
2. **Test All 节奏**：上游 `testAllIndexers()` 对所有已配置索引器**并发**触发且全程无 Toast；SPEC §3.1.6
   要求串行 + 200ms 间隔 + 逐项 Toast（"Test started for <id>"/"Test <successful|failed> for <id>"）。
   已实现 SPEC 口径（store `testAll` 顺序 await + sleep(200)），上游并发行为不再保留。
3. **筛选器形态**：上游为单一下拉、值为 id 字符串（`test:passed`/`type:public`/`tag:xxx`，
   custom.js `updateFilter`），hash 形如 `#indexers&filter=<id>`；SPEC 要求 Type/Tag/Status 三下拉 AND 组合。
   已实现 SPEC 口径。深度链接因 hash 路由无法承载裸 `#indexers=1&filter=...`（与路由 hash 冲突），
   采用路由 query：`#/dashboard?indexers=1&filter=<urlencoded json>`，刷新后完整恢复（联调已验证）。
4. **状态列**：上游无独立状态列，测试状态由测试按钮图标（spin/check/times）表达；SPEC 要求 Status
   徽标列（Healthy=success/Failing=danger/Untested=secondary，title=last_error）。已按 SPEC 实现，
   测试按钮三态图标同时保留（与上游一致）。
5. **未配置表复选框**：上游仅 public 类型行显示复选框与 Add 按钮（custom.js `updateUnconfiguredButtons`）；
   SPEC 未作此限制，当前所有未配置行均可勾选。属 SPEC 覆盖上游，如需对齐上游可后续加 `type === 'public'`
   条件。

### 后端契约澄清（联调中发现并已修复/登记）

6. **inputtags 为第 9 种字段类型**：backend-api.md §2.2 原写 8 类，实际 Tags 配置项发射
   `inputtags`（582 份定义全部使用）。前端已按 InputChips 渲染、初始值逗号拆分、提交逗号拼接；
   backend-api.md 与 frontend.md 已同步更正。
7. **sitelink 预填修复**：上游 `GetConfigurationForSetup` 在返回表单前把空 SiteLink 预填为定义默认值；
   我方 `Base.ConfigurationForSetup`/`Cardigann.ConfigurationForSetup` 原缺少该步骤导致 GET Config 返回空
   sitelink。已补 `PrefillDefaultSiteLink`（base.go）并加回归测试（base_test.go
   TestConfigurationForSetupPrefillsSiteLink）。
8. **`:not(:contains())` 修复**：47 份定义使用 AngleSharp 的 `:not(:contains("..."))` 行过滤（如
   div.error:not(:contains("There are no results found."))）；goquery/cascadia 不支持该扩展，原实现留下
   空 `:not()` 导致匹配 0 行。selectors.go 已移植该伪类（先求 contains 匹配集再取补集），
   回归测试 5 子例（selectors_test.go TestQuerySelectorAllNotContains）。
9. **cookie 通道提交形状**：定义 `login.method: cookie` 时表单含 id 为 `cookie` 的输入项；上游提交
   `{id, cookie}` 双字段，后端 `ApplyItems` 将 `cookie` 映射为 item 值（base.go:291）。前端
   `buildConfigItems` 对 id=`cookie` 的 inputstring 输出 `{id, value?, cookie}` 形状，与后端一致。
10. **captcha 动态项 id**：GET Config 发射的动态项 id 为小写 `captchaimage`/`captchatext`
    （ItemsJSON 的 id 由 item 名 "Captcha Image"/"Captcha Text" 生成）；SPEC 措辞 CaptchaImage/CaptchaText
    指同一对字段。后端 `GetDynamic("CaptchaText")` 用 AddDynamic 键精确匹配，前后端大小写口径内部一致，
    联调（3dtorrents 验证码全流程）已验证。
11. **配置失败的 HTTP 状态**：登录/测试失败时后端返回 **500**（上游 `ConfigureIfOK` 把异常包装为
    Exception(...)），并非 400。前端错误处理：对话框内 Alert 展示服务器原文（所有错误）；非 400 时
    另发 danger Toast `Configuration failed: <msg>`（对齐上游 `doErrorNotify` 双通道）；400（captcha 失效）
    不 Toast、仅刷新验证码并保留已填字段。SPEC 措辞"400 → 不 Toast"实现为"400 不 Toast"。

### E2E 联调记录（真实浏览器 + dist/vestt + 假站点）

- 假站点 `.scratch/fakesite.py`（127.0.0.1:9777）按路径分发：`/search` 供 0Magnet（原单行 markup）；
  `/captcha.png` 返回 1x1 PNG；其余 GET/POST 返回组合页（logout 链接 + 登录 form + img.captcha +
  13city `table.torrents` NexusPHP 行 + 3dtorrents `table.lista` xbtit 行）。
- 已验证场景：
  1. 0Magnet（免密公共）配置 + sitelink 覆盖到假站点 → 自动测试 → Healthy 徽标 + Toast 序列；
  2. 0day.kiev（form 登录）sitelink 指向死端口 127.0.0.1:9999 → 对话框保持打开、Alert 显示服务器原文
     `Post "http://127.0.0.1:9999/takelogin.php": dial tcp 127.0.0.1:9999: connect: connection refused`，
     非 400 附 danger Toast；
  3. 13City（cookie 登录）粘贴 cookie 文本 → `Configured indexer: 13city` →
     `Test search in 13City => Found 1 releases`（table.torrents 行解析成功）；
  4. 3D Torrents（image captcha）→ GET Config 发射 captchaimage（data URL，120x30）+ captchatext →
     输入答案提交 → `Configured indexer: 3dtorrents` → `Test search in 3D Torrents => Found 1 releases`；
  5. 批量添加（ACG.RIP/BitMagnet/Catorrent/comicat/CrackingPatching/Anime Tosho）→ Toast 序列
     `Adding selected Indexers, please wait...`（sticky）→ 逐项 `Successfully configured <id>` →
     `Selected indexers successfully added.`，与 upstream custom.js:512-574 文案一致；
  6. 筛选：Type=Public → "Showing 5 of 7" + URL 同步 `?indexers=1&filter={"type":"public",...}`，
     刷新后完整恢复；删除 ACG.RIP/CrackingPatching → ConfirmDialog → `Deleted <id>` Toast + DELETE 204；
  7. Test All → 5 个 /Test 请求严格串行（0magnet 204、animetosho-xyz 500、bitmagnet 500、catorrent 204、
     comicat 500），徽标随之 Healthy/Failing 切换。
- 已知附带说明：未配置索引器首次 GET Config 时，验证码图的抓取使用定义默认 sitelink（上游
  GetConfigurationForSetup 同行为）；联调中 3dtorrents 因此对其公网默认站点产生一次只读 GET
  （登录页 + 验证码图），随后 sitelink 改为假站点，全流程其余请求均走 127.0.0.1:9777。
  Test All 对 catorrent/comicat 的测试亦触达了其真实公网站点（匿名只读搜索，与 Jackett 日常行为相同）。
