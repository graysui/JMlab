# JMlab 模块使用与开发指南

JMlab 只分发主程序。本仓库及 Docker 镜像均不附带具体模块；下文 JSON 是协议片段，不是可运行扩展。

## 安装和使用

1. 按模块提供者说明独立启动服务，准备 manifest.json。
2. 登录管理员账号，进入“模块管理 → 添加模块”，选择清单，确认地址并填写凭证。
3. 保存会预检连接，成功后即时生效；普通用户通过添加内容和媒体库使用相关能力。
4. 新建虚拟媒体库时先选择模块，再读取它声明的模板和榜单范围。新部署不会自动创建库。
5. 视频模块可在功能设置选择适用媒体库。账号、检查、榜单和其他业务操作均位于模块页面。
6. 状态与维护支持清单更新和卸载；配置、启停与热切换无需重启主程序。外部服务升级由其维护者负责。

秘密字段留空保留，明确勾选清除才删除。卸载保留媒体及个人记录；含额外程序文件的目录须由用户自行整理。

## 目录与清单

宿主清单目录中，每个模块使用独立子目录并保存 manifest.json。实现运行在单独服务中，不随清单复制到宿主。清单示例：

```json
{
  "id": "example-metadata",
  "name": "示例元数据模块",
  "version": "0.1.0",
  "type": "metadata",
  "supportedItemTypes": ["movie"],
  "transport": {"type": "http", "url": "http://127.0.0.1:9091"},
  "configSchema": {
    "apiKey": {"type": "password", "label": "API Key", "secret": true, "required": true}
  },
  "subscriptions": ["media.item.created"]
}
```

模块类型为 metadata、stream 或 composite；媒体类型为 movie、series、season、episode、video。版本、能力、字段类型和地址应有完整校验，现有 Validate 只做基础检查，不能当作完整 JSON Schema 校验器。

当前外部加载只支持 http；in_process 需由 Go 代码注册。grpc 枚举及 Protobuf 文件只是后续协议草案。configSchema 已用于管理员配置表单，支持文字、数字、开关和秘密字段。subscriptions 已用于外部 HTTP 事件投递，契约及通知边界见下文。

## 模块虚拟库模板

模块在 `manifest.json` 的 `libraryTemplates` 中声明预设；前端不再维护全局的来源预设。管理员从“设置 → 媒体库配置 → 新建媒体库”依次选择模块、模板与榜单范围，名称与排序自动填入，仍可调整通用条件或高级 JSON。模块详情的“虚拟库模板”页列出该模块提供的模板。切换模块清除旧条件；自定义名称在同一模块内切换模板/范围时保留。

```json
{
  "libraryTemplates": [{
    "id": "local-movies",
    "name": "本模块高分作品",
    "description": "本模块已入库且评分不低于 8 分的内容",
    "itemType": "mixed",
    "scope": "metadata",
    "isAdult": false,
    "filter": {"minRating": 8},
    "sort": {"field": "rating", "direction": "desc"}
  }]
}
```

- 每模块最多 32 个模板，ID 遵循模块 ID 的格式且在模块内唯一；类型为 `movie` 或 `mixed`，范围为 `metadata` 或 `stream`，需与模块类型匹配，`composite` 可按模板分别声明。
- 模板 `filter` 支持 `minRating`（0～10）、`hasPlayableSource`、`isFavorite` 与 `years`（1900～2100）。宿主读取时注入当前模块的 `metadataModuleId` 或 `streamModuleId`，模板不能自行指向其他模块。后者匹配此模块当前启用、状态可用且类型支持的已记录媒体流；不表示此时已完成网络探测。
- `rankingScopes` 每项为 `{id,name,rankingType,rankingPeriod}`，最多 128 项。所有元数据模块均可声明，宿主不按模块 ID 限制；范围筛选已入库的当前快照。类型与周期允许 1～64 个字母、数字、下划线或连字符。模块可显式声明 `rankings: [{id,name,periods:[{value,label}],maxPages}]`（最多 32 类、每类 128 范围、1～10 页）；未声明时从模板范围推导。显式声明后模板范围必须匹配声明。各模块通过通用任务入库，不自动下载媒体文件。
- `GET /api/v1/admin/modules/library-templates` 返回模块状态及模板数量；先选择模块，再通过 `GET /api/v1/admin/modules/{moduleID}/library-templates` 读取当前模板、范围与 `revision`。均仅 Admin 可访问，不返回模块连接配置。未声明模板的旧模块仍可运行，但不能提供此快捷入口；需更新清单。停用/无模板模块在选择器中说明原因，加载失败可重试，目录变化后可刷新列表。
- 清单安装与更新会校验模板，失败保留原版本；目录热加载同步替换模板。Web 创建库时向 `POST /api/v1/libraries` 回传 `template: {moduleId,templateId,revision}`，版本失效或模块停用/移除返回 409，需重新读取。旧 API 可省略此字段，编辑已保存库不依赖它。
- 模板是创建时的副本，不自动覆盖已有库。已保存的筛选规则、授权和媒体继续保留；停用或移除元数据模块不隐藏已入库内容，播放模块范围则按当前可用源显示。普通筛选修改保留其他条件，复杂 all/any/not 规则只在 JSON 中编辑，避免无意丢失。
- 榜单类型与周期必须匹配同一条当前快照记录，避免用“热门周榜 + 热播日榜”错误匹配“热门日榜”。本功能不新增数据库迁移。

