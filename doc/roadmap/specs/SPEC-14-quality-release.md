# SPEC-14 质量体系与发布工程

| 项 | 内容 |
|---|---|
| 规模 | M |
| 前置依赖 | SPEC-13（且 SPEC-08/09/10 已全部完成） |
| 产出 | 测试收口 / 交叉编译与产物 / Docker / systemd / Jackett 配置导入工具 / 文档同步与发布清单 |

## 1. 目标与范围

项目级收尾：把分散在各 SPEC 的测试资产整合为质量门禁；建立发布产物链；
提供从 Jackett 数据目录迁移到 Vestt 的导入工具；完成文档与 PRD 验收核对。

## 2. 上游参考

| 参考文件 | 提取内容 |
|---|---|
| `azure-pipelines.yml`、`Installer.iss`、`install_service_systemd.sh`、`jackett_launcher.sh/.bat` | 发布矩阵与服务安装行为 |
| `src/Jackett.Test/` | 上游测试组织方式（单元 + 核心用例） |
| `src/Jackett.Common/Services/ConfigurationService.cs` | 数据目录文件布局（导入工具的读取规范） |

## 3. 实现步骤与代码结构

### 3.1 测试体系收口

1. **分层**：
   - L1 单元（`go test -C src/backend ./...`）：models/engine/converters/manual indexers/store —— 已在各 SPEC。
   - L2 集成（`src/backend/test/integration`）：起真实 HTTP server（内存库）打全部端点；
     fixture 索引器 20+ 个，覆盖协议错误矩阵。
   - L3 冒烟（`src/backend/test/smoke`）：构建产物启动 → `/health` → caps → 假站点搜索 → 下载代理 → 退出。
2. **质量门禁**（`make test` 聚合）：
   - `gofmt/vet` 零输出；`golangci-lint`（启用 errcheck/staticcheck/govet）零严重项。
   - 前端：`typecheck + eslint + vitest`。
   - 覆盖率阈值：`common/` ≥ 70%、`common/engine` ≥ 80%、`common/models`+`torznab` ≥ 85%。
3. **CI**（`.github/workflows/ci.yml`）：lint → test（含 SQLite 临时目录）→ build 三平台 → 冒烟。

### 3.2 交叉编译与发布产物

1. `make release`：矩阵 = linux/amd64、linux/arm64、linux/arm/v7、windows/amd64、darwin/amd64、darwin/arm64。
   - 命名：`vestt-v<ver>-<os>-<arch>[.exe]`；附 `SHA256SUMS`。
   - 前置：`make web`（产物 embed）；版本注入同 SPEC-01。
2. `Dockerfile`：多阶段（golang builder + distroless/base 或 alpine）；
   `EXPOSE 9117`；`VESTT_DATA=/data`；HEALTHCHECK `/health`；non-root 运行。
3. `docker-compose.yml` 示例（端口/卷/FlareSolverr 旁路示例）。
4. `install/systemd/vestt.service` + `install/install_service_systemd.sh`
   （参数化用户/路径；对齐上游脚本行为）。
5. `CHANGELOG.md` 初始化与语义化版本约定（`vMAJOR.MINOR.PATCH`，对应上游基线 0.24.2900 的功能面）。
6. nginx 分离部署支持（architecture.md §7.1 形态 B）：`install/nginx/vestt.conf`
   （`root` 指向前端 dist + `try_files $uri @backend` 反代规则与 `X-Forwarded-*` 头）；
   release 附带前端静态包 `vestt-web-v<ver>.tar.gz`（= `src/frontend/dist` 打包）。

### 3.3 Jackett 迁移导入工具

`src/backend/cmd/vestt-import/main.go`：

1. 输入：Jackett 数据目录（含 `ServerConfig.json`、`Indexers/`）。
2. 转换：ServerConfig 字段映射（data-model.md §3）→ `server_config`；每索引器 JSON →
   `indexer_config`（id 匹配内置清单，未匹配项报告跳过）；cookies 文件 → cookies_json。
3. 输出：写入指定 Vestt 数据目录（`--out`），打印报告（导入 n / 跳过 m + 清单）。
4. 单测：样例 Jackett 目录 fixture（手工构造 3 索引器 + 配置）全流程断言。

