# SPEC-04 索引器框架与 HTTP 客户端栈

| 项 | 内容 |
|---|---|
| 规模 | M |
| 前置依赖 | SPEC-03 |
| 产出 | Indexer 接口与基类 / 注册表与生命周期管理 / HTTP 客户端栈（限速·Cookie·代理·Cloudflare·FlareSolverr）/ 动态配置数据模型 |

## 1. 目标与范围

建立所有索引器（YAML 驱动与手写）共用的运行框架：
"一个索引器如何声明自己、如何被配置/测试/删除、如何发请求、如何产出 ReleaseInfo"。

不在范围：YAML 引擎（SPEC-05/06）、具体索引器实现（SPEC-08~10）、对外端点（SPEC-07）。

## 2. 上游参考

| 参考文件 | 提取内容 |
|---|---|
| `src/Jackett.Common/Indexers/IIndexer.cs`、`IndexerBase.cs`、`BaseIndexer.cs`、`BaseWebIndexer.cs` | 接口/基类职责、FloodLimit、下载与图片代理 |
| `src/Jackett.Common/Indexers/IndexerResponse.cs`、`IndexerRequest.cs`、`IndexerPageableRequestChain.cs` | 请求/响应与分页链模型 |
| `src/Jackett.Common/Services/IndexerManagerService.cs`、`IndexerConfigurationService.cs` | 生命周期：加载/实例化/配置/测试/删除/元索引器初始化 |
| `src/Jackett.Common/Models/IndexerConfig/ConfigurationData.cs` | 动态配置项模型与 8 种 item type（§3.4） |
| `src/Jackett.Common/Utils/Clients/WebClient.cs`、`HttpWebClient2.cs` | 请求栈行为（重试、CF 检测、UA、头） |
| `src/Jackett.Common/Utils/BrowserUtil.cs` + `FlareSolverr` 引用 | FlareSolverr 调用协议 |
| `src/Jackett.Common/Models/DTO/ConfigItem.cs`、`Indexer.cs` | DTO 形状（backend-api.md §2） |

## 3. 实现步骤与代码结构

### 3.1 Indexer 接口（`src/backend/common/indexer`）

1. `indexer.go`：按 [architecture.md §4.2](../../tech/architecture.md) 签名定义接口
   （含 `IndexerResult{Indexer, Releases, ElapsedTime, IsFromCache}`）。
2. `errors.go`：
   ```go
   type ErrKind int // Config Auth Parse RateLimit Network Internal
   type IndexerError struct { IndexerID string; Kind ErrKind; Message string; RetryAfter time.Duration }
   func (e *IndexerError) Error() string   // 格式对齐上游 IndexerException.ToString
   type TooManyRequestsError struct{ ... } // RetryAfter，由 client 栈抛出
   ```
3. `base.go` `BaseIndexer`：
   - 通用字段：id/name/description/siteLink/altLinks/language/type/tags/caps/config state/lastError。
   - `LoadFromSavedConfiguration`、`Unconfigure`、`SaveConfig`（写 `indexer_config`）。
   - `Test(ctx)` 默认实现 = `t=search` 空词查询并要求 ≥0 条（可被子类覆盖）。
   - `Download(ctx, link)`：委托 client 栈带 Cookie 转发（HTML 站点需先经索引器解析函数定位真实 URL ——
     由子类实现 `ResolveDownload` 钩子）。

### 3.2 HTTP 客户端栈（`src/backend/common/httpclient`）

1. `client.go`：`Request{Method, URL, Headers, Cookies, Body, RawBody}` / `Result{Body, Headers, StatusCode}`；
   `Do(ctx, indexerID string, req) (Result, error)`。
