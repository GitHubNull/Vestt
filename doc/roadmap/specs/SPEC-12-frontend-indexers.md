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
