# 视频播放与宿主协作

视频模块阅读播放部分；仅需存储、调用或事件的信息模块可以直接跳到对应部分。清单及表单基础见[清单与信息模块](manifest-and-metadata.md)。

## 三个视频接口

| 模块路径 | 请求 | 成功响应 |
| --- | --- | --- |
| `POST /api/v1/stream/discover` | `{itemId,title,itemType,providerIds,extra}` | `{candidates:[...]}` |
| `POST /api/v1/stream/resolve` | `{candidateId,encryptedPayload,clientHeaders}` | `ResolvedStream` 对象本身 |
| `POST /api/v1/stream/probe` | `{candidateId,encryptedPayload}` | `{healthy,statusCode,durationMs,error}` |

`itemId` 是宿主媒体 ID，不能当作上游内容 ID。优先从 `providerIds` 或明确的关联数据匹配上游；标题搜索只能作为有置信度的补充，避免把同名影片或预告片误当正片。

发现只列有限候选，不下载媒体；无内容返回空列表。单模块每轮最多接收 100 候选，ID 去重。每个候选保留自己的画质、语言、协议、预览标记和媒体属性：

```json
{
  "candidates": [{
    "candidateId": "movie-550-original", "label": "原画 · 中文",
    "protocol": "http", "quality": "1080p", "language": "zh",
    "isPreview": false, "encryptedPayload": "opaque-module-reference"
  }]
}
```

`candidateId` 在模块范围内稳定；短期令牌放解析结果，不拼进会过期的候选 ID。`encryptedPayload` 可为模块不透明引用，宿主负责加密持久化。不要将需要保密的数据放 label、quality、language 或媒体标题。

解析时核对候选所属内容，并获取新鲜的媒体 URL：

```json
{
  "streamUrl": "https://media.example.test/movie.mp4",
  "protocol": "http", "requiredHeaders": {},
  "expiresAt": "2099-01-01T00:00:00Z",
  "directPlay": false, "requiresProxy": true,
  "media": {
    "container": "mp4", "durationSeconds": 7200, "sizeBytes": 17179869184,
    "tracks": [
      {"index":0,"type":"video","codec":"h264","width":1920,"height":1080,"isDefault":true,"isForced":false},
      {"index":1,"type":"audio","codec":"aac","language":"zh","channels":2,"isDefault":true,"isForced":false}
    ]
  },
  "subtitles": [{"url":"https://media.example.test/movie.zh.vtt","language":"zh","label":"中文","format":"vtt"}]
}
```

示例地址、到期时间和属性仅用于说明，必须替换为真实数据；没有到期信息可省略 `expiresAt`。不要复制 `clientHeaders` 中任意 Authorization/Cookie 到上游；只使用实现明确需要的字段与可信配置。探测应有界、可取消，使用小范围 GET 或合适的轻量请求，不读取完整视频；上游不支持 HEAD 时不能仅据此判断媒体损坏。

## 媒体描述决定客户端能否播放

候选和解析都可以带 `media`，解析优先，未提供时沿用该候选已知属性。

- `container` 是真实容器；`durationSeconds` 以秒计，`sizeBytes` 是 64 位字节数，不能截断大于 2 GiB 的文件。未知属性省略。
- `tracks` 最多 64 条，同一媒体 `index` 唯一且在 0～255；type 为 `video/audio/subtitle`，这里的字幕是内嵌轨。
- 轨道按实际情况提供 `codec/language/title/width/height/channels/sampleRate/bitRate/frameRate/isDefault/isForced`。不能从 `.mkv` 猜出 HEVC 或杜撰音轨。
- 外挂字幕放 `subtitles`，最多 64 条，format 用 `vtt/srt/ass/ssa` 或省略。字幕 URL 与 HLS 子资源同样需要媒体网络授权。
- 各候选使用各自的描述，不能把一个来源的轨道复制给所有备选。

视频发现整轮最多六秒；单次模块发现/解析通常最多两秒，选源整轮另有八秒预算。不要把客户端 10 秒超时当作所有业务都能等待 10 秒。慢上游通过有限分页、缓存和按需解析处理，配置更换后不能复用不属于新配置的缓存。

## 媒体访问和适用媒体库

默认只允许公开 HTTPS 媒体；需要明确授权的来源在清单中声明，例如协议片段：

```json
{"mediaAccess":{"allowedOrigins":["http://127.0.0.1:5244"],"allowPrivate":true}}
```

`allowedOrigins` 精确匹配协议、主机和端口，不含路径、账号或查询。HTTP 需要授权，私网另需 `allowPrivate`；两者不使云元数据或链路本地等特殊地址合法。模块 RPC 地址授权与媒体授权互不代替。宿主校验 DNS、重定向和跨来源请求头，模块也不能不受控地访问用户给定任意 URL。

模块流始终经过宿主中继，`directPlay` 不会绕过用户授权。宿主完成 Range、HLS 清单/密钥/分片与外挂字幕重写，不将原始认证头交给客户端。

视频模块的 `libraryScope` 在宿主安装或更新接口中设置：

```json
{"libraryScope":{"mode":"selected","libraryIds":["lib_example"]}}
```

