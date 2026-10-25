# Vestt — Jackett 0.24.2900 功能对等复刻（Go + Vue 3，单二进制内嵌前端）。
#
# 多阶段构建：
#   1. web     node:24-alpine + corepack(pnpm) 构建前端 → dist/
#   2. backend golang:1.26-alpine 构建后端（embed 前端产物 + 582 个 YAML 定义）
#   3. runtime alpine + ca-certificates + tzdata，非 root 用户运行
#
# 构建：docker build -t vestt .
#   （受限网络下可经宿主代理拉取 Go 模块，代理参数不会写入镜像：
#     docker build --network host --build-arg HTTPS_PROXY=http://127.0.0.1:7890 -t vestt .）
# 运行：docker run -d -p 9117:9117 -v vestt-data:/data vestt
# 健康检查：HEALTHCHECK 命中 GET /health（匿名端点，返回 version/uptime）。
#
# 版本注入与 Makefile 一致：-X main.vesttVersion=<ver>（默认 0.1.0-dev）。

# ---------------------------------------------------------------- web 前端 --
FROM node:24-alpine AS web

ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
WORKDIR /build

# 先拷贝清单文件以利用层缓存；pnpm 版本与仓库本地工具链一致（10.32.1）。
# --ignore-scripts：pnpm 10+ 默认拦截依赖安装脚本（本仓库依赖中仅 esbuild 带
# postinstall，其平台二进制由 optionalDependencies 直接提供，无需脚本）。
COPY src/frontend/package.json src/frontend/pnpm-lock.yaml ./
RUN corepack enable pnpm \
    && corepack prepare pnpm@10.32.1 --activate \
    && pnpm install --frozen-lockfile --ignore-scripts

# 源码与配置（不含 node_modules；vitest.config.ts 是 tsconfig.node.json 的 -b 输入）
COPY src/frontend/index.html src/frontend/vite.config.ts src/frontend/vitest.config.ts \
     src/frontend/tsconfig.json src/frontend/tsconfig.app.json src/frontend/tsconfig.node.json ./
COPY src/frontend/public ./public
COPY src/frontend/src ./src

RUN pnpm build
# 产物位于 /build/dist

# ---------------------------------------------------------------- 后端 Go --
FROM golang:1.26-alpine AS backend

ARG VESTT_VERSION=0.1.0-dev
ENV GOTOOLCHAIN=auto
WORKDIR /src

RUN apk add --no-cache git

COPY src/backend/go.mod src/backend/go.sum ./
RUN go mod download

COPY src/backend/ ./
# 前端产物复制到 embed 目录（server/static/embed.go: //go:embed all:dist）
COPY --from=web /build/dist ./server/static/dist

RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags "-s -w -X main.vesttVersion=${VESTT_VERSION}" \
    -o /out/vestt ./cmd/vestt

# ---------------------------------------------------------------- 运行时 ----
FROM alpine:3.22 AS runtime

ARG VESTT_VERSION=0.1.0-dev
LABEL org.opencontainers.image.title="Vestt" \
      org.opencontainers.image.description="Jackett 0.24.2900 feature-parity indexer proxy (Go + Vue 3)" \
      org.opencontainers.image.version="${VESTT_VERSION}" \
      org.opencontainers.image.licenses="GPL-2.0" \
      org.opencontainers.image.source="https://github.com/GitHubNull/Vestt"

# wget（busybox 自带）供 HEALTHCHECK 使用；ca-certificates 供出站 HTTPS；
# tzdata 供容器内本地时间解析
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S vestt \
    && adduser -S -H -G vestt vestt \
    && mkdir -p /data \
    && chown vestt:vestt /data

COPY --from=backend /out/vestt /usr/local/bin/vestt

USER vestt
EXPOSE 9117
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -q --spider http://127.0.0.1:9117/health || exit 1

ENTRYPOINT ["/usr/local/bin/vestt"]
# --port/--listenpublic 为当次运行覆盖旗标（flag > 数据库配置 > 默认值）；--data-dir 存
# vestt.db 与日志。容器内绑定 0.0.0.0 是容器场景的标准做法（对外隔离由 docker 网络负责，
# 对等上游 Jackett 官方镜像行为）；宿主侧默认端口映射已隔离，无需暴露时移除 --listenpublic。
CMD ["--data-dir", "/data", "--port", "9117", "--listenpublic"]
