# JMlab 部署说明

## 使用公开 Docker 镜像

镜像发布于 [Docker Hub](https://hub.docker.com/r/graysui/jmlab)，地址为 **`graysui/jmlab`**，本次提供 `linux/amd64`（x86_64）镜像，暂不发布 ARM 镜像。推荐固定版本 `v1.0.0`；`latest` 跟随最新正式版。完整的首次安装步骤见 [README 的 Docker 部署](../README.md#docker-部署)。公开镜像可以直接拉取，无需登录镜像仓库。

下载[运行 Compose](../deploy/docker-compose.release.yml)为 `compose.yaml`，下载[发布环境示例](../deploy/release.env.example)为 `.env`，替换密钥、访问地址和管理员密码后执行：

```sh
docker compose pull
docker compose up -d
docker compose ps
```

查看运行日志和就绪状态：

```sh
docker compose logs --tail=100 jmlab
curl -fsS http://localhost:8097/health/ready
```

镜像以非 root 用户运行，Compose 默认使用只读根文件系统、独立数据卷与资源限制。默认端口为 `8097`；修改 `JMLAB_HOST_PORT` 后，需要同步修改 `JMLAB_PUBLIC_URL`。

会话 Cookie 按 `JMLAB_PUBLIC_URL` 的协议设置：HTTPS 地址使用 Secure Cookie；明确配置为 HTTP 时允许 HTTP 会话。反向代理终止 HTTPS 时，也应将该变量填写为用户实际访问的 HTTPS 地址。

## 首次登录

空数据库会使用 `JMLAB_BOOTSTRAP_ADMIN_USERNAME` 和 `JMLAB_BOOTSTRAP_ADMIN_PASSWORD` 创建管理员。登录后在**模块管理**中导入清单，填写模块连接与配置，再手动创建虚拟媒体库。全新安装不包含预置模块和虚拟库。

管理员创建成功后，可以从 `.env` 删除两项初始化账号配置，再执行 `docker compose up -d`。这不会删除已创建的管理员，也不会修改其密码；后续密码修改通过页面完成。

## 模块部署与网络

镜像只包含主程序、Web 静态文件和数据库迁移。具体模块按照维护者说明独立运行，JMlab 导入清单后不会自动启动模块进程。

- 容器内的 `127.0.0.1` 指向容器自身。模块独立部署时，应填写主程序容器能够访问的实际服务地址。
- 带凭证的模块 HTTP / JSON 请求必须使用 **HTTPS**，或与主程序处于同一网络命名空间的回环地址。普通容器服务名配合 HTTP 不能用于传递秘密配置。
- `JMLAB_PUBLIC_URL` 应为客户端和模块能够访问的主程序地址。使用域名时，先配置反向代理与 HTTPS。
- 页面导入的清单保存在 `/data/modules`。也可向该目录写入 `模块ID/manifest.json`，等待自动扫描；模块配置按身份独立保存。

模块接口、热加载和管理方式见[模块指南](module-developer-guide.md)。

## 数据与配置

默认使用 SQLite，数据库路径为 `/data/jmlab.db`。`/data` 卷同时保存模块清单、缓存和备份，是必须保留的持久化卷。Compose 另外声明 `/config`、`/cache`、`/backup` 的可写卷；当前默认缓存和备份路径仍在 `/data` 内。

| 环境变量 | 用途 |
| --- | --- |
| `JMLAB_IMAGE` | Compose 拉取的镜像，推荐固定版本或镜像摘要。 |
| `JMLAB_HOST_PORT` | 主机访问端口，默认 `8097`。 |
| `JMLAB_PUBLIC_URL` | 客户端及模块回调可达的宿主地址。 |
| `JMLAB_MASTER_KEY` | 至少 32 字节；保护持久化秘密与加密备份，升级时必须保留。 |
| `JMLAB_SESSION_SECRET` | 至少 32 字节；使用与主密钥不同的随机值。 |
| `JMLAB_BOOTSTRAP_ADMIN_USERNAME` / `JMLAB_BOOTSTRAP_ADMIN_PASSWORD` | 仅无用户时创建管理员，需同时提供。 |
| `JMLAB_HTTP_ADDR` | 容器监听地址，Compose 配置为 `0.0.0.0:8097`。 |
| `JMLAB_DATA_DIR` | 数据目录，Compose 配置为 `/data`。 |
| `JMLAB_DATABASE_URL` | SQLite 或 PostgreSQL 连接串。 |
| `JMLAB_MODULES_DIR` | 可写清单目录，默认 `/data/modules`。 |
| `JMLAB_MIGRATIONS_DIR` / `JMLAB_WEB_DIR` | 迁移和前端目录，镜像默认已配置。 |

丢失主密钥会导致已有秘密配置和加密备份无法解密。备份时同时妥善保存 `.env` 与数据；不要在已有部署中重新生成主密钥。

## 更新与停止

1. 在页面创建并下载备份，同时保存当前 `.env`，记录正在使用的镜像版本或摘要。
2. 在 `.env` 中将 `JMLAB_IMAGE` 改为目标版本，保留原密钥、数据卷与 Compose 项目名。
3. 拉取并更新容器，查看状态和日志：

```sh
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=100 jmlab
```

临时停止使用 `docker compose stop`，再次启动使用 `docker compose up -d`。**不要执行 `docker compose down -v`，该命令会删除持久化数据卷。** 更换部署目录可能改变默认项目名；已有部署应继续使用原目录，或通过 `-p` 明确指定原项目名。

升级时程序会运行数据库迁移。涉及数据库结构变更时，回退旧镜像可能需要恢复升级前的数据备份。历史数据库不会自动改名；复用已有数据库应明确保留原路径与原主密钥。

## PostgreSQL

主程序同时支持 PostgreSQL。[PostgreSQL Compose 示例](../deploy/docker-compose.postgres.yml)用于自建数据库的部署布局；使用公开镜像时，移除其中的 `build` 配置，并将 `image` 改为固定发布版本。数据库密码、持久化与备份由部署者维护。

切换数据库需要单独迁移和验证，修改连接串不会自动搬移 SQLite 数据。实际模块与客户端需在目标环境单独验收，客户端能力与播放边界见[Jellyfin 兼容说明](jellyfin-compatibility.md)。
