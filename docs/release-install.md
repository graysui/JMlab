# JMlab 部署说明

## 单文件部署

镜像发布于 [Docker Hub](https://hub.docker.com/r/graysui/jmlab)，使用 **`graysui/jmlab:v1.0.1`** 或 `graysui/jmlab:latest`，当前提供 `linux/amd64`（x86_64），暂不提供 ARM 镜像。

从 `v1.0.1` 起，主程序自动管理密钥，镜像提供生产环境默认值与健康检查。全新安装只需要一个 [Compose 文件](../deploy/docker-compose.release.yml)，无需 `.env`：

```sh
mkdir jmlab && cd jmlab
curl -fSL https://raw.githubusercontent.com/graysui/JMlab/main/deploy/docker-compose.release.yml -o compose.yaml
```

只修改其中两个设置：

```yaml
JMLAB_PUBLIC_URL: "http://你的服务器IP:8097"
JMLAB_BOOTSTRAP_ADMIN_PASSWORD: "填写你自己的密码"
```

默认用户名为 `admin`。密码最低 6 位英文、数字或符号；`$` 写成 `$$`，避免被 Compose 当作变量。保持 YAML 引号，双引号和反斜杠按 YAML 规则转义。启动后打开访问地址登录：

```sh
docker compose up -d
```

查看状态、日志和就绪检查：

```sh
docker compose ps
docker compose logs --tail=100 jmlab
curl -fsS http://localhost:8097/health/ready
```

镜像以非 root 用户运行。默认端口为 `8097`；改端口时修改 `ports` 左侧，例如 `"18097:8097"`，再同步修改访问地址。反向代理使用 HTTPS 时，`JMLAB_PUBLIC_URL` 填写用户实际访问的 HTTPS 地址，以便生成正确的媒体地址与 Secure Cookie。

## 首次账号与模块

空数据库使用初始化密码创建管理员；需要自定义用户名时，可在 Compose 的 `environment` 中额外加入 `JMLAB_BOOTSTRAP_ADMIN_USERNAME`。管理员创建成功后，可以移除初始化密码这一行，已保存的账号不受影响。已有用户时修改初始化设置不会重置密码，后续修改通过页面完成。

全新安装必须填写初始化密码。后端按至少 6 字节校验密码，常用英文、数字和符号各占 1 字节。

登录后在**模块管理**中上传本地 ZIP 或填写公网 ZIP 直链安装模块，填写模块配置，再手动创建媒体库。镜像不包含具体模块、内容或预置虚拟库。

已有独立模块服务可展开“连接外部服务（JSON 清单）”接入，该模式的服务仍按维护者说明启动。连接时注意：

- 容器内 `127.0.0.1` 指向容器自身，应填写主程序容器能访问的模块服务地址。
- 带凭证的模块请求使用 HTTPS，或与主程序处于同一网络命名空间的回环地址；普通服务名配合 HTTP 不能传递秘密配置。
- `JMLAB_PUBLIC_URL` 也应能被客户端和模块访问。
- 页面导入的清单保存在 `/data/modules`；也可向其中写入 `模块ID/manifest.json` 等待扫描。

本地 ZIP 模式由 JMlab 安装并启停程序，不需要填写服务地址。平台以主程序容器为准，Alpine 需要兼容 musl 或静态程序。ZIP 安装需包含此改动的新版主程序，旧发布镜像不因文档更新自动支持。页面配置备份不包含模块程序和 `.data` 文件，迁移时另外保留 `/data/modules`，恢复后缺少程序可重新导入同 ID 的 ZIP。

接口、打包约定、热加载与管理方式见[模块指南](module-developer-guide.md)。

## 数据、密钥与备份

| 卷 | 内容 |
| --- | --- |
| `jmlab-data` → `/data` | SQLite 数据库、模块清单及本地程序/数据、私有配置、图片缓存及自动生成的密钥，必须保留。 |
| `jmlab-backup` → `/backup` | 页面配置本地备份目标后保存的备份归档，使用时需保留。 |

数据库默认是 `/data/jmlab.db`。主密钥与兼容会话配置分别保存在 `/data/keys/master.key`、`/data/keys/session.secret`，以受限权限写入，重启或重建容器会复用；无需手工生成或填写。

`JMLAB_MASTER_KEY` 用于加密模块凭证、私有存储与备份，也参与请求及媒体签名。`JMLAB_SESSION_SECRET` 当前保留兼容配置，尚未用于会话签名。默认 SQLite 不需要 `JMLAB_POSTGRES_PASSWORD`。

日常备份可在页面创建并下载，同时妥善保留数据目录中的密钥。加密备份恢复需要原主密钥，可单独保存密钥副本：

```sh
docker compose cp jmlab:/data/keys ./jmlab-keys-backup
```

将密钥副本保存在私密位置。迁移服务器时同时迁移数据库、模块资料及密钥；运行中的数据库使用页面备份或一致的数据库备份方式，避免直接复制正在写入的数据库文件。已有数据库缺原主密钥、密钥文件损坏或显式配置与文件冲突时，程序会停止并提示，不会重新生成替代密钥。

## 从旧版 .env 部署迁移

`v1.0.0` 不支持原生自动密钥，新 Compose 需要使用 `v1.0.1` 或之后的配套镜像。升级前备份数据、原 Compose 和 `.env`，继续使用原部署目录、Compose 项目名与数据卷。

1. 替换 Compose，保留原端口、访问地址和数据库路径。已有账号时可以删去初始化密码这一行。
2. 暂时保留原 `.env`，在新 Compose 的 `environment` 下追加以下两项，将原密钥传入程序：

```yaml
      JMLAB_MASTER_KEY: ${JMLAB_MASTER_KEY:?请保留原主密钥}
      JMLAB_SESSION_SECRET: ${JMLAB_SESSION_SECRET:-}
```

3. 执行 `docker compose up -d`。程序会将原密钥保存到数据目录；确认原账号可登录、模块配置可用后，即可删除追加的两行与旧 `.env`。

也可直接填写原密钥值，含 `$` 时写成 `$$`。不能用新随机值替代原主密钥。原数据库使用自定义路径时，在 `environment` 中保留对应 `JMLAB_DATABASE_URL`；数据库不会自动改名或搬移。

## 更新与停止

先在页面下载备份并保存密钥，记录当前镜像版本。修改 Compose 的 `image` 后，在原部署目录执行：

```sh
docker compose pull
docker compose up -d
docker compose ps
```

临时停止用 `docker compose stop`，再次启动用 `docker compose up -d`。**不要执行 `docker compose down -v`，它会删除持久化卷。** 更换目录可能改变默认项目名，已有部署继续使用原目录，或用 `-p` 指定原项目名。

升级会执行数据库迁移。涉及结构变更时，回退旧镜像可能需要恢复升级前的备份；保留同一数据卷和原密钥。

## 高级配置与 PostgreSQL

日常部署使用前面的两个设置即可。需要调整时，可额外配置 `JMLAB_HTTP_ADDR`、`JMLAB_DATA_DIR`、`JMLAB_MODULES_DIR`、`JMLAB_DATABASE_URL` 等；更改数据目录时需同步迁移数据与 `keys`。

主程序支持 PostgreSQL，[PostgreSQL Compose 示例](../deploy/docker-compose.postgres.yml)供有外部数据库需求的用户参考。使用公开镜像时移除 `build`，将 `image` 改为目标发布版本。`JMLAB_POSTGRES_PASSWORD` 是数据库连接密码，与 JMlab 登录密码不同；外部数据库首次配置还需显式提供主密钥，之后由数据目录持久保存，避免无法判断数据库新旧时误生成密钥。

切换数据库需要单独迁移和验证，修改连接串不会自动搬移 SQLite 数据。实际模块与客户端在目标环境单独验收，见[Jellyfin 兼容说明](jellyfin-compatibility.md)。

## 维护者：通过 GitHub Actions 发布镜像

[JMlab Docker 发布工作流](https://github.com/graysui/JMlab/actions/workflows/docker-publish.yml)在 GitHub 上完成检查、构建与 Docker Hub 推送，当前平台为 `linux/amd64`。

- 推送新的 `v主版本.次版本.修订号` Git 标签，自动发布同名镜像并更新 `latest`。带 `-rc.1` 等后缀的预发布版本仅发布对应标签。
- 手动发布：进入 **Actions → JMlab Docker 发布 → Run workflow**，选择 `main`，填写 `image_tag`。默认 `edge` 用于验证新代码；正式版本可勾选 `update_latest`。
- 已发布的版本号不能覆盖；需要更新内容时使用新的版本号。`edge` 可以重复更新，且不会覆盖 `latest`。每次同时发布 `sha-提交前12位` 标签用于追溯。
- 仓库 **Settings → Secrets and variables → Actions** 中设置变量 `DOCKERHUB_USERNAME` 和加密 Secret `DOCKERHUB_TOKEN`，令牌需要目标镜像仓库的推送权限。令牌只供发布步骤使用。

工作流先运行 Go、前端、文档和浏览器检查，再构建镜像，用临时数据卷验证空安装、登录、模块与观看流程、自动密钥和重启持久化。通过后推送同一份镜像，核对远端内容，并在 Actions 摘要中记录镜像标签、摘要和源码提交。已有服务器需按上面的更新步骤拉取镜像。