### 3.4 版本检查（对等上游 UpdateService 的提示部分）

- `POST /server/update`（SPEC-02 已占位）→ 实现：查询 GitHub Releases latest tag
  （网络失败静默记日志）→ 写 `saved_state.update.last_check` → 日志提示新版本。
- UI（SPEC-13 设置页）在 config 响应附带 `latest_version`（可选字段，经 backend-api.md 澄清后加入）。

### 3.5 文档与验收同步

1. 核对 [PRD §9](../../product/prd.md) 产品级验收 1~5 逐条出具证据（测试名/命令/产物路径）。
2. `doc/tech/*` 与实现的偏差全部回写（backend-api.md / data-model.md / frontend.md）。
3. 根目录 README 快速开始与实际命令一致；AGENTS.md 命令可用性走查。
4. roadmap.md 里程碑 M1~M6 全部勾选。

## 4. 接口定义

- 新增（可选）`latest_version` 字段进 `GET /server/config` 响应 —— **先更新 backend-api.md §1.1，再实现**。
- 导入工具为独立 CLI：`vestt-import --src <jackett-dir> --out <vestt-data-dir>`。

## 5. 验收标准与验证方法

1. `make test`（含 lint/coverage 门禁）全绿；CI 全绿（本地模拟 act 或脚本等价执行）。
2. `make release` 产物：6 目标二进制 + 校验和；抽样 linux/amd64 与 windows/amd64 在对应环境
   （或 wine/容器）冒烟通过。
3. `docker build` 成功；`docker run` 冒烟（health/caps）通过；镜像 < 60MB（NFR-02）。
4. systemd：单元文件 `systemd-analyze verify` 通过（本机装 systemd 时）。
5. 导入工具：fixture 迁移报告正确，Vestt 启动后索引器配置在位（测试态索引器列表一致）。
6. 文档走查：三份 tech 文档、README、AGENTS 与实现一致（复核清单勾选）。
7. nginx 形态（SHOULD，本机有 nginx 时）：`install/nginx/vestt.conf` 经 `nginx -t` 校验；
   以形态 B 启动后 Dashboard 可用、`/api` 反代正常、静态请求由 nginx 命中（access log 佐证）。
8. 更新 roadmap 进度表（SPEC-14 → 完成，里程碑 M6 完成）。

## 澄清记录

### §3.3 "cookies 文件" 的上游事实（2026-10-25）

上游 Jackett 0.24.2900 **不存在** `Indexers/<id>.cookies` 独立文件：Cookie jar 以
`HiddenStringConfigurationItem`（name "CookieHeader"）存放在 `Indexers/<id>.json` 的配置
数组内（`Jackett.Common/Models/IndexerConfig/ConfigurationData.cs:17`）。全树 grep 无
`.cookies` 文件引用。据此：
- `data-model.md` §3 对等表已修正（原 "Indexers/<id>.cookies" 行系误设）。
- 导入工具从配置数组抽取 `CookieHeader` 项生成 Vestt 侧 cookie jar，跳过
  `__jackett_expired__` 哨兵对（对等 `CookieUtil` 语义）。

### §3.3 导入工具的掩码/密码语义（2026-10-25）

- 上游 `ServerConfig.json` 的 `AdminPassword` 为明文；Vestt 只存 bcrypt 哈希 → 导入时
  以 bcrypt cost 11 哈希写入，报告注明；`APIKey` 原样保留。
- password 配置项值为 `|||%%PREVJACKPASSWD%%|||`（上游 forDisplay 哨兵）时规范化为
  `""`，对等上游 `FromJson`"哨兵=不覆盖"语义；明文原样保留。
- `ProxyUrl==""` 时强制 `ProxyType=-1`（复刻上游 `OnDeserialized`）。

### §3.1 golangci-lint 与上游原文案的冲突裁决（2026-10-25）

staticcheck ST1005（错误串小写/无标点）与本项目"文案与上游一致"规则冲突：下列字符串是
上游 C# 异常文案的逐字移植，**保留原文 + `//nolint:staticcheck // 上游文案 <cs路径:行>`**
压制，不改写（涉及判定：先在 tmp/ref 全树 grep 核实为上游原文才允许 nolint）：

