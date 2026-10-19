# 清单、管理设置与信息模块

适用于模块创建、配置表单、管理操作、信息导入、榜单及虚拟库模板。视频与宿主回调见[播放与宿主协作](playback-and-host.md)。

## 清单如何驱动宿主

清单描述连接及能力，服务实现具体业务。下面是可供改写的复合模块声明，**不是可以独立运行的模块**；不要将未实现的事件、榜单或操作保留在交付清单中。

```json
{
  "id": "example-library",
  "name": "示例媒体模块",
  "version": "0.1.0",
  "type": "composite",
  "supportedItemTypes": ["movie", "series", "season", "episode"],
  "transport": {"type": "http", "url": "http://127.0.0.1:9091"},
  "configSchema": {
    "apiKey": {"type": "password", "label": "服务密钥", "secret": true, "required": true},
    "language": {
      "type": "string", "label": "内容语言", "default": "zh-CN",
      "options": [{"value": "zh-CN", "label": "简体中文"}, {"value": "en", "label": "英文"}]
    }
  },
  "actions": [{
    "id": "check-connection", "name": "检查连接", "readOnly": true,
    "fields": {}, "columns": {"status": "连接状态"}
  }],
  "rankings": [{
    "id": "popular", "name": "热门电影",
    "periods": [{"value": "week", "label": "本周"}], "maxPages": 1
  }],
  "libraryTemplates": [{
    "id": "popular-movies", "name": "本周热门电影",
    "description": "本模块已入库的热门电影", "itemType": "movie", "scope": "metadata",
    "isAdult": false, "filter": {"minRating": 7},
    "sort": {"field": "rating", "direction": "desc"},
    "rankingScopes": [{"id": "popular-week", "name": "本周", "rankingType": "popular", "rankingPeriod": "week"}]
  }],
  "permissions": {"storage": true},
  "subscriptions": ["media.item.created"]
}
```

修改要点：

- `id` 匹配 `^[a-z0-9][a-z0-9_-]{2,62}$`，名称和版本非空。更新同一模块保持 ID；推荐语义化版本，但基线不执行完整 SemVer 校验。
- 模块类型是 `metadata/stream/composite`；媒体类型与返回内容相符。信息专用模块不声明视频能力，视频专用模块不声明榜单。
- `transport.url` 是宿主可访问的服务基址，不能含用户名、密码、查询或片段；宿主会在基址后追加 `/api/v1/...`。
- `entrypoint` 不代表自动启动。宿主清单目录只需每模块一个目录及 `manifest.json`，不向其中复制实现或依赖。
- `mediaAccess` 仅在视频需要额外媒体来源授权时加入；连接令牌在宿主安装表单单独填写，不能嵌入清单。

## 握手和按请求配置

`GET /api/v1/caps` 返回以下结构；本例假定对应接口全部实现：

```json
{
  "supportedTypes": ["movie", "series", "season", "episode"],
  "supportsSearch": true,
  "supportsRankings": true,
  "supportsStreams": true,
  "availableRankingTypes": ["popular"]
}
```

视频专用模块同样提供 caps，只开启自己的能力。清单类型与 caps 必须一致；不要声明一个类型，期望宿主自动转换为其他类型。详情专用信息模块设置 `supportsSearch: false`，仍可按明确的 `sourceId` 导入。

每个请求中：

1. 先验证 `Authorization: Bearer <模块连接令牌>`。`X-JMlab-Module-ID` 只是标识；不能用它代替认证。
2. 用标准 Base64 解码 `X-JMlab-Module-Config`，再解析 UTF-8 JSON。空值可能为 `null`，按无配置处理；按本模块 schema 校验类型和必填项。
3. 从验证后的配置创建请求上下文或复用对应的不可变客户端。上游 API Key 与模块连接令牌是两种凭据。
4. 预检 caps 不依赖宿主反向服务。凭据或上游不可用时返回合适错误，不能伪报连接成功。

Base64 不加密。含连接令牌或秘密配置时，宿主只接受 HTTPS 或本机回环地址。普通 JSON 成功响应使用 2xx 与 `application/json`；除事件可用 204 外，需要响应体的接口不要返回空体。错误使用恰当的非 2xx，不返回 `200 {"error":...}` 让宿主误判成功。

## 动态管理页面

`configSchema` 支持 `string/password/number/boolean/json`；`secret: true` 和 `password` 标记秘密。秘密字段不放默认值，文字字段可使用 `options:[{value,label}]`，最多 128 选项。配置应有用户能理解的标签和说明，不让用户输入不透明内部 ID。

宿主在模块页面渲染 `actions`：

| 功能 | 声明及实现方式 |
| --- | --- |
| 检查连接、列出账号等只读操作 | `readOnly: true`，返回消息、状态或列表 |
| 添加、修改、删除外部记录 | `fields` 声明输入，`columns` 声明展示列；破坏性操作加 `confirmation` |
| 登录后保存上游会话 | 返回 `configPatch`，只包含已在 `configSchema` 声明的字段 |

模块接收 `POST /api/v1/actions/{actionID}`，请求是 `{"input":{}}`；宿主 API 的 `{revision,input}` 不会原样传入。响应示例：

