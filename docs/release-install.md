# JMlab 部署说明

## 构建主程序镜像

完整重构完成并通过本地检查后执行：

```sh
docker build -t jmlab:local .
```

镜像只包含主程序、Web 静态文件和迁移，不包含具体模块。模块按其维护者说明独立运行；容器中的 127.0.0.1 指向容器本身，模块地址应使用实际可达地址。带凭证的模块 RPC 必须使用 HTTPS 或同一网络命名空间的回环地址。

## SQLite

复制[环境示例](../.env.example)，生成并填写主密钥、会话密钥和初始管理员密码，按[SQLite Compose](../deploy/docker-compose.sqlite.yml)运行：

```sh
docker compose --env-file .env -f deploy/docker-compose.sqlite.yml up -d --build
```

已有镜像可通过[运行 Compose](../deploy/docker-compose.release.yml)启动，显式设置 JMLAB_IMAGE 为已构建或已发布的镜像。本仓库不假定存在公开镜像。

/data 持久卷保存数据库、缓存、备份及安装的模块清单。默认 JMLAB_MODULES_DIR=/data/modules，页面导入的清单保存在该目录。也可将单独的可写目录挂载到这里，放入“模块ID/manifest.json”后等待扫描。程序不会从清单启动外部服务。

## 配置

| 环境变量 | 用途 |
| --- | --- |
| JMLAB_HTTP_ADDR | 监听地址，默认 :8097 |
| JMLAB_PUBLIC_URL | 客户端及模块回调可达的宿主地址 |
| JMLAB_DATA_DIR | 数据目录，默认 /data |
| JMLAB_DATABASE_URL | SQLite 或 PostgreSQL 连接串 |
| JMLAB_MODULES_DIR | 可写清单目录，默认位于数据目录 |
| JMLAB_MASTER_KEY | 至少 32 字节；保护持久化秘密和加密备份 |
| JMLAB_SESSION_SECRET | 至少 32 字节 |
| JMLAB_BOOTSTRAP_ADMIN_USERNAME / JMLAB_BOOTSTRAP_ADMIN_PASSWORD | 仅无用户时创建管理员，需同时提供 |
| JMLAB_MIGRATIONS_DIR / JMLAB_WEB_DIR | 迁移和前端目录，镜像默认已配置 |

主密钥应妥善保管，遗失后无法解密已有配置。首次登录后导入清单，再手动创建虚拟媒体库。重启不会重新创建已删除的库。

## PostgreSQL 与升级

[PostgreSQL Compose](../deploy/docker-compose.postgres.yml)提供另一种部署布局。切换数据库需要单独迁移和验证，不是修改连接串即可搬移数据。

升级前备份数据和配置，记录镜像摘要。复用数据库时显式设置原数据库路径并保留原主密钥，检查迁移结果。新品牌默认使用 jmlab.db；历史数据库不会自动改名。重新登录 Web，按新模块协议头更新外部服务。镜像不再提供此前的具体扩展，需单独部署并重新接入。

实际环境的数据库、外部服务与客户端验收按[发布检查](release-checklist.md)执行。