| 位置 | 上游依据 |
|---|---|
| common/httpclient/proxy.go（Proxy type ... not implemented!） | `Utils/Clients/WebClient.cs:90` |
| common/httpclient/client.go（Remote server returned %d） | `Indexers/BaseIndexer.cs:569`（Vestt 以 %d 呈现状态码，属既有适配） |
| common/services/indexer_manager.go（Unknown indexer: %s） | `Services/IndexerManagerService.cs:328` |
| common/utils/dateutil/dateutil.go（Error while parsing DateTime ...） | `Utils/DateTimeUtil.cs:315` |
| server/api/proxy.go（This indexer is not configured. / Blackhole directory not set!） | `Server/Controllers/BlackholeController.cs:47/:77` |
| common/models/category_mapping.go（ParseCardigannSearchModes 全部校验文案） | `Models/TorznabCapabilities.cs:125/127/133/148/161/163/175/177/189/191/203/205` |

`.golangci.yml`（v2 schema）显式启用 SPEC 要求的 errcheck/govet/staticcheck；v2 默认集
unused/ineffassign 一并生效（比 SPEC 严格，未禁用）。

### RealIP 弃用裁决（2026-10-25）

chi `chimiddleware.RealIP` 已被上游弃用（IP 伪造风险，GHSA-3fxj-6jh8-hvhx 等），且其
"无条件信任 X-Forwarded-For" 语义比上游宽松。上游实际行为（`Jackett.Server/Startup.cs:61-75`）：
仅当连接对端属于环回或私网（10/8、172.16/12、192.168/16、fc00::/7、fe80::/10；v4 链路本地
169.254/16 明确不在列）才处理 XFF，`ForwardLimit=10`，从右往左逐跳校验。据此以
`server/middleware.RealIP`（realip.go）按上游语义实现替换，X-Forwarded-Proto/Host 未复刻
（Vestt 无绝对 URL 生成需求，原 RealIP 亦未处理，行为无回退）。

### §3.2 外部绑定旗标（2026-10-25）

Docker 冒烟暴露运行缺口：绑定地址此前仅由 DB 配置 `allow_external` 决定，全新数据卷的
容器 `-p` 端口映射不可达。上游语义核实：绑定同样由 `ServerConfig.AllowExternal` 驱动
（`Jackett.Server/Program.cs:115` → `GetListenAddresses`），并提供 CLI 覆盖旗标
`-x/--ListenPublic`、`-z/--ListenPrivate`（`Utils/ConsoleOptions.cs:32-35`）——命中时
`AllowExternal = ListenPublic` 且仅当次运行生效、不持久化
（`Jackett.Server/Initialisation.cs:119-136`，日志 "Overriding external access to ..."）。
据此为 vestt 新增 `--listenpublic` / `--listenprivate`（对等上游语义，含冲突时 public 优先
与同款日志文案）；镜像 CMD 追加 `--listenpublic`（容器内绑 0.0.0.0 为容器场景标准做法，
对等 Jackett 官方镜像；对外隔离由 docker 网络负责）。已实测：默认仅绑 127.0.0.1、
`--listenpublic` 绑 `*`、DB `allow_external` 不被写回。

### §3.4 版本检查行为口径（2026-10-25）

`POST /server/update` 同步执行：查 GitHub latest → 成功写 `saved_state` 的
`update.last_check`/`update.latest_version` → 按 semver 关系记日志（新版本 info / 对等上游
`UpdateService.cs:147` 的 downgrade warn / 相等 info）；网络失败/非 200/不可解析仅 warn
静默；**响应恒 204**（含失败路径），检查结果经 `GET server/config` 的可选
`latest_version`（omitempty，重启保留）获知。契约见 backend-api.md §1.1/§1.5。

### §3.1 w-cover 两轮 engine 缺陷声明的裁决与修复（2026-10-25）

覆盖率代理 w-cover 在补测过程中报告 `common/engine/json.go` 两处移植偏差，经与上游
逐行对拍确认**均为真实缺陷**（上游自家测试夹具即为反证），已修复：