## 播放模块适用媒体库

所有 `stream` 和 `composite` 模块（包括内置和第三方）使用相同的宿主配置 `libraryScope`，与模块私有 `configSchema` 分开。设置入口在模块的“功能设置 → 适用媒体库”，列表展示当前适用库名称。

```json
{"libraryScope":{"mode":"selected","libraryIds":["lib_example"]}}
```

- 安装 `POST /api/v1/admin/modules` 和更新 `PATCH /api/v1/admin/modules/{moduleID}` 都支持此字段；需要 Admin，Cookie 会话还需 CSRF。
- 省略、`null` 或既有模块没有该设置时，保留原设置；从未配置时默认全部。显式设为 `{"mode":"all","libraryIds":[]}` 恢复全部。`selected` 空列表表示不适用任何媒体，最多 100 个库，不能重复。
- `GET /api/v1/admin/modules/playback-libraries` 返回 `{items:[{id,name,enabled,selectable,reason}]}`。允许选择普通内容规则库或手工成员库；媒体流条件和个人收藏/已看条件不能作为路由范围，以避免循环与用户状态混用。已被模块选中的库不能直接改成这类规则，应先从模块范围移除。
- 指定范围取多个启用库成员的并集，停用/删除库不匹配。范围只决定模块是否为媒体提供视频，不改变用户授权，也不会按用户当前打开的页面切换来源。
- 候选发现、已有源列表、解析、探测和带令牌的后续资源请求均检查范围，目录可播放标记使用同样规则。更新配置会更换模块注册版本，范围随加密配置持久化、备份与恢复；模块无需重启宿主。
- 此字段由宿主执行，不作为秘密配置请求头发送给第三方服务。模块仍实现同一套 `DiscoverSources`、`ResolveStream`、`ProbeSource` 接口，无需根据特定模块 ID 编写宿主分支。

宿主统一管理一个数据库。媒体/媒体流通过模块 ID 标明归属，模块私有存储通过宿主开放存储接口按模块 ID 隔离命名空间，模块不能借此直接读其他模块数据。这是逻辑隔离，不是每模块单独建立数据库；模块自己的外部服务若另有数据库，由其服务维护。

## HTTP/JSON 契约

### 模块管理操作

所有模块可在清单 `actions` 声明操作：`{id,name,description,readOnly,fields,columns,confirmation}`。fields 使用与配置相同的 string/password/number/boolean/json 字段定义，字符串支持 `options:[{value,label}]`；输入最多 32 字段，操作最多 32 项。登录、连接检查、站点增删改等业务归模块，宿主不根据模块 ID 选择页面。

管理员调用 `POST /api/v1/admin/modules/{moduleID}/actions/{actionID}`，提交 `{revision,input}`；模块列表返回当前 revision。宿主校验角色、CSRF、版本和声明字段，再调用模块 `POST /api/v1/actions/{actionID}`，模块接收 `{input}`。操作 10 秒超时、结果上限 1 MiB，HTTP 通信仍使用模块认证与配置头。

