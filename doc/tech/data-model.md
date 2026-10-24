# Vestt 数据模型（SQLite）

| 项 | 内容 |
|---|---|
| 文档版本 | 1.0 |
| 存储 | SQLite 单文件（默认 `<data>/vestt.db`），`modernc.org/sqlite`，WAL 模式 |
| 上游基线 | Jackett 的 `ServerConfig.json` + `Indexers/*.json` + 进程内缓存 |
| 迁移机制 | `schema_migrations` 顺序执行（见 §4） |

---

## 1. 库级约定

- PRAGMA：`journal_mode=WAL`、`synchronous=NORMAL`、`foreign_keys=ON`、`busy_timeout=5000`。
- 时间：一律 `TEXT` ISO8601 UTC（`YYYY-MM-DDTHH:MM:SSZ`）。
- 主键：配置类表用自然键（如 indexer_id）；缓存类表用自增 rowid + 唯一索引。
- JSON 列：命名后缀 `_json`，应用层序列化/反序列化（不使用 SQLite JSON1 之外的能力）。

## 2. 表设计

### 2.1 `schema_migrations`

```sql
CREATE TABLE schema_migrations (
    version     INTEGER PRIMARY KEY,   -- 递增编号
    name        TEXT NOT NULL,
    applied_at  TEXT NOT NULL
);
```

### 2.2 `server_config`（键值 + 单行 JSON，二选一实现按 SPEC-02）

采用"单行 JSON"形态（字段全集与 `ServerConfig` 对象一致，避免列漂移）：

```sql
CREATE TABLE server_config (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    config_json  TEXT NOT NULL,        -- models.ServerConfig 序列化
    updated_at   TEXT NOT NULL
);
```

`config_json` 字段（对等上游 ServerConfig.cs，缺省值见上游构造函数）：

| 字段 | 类型 | 默认 |
|---|---|---|
| port | int | 9117 |
| local_bind_address | text | "127.0.0.1" |
| allow_external | bool | false |
| allow_cors | bool | false |
| api_key | text | 启动时随机生成 |
| admin_password_hash | text/null | null（未设置） |
| instance_id | text | 首启随机 |
| blackhole_dir | text | "" |
| update_disabled / update_prerelease | bool | false |
| base_path_override / base_url_override | text | "" |
| cache_enabled | bool | true |
| cache_ttl | int | 2100 |
| cache_max_results_per_indexer | int | 1000 |
| flaresolverr_url | text | "" |
| flaresolverr_max_timeout | int | 60000 |
| omdb_api_key / omdb_api_url | text | "" |
| proxy_type | int | -1（disabled，上游枚举 `ProxyType.Disabled = -1`） |
| proxy_url / proxy_username / proxy_password | text | "" |
| proxy_port | int/null | null |

> 注意：管理密码只存 `admin_password_hash`（bcrypt）。API Key/代理凭据等明文字段依赖
> 数据目录文件系统权限（信任边界 = SQLite 文件），见 backend-api.md 差异清单。

### 2.3 `sessions`（管理会话）

```sql
CREATE TABLE sessions (
    token       TEXT PRIMARY KEY,      -- 32B 随机 hex
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL          -- created + 14d
);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);
```

过期会话由后台任务或登录时惰性清理。

### 2.4 `indexer_config`（每索引器配置）

```sql
CREATE TABLE indexer_config (
    indexer_id   TEXT PRIMARY KEY,     -- 全局唯一（yml id 或手写索引器 id）
    enabled      INTEGER NOT NULL DEFAULT 1,
    config_json  TEXT NOT NULL,        -- 该索引器 ConfigurationData 序列化（含字段值）
    cookies_json TEXT,                 -- 会话 Cookie jar（name/domain/value/path/expires）
    last_error   TEXT,
    state        TEXT NOT NULL DEFAULT 'unconfigured',  -- unconfigured | healthy | failing
    tested_at    TEXT,
    updated_at   TEXT NOT NULL
);
```

- `config_json` 内容 = 上游每索引器 JSON 配置等价物（如 `username/password/api_key/categories/...`
  + 手写索引器的基类字段 `flaresolverr`、`alternativeSiteLinks`、`idlerateLimit` 等）。