1. **JSON rows 过滤链全丢行**：上游 `JsonParseFieldSelector` 以 null/非空区分成败，
   成功时返回的 base selector 可为空串（rows 路径下 `rest` 以 `:` 开头、base 恒为 ""）；
   Go 移植把"失败"与"空 base 成功"都折叠成 `""`，导致 `jsonParseRowsSelector` 的
   `!= ""` 判定在带任何过滤器的 rows 选择器下把所有行丢弃（无过滤时因短路分支而不触发）。
   修复：`jsonParseFieldSelector` 改为返回 `(string, bool)`，`bool` 精确复刻上游 null 语义；
   `handleJsonSelector` 同步改判 `ok`（上游只判 `fieldSelector != null`，
   Newtonsoft `SelectToken("")` 返回根节点，`jsonSelectToken` 的空路径=根行为与其一致）。
   依据：`CardigannIndexer.cs:2491-2508`（rows）、`2510-2580`（field）、`1368-1420`（handle）。
2. **`_JsonSelectorRegex` 嵌套括号截断**：上游正则 `\:(?<filter>.+?)\((?<key>.+?)\)(?=:|\z)`
   带 `(?=:|\z)` 前瞻，key 可跨越内层括号（如 `:has(:contains(x))`、`name:not(:contains(DTS))`）；
   Go 端用 `:([A-Za-z]+)\(([^)]*)\)` 在首个 `)` 截断，嵌套过滤全坏，且 has/not 的递归判定
   用 `strings.Contains(key, ":")` 近似上游 `_JsonSelectorRegex.Match(key).Success`，
   对含冒号但无过滤组的 key（如命名空间键）误判。修复：新增 `jsonNextFilterMatch` 手写
   扫描器精确复刻正则的左起最短路匹配、前瞻闭合规则与回溯（RE2 无前瞻）；递归判定改用
   该扫描器。同时把 has/not 对无内嵌过滤 key 的 `SelectToken` 直查语义归位。
   依据：`CardigannIndexer.cs:73`、`:2510-2580`。

**验证**：上游 `Jackett.Test/Common/Indexers/CardigannIndexerJsonTests.cs` 的
`TestCardigannJsonAsync` 首次移植（夹具 `json-definition1.yml`+`json-response1.json` 入库
`testdata/pages/`，测试 `json_fixture_test.go`）：rows 选择器正是
`data:has(attributes.size):has(attributes.name:contains(1080)):has(attributes.poster:contains(.jpg)):not(attributes.fake_att):not(attributes.uploader:contains(DarkSwan2001))`
嵌套链，断言 78 releases 及首条全字段（含 `[2000,100001]` 双类目、title_dts/notdts 的
`:not(:contains(DTS))` 分支、case 映射、date/size/gain/imdb/tmdb）——修复前该用例 0 结果，
修复后全绿；engine 全量测试保持绿。w-cover 原先按 buggy 现状写的两条特征化断言
（`json_extra_test.go` 嵌套参数截断、带过滤 rows=0）已改为上游行为断言。

## 验收记录（2026-10-25）

### 本 SPEC §5 验收标准逐条证据

1. **质量门禁全绿**：`make test` 通过 = go test（含覆盖率阈值 common≥70 实 80.33 /
   engine≥80 实 82.18 / models+torznab≥85 实 94.98）+ `go vet` + `golangci-lint`
   （全后端 0 issues）+ 前端 typecheck/eslint 0/vitest 95/95。CI（`.github/workflows/ci.yml`）
   各步骤（lint→test→三平台 build→冒烟）本地等价执行均通过；未装 act，以命令等价性代替。
2. **`make release`**：`dist/release/` 下 6 目标二进制（linux amd64/arm64/armv7、windows
   amd64、darwin amd64/arm64）+ `vestt-web-v0.1.0-dev.tar.gz` + `SHA256SUMS`。
   linux/amd64 经 `make smoke`（health/caps/搜索/下载代理）通过；windows/amd64 无 wine 环境，
   **需人工验证**（Windows 测试机）。