模块返回 `{message,values,items,configPatch}`。前端按声明生成“模块功能”，展示操作、输入、确认及结果列；选择结果行可将非秘密字段带入编辑/删除操作。临时密码执行后清空。宿主仅返回声明的展示列或非秘密编辑字段，所有声明秘密顶层字段被过滤；模块仍须保证消息和嵌套结果不含秘密。停用模块不能执行操作。可选 configPatch 只更新清单声明的配置字段，宿主验证并加密保存、更换注册版本，浏览器永远拿不到该字段；readOnly 操作不能写配置。进程内实现 `ActionProvider`，使用 `RegisterFactory` 按配置创建新实例，初始化或保存失败保持旧实例；配置恢复也通过工厂重建并刷新 caps，成功后关闭旧实例。HTTP 模块通过同一协议，无需宿主新增代码。

### 模块选择规则

元数据搜索/导入必须指定模块 ID；后续同步继续使用媒体原来的 metadataModuleId。来源停用、缺失或故障时返回明确错误，不能偷偷改用其他模块并改变媒体身份。搜索列表只展示已启用且支持搜索的来源；详情专用模块仍可用于明确的详情导入。已预检的 caps 随注册版本保存，支持类型取清单与非空 supportedTypes 的交集；旧模块省略类型时采用已有声明。能力变化需要更新版本或重新保存来源设置。

播放发现聚合符合类型的活跃模块；来源的显示顺序、配置优先级及用户指定画质/语言参与评分，协议和可用状态继续参与选择；显式指定 sourceId 时只使用该源，自动选择失败才依次尝试其他候选。只有预告片时仍明确标记预告片，不改变作品身份。具体等待和退避边界见后文。

以 [Go 契约](../internal/module/contract.go) 与 [HTTP 客户端](../internal/module/transport_http.go) 为当前可执行参考；字段名使用 camelCase。

| 方法与路径 | 请求字段 | 返回 |
| --- | --- | --- |
| GET /api/v1/caps | 无 | supportedTypes、supportsSearch、supportsRankings、supportsStreams、availableRankingTypes |
| POST /api/v1/search | query、itemType、page、pageSize | items、total |
| POST /api/v1/detail | sourceId、providerIds | MediaDetail |
| POST /api/v1/rankings | rankingType、period、page | RankingSnapshot |
| POST /api/v1/stream/discover | itemId、title、itemType、providerIds、extra | candidates |
| POST /api/v1/stream/resolve | candidateId、encryptedPayload、clientHeaders | ResolvedStream |
| POST /api/v1/stream/probe | candidateId、encryptedPayload | healthy、statusCode、durationMs、error |

元数据详情包含来源 ID、类型、标题、简介、上映日期、年份、时长、评分、人物/片商等关系、图片、providerIds 和 extraAttributes。来源名称、专属字段和内部 ID 不应混用。

entities 每项支持 name、role、sourceId、providerIds、originalName、avatarUrl 和 extra；role 为 actor / director / writer / studio / genre / tag。同一实体的多个角色使用相同 sourceId。宿主按模块和外部标识匹配，只有尚无来源标识且唯一的旧实体才按名字认领；同名不同 ID 保持独立，手工锁定属性不会被同步覆盖。

实体 avatarUrl 目前保存及备份，尚未接入新的头像下载与展示链路。关系同步沿用累加语义，不承诺删除上游移除的旧关系。

候选示例：

```json
{
  "candidates": [{
    "candidateId": "candidate-001",
    "label": "1080p",
    "protocol": "http",
    "quality": "1080p",
    "isPreview": false,
    "encryptedPayload": "module-opaque-reference"
  }]
}
```

解析示例（仅在宿主与模块之间传递，不能原样发送给客户端）：

```json
{
  "streamUrl": "https://media.example.test/video.mp4",
  "protocol": "http",
  "requiredHeaders": {"Authorization": "module-managed-value"},
  "directPlay": false,
  "requiresProxy": true
}
```

可附带 expiresAt（RFC3339）以及下列媒体描述。候选和解析均可返回 `media`，解析优先；未返回时沿用该候选已知属性。容器缺失时仅按 HLS 协议或真实文件扩展名推断，编码和轨道未知则省略。Protobuf 尚未与 HTTP 完整对齐，当前可执行契约为 Go 接口和 HTTP，不能宣称两种传输可互换。

