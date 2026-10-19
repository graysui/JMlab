# JMlab

JMlab 是通过模块扩展的虚拟媒体管理服务。主程序负责媒体目录、用户权限、个人清单、观看记录、模块管理和 Jellyfin 兼容接口，可用于电影、剧集和个人视频。

## 发布范围

本仓库只提供主程序、通用接口及模块使用与开发说明。具体模块由用户自行准备和运行，不随源码或 Docker 镜像分发。全新安装没有模块，也不会自动创建虚拟媒体库。

## 开始使用

1. 按[本地开发说明](docs/development.md)启动，或按[部署说明](docs/release-install.md)构建主程序镜像。
2. 登录后进入“模块管理 → 添加模块”，导入模块提供者的 manifest.json，填写连接地址和凭证。
3. 在“设置 → 添加内容”选择信息模块，搜索并添加媒体。
4. 在“设置 → 媒体库配置”选择模块及模板，手动创建虚拟媒体库。模板可提供榜单范围。
5. 为视频模块选择适用媒体库；用户从 Web 或兼容客户端访问有权限的媒体。

安装、配置、启停和更换 HTTP 模块连接即时生效。独立服务的运行和程序升级由用户维护，主程序不会自动拉起模块进程。

## 文档

- [模块使用与开发指南](docs/module-developer-guide.md)
- [主程序架构](docs/technical-spec.md)
- [本地开发与检查](docs/development.md)
- [部署说明](docs/release-install.md)
- [发布检查](docs/release-checklist.md)
- [Jellyfin 兼容范围](docs/jellyfin-compatibility.md)
- [API 参考](docs/openapi.yaml)

接口实现和受控测试不代表所有外部服务、数据库或客户端版本都已验收。实际能力与限制见对应文档。
