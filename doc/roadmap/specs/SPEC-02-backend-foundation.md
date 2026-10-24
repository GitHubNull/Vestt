# SPEC-02 后端基础框架

| 项 | 内容 |
|---|---|
| 规模 | M |
| 前置依赖 | SPEC-01 |
| 产出 | HTTP 框架 + SQLite 存储层 + 日志体系 + 管理会话鉴权 + Server Config API（backend-api.md §1 全量） |

## 1. 目标与范围

落地与上游 `Jackett.Server/Startup.cs` 等价的请求管线，以及所有"与索引器无关"的后端能力：
配置持久化、日志、鉴权会话、服务器配置 API、UI 登录路由。

不在范围：Torznab 模型（SPEC-03）、索引器（SPEC-04+）、前端（SPEC-11+）。

## 2. 上游参考

| 参考文件 | 提取行为 |
|---|---|
| `src/Jackett.Server/Startup.cs` | 管线顺序：Exception → BasePath → Static → Authentication → MVC/CORS |
| `src/Jackett.Server/Controllers/ServerConfigurationController.cs` | §1 全部端点逻辑与校验错误文案 |
| `src/Jackett.Server/Controllers/UIController.cs` | 登录/登出/TestCookie 重定向链 |
| `src/Jackett.Common/Models/Config/ServerConfig.cs` | 配置字段与默认值 |
| `src/Jackett.Common/Services/ConfigurationService.cs`、`IndexerConfigurationService.cs` | 配置读写/持久化时机 |
| `src/Jackett.Common/Services/SecurityService.cs` | 密码哈希与会话校验 |
| `src/Jackett.Common/Services/LogCacheService.cs` + `Utils/LoggingSetup.cs` | 日志缓存与文件滚动 |

## 3. 实现步骤与代码结构

### 3.1 存储层（`src/backend/common/store`）

1. `store.go`：`Open(path) (*Store, error)` —— PRAGMA 按 [data-model.md §1](../../tech/data-model.md)；
   `db.SetMaxOpenConns(1)`。
2. `migrations.go`：迁移 1~3（schema_migrations / server_config / sessions；indexer_config 等表
   亦在本 SPEC 一并建表，避免后续二次迁移）。
3. DAO 文件：`config.go`（Get/Save server_config 单行 JSON）、`sessions.go`（Create/Get/Cleanup）、
   `logs.go`（Append/List，环形窗口）。全部方法带 `ctx`，内存库单测。

### 3.2 日志（`src/backend/common/utils/logging`）

1. 基于 `log/slog`：
   - Console handler（启动横幅 + 运行日志）。
   - File handler：`<data>/Logs/vestt.log`，按 10MB 滚动保留 1 份（对等上游滚动行为）。
   - Ring handler：容量 1000 的环形缓冲，线程安全，供 `/server/logs` 读取。
2. 级别切换 `SetLevel(info|debug)` 运行时生效（`logging` 配置项）。
3. 脱敏：日志输出前对 `apikey`、`password`、`cookieheader` 值替换 `***`（工具函数 `utils.Sanitize`）。

### 3.3 服务（`src/backend/common/services`）

1. `SecurityService`：
   - `HashPassword / CheckPassword`（bcrypt cost 11）。
   - `CreateSession() → token`（32B hex）、`ValidateSession(token) → bool`。
2. `ConfigService`：装载/保存 `models/config.ServerConfig`（结构体字段对齐 data-model.md §2.2 全表）。
   - 首启：生成 API Key（`crypto/rand` 32B hex）与 `instance_id`。
   - `Save` 同步写库；端口/绑定/external/basepathoverride 变更置 `restartRequired=true`（内存态）。

### 3.4 HTTP 层（`src/backend/server`）

1. `server.go`：chi 装配（顺序对齐 Startup.cs）：
   ```
   Recoverer → RequestID → RealIP → BasePath 剥离（basepathoverride 前缀）
   → CORS（allow_cors=true 时全放行 + 显式头） → 日志中间件（debug 级 URL）
   → 路由（见下） → 静态 404 兜底
   ```
2. 中间件：
   - `RequireSession`：管理 API 守卫；未设置管理密码时直接放行；`Cookie: vestt_session` 校验。
   - `RequireApiKey`：查询串 `apikey|passkey` 与配置比对（供 Shutdown 与 SPEC-07 复用）。
3. 路由与 handler（`src/backend/server/api/`，文件名对齐上游控制器）：
   | 文件 | 路由 |
   |---|---|
   | `health.go` | `GET /health` |
   | `server_config.go` | `GET/POST /api/v2.0/server/config`、`POST server/adminpassword`、`GET server/logs`、`POST server/update`、`POST server/shutdown` |
   | `ui_auth.go` | `GET /UI/Login`、`GET /UI/TestCookie`、`POST/GET /UI/Dashboard`、`GET /UI/Logout`（页面正文先以占位 HTML 返回，SPEC-11 换 SPA） |
4. 校验规则与错误响应：逐条对照上游 `ServerConfigurationController.UpdateConfig`
   （端口范围/回环绑定/basepath 以 / 开头/baseurl 与 flaresolverr 合法 URL/flaresolverr_maxtimeout≥5000/
   proxy port 范围、url 空则 type=disabled、blackholedir 必须存在、受限端口列表）。
   400 文本消息使用上游等价英文文案。
5. 会话 Cookie：`vestt_session`，HttpOnly，SameSite=Lax，Path=/，MaxAge 14 天；Secure 由 `r.TLS` 决定。
6. `POST /server/adminpassword`：接收 JSON 字符串；bcrypt 入库；清除现存会话（等效强制重登）。
7. 优雅停机：`Shutdown` handler 触发 `http.Server.Shutdown`（对齐上游 StopApplication）。

### 3.5 main.go 装配

按 [architecture.md §4.1](../../tech/architecture.md) 顺序构造并注入；
flags 覆盖配置的优先级 = flag > DB 配置 > 默认值（flag 变更不回写 DB，仅当次运行生效，行为记录在 README）。

## 4. 接口定义

全部契约见 [backend-api.md §1、§5、§6](../../tech/backend-api.md)。
本 SPEC 交付其中：`server/config`（GET/POST）、`server/adminpassword`、`server/logs`、
`server/update`、`server/shutdown`、`UI/*` 全部、`/health`。

## 5. 验收标准与验证方法

1. **单测**（`go test -C src/backend ./common/... ./server/...`）：
   - 迁移幂等：连开两次内存库不报错；表结构断言（`PRAGMA table_info`）。
   - ConfigService 默认值断言（端口 9117、cache_ttl 2100、cache_max 1000、flaresolverr_maxtimeout 60000）。
   - bcrypt 往返；会话过期判定。
   - Server Config 校验矩阵（至少 12 条用例：非法端口/受限端口/非回环绑定/basepath 无斜杠/非法 URL/…）。
   - Ring handler：写入 >1000 条后最早日志被淘汰。
2. **黑盒**（`go test -C src/backend ./test/integration`，httptest 起真实 server）：
   - `GET /api/v2.0/server/config` 返回结构含全部字段（对拍 backend-api.md §1.1 键集合）。
   - 修改 `port=9118` → 响应 `restart_required: true`；重启进程后生效。
   - 设置 adminpassword → 未带会话访问 `GET /api/v2.0/indexers`（404 占位亦可，验证 401 先行）→ 401；
     POST /UI/Dashboard（password）→ Set-Cookie → 再访问 200。
   - `POST /api/v2.0/server/shutdown?apikey=...` 进程退出码 0；错误 apikey → 401。
3. `make test` 全绿；更新 roadmap 进度表。