```json
{
  "media": {
    "container": "mkv", "durationSeconds": 3600, "sizeBytes": 17179869184,
    "tracks": [
      {"index": 0, "type": "video", "codec": "hevc", "width": 1920, "height": 1080, "isDefault": true},
      {"index": 2, "type": "audio", "codec": "eac3", "language": "en", "channels": 6, "isDefault": true}
    ]
  },
  "subtitles": [{"url": "https://media.example.test/subtitle.vtt", "language": "zh", "label": "中文", "format": "vtt"}]
}
```

MediaDescription 包含 container、durationSeconds、64 位 sizeBytes、bitRate 和 tracks。每条轨道为 index、type（video/audio/subtitle，内嵌）、codec、language、title、width/height、channels、sampleRate、bitRate、frameRate、isDefault/isForced；同一媒体的 index 不重复，范围 0～255，最多 64 轨。时长上限一年，数值范围见 OpenAPI。外挂字幕最多 64 条，format 为 vtt/srt/ass/ssa 或省略，地址单独执行媒体授权；不得将秘密写入标签或媒体属性。时长、大小同步到通用媒体流列，完整描述与候选引用加密缓存，刷新和版本切换保持一致。

### Jellyfin 播放对接

模块只实现同一套 discover/resolve/probe，无须自己实现 Jellyfin。宿主的目录详情与 PlaybackInfo 共用播放服务、权限、模块状态、适用库和候选选择；电影、普通视频和单集可播放，剧集/季返回层级，需选择具体单集。

- `GET/POST /Items/{itemID}/PlaybackInfo` 支持 MediaSourceId（SourceId 兼容）、Quality/Language、AllowPreview、PlaySessionId、StartTimeTicks、AudioStreamIndex/SubtitleStreamIndex 和 EnableDirectPlay/EnableDirectStream。查询参数优先；StartTimeTicks 非负，字幕 -1 表示关闭，不存在的轨道返回 400。默认只选择正片，不自动用预告片代替。
- MediaSources 使用实际容器、时长/大小与声明轨道；各备选源分别使用自己的描述，不复制主源属性。外挂字幕转换为 External MediaStream，DeliveryUrl 是宿主的短期地址，认证头与上游地址不发给客户端。缺少 PlaySessionId 时生成。
- 宿主提供中继、HTTP Range、HLS 清单/密钥/分片重写及外挂字幕；没有服务端转码、封装转换或内嵌字幕提取。SupportsTranscoding=false，不生成 TranscodingUrl；仅要求转码返回空来源和 NoCompatibleStream。客户端须支持容器与编码，轨道参数用于选择默认序号。
- StartTimeTicks 用于请求校验；直接播放起点由客户端按时间 seek，宿主不将秒数猜成字节偏移，也不服务器裁切。Sessions/Playing、Progress、Stopped 写回当前用户状态；Resume 列出当前用户未完成且进度大于零的电影、视频与单集，默认最近播放优先，支持权限过滤和分页。
- 视频与资源签名分类型，绑定用户、媒体、来源、模块注册版本和到期时间。匿名播放器可凭签名访问；已登录的其他用户不能借用。每次请求复核权限、范围、来源启停与模块版本。重新启用、改配置或替换实例使旧签名失效，客户端需重新请求 PlaybackInfo，用户进度保留。

`go test ./internal/integration ./internal/playback ./internal/jellyfin` 覆盖公开 HTTP 普通影视模块 → movie/series/season/episode 入库 → Jellyfin 选源、MKV HEVC/EAC3 描述、17GB 大小、Range/HLS/外挂字幕、音轨/字幕参数、分用户 Resume 和热启停。均为受控上游协议测试，不证明真实客户端已完成解码。

## 凭证与网络责任

- X-JMlab-Module-ID 只标识调用目标；HTTP 客户端支持独立 Bearer 凭证。模块服务须校验凭证，不能把 ID Header 当成鉴权。
- 配置以 Base64 JSON 放在 X-JMlab-Module-Config 请求头中，仅供宿主与可信模块通信；Base64 不是加密。含凭证的连接要求 HTTPS 或本机回环地址。客户端禁止重定向、限制响应 8 MiB、并发 8 和请求 10 秒。
- 媒体流模块也需实现 GET /api/v1/caps 并声明 supportsStreams=true，供热加载检查。
- encryptedPayload 是模块不透明引用的字段名，可以是模块内部 JSON；宿主负责加密持久化，不回显给客户端。
- 不向普通日志写入流地址、请求头、密码或 Token。
- 需要认证头或私网访问的流设置为宿主中继；示例中的 directPlay 不能代替正式授权策略。
- 管理员明确配置本地模块地址；私网媒体采用独立允许列表，不全局放开 SSRF。