2. RoundTripper 链（architecture.md §4.4 顺序）：
   - `ratelimit.go`：`limiterRegistry map[indexerID]*limiter`；行为 = 固定最小间隔（`requestDelay`，默认 0）+
     突发上限 1；`Test()` 索引器用独立短间隔。**禁用请求并发**于同一索引器（信号量=1，分页串行）。
   - `cookiejar.go`：每索引器 `net/http/cookiejar` + 变更回调持久化到 `indexer_config.cookies_json`。
   - `proxy.go`：按 proxy_type 构造 `http.Transport`（http/socks4/socks5）。
   - `cloudflare.go`：响应判定 = (403/503) && (body 含 `__cf_chl`/`cf-browser-verification` ||
     header `cf-mitigated: challenge`) → 触发 FlareSolverr。
   - `flaresolverr.go`：`POST {url}/v1` body `{cmd:"request.get", url, maxTimeout}`；
     解析 `solution.cookies` + `solution.userAgent` → 注入该索引器 Cookie/UA 并重放一次（次数上限 2）。
3. `headers`：默认 UA（对齐上游默认 UA 字符串）、`Accept-Language: en-US`；索引器可覆盖。

### 3.3 注册表与生命周期（`src/backend/common/services`）

1. `registry.go`：
   - `Register(factory func(deps Deps) (indexer.Indexer, error))`；内置清单 =
     62 个手写索引器工厂（SPEC-08~10 逐个登记，本 SPEC 先以 `// TODO(manual)` 占位注册 0 个）+
     1 个 Cardigann 引擎工厂（SPEC-05 接入）。
2. `indexer_manager.go` `IndexerManagerService`：
   - `Init(ctx)`：实例化全部内置索引器 → 读取 `indexer_config` 表 → 已配置者 `LoadFromSavedConfiguration`
     → `InitMetaIndexers()`（all/aggregate）。
   - `GetAllIndexers() / GetIndexer(id) / GetConfiguredIndexers()`。
   - `TestIndexer(id)`：跑 `Test` → 更新 `state`（healthy/failing）与 `last_error`。
   - `ConfigureIndexer(id, items)` → `ApplyConfiguration` → `RequiresTesting` 时自动测试 → 保存。
   - `DeleteIndexer(id)`：Unconfigure + 清缓存 + 删表行。
   - `ValidIndexers(query)`：按 `CanHandleQuery` 裁剪（元索引器扇出用）。
3. 元索引器骨架（`indexer/meta/`）：`all`、`aggregate`；行为细节在 SPEC-07 落地，
   本 SPEC 提供注册与 `t=indexers` 所需的成员枚举。

### 3.4 动态配置数据模型（`src/backend/common/indexer/configdata`）

对齐上游 `ConfigurationData.cs`，字段模型：

```go
type Item interface {
    ID() string; Type() string   // inputstring|password|hiddendata|inputbool|inputselect|inputcheckbox|displayinfo|displayimage
    ToDisplay() map[string]any   // backend-api.md §2.2 形状
    FromItems([]dto.ConfigItem) error
}
```

- 提供 `StringItem / PasswordItem / HiddenItem / BoolItem / SelectItem / MultiSelectItem / InfoItem / ImageItem(captcha)`。
- 基类 `ConfigData`：`Items() []Item`（反射收集，顺序=字段声明序，对齐上游 GetConfigurationItems）。
- DTO 序列化键：`id/type/name/value/values/options/cookieheader说明`（对拍 backend-api.md §2.2 示例）。

## 4. 接口定义

- 本 SPEC 无新 HTTP 端点，但 §3.4 的 `ToDisplay` 输出必须满足 backend-api.md §2.2；
  `ConfigureIndexer` 输入必须满足 §2.3。
- 下游约定（SPEC-05/08+ 实现者视角）：
  ```go
  func (x *MyIndexer) ResultsForQuery(ctx, q, isMeta) (*IndexerResult, error)
  // 内部：client.Do(ctx, x.ID(), req) → 解析 → []models.ReleaseInfo
  ```

## 5. 验收标准与验证方法

1. **限速器**：并发 20 请求同一索引器 → 实际串行，间隔 ≥ requestDelay（计时断言，容差 ±10%）；
   不同索引器互不阻塞。
2. **Cookie 持久化**：httptest 站点 Set-Cookie → 第二次请求自动携带 → 重启 store（内存库重建）后
   cookies_json 恢复并再携带。
