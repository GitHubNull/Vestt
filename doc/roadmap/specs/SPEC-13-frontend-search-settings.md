# SPEC-13 前端 III：手动搜索、缓存视图与设置中心

| 项 | 内容 |
|---|---|
| 规模 | M |
| 前置依赖 | SPEC-12 |
| 产出 | SearchDialog（聚合搜索/预设/深链） / 缓存 releases 视图 / Server Config 表单 / 日志查看 —— UI 功能收口 |

## 1. 目标与范围

完成管理 UI 剩余功能，达到对上游 `custom.js` 的全覆盖（前端映射表 frontend.md §7 全部条目就位）。

上游对照：`showSearch / updateSearchResultTable / updateReleasesRow / getSavedPresets /
setSavedPresets / prepareSearchButtons`（搜索）；`loadJackettSettings + 设置弹窗`（服务器配置）；
`Logs` 视图；`Cache` 视图。

## 2. 上游参考

| 参考文件 | 提取内容 |
|---|---|
| `Content/index.html`（搜索弹窗/设置弹窗骨架） | 字段布局：query/tracker 多选/类别；设置分区（Server base / Cache / Proxy / FlareSolverr / OMDb / Blackhole） |
| `Content/custom.js`（§1047~1459） | 搜索流程、结果表行内展开、预设存取 |
| `Content/custom.js`（§93~163、§1459+） | 设置装载与保存、proxyWarning |

## 3. 实现步骤与代码结构

### 3.1 SearchDialog（`components/search/`）

1. **打开方式**：顶栏全局搜索；已配置行内搜索按钮（预置 tracker）；hash 深链
   `#search=<q>&tracker=<id|all>&category=<cat>&filter=<t>`（刷新可恢复，打开即执行）。
2. **表单**：关键词输入（回车提交）；Tracker MultiSelect（选项=已配置索引器，`all` 快捷项）；
   类别 Select（Torznab 主类树，来自 caps 汇总或常量表——与上游 UI 一致用常量树）。
3. **执行**：`manualSearch(indexerId, params)` → loading（分项骨架）→
   `searchStore` 写入 results + indexers 状态。
4. **结果表**（DataTable，paginator 50/页）：
   列：日期（相对+title 绝对）、标题（外链 Details）、大小（人类可读+title 字节数）、
   种子/下载（seeders/peers，红绿着色规则对齐上游）、类别（CategoryDesc）、站点（Tracker）、
   操作（下载 Link / Blackhole Link / 磁链图标）。
   行展开：海报、InfoHash、MinimumRatio/MinimumSeedTime、Description、Upload/DownloadVolumeFactor
   （Freeleech 徽标当 d v f=0）。
   `indexers` 分项状态条：每目标站点一行（OK n 条 x ms / Error 文本 tooltip / Unknown）。
5. **预设**：保存当前表单（命名）→ localStorage `vestt.presets`（数组，含 name/query/trackers/cats）；
   预设栏支持应用/删除；重复名覆盖（对齐上游 savedPresets 行为）。
6. 空结果与失败态：分项失败不阻塞结果展示（Error 徽标）。

### 3.2 缓存 releases 视图（SettingsDialog 内 Tab 或独立路由页）

- 入口：设置弹窗内「Cached Releases」（`cache_enabled=false` 时禁用并提示，对齐上游）。
- `getCache()` → 同 3.1 结果表渲染（无搜索条，含 FirstSeen 列）。

### 3.3 SettingsDialog（`components/settings/`）

分区（对齐 index.html 设置弹窗）：

| 分区 | 字段（backend-api.md §1.1） |
|---|---|
| Server base | port、local_bind_address、external、cors、basepathoverride、baseurloverprise、blackholedir、admin password（新值/确认） |
| Cache | cache_enabled、cache_ttl、cache_max_results_per_indexer |
| Proxy | proxy_type（下拉：disabled/http/socks4/socks5）、proxy_url、proxy_port、proxy_username、proxy_password；type≠disabled 且 url 空 → proxyWarning 提示 |
| FlareSolverr | flaresolverrurl、flaresolverr_maxtimeout |
| OMDb | omdbkey、omdburl |
| 更新 | updatedisabled、prerelease、检查更新按钮（POST update → Toast 结果） |

1. 保存：PATCH 语义（合并当前 config 与变更字段）→ `postServerConfig`。
   响应含 `restart_required=true` → 顶部横幅"配置已保存，需要重启服务生效"（提供"立即重启"？——否：
   Vestt 由用户手动重启，横幅仅提示；对照 backend-api.md 差异清单）。
2. 校验错误：400 文本 → 对应分区顶部 Alert。
3. 修改管理密码：`setAdminPassword(JSON.stringify(pwd))` → 成功后强制重新登录（后端清会话）。

### 3.4 日志查看（LogViewer）

- `getLogs()` 渲染：时间 / 级别徽标（INFO/DEBUG/WARN/ERROR 色） / 消息（等宽字体）。
- 手动刷新 + 10s 轮询开关；底部"加载更早"分页（后端环形缓冲直接全量返回）。

### 3.5 顶栏收尾

- 搜索按钮触发 SearchDialog；设置按钮触发 SettingsDialog（Tab：Server / Cached Releases / Logs / API Key 说明）。
- API Key Tab：完整 key + Torznab base URL 示例（复制按钮）。

## 4. 接口定义

消费 backend-api.md：§1.1/1.2（config）、§1.4（logs）、§1.5（update）、§2.6（Cache）、§3.1（手动搜索）。
无新后端接口。

## 5. 验收标准与验证方法

1. **搜索端到端**（fixture 假索引器）：
   - 关键词搜索返回 fixture 条目；日期/大小格式正确；下载按钮指向 /dl 代理（点击 200）。
   - Tracker 多选过滤生效；Category 过滤生效；分项错误显示。
   - 行展开信息完整；Freeleech 徽标正确。
   - 预设保存→刷新页面→应用预设→同请求重发（msw 断言 URL）。
   - hash 深链 `#search=matrix&tracker=all` 直开弹窗并执行。
2. **设置端到端**：
   - 修改 port → 保存 → `restart_required=true` 横幅出现。
   - 非法值（port=70000 / basepath 无斜杠 / flaresolverr timeout=1000）→ Alert 显示服务端文案。
   - proxy type=socks5 但 url 空 → proxyWarning。
   - 日志视图显示最近操作（与后端日志时间线一致）。
3. **组件测试**：SearchDialog 状态机（idle/loading/done/error）≥5 用例；Settings 表单合并逻辑 ≥5 用例；
   LogViewer 轮询开关 2 用例。
4. **上游行为核对**：frontend.md §7 映射表逐行勾选，全部有对应实现（写入澄清记录）。
5. `pnpm --dir src/frontend typecheck && test && build` 全绿；`make build` 产物联调通过；更新 roadmap 进度表。