```json
{"message":"连接正常","values":{"status":"正常"},"items":[]}
```

`configPatch` 由宿主验证、加密保存并替换模块注册版本，不送到浏览器；只读操作不能返回配置更新。普通消息、嵌套 values/items 仍由模块负责脱敏，不能依赖宿主过滤任意层级秘密。操作最多 32 项，每项输入与展示列各最多 32 项；操作 ID 使用模块 ID 的格式。

宿主管理员操作接口为 `POST /api/v1/admin/modules/{moduleID}/actions/{actionID}`，需要当前 `revision`；Cookie 会话还需要 CSRF。不要绕过宿主在浏览器中直连模块服务。

## 信息接口与稳定标识

| 模块路径 | 请求 | 成功响应 |
| --- | --- | --- |
| `POST /api/v1/search` | `{query,itemType,page,pageSize}` | `{items:[MediaSummary],total}` |
| `POST /api/v1/detail` | `{sourceId,providerIds}` | `MediaDetail` 对象本身，不套 `item` |
| `POST /api/v1/rankings` | `{rankingType,period,page}` | `{rankingType,period,observedAt,items}` |

摘要使用 `sourceId/title/itemType`，按需附加 `originalTitle/releaseDate/posterUrl/sourceUrl/providerIds/rank`。空搜索结果返回 `items: []` 和 `total: 0`，分页 `total` 是总结果数。遵循上游排序，避免同一页重复条目。

详情示例：

```json
{
  "sourceId": "movie:550", "itemType": "movie", "title": "示例电影",
  "overview": "作品简介", "productionYear": 2024, "runtimeSeconds": 7200,
  "rating": 8.1, "ratingCount": 200, "isAdult": false,
  "providerIds": {"catalog": "550"},
  "entities": [{"name":"示例导演","role":"director","sourceId":"person:100"}],
  "images": [{"type":"poster","url":"https://images.example.test/poster.jpg","priority":1}],
  "extraAttributes": {"originalLanguage":"zh"}
}
```

时间以秒计，`releaseDate` 用日期字符串；不要将上游分钟直接填进 `runtimeSeconds`。人物、机构、标签等放 `entities`，角色为 `actor/director/writer/studio/genre/tag`。外部身份放 `sourceId/providerIds`，扩展属性放 `extraAttributes`，不复用片名作为唯一键。

电影与剧集可能有相同上游数字 ID，采用 `movie:550`、`series:550` 等稳定命名空间。季与单集也有独立 ID。宿主导入季/单集时另传已存在的 `parentId/indexNumber`，不能往未定义的详情字段中塞入层级并期待自动创建整部剧。

用户通过宿主 `GET /api/v1/metadata/{moduleID}/search` 搜索，再向 `POST /api/v1/metadata/{moduleID}/import` 提交 `sourceId` 和可选 `providerIds`。宿主重新读详情后幂等入库；模块不直接写业务表。后续刷新仍使用原模块，不能静默换来源改变作品身份。

## 榜单和模板相互配合

榜单声明描述可选类型、周期与页数；`caps.availableRankingTypes` 和实际接口与之对应。`observedAt` 用 RFC3339 时间，items 使用媒体摘要。类型与周期各匹配 `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`，不是随意自然语言。

- 最多 32 类榜单，每类最多 128 周期；`maxPages` 推荐显式填 1～10。
- 宿主负责榜单任务、详情导入及快照；失败、取消或版本更换不能破坏旧快照。自动更新默认关闭，由管理员在模块页面配置。
- 虚拟库模板筛选**已入库**内容，不自动下载或创建作品。`itemType` 当前只支持 `movie/mixed`；即使支持剧集，也不能填写 `series` 作为模板类型。
- `scope` 为 `metadata/stream`，需与模块类型匹配；复合模块可分别声明。只有 metadata 范围能有 `rankingScopes`。
- `filter` 支持 `minRating`（0～10）、`years`（1900～2100）、`hasPlayableSource`、`isFavorite`；不要自行加其他模块 ID，宿主注入当前模块身份。
- 排序用 `title/code/year/rating/releaseDate/updatedAt/createdAt` 及 `asc/desc`。每模块最多 32 模板，每模板最多 128 榜单范围；显式榜单存在时，模板的类型与周期必须已声明。
- 新建库流程是模块 → 模板 → 范围。模板读取带 `revision`，创建库回传 `{moduleId,templateId,revision}`；冲突时重新读取。模板更新不会追溯修改已建媒体库。

## 契约核对位置

以目标版本为准。基线可查：[清单校验](https://github.com/graysui/JMlab/blob/2d564c9eca1047614ad70c0c84a3503749a41d8c/internal/module/manifest.go)、[HTTP 请求与响应](https://github.com/graysui/JMlab/blob/2d564c9eca1047614ad70c0c84a3503749a41d8c/internal/module/transport_http.go)、[模板约束](https://github.com/graysui/JMlab/blob/2d564c9eca1047614ad70c0c84a3503749a41d8c/internal/module/library_templates.go)、[完整开发指南](https://github.com/graysui/JMlab/blob/2d564c9eca1047614ad70c0c84a3503749a41d8c/docs/module-developer-guide.md)。
