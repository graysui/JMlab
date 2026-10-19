# 本地开发与检查

需要 Go（版本见 go.mod）、Node.js 24 和 npm。本地开发使用单独数据目录。

## 启动

在仓库根目录打开 PowerShell：

```powershell
npm --prefix web/app ci
npm --prefix web/app run build
$env:JMLAB_DATA_DIR = Join-Path $env:TEMP ("jmlab-dev-" + [guid]::NewGuid())
$env:JMLAB_MIGRATIONS_DIR = Join-Path (Get-Location) "migrations"
$env:JMLAB_WEB_DIR = Join-Path (Get-Location) "web/app/dist"
$env:JMLAB_MASTER_KEY = [guid]::NewGuid().ToString("N") + [guid]::NewGuid().ToString("N")
$env:JMLAB_SESSION_SECRET = [guid]::NewGuid().ToString("N") + [guid]::NewGuid().ToString("N")
$env:JMLAB_BOOTSTRAP_ADMIN_USERNAME = "admin"
$env:JMLAB_BOOTSTRAP_ADMIN_PASSWORD = "Local-Development-Only-2026!"
go run ./cmd/jmlab
```

打开 http://localhost:8097。首次启动没有模块和媒体库；按[模块指南](module-developer-guide.md)连接自己的服务。需要实时前端开发时，另开终端执行 `npm --prefix web/app run dev`，代理见[Vite 配置](../web/app/vite.config.ts)。

## 检查

```powershell
go test -p 1 ./...
go vet ./...
npm --prefix web/app run build
npm --prefix e2e ci
npm --prefix e2e exec playwright install chromium
node scripts/check-web.mjs
go -C scripts/check-docs run .
python scripts/check-public.py
git diff --check
```

浏览器检查生成临时数据库、随机端口、密钥和 HTTP 测试夹具，不依赖用户模块。Go 联合测试覆盖媒体导入、权限、热加载、模块回调、Range/HLS/字幕、Jellyfin 轨道和续播；协议数据不证明真实客户端解码。

容器检查是单独交付步骤：完整重构完成后构建镜像，再运行 `node scripts/check-container.mjs <镜像>`。本轮不会自动构建或部署。