3. **Docker**：`docker build` 成功；全新数据卷 `docker run -p` 冒烟 health 200/caps 可达；
   镜像 49MB < 60MB（NFR-02）；non-root vestt 用户；HEALTHCHECK 转 healthy；
   CMD 内嵌 `--listenpublic`，端口映射开箱即达（首次启动无需手改配置，实测）。
4. **systemd**：`systemd-analyze verify` 沙箱法（`--root` 空根）退出 0；本机直跑仅报
   二进制未安装的预期警告（服务文件语法与路径单元合法）。
5. **导入工具**：`cmd/vestt-import` 单测覆盖样例 Jackett 目录全流程（ServerConfig 映射/
   索引器匹配与跳过清单/cookie jar/密码哨兵/掩码占位），见 main_test.go。
6. **文档走查**：backend-api.md（latest_version 契约/错误映射）、data-model.md（cookies
   事实修正）、frontend.md（服务清单补 4 项）、README（make test/smoke/release 说明 +
   "从 Jackett 迁移"章节 + listen 旗标）、AGENTS.md（命令可用性）——本轮全部同步完成。
7. **nginx（SHOULD）**：`install/nginx/vestt.conf` 随 release 交付；本机无 nginx，
   `nginx -t` **平台限制登记（需人工验证）**；形态 B 部署属运维侧组合，静态包已可独立取用。
8. **roadmap**：§4 进度表 SPEC-01~14 全部 ☑；里程碑 M1~M6 退出条件全部满足。

### PRD §9 产品级验收逐条证据

1. **582 YAML 100% 加载**：`engine` 测试 `TestLoadAll` 断言 `Loaded==582 && Skipped==0`，
   实例化测试断言 582；schema 校验走上游 schema.json 同一份。
2. **62 手写索引器**：`TestAllManualIndexersRegistered=62` + identity 对拍 62 组（id/name/
   type/language）+ 各批次 caps/请求/fixture 解析/错误路径单测 + spotcheck 全链路抽检
   12 个（Anilibria/AlphaRatio/DivxTotal/FileList/Orpheus/IPTorrents/RuTracker/SpeedApp/
   TorrentDay 等），详见 SPEC-08/09/10 验收记录。
3. **Sonarr/Prowlarr 全链路**：契约层已由集成测试 17 项 + SPEC-03 golden XML 对拍 +
   torznab 六型错误矩阵覆盖；真实自动化工具添加 Vestt 的端到端联调 = **需人工验证**
   （Windows 测试机， Burp 抓包备证）。
4. **管理 UI 覆盖**：SPEC-11/12/13 验收 + 上游 custom.js 行为映射表逐项核对（SPEC-13 §7
   映射表 9 行勾选）+ vitest 95/95 + 真实浏览器 E2E 场景（登录/配置/测试/批量添加/搜索
   深链/缓存/设置四页签/日志）记录于各 SPEC 澄清记录。
5. **全部 SPEC 验收通过**：roadmap.md §4 进度表 14 份 SPEC 全 ☑（本行为最后收口）。

### 已知限制登记（需人工验证 / 平台限制）

- windows/amd64 产物冒烟：无 wine → Windows 测试机人工验证。
- nginx `nginx -t` + 形态 B 实机部署：本机无 nginx → 平台限制；conf 已随包交付。
- Sonarr/Prowlarr 真实工具联调：需人工验证（契约层自动化已全覆盖）。
- 版本检查 `POST /server/update` 的真实 GitHub 查询路径：网络出口依赖，失败静默语义
  已单测；成功写库路径以 httptest 伪 latest 响应验证。

### §3.4 补充裁决：latest_version 的前端展示（2026-10-25）

上游 custom.js 全文核实：**不消费 `latest_version`**——UI 仅有 "Check for updates" 按钮
（trigger-updater）、`updatedisabled`/`prerelease` 复选与 `app_version` 展示，无
"update available" 横幅。Vestt 设置页（ServerConfigForm）已对等提供 Check-for-updates
按钮与 `updatedisabled` 开关；`latest_version` 仅作 API 契约字段（backend-api.md §1.1，
重启保留）存在。结论：无需前端变更，行为与上游一致。
