# Vestt

Vestt 是 [Jackett](https://github.com/Jackett/Jackett) 0.24.2900 的功能对等复刻：
一个自托管的索引器代理服务器，把数百个种子站的私有搜索接口统一封装为标准
**Torznab / TorrentPotato** API，供 Sonarr、Radarr、Prowlarr 等自动化工具直接对接，
并附带一个基于 Web 的管理控制台。

> 复刻基线：Jackett 0.24.2900（GPL-2.0）。Vestt 以 Golang + Vue 3 重新实现，
> 并复用上游的 582 个 YAML 索引器定义。本项目与 Jackett 官方无隶属关系。

## 功能特性

- **645+ 内置索引器**：582 个 YAML 定义驱动（Cardigann 引擎）+ 62 个手写实现 + 聚合元索引器（all/aggregate）
- **Torznab 协议**：`t=caps|search|tvsearch|movie|music|book`，含全部 ID 参数（imdb/tmdb/tvdb/tvmaze）
- **TorrentPotato** 协议：CouchPotato 兼容
- **聚合搜索**：按 tracker/类别过滤的并发扇出，单站失败不影响整体
- **结果缓存**：TTL + 每索引器容量可配置（SQLite 落盘）
- **代理下载/图片代理/Blackhole**：带站点会话 Cookie 的 `/dl` `/img` `/bh` 路由
- **FlareSolverr 集成**：Cloudflare 盾自动绕过
- **管理控制台**（Vue 3 + PrimeVue 4）：索引器筛选/配置（动态表单/验证码）/测试/标签、
  手动搜索、缓存视图、服务器设置、日志查看
- **安全**：API Key 鉴权、可选管理密码（bcrypt + Cookie 会话）、CORS/BasePath 支持
- **单二进制交付**：前端资源与索引器定义全部内嵌

## 快速开始

### 从源码构建

依赖：Go ≥ 1.23、Node ≥ 20、pnpm ≥ 9。

```bash
make build        # 构建前端 + 产出 dist/vestt 单二进制
./dist/vestt      # 默认 http://127.0.0.1:9117
```

### 运行

```bash
./dist/vestt --port 9117 --data-dir /var/lib/vestt
# 打开 http://127.0.0.1:9117/UI/Dashboard
```

常用参数：`--port`、`--data-dir`、`--basepathoverride`、`--loglevel`（info|debug）。

参数优先级：`--port` / `--basepathoverride` / `--loglevel` 为**当次运行覆盖** ——
优先级 `flag > 数据库配置 > 默认值`，flag 的变更**不会写回**数据库，重启后仍按数据库配置生效；
`--data-dir` 决定 SQLite 库与日志目录位置（默认：平台用户配置目录下的 `vestt/`）。

### Docker

```bash
docker build -t vestt .
docker run -d --name vestt -p 9117:9117 -v vestt-data:/data vestt
```

### 分离部署（nginx，可选）

前端静态资源也可由 nginx 直接托管（静态流量卸载，后端专注 API）：

```nginx
server {
    listen 80;
    root /var/www/vestt;              # src/frontend/dist 构建产物
    location / { try_files $uri @backend; }
    location @backend {
        proxy_pass http://127.0.0.1:9117;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

未命中静态文件的请求（`/UI`、`/api`、`/dl` 等）全部反代后端，行为与单二进制模式一致；
完整配置随 SPEC-14 交付于 `install/nginx/vestt.conf`（见 architecture.md §7.1）。

### 对接自动化工具

在 Sonarr/Prowlarr 中添加自定义 Torznab 索引器：

- URL：`http://<host>:9117/api/v2.0/indexers/<indexerId>/results/torznab/`
- API Key：仪表盘顶栏获取

## 开发

```bash
make run          # 构建并启动
make test         # Go 测试 + vet
pnpm --dir src/frontend dev   # 前端 HMR（API 代理到 9117）
```

### 项目结构

全部源码收于 `src/` 下，前后端分离开发（各自独立工程，仅经 API 契约交互）：

```
src/
├── backend/    # Go 后端（module "vestt"）：cmd/ · common/ · server/ · test/
└── frontend/   # Vue 3 前端（pnpm 工程）：构建产物同步至 backend 由 go:embed 打包
```

详细布局与分层规则见 [doc/tech/architecture.md §2](doc/tech/architecture.md)。

文档：[doc/README.md](doc/README.md)（PRD、架构、API 契约、路线图与 SPEC）。

## 与 Jackett 的差异

| 项 | Jackett | Vestt |
|---|---|---|
| 后端 | C# / ASP.NET Core | Go |
| 前端 | jQuery + Handlebars | Vue 3 + PrimeVue 4 |
| 存储 | JSON 文件 | SQLite |
| 自更新 | 下载替换二进制 | 仅版本检查提示 |

API 路径与报文保持兼容，详见 [doc/tech/backend-api.md](doc/tech/backend-api.md)。

## 法律声明

本项目按 GPL-2.0 发行（见 [LICENSE](LICENSE)），使用风险与合规边界见
[DISCLAIMER.md](DISCLAIMER.md)。Vestt 不托管任何内容，仅为协议转换中间件；
使用前请确认符合当地法律法规与目标站点条款。