播放模块默认只允许公开 HTTPS 媒体。清单可增加独立的媒体授权（与模块 RPC 地址分开）：

```json
"mediaAccess": {
  "allowedOrigins": ["http://127.0.0.1:5244"],
  "allowPrivate": true
}
```

allowedOrigins 精确匹配协议、主机和端口，不能带路径、账号或查询参数。HTTP 需要明确授权，私网另需 allowPrivate；即使授权仍拒绝链路本地、云元数据等特殊网段。请求在拨号前校验并固定 DNS 解析结果，不使用环境代理；跨来源不会转发认证头。模块流始终经宿主中继，字幕及 HLS 子资源也使用绑定用户的短期地址。

## 主动探活与故障退避

### 播放发现的等待边界

播放发现整轮最多六秒，模块及候选分别使用四个工作协程，整个播放服务最多八个在途模块调用，每次调用最多两秒（包含等待名额）。只调用已启用、具有播放接口并支持当前媒体类型的模块；未声明类型的旧进程内适配器由模块自行判断。每模块最多接受 100 个候选，重复候选 ID 合并。已完成的健康候选立即保存，慢模块、候选错误和本轮超时不删除已有记录。

同一作品、模块及注册版本的并发发现共享一次工作；成功缓存 30 秒，普通错误缓存三秒，取消或超时允许下次立即重试。主动刷新绕过已完成的缓存，仍共享在途工作。候选解析后保存前检查当前注册版本；配置更新、停用或移除后，旧调用不能覆盖新实例记录。网络调用不持有注册锁，短暂的数据库保存与注册替换互斥。

选源阶段另有八秒总预算，每次解析最多两秒；明确选择已有来源且未要求刷新时，直接解析该来源。自动选源仍按现有排序、优先级、画质、语言、协议及可用状态评分；移除了解析时间取余对排序的干扰，分数相同按来源 ID 稳定排序。失败尝试下一候选，取消、超时和实例替换不标记原来源损坏。所有来源失败时保留媒体及个人记录。

进程内实现必须响应 Context；超时后宿主停止等待，调用名额直到实际返回才释放，以限制不配合取消的实现。宿主无法强制终止 Go 函数；外部 HTTP 调用会取消网络请求。上述时间不包含数据库调度或媒体流传输。

宿主每 15 秒调度空闲 HTTP 来源的 caps 检查，最多四个并行，每个两秒、整轮十秒，网络期间不占管理器配置锁。正常业务调用也更新连接状态；连续三次网络/服务端、认证、限流或响应解码失败后暂停该来源，健康来源继续工作。普通内容不存在或参数错误等 4xx、用户主动取消和通知处理失败不计为连接故障；caps 失败始终计入检查。

暂停后业务调用快速失败，后台先等 30 秒再探测，继续失败则指数退避，最长五分钟。成功后自动恢复来源，更新注册版本和宿主凭据；热更新、配置保存和重新启用也会预检新实例。停用来源不参与探活，旧实例的结果不能改变新版本状态。通知使用自己的重试和计数，不因通知处理器异常关闭可用的搜索或播放能力。

管理员列表的 health 包含 state（unknown/healthy/degraded/open）、consecutiveFailures、lastCheckedAt、lastSuccessAt 和 nextCheckAt。来源页面定期刷新，用“连接正常/连续失败/下次检查”解释状态；错误不包含上游正文或地址。记录属于当前进程，重启后重新检查，不替代上游媒体文件探测或真实播放验收。

## 外部事件投递

清单 subscriptions 可声明 media.item.created、media.item.updated、user.state.updated、playback.started、playback.stopped、module.status.changed。宿主在媒体/观看状态保存成功后发布事件，不因写入失败或过期播放进度产生事件；模块状态在成功安装、替换、启停或移除后发布。负载只含稳定 itemId、必要的 userId/version 或 moduleId/status，不含私人备注、设备、会话凭据或上游地址。

