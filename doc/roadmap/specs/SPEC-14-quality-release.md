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