这是宿主设置，不能放进 `configSchema` 或指望通过配置头接收它。省略时保留旧设置，新安装默认全部；`selected` 空数组匹配零媒体。选择范围取启用库成员并集，不随用户打开的页面变化、不额外授予媒体权限。宿主在发现、解析和签名资源访问中持续检查范围。

## Jellyfin 对接分工

模块实现通用接口，宿主把同一目录和媒体描述转换为 Jellyfin 字段。

1. 宿主权限过滤后返回 Movie/Series/Season/Episode/Video。剧集和季是目录，需选择具体单集播放。
2. `GET/POST /Items/{itemID}/PlaybackInfo` 经同一模块注册表选源，生成 MediaSources、轨道与短期签名 URL；不要让模块单独创建用户会话或签名。
3. 客户端用签名 URL 读取视频与字幕；宿主处理 Range/HLS，播放事件写回当前用户的观看进度。
4. 配置、启停、实例或范围变化使旧签名失效，需要客户端重新请求 PlaybackInfo；用户进度继续保留。

主程序不转码、不重新封装、不提取内嵌字幕。SupportsTranscoding=false，没有 TranscodingUrl；只接受转码的请求可能得到 NoCompatibleStream。客户端必须能解码真实格式；拖动由客户端 seek，不把秒数猜成字节偏移。仅支持 Emby 协议的客户端没有自动兼容承诺。

## 宿主存储及授权调用

按需在清单声明 `permissions`：

```json
{"permissions":{"storage":true,"invoke":[{"moduleId":"example-catalog","methods":["search","detail"]}]}}
```

反向服务凭据只在已注册模块的业务请求中下发：

| 头 | 用法 |
| --- | --- |
| `X-JMlab-Host-URL` | 当前模块专属回调基址，已包含 `/api/v1/module-host/{id}` |
| `X-JMlab-Host-Token` | 回调时作为 `Authorization: Bearer ...` |
| `X-JMlab-Call-Ticket` | 调用 `/invoke` 时原样传递，宿主扩展调用链 |

仅在验证连接 Bearer 后读取这些头，且只向已信任的宿主基址回调。不能追加重复的 module-host 前缀、替换模块 ID、改用用户会话令牌，也不能将回调凭据转发给图片/媒体服务器。

- `GET <基址>/storage` 列键；`GET /storage/{key}` 读 `{key,value}`；`PUT` 提交 `{"value":任意JSON}`；`DELETE` 幂等删除。
- key 匹配 `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`，值最大 16 KiB，每模块最多 256 键和 1 MiB 密文。只可访问本模块加密命名空间，没有任意 SQL 或文件操作。
- `POST <基址>/invoke` 提交 `{moduleId,method,payload}`；方法仅 `caps/search/detail/rankings/discover/resolve/probe`，不能调用管理 actions。目标和方法必须被清单授权，目标当前启用。
- 调用链最多四模块且不能成环，单次目标调用最多五秒；payload 最大 32 KiB，响应最大 8 MiB。每模块反向接口最多四并发、每分钟 120 请求，遇到 429 按 Retry-After 退避。
- 调用票据有效 45 秒，并绑定版本；不能缓存后用于定时任务。热更新、重新启用或重启后重新接收凭据，caps 安装预检阶段不能依赖它们。

宿主共用一个数据库，但模块命名空间隔离；模块自行维护的外部数据库由模块负责。宿主加密备份不打包模块进程、ZIP/可执行文件、`.data` 文件或主密钥；恢复后外部模块离线需修复连接，本地模块缺程序需重新导入同 ID ZIP，不能宣称恢复数据就恢复了程序及其文件。

## 外部事件

清单 `subscriptions` 可选择 `media.item.created/media.item.updated/user.state.updated/playback.started/playback.stopped/module.status.changed`。实现 `POST /api/v1/events`，先验证连接认证，接收：

```json
{"topic":"media.item.created","eventId":"event-example","timestamp":"2026-10-19T00:00:00Z","payloadJson":"{\"itemId\":\"item_example\"}"}
```

`payloadJson` 是编码后的 JSON **字符串**，不是嵌套对象。按 `eventId` 幂等，推荐尽快持久化必要检查点后返回 204；重复事件返回成功。

通知是每模块独立的有界内存 FIFO，最多 64 等待事件，每次五秒、最多三次尝试；事件体最多 32 KiB。满队列会丢弃新事件，重启或热切换会取消旧投递和队列。它没有持久化 outbox 或恰好一次保证，不能作为唯一可靠的业务记录；重要工作需模块主动对账。通知失败不应阻塞正常搜索或播放。

## 契约核对位置

基线：[媒体描述验证](https://github.com/graysui/JMlab/blob/2d564c9eca1047614ad70c0c84a3503749a41d8c/internal/stream/media.go)、[模块回调](https://github.com/graysui/JMlab/blob/2d564c9eca1047614ad70c0c84a3503749a41d8c/internal/modulehost/service.go)、[事件处理](https://github.com/graysui/JMlab/blob/2d564c9eca1047614ad70c0c84a3503749a41d8c/internal/module/events.go)、[兼容客户端边界](https://github.com/graysui/JMlab/blob/2d564c9eca1047614ad70c0c84a3503749a41d8c/docs/jellyfin-compatibility.md)。