模块实现 `POST /api/v1/events`，验证连接 Bearer 后接受 Event 对象：topic、eventId、timestamp、payloadJson（编码后的 JSON 字符串），成功返回任意 2xx（推荐 204）。宿主提供与业务请求相同的反向服务凭据，事件处理中可按清单权限访问自己的存储或调用其他模块。模块应先按 eventId 幂等处理，重复收到相同事件直接返回成功；不要将不受控请求头当作宿主身份。

每个模块一个独立 FIFO，最多 64 个等待事件，一次只投递一个。每次最多五秒，失败最多尝试三次（间隔 100/200ms），重试保留 ID 与时间。事件体最大 32 KiB，宿主在当前订阅内记住最近 1024 个 topic/eventId 避免重复排队；重启或热更新后去重记录重置。队列满丢弃新事件，失败与丢弃计数在管理员模块接口的 events 字段及来源页面显示。处理器异常不记录秘密错误正文。

这是有界的内存通知机制，没有持久化 outbox，也不承诺崩溃恢复或恰好一次投递。热更新、停用和关闭取消当前 HTTP 投递并丢弃旧队列，重启后只接受新事件。业务数据不依赖通知成功，消费模块可用自身命名空间保存检查点并主动对账。进程内 Go 处理器是可信代码，必须遵守 Context；宿主无法强制杀死不配合取消的原生函数，只能在调用者给定期限内停止等待。

## 宿主反向存储与模块调用

模块需要先在清单中声明权限，例如：

```json
"permissions": {
  "storage": true,
  "invoke": [{"moduleId": "example-catalog", "methods": ["search", "detail"]}]
}
```

宿主在已注册模块的业务请求中下发 `X-JMlab-Host-URL`、`X-JMlab-Host-Token` 和 `X-JMlab-Call-Ticket`。回调使用收到的 URL 为基址，在 Authorization 中传 `Bearer <Host-Token>`；不能使用用户登录令牌。跨模块调用还必须原样传当前请求的 Call-Ticket，不能使用模块配置中的连接令牌代替。模块服务应只在验证宿主连接令牌后接受这些请求头，并只回调管理员信任的宿主地址，避免公开模块服务被任意请求利用。

`GET /storage` 列出键；`GET /storage/{key}` 返回 `{key,value}`；`PUT /storage/{key}` 提交 `{"value": 任意JSON}`；`DELETE /storage/{key}` 幂等删除。键允许字母、数字、点、下划线和连字符，首字符为字母或数字，最长 128 字符。值最大 16 KiB，每模块最多 256 键和 1 MiB 密文。值以主密钥派生的模块专属密钥加密；路径只能访问自己的命名空间，不提供任意业务 SQL 或文件读写。

`POST /invoke` 提交 `{"moduleId":"example-catalog","method":"search","payload":{"query":"片名","itemType":"movie","page":1}}`。方法限定为 caps/search/detail/rankings/discover/resolve/probe，参数及结果与上述 HTTP 契约相同。清单精确匹配目标及方法，目标必须已启用；总链最多四个模块，循环调用拒绝。每次目标调用最多五秒，请求 payload 最多 32 KiB、响应最多 8 MiB。宿主签名票据有效 45 秒，绑定调用链及当前版本，下游收到扩展后的新票据。每模块反向接口限制四个并发及每分钟 120 次请求，429 应按 Retry-After 退避。

热更新、重新启用或宿主重启使旧模块凭据失效；旧客户端快照不会领到新实例凭据。不要缓存票据用于后台调度，也不要把这些请求头转发给媒体、图片服务器或日志。回调凭证要求模块连接使用 HTTPS 或本机回环地址。首次安装和更新的 caps 是发布前预检，不下发有效宿主凭据，不能依赖反向服务。JMLAB_PUBLIC_URL 应为模块服务实际可访问的宿主地址。

本地 `go test ./internal/modulehost ./internal/module ./internal/moduleadmin` 覆盖真实管理器与 HTTP 模块的存储回调、授权调用及链传播，旧凭据失效、循环/深度/超时、健康隔离、配额、并发和限流；外部事件回归见 module 和 catalog 包。