- 删除索引器配置 = DELETE 本行 + 清理 cache_rows（应用层同步执行）。

### 2.5 `cache_results`（结果缓存）

对等上游 `CacheService`（内存 + TTL）；SQLite 落盘用于重启保活与 `Cache` 视图：

```sql
CREATE TABLE cache_results (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    query_key    TEXT NOT NULL,        -- sha1(indexerId + normalized query)
    indexer_id   TEXT NOT NULL,
    release_json TEXT NOT NULL,        -- TrackerCacheResult（含代理化链接生成所需的原始字段）
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_cache_query ON cache_results(query_key);
CREATE INDEX idx_cache_created ON cache_results(created_at);
```

- 写入：查询成功后批量插入（单事务），受 `cache_max_results_per_indexer` 截断。
- 读取：`SELECT ... WHERE query_key = ? AND created_at > now - ttl ORDER BY PublishDate DESC`。
- 清理：后台每 5 分钟删除过期行；索引器配置变更/删除时按 indexer_id 全删。
- `release_json` 存**原始** ReleaseInfo；代理化链接（/dl /img /bh）在读取输出时按请求 host 动态生成。

### 2.6 `log_cache`（日志环形缓冲落盘，可选）

环形缓冲默认在内存（slog handler）；`log_cache` 仅在启用"日志持久化"时写入：

```sql
CREATE TABLE log_cache (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    level     TEXT NOT NULL,
    message   TEXT NOT NULL,
    when_ts   TEXT NOT NULL
);
CREATE INDEX idx_log_when ON log_cache(when_ts DESC);
```

容量窗口由清理任务维护（默认保留最近 1000 条，与内存一致）。

### 2.7 `saved_state`（杂项键值）

```sql
CREATE TABLE saved_state (
    key        TEXT PRIMARY KEY,       -- 如 "update.last_check"、"ui.notices_dismissed"
    value_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
```

## 3. 与上游文件格式的对等/转换

| 上游 | Vestt | 说明 |
|---|---|---|
| `ServerConfig.json` | `server_config.config_json` | 字段一一映射（camelCase → snake_case 仅内部命名；对外 DTO 不变） |
| `Indexers/<id>.json` | `indexer_config.config_json` | 键名保持上游原样（如 `username`、`apiKey`、`cookieheader`），便于导入工具 |
| `Indexers/<id>.cookies` | `indexer_config.cookies_json` | Cookie jar 序列化 |
| 进程内 CacheService | `cache_results` | 查询语义一致；内存索引加速（map[queryKey][]rowid） |

导入工具（SHOULD，SPEC-14）：读取 Jackett 数据目录 → 逐表转换 → 报告跳过项。

## 4. 迁移流程

1. 启动时打开库 → 读取 `schema_migrations` 最大 version。
2. 顺序执行内嵌迁移（每个迁移一个函数，显式 SQL，无第三方迁移框架）。
3. 迁移在单事务内完成并登记版本；失败即启动失败并打印 SQL 错误。
4. 迁移文件位置：`src/backend/common/store/migrations.go`（表结构变更必须新增迁移，禁止修改旧迁移）。

## 5. 访问层（src/backend/common/store）

- `Store` 结构体持有 `*sql.DB`；DAO 方法按域拆分文件：`config.go / indexers.go / cache.go / logs.go / sessions.go`。
- 写并发：`db.SetMaxOpenConns(1)`（modernc/sqlite 单写者模型），读多路复用同一连接足够本项目规模。
- 所有 DAO 方法必须 `context.Context` 感知，取消不残留半写状态。
- 单测：内存 SQLite（`:memory:`）跑全部 DAO 与迁移。

## 6. 容量与清理基线

| 数据 | 上限 | 清理方式 |
|---|---|---|
| cache_results | 每索引器 1000 行（可配）+ TTL 2100s（可配） | 周期任务 + 写入截断 |
| log_cache | 1000 行 | 周期任务 |
| sessions | 自然过期 | 登录时惰性删除 |
| indexer_config | 索引器数（~645） | 删除接口 |