3. **代理**：proxy_type=socks5 指向本地无服务端口 → 请求失败错误 Kind=Network；type=disabled 直连成功。
4. **Cloudflare/FlareSolverr**：httptest 模拟 CF 盾响应 + 内嵌假 FlareSolverr → 断言触发 1 次解算、
   重放携带注入 Cookie、成功后正常返回；解算失败返回 IndexerError{Kind: Auth}。
5. **生命周期**：注册 3 个假索引器（httptest 假站点）→ Configure（inputstring/password/bool/select 混合）→
   `GET Config` 回显值一致 → Test 更新 state → Delete 后 config 表行为空、caps 查询不可见。
6. **DTO 形状**：ConfigData → JSON golden 对拍（8 种 item type 各 1 例）。
7. `make test` 全绿；更新 roadmap 进度表。

## 澄清记录

实现期间以下行为以上游源码为准澄清（2026-10-24）：

1. **空代理 URL guard**（`tmp/ref/.../Utils/Clients/WebClient.cs InitProxy`）：`ProxyType=disabled`
   或代理 URL 为空 → 直连。Go 零值 `Config` 的 `ProxyType=0` 若不做此 guard 会被
   `http.ProxyURL("http://")` 静默退化为拨号 80 端口（本地测试时把请求发给 127.0.0.1:80）。
   `buildTransport` 已按上游语义 guard；手写索引器禁止绕过。
2. **EmulateBrowser 三态**：上游默认 `true`（可空）。Go 侧用 `*bool`（nil=emulate），
   避免零值 false 改变默认行为。Base.Download 显式传 `Bool(true)`（上游
   `BaseWebIndexer.Download` 里 `EmulateBrowser = true`）。
3. **Base 必须匿名嵌入**：Go 方法提升只对匿名字段生效。`meta.Aggregate` 曾用
   命名字段 `Base indexer.Base` 导致接口方法集为空（编译期 "does not implement"）。
   所有手写索引器（SPEC-08~10）一律 `indexer.Base` 匿名嵌入。
4. **Cookie 渠道别名**：前端/下游 POST 的 `ConfigItem.Cookie` 字段作为该 item 的
   value 应用（上游 cardigann cookie 登录即写入 cookieheader item value）。
5. **BoolItem.FromJSON 接受字符串**：上游 `Value<bool>()`（Newtonsoft）把字符串
   `"true"` 转 bool；Go `encoding/json` 不做该转换，FromJSON 显式兼容 `"true"/"1"`
   （posted ConfigItem 的 value 一律是字符串）。
6. **Init 锁序**：`Init` 持写锁期间不得调用 `configuredMembers`（内部 RLock → 自死锁）。
   aggregate 在释放锁后构建、再回写（竞争窗口无害：Init 完成前管理器不可用）。
7. **ConfigureIndexer 落库顺序**：apply 成功立即 `SaveConfig`，再按需自测。上游
   在 `ApplyConfiguration` 期间即保存（`ConfigureIfOK` → `SaveConfig`），测试的 state
   写入必须看到已存在的 config 行。
8. **state 行生命周期跟随 config 行**：`setState` 遇到无 config 行（从未配置/已删除）
   时不创建 state 行，仅更新内存 LastError（上游无独立 state 存储，LastError 存于
   configData；`state` 列是本项目 data-model §2.4 的仪表盘设计）。
9. **重试语义**（上游 Polly `RetryStrategy`）：5xx/408/异常可重试（2 次、2s 基数、
   指数+jitter 0.5~1.5×）；重试耗尽后**返回最后一次响应**（Polly 返回最后 outcome
   而非抛错）。429/CF 挑战/site-down 不可重试，直接映射 IndexerError
   （Kind=RateLimit/Auth/Network）。初版 doOnce 把 retriable 标志挂在 nil err 上被
   Do 忽略，已修。
10. **cookie jar 路径归一**：origin-form 请求的根路径为空字符串，jar 匹配前归一为
    "/"，否则 FlareSolverr 注入的 `cf_clearance`（path=/）不会随重放请求携带。
11. **限速器语义**：整调用持有索引器互斥锁（同索引器请求完全串行，突发上限 1），
    `IsTest` 请求用独立短间隔（`TestRequestDelay`）。