## 模块备份与恢复

管理员备份勾选加密配置后，`encrypted-config.json` 使用 `jmlab.encrypted-config.v2`，整体 AES-GCM 加密，包含模块清单、配置/连接令牌、启停状态、模块专属存储及选定备份目标。恢复环境必须使用备份时的主密钥；不归档主密钥、模块可执行文件、进程和投递队列。旧 v1 仅包含备份目标，仍可恢复。

恢复默认只处理媒体；显式勾选配置后，预览会验证解密、模块身份、配置类型及命名空间配额。合并保留已存在的模块与目标配置，存储仅补缺少的键；覆盖替换归档中涉及的配置和整个对应模块命名空间，其他模块保留。

媒体、配置和迁移 24 的加密重载队列在同一数据库事务提交。提交失败全部回滚；提交后，旧模块实例与事件订阅失效，管理器在锁内恢复清单并重新预检。离线、目录不可写等情况返回 `moduleReloadPending=true`，来源显示异常，后台扫描或重启后继续处理队列；数据库已恢复，不能报告为整体回滚。管理员可在来源卡片修复地址/凭证或停用，修改也通过队列持久化。文件写入成功、队列确认后再发布新实例，不需要重启宿主。

本地 `go test ./internal/backup` 使用独立临时库和 HTTP 服务，覆盖鉴权恢复 API、真实模块搜索、加密边界、命名空间、启停、错误密钥、v1、合并配额、SQL 失败回滚、目录失败重试与重启恢复。

## 通用元数据入库接口

Admin/Editor 可通过 GET /api/v1/metadata/modules 选择支持搜索的启用来源，GET /api/v1/metadata/{moduleID}/search?q=...&itemType=movie 搜索，POST /api/v1/metadata/{moduleID}/import 提交 sourceId 和可选 providerIds。宿主再次读取详情并幂等保存；不能由浏览器直接提交未经来源读取的详情。详情专用来源可按明确 ID 导入，不出现在搜索列表中。

不同媒体类型应使用不同 sourceId（例如 movie:550 与 tv:550）。季/单集入库需提交已存在的 parentId、indexNumber，父子类型严格校验。媒体无来源时仍保留。后续同步从记录的元数据模块取详情，字段锁定生效。新来源不需要来源编号；旧 code 列暂存内部兼容键，前端按类型展示来源标识。关系写入统一 entities / entity_identifiers / item_entities，旧分类名作为查询兼容视图保留。

## 来源身份与后台维护

StreamProvider 返回的候选由宿主以 module_id 保存，无需创建或选择传统 Provider。API 返回 moduleId/moduleName；providerId 仅在传统站点来源中有值，模块候选为空字符串。候选引用与请求头加密保存，宿主根据模块当前清单校验媒体网络权限。后台探测调用同一模块的 ProbeSource，错误脱敏后保存探测历史。管理员可以调整名称、画质、语言、排序及启停；连接地址/秘密在来源设置修改，不能把不透明候选引用当 URL 编辑。热更新和停用仍由 Manager 统一控制。

## 通用榜单同步与自动更新

管理员使用 `GET /api/v1/admin/modules/{moduleID}/rankings` 读取 `items/revision/schedule`，`POST` 同路径提交 `{rankingType,period,revision}`，202 返回 `taskId`。任务按模块与榜单范围隔离锁和快照；详情失败、取消或热更新保留旧快照，无媒体流仍保留媒体。每次最多 250 项，通过同一 MetadataProvider.GetDetail 与通用目录保存。模块只声明来源范围与上游规则，不直接修改宿主业务表。

`PUT /api/v1/admin/modules/{moduleID}/rankings/schedule` 保存 `{enabled,intervalHours,updateTime,targets:[{rankingType,period}]}`。默认关闭；空 updateTime 按 1～720 小时间隔，HH:MM 为服务器本地时间每天运行。停用不执行，设置保留；计划随加密配置备份恢复。迁移 28 新增独立计划表，不重新创建既有内容。

普通登录用户从 `/api/v1/rankings/modules` 读取来源，再从 `/api/v1/rankings/modules/{moduleID}/{rankingType}?period=...` 读取权限过滤后的 `{items:[{rank,item}]}`。前端不能推断固定榜单类型或把某个模块作为默认来源。
