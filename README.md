# MiniMax Design 2API

把 **MiniMax Design 桌面版**（`@hilo/desktop` 3.0.16，海外区）里内置的模型反代出来，
做成 **OpenAI 兼容 API**。

纯 Go 标准库，无第三方依赖，单二进制 + 单 `config.json` 即可跑起来。

---

## 快速开始

```bash
# 1. 从本机已安装并已登录的 MiniMax Design 里把令牌读出来
./minimax-design2api.exe -import-token

# 2. 起服务
./minimax-design2api.exe
#   → http://127.0.0.1:18080/admin/   管理台
#   → http://127.0.0.1:18080/v1       OpenAI 兼容接口
```

> 默认监听写在 `config.json` 的 `listen` 里（代码兜底值是 `:8080`）。
> 本机 `8080` 已被隔壁 `minimax2api` 占着，所以这里配的是 `127.0.0.1:18080`。
> 换端口改 `config.json` 或用 `-listen :9000`。

克隆下来没有 `config.json`（里面有令牌，不入库），先复制模板再改：

```bash
cp config.example.json config.json   # Windows: copy config.example.json config.json
```

令牌可以从本机客户端导入（`-import-token`），也可以自己粘进去；
字段含义见下面「配置」一节。查账号积分用 `python tools/check_credit.py`。

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{"model":"MiniMax-M3","messages":[{"role":"user","content":"你好"}],"stream":true}'
```

客户端（Cherry Studio / NextChat / Cursor / OpenAI SDK / LangChain …）填
`http://127.0.0.1:8080/v1` 即可，无需 API Key（除非在配置里设了 `api_keys`）。

命令行开关：

| 开关 | 作用 |
| --- | --- |
| `-import-token` | 从本机客户端读出令牌写进号池，然后退出 |
| `-show` | 打印解析后的配置 + 模型目录，然后退出 |
| `-listen :8080` | 覆盖监听地址 |
| `-no-catalog-fetch` | 跳过启动时向网关拉模型目录 |

---

## 可用模型

启动时会调 `GET https://design.minimax.io/api/v1/config` 拿 provider 矩阵，
所以**模型列表是动态的**——MiniMax 上新模型，这里重启就跟着变。

当前拿到的 9 个模型（每个都有裸名和 `provider/name` 两种写法）：

| 模型 | 上游协议 | 说明 |
| --- | --- | --- |
| `MiniMax-M3` | Anthropic Messages | 默认模型，支持图片输入 |
| `MiniMax-M2.7` | Anthropic Messages | 支持图片输入 |
| `alpha` | Anthropic Messages | 带 thinking，支持图片 / PDF |
| `alpha_high` | Anthropic Messages | 同上 |
| `gamma` | OpenAI Responses | |
| `gamma_high` | OpenAI Responses | 带推理摘要（→ `reasoning_content`） |
| `gamma_mid` | OpenAI Responses | |
| `gpt-6-astra` | OpenAI Responses | |
| `omega-3.1-pro` | Google | **暂不支持**，见下 |

> `omega-3.1-pro`（Gemini 3.1 Pro）走 Google 的 `:generateContent` 协议。
> 上游路由没能定位到（`@ai-sdk/google` 拼的是 `{baseURL}/{model}:generateContent`，
> 试过的十几种前缀组合全是 404）。这种情况服务会**明确返回 501**，
> 而不是静默失败或把请求塞给别的模型。

---

## 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/v1/chat/completions` | OpenAI 格式，支持流式（SSE）/ 非流式 / 工具调用 / 多模态 |
| `GET` | `/v1/models` | 模型列表（带 `meta.protocol` 等元信息） |
| `POST` | `/v1/messages` | Anthropic 原生格式直通（给 Anthropic SDK 用） |
| `GET` | `/v1/video/models` | 视频模型目录（11 个，含分辨率/比例/时长可选值） |
| `POST` | `/v1/video_generation` | 提交视频任务（MiniMax 平台格式，见下） |
| `GET` | `/v1/query/video_generation?task_id=` | 查询视频任务 |
| `GET` | `/v1/files/retrieve?file_id=` | 取视频下载地址 |
| `GET` | `/v1/image/models` | 图像模型目录（11 个，含 backend 与可选参数） |
| `POST` | `/v1/images/generations` | **OpenAI 图像格式**，同步返回（内部提交+轮询） |
| `GET` | `/v1/audio/voices` | 音色目录（671 个，支持 `?language=` / `?q=` 过滤） |
| `POST` | `/v1/audio/speech` | **OpenAI TTS 格式**，同步返回音频字节 |
| `POST` | `/v1/lyrics/generations` | 歌词生成（同步，免费） |
| `POST` | `/v1/music/generations` | 提交音乐生成任务 |
| `GET` | `/v1/query/music_generation?task_id=` | 查询音乐任务 |
| `GET` | `/health` | 健康检查 + 号池概览 |
| `GET` | `/admin/` | 管理台 |

管理台接口：`GET /admin/api/state`、`GET /admin/api/metrics`、`GET /admin/api/catalogs`、
`GET /admin/api/credits`、`PUT /admin/api/config`、`POST /admin/api/tokens`、
`PUT|DELETE /admin/api/tokens/{i}`、`POST /admin/api/import-local`、`POST /admin/api/reload`、
`POST /admin/api/pool/reset`。

---

## 管理台

`http://127.0.0.1:18080/admin/`（端口跟 `config.json` 的 `listen`）。

界面语言、布局与交互仿照 [chenyme/grok2api](https://github.com/chenyme/grok2api) 的
React 管理台：288px 固定侧边栏 + 1280px 居中主区、`oklch` 中性色深浅两套主题、
全圆角 h-8 控件、h-5 徽章、安静的小字号排版。**但它是零构建的**——纯 HTML/CSS/JS，
`go:embed` 进二进制，`go build` 一个命令出活，不引入 node 工具链。

| 页面 | 内容 |
| --- | --- |
| 仪表盘 | 模型数 / 可用令牌 / 请求总数 / 平均耗时 / 并发五张指标卡，号池可用率环形图、令牌构成、按能力（对话·图像·视频·语音）与按协议的流量分布、最近 40 条请求 |
| 模型 | 四个标签：**对话**（协议徽章、上游名、上下文、最大输出、工具/视觉能力）、**图像**、**视频**、**音色**。后三个是媒体网关的目录，标签上带条数；支持搜索，对话页另有协议筛选与隐藏 `provider/` 前缀写法 |
| 号池 | 令牌增删改（批量粘贴、启用停用、编辑、删除）、从本机客户端导入、一键清空冷却、**查询积分**（余额 / 积分类型 / 到期 / 免费试用，按需查询不轮询），以及调度与失败策略的说明。冷却会显示原因：凭证失效 / 积分耗尽 / 上游抖动 |
| 调试台 | 聊天（SSE 流式、可看 reasoning_content、可中断）/ 图像 / 视频（提交 + 每 5 秒轮询到出片，可播放）/ 语音（671 音色目录可搜可过滤，直接试听下载）|
| 文档 | 按 Chat / 图像 / 视频 / 语音分组的端点参考，含参数表与可复制的 curl |
| 设置 | 直接编辑并写回 `config.json` |
| 关于 | 接口清单、逆向结论、已知限制 |

几个实现上的取舍：

- **仪表盘的数字是真实的**。`metrics.go` 在 `ServeHTTP` 里对 `/v1/*` 埋点，统计请求数、
  成功率、平均耗时、按能力/协议/模型的分布与最近请求环形列表。**管理台与 `/health` 的流量不计入**，
  否则页面描述的就是监控而不是服务本身。指标只在内存里，重启即清零。
- **请求体只被窥探前 8KB**（tee，不整体缓冲）就能拿到 `model` 字段，处理器对此无感知。
  处理函数若完全没读 body，中间件会自己补读一次，否则那条路由的模型名会静默消失。
- **调试台的四个页面走的是真实的 `/v1` 接口**，和外部客户端同一条链路。`config.json` 设了
  `api_keys` 时，需在页面顶部填入同一个 key（只存在浏览器 localStorage，不写回服务端）。
- **媒体目录服务端缓存 10 分钟 + 启动时后台预热**。音色 671 条实测冷拉约 40 秒，
  不缓存的话控制台每敲一个字都会打一次上游；不预热的话，每次重启后第一个点开
  媒体选择器的人要干等这 40 秒。预热是尽力而为，失败走每目录各自的错误上报。
- 主题跟随系统，也可在侧边栏左下角切浅色/深色/跟随系统。

---

## 视频（MiniMax H3）

媒体接口不在 LLM 网关上，而在**云网关** `design.minimax.io`——但用的是**同一个 `token`**。
上游是这套：

```
POST {videoBase}/api/v1/video/minimax-v3/generate     -> {task_id, base_resp}
GET  {videoBase}/api/v1/video/minimax-v3/tasks/{id}   -> {task_id, file_id, status, base_resp}
GET  {videoBase}/api/v1/video/minimax/files/{fid}     -> {file: {download_url}}
```

本服务**没有**直接暴露这套，而是转成 **MiniMax 平台格式**，因为那是 new-api 的
MiniMax 渠道（type 35，适配器 `hailuo`）认识的形状。这样它就成了一条能直接
给 new-api 用的上游。

两个容易踩的坑，代码里显式处理了：

- **纯文本生成必须显式给 `ratio`，且不能是 `adaptive`**。平台请求里没有 ratio 字段，
  所以本服务会推导：优先用调用方给的 `ratio`，其次从 `size`（`1280x720`）算最接近的
  比例，最后兜底 `16:9`。`image_mode=first-last-frame` 时目录里所有具体比例都被禁用，
  所以那种情况不发 ratio。
- 上游状态是小写 `processing/success/fail`，平台要的是首字母大写的
  `Preparing/Queueing/Processing/Success/Fail`。

模型名兼容两套：hub 原生的 `MiniMax-H3` / `-Max` / `-Max-Turbo` 直接透传；
平台名（`MiniMax-Hailuo-2.3`、`T2V-01`、`I2V-01`…）映射到 H3 线。
分辨率也会折算：`512P/720P → 768P`，`1080P → 2K`；Max 系列只支持 `480P/768P`。

```bash
# 提交（4 秒，720P→768P，约 3~10 分钟出片）
curl http://127.0.0.1:18080/v1/video_generation \
  -H 'content-type: application/json' \
  -d '{"model":"MiniMax-Hailuo-2.3","prompt":"a paper boat in a gutter","duration":4,"size":"1280x720"}'
# -> {"task_id":"7GyYK24V81Xr","base_resp":{"status_code":0,"status_msg":"success"}}

curl "http://127.0.0.1:18080/v1/query/video_generation?task_id=7GyYK24V81Xr"
# -> {"task_id":"...","status":"Success","file_id":"..."}

curl "http://127.0.0.1:18080/v1/files/retrieve?file_id=7GyYK24V81Xr"
# -> {"file":{"download_url":"https://cdn.hailuoai.video/..."}}
```

实测 6 秒 768P 出片约 1.97 MB，是真实 MP4。

> 其余 8 个视频模型（Kling / Veo / Wan / Jimeng）走各自不同的后端协议，
> 本服务目前只实现了 `minimax_v3` 这一条线，调用它们会返回明确错误。

### 多后端（全部 11 个模型）

11 个视频模型分属 **6 个 backend**，每个后端的请求体、状态词、产物位置都不一样。
本服务全部实现了，路由按 **model id** 决定（不是目录里的 `backend` 字段——Kling 三个
模型共用 `kling` 这个 backend，靠 model id 分端点）。

| backend | 提交 / 查询 | 模型 | 产物 |
| --- | --- | --- | --- |
| `minimax_v3` | `/video/minimax-v3/generate` · `/tasks/{id}` | H3 / H3-Max / H3-Max-Turbo | `file_id` |
| `wan_i2v` | `/video/wan3/generate` · `/tasks/{id}` | wan3.0-video / -prime | CDN URL |
| `veo3` | `/video/veo3/generate?model=` · `/tasks/{id}` | veo-3.1-fast / veo-3.1 | CDN URL |
| `kling_omni` | `/video/kling-omni/generate` · `/tasks/{id}` | kling-v3-omni-video | CDN URL |
| `kling` | `/video/kling/avatar` · `/avatar/{id}` | kling-avatar | CDN URL |
| `kling` | `/video/kling/motion-control` · `/{id}` | kling-motion-control | CDN URL |
| `jimeng` | `/video/jimeng/generate` · `/tasks/{id}` | jimeng_motion_control | CDN URL |

**只有 `minimax_v3` 真的返回 `file_id`。** 其余后端直接给成品 CDN 地址，而平台形状
只有 `file_id` 一个产物槽（new-api 拿它去 `/v1/files/retrieve` 换地址）。所以本服务
把 URL **百分号编码后塞进 `file_id` 槽**，并让 `/v1/files/retrieve` 认出绝对 URL
原样回吐。编码是必须的——不编码的 URL 会被调用方的 query parser 在第一个 `&` 处截断，
签名链接就废了。

查询接口没有 backend 参数，所以非默认后端的 task id 会被打上标签
（`{backend}~{upstream_id}`）。`~` 是 RFC 3986 的 unreserved 字符，能安全穿过每一跳。
不带标签的 id 仍按 `minimax_v3` 处理，所以老任务不会失效。

**时长会按后端钳制。** 各家的合法区间不同（H3 4–15、H3-Max 5–15、Wan 2–30、
Kling 3–15、Veo3 **只能 8**），而 new-api 对不在它配置表里的模型一律填 6 秒——
Veo3 会因此直接死在参数校验上。钳制逻辑有单测（`video_duration_test.go`）。

```bash
# Wan 3.0（非会员可用）
curl http://127.0.0.1:18080/v1/video_generation \
  -H 'content-type: application/json' \
  -d '{"model":"wan3.0-video","prompt":"a dewdrop sliding down a leaf","resolution":"480P","duration":2}'
# -> {"task_id":"wan_i2v~YZ8rbE12q0vD","base_resp":{"status_code":0,...}}
# 查询时把带标签的 id 原样传回去即可
curl "http://127.0.0.1:18080/v1/query/video_generation?task_id=wan_i2v~YZ8rbE12q0vD"
# -> {"status":"Success","file_id":"https%3A%2F%2Fcdn.hailuoai.video%2F...mp4"}
curl "http://127.0.0.1:18080/v1/files/retrieve?file_id=https%3A%2F%2Fcdn..."
# -> {"file":{"download_url":"https://cdn.hailuoai.video/..."}}
```

实测 Wan 3.0 480P/2s 出片 1.2 MB，真实 MP4。

> **Kling / Veo / Jimeng 是会员专属**，非会员账号会收到上游 403
> `permission_error`。这是账号权限，不是配置问题——本服务不预判，把上游的 403
> 原样透传，因为它的信息量比我们编的任何提示都大。


---

## 图像

图像和视频一样在云网关 `design.minimax.io` 上，同一个 `token`。区别是**它按 backend
（厂商）分端点，不按模型分**：

| backend | 提交 / 查询 | 目录里的模型 |
| --- | --- | --- |
| `openai` | `/api/v2/image/openai/{generate,tasks}` | `gpt-image-2.5-sunburst` / `-flare` / `gpt-image-2` |
| `nano_banana` | `/api/v2/image/nano_banana/{generate,tasks}` | `nano_banana_2_flash` / `nano_banana_2` |
| `seedream` | `/api/v2/image/seedream/{generate,tasks}` | `doubao-seedream-5-0-pro-260628` / `-4-5-251128` |
| `midjourney` | `/api/v1/image/midjourney/{generate,tasks}` | `midjourney-8.1` / `-8.2` / `-7` / `-niji7` |
| `kling` | `/api/v1/image/kling/{generate,tasks}` | 目录里没有（见下） |
| `qwen` | `/api/v2/image/qwen/{generate,tasks}` | 目录里没有（见下） |

每个 backend 的**请求体都不一样**（是从桌面客户端各 Service 类里逐个抠出来的），
所以代码里是按 backend 分支构造，而不是套一个通用形状：

```jsonc
// nano_banana
{"prompt","model_name","image_paths","aspect_ratio","resolution"}
// openai
{"prompt","model","size","image_paths","quality","n","background"?}
// qwen
{"prompt","image_paths","aspect_ratio"}
// seedream   （5-0 不能带 seed / guidance_scale，带了报错）
{"prompt","image_paths","aspect_ratio","model","size"?, "seed"?, "guidance_scale"?}
// kling
{"prompt","model_name","aspect_ratio","n", "image"?,"image_reference"?}
// midjourney（参数全走 prompt flag）
{"prompt":"... --ar 16:9 --v 8.2","params":{}}
```

对外本服务说 **OpenAI 的 images 形状**（`POST /v1/images/generations`），
因为那是 new-api 的 type-1 渠道认识的——于是一条普通 OpenAI 渠道指过来就能用。
OpenAI 端点是同步的，所以内部是「提交 + 轮询」拼出来的，最长等 5 分钟。

```bash
curl http://127.0.0.1:18080/v1/images/generations \
  -H 'content-type: application/json' \
  -d '{"model":"nano_banana_2_flash","prompt":"a sleepy ginger cat","n":1,"size":"1024x1024"}'
# -> {"created":...,"data":[{"url":"https://cdn.hailuoai.video/..."}]}
```

除了 OpenAI 的标准字段，还认这些扩展（都能省，省了就用目录里的默认值）：

| 字段 | 作用 |
| --- | --- |
| `aspect_ratio` | 比例，如 `16:9`。不给就从 `size` 推 |
| `resolution` | `1K` / `2K` / `4K`。不给就从 `size` 推 |
| `image_paths` / `image` | 参考图（URL 或 data URI），做图生图 |
| `response_format` | `url`（默认）或 `b64_json`（服务端代下再 base64） |
| `model_name` | 覆盖 backend 的上游模型名 |
| `stylize` / `chaos` / `weird` / `version` | 仅 midjourney，拼成 prompt flag |

实测：`nano_banana_2_flash` → 1.8 MB PNG；`doubao-seedream-4-5` → JPEG；
`midjourney-8.2` → **4 张** PNG（MJ 原生 4 宫格，new-api 会按 4 张计费）；
`gpt-image-2` + `b64_json` → 2.0 MB 解出来仍是合法 PNG。

> **`qwen` 和 `kling` 能路由但基本没用**：客户端这一版没在目录里注册它们的模型，
> 而 qwen 上游默认模型是 `qwen-image-edit`（图生图），纯文生图会被
> 上游以「参数组合不支持」拒掉。想用就显式传 `model_name`，但得自己知道
> 上游有哪些模型名。两个 backend 仍保留在路由表里，方便将来目录补上后直接用。
>
> **图生图的 `image_paths` 只吃 URL / data URI**。客户端是先调
> `/api/v1/files/upload` 把本地文件传上去再引用，这个上传链路没实现，
> 所以本地路径传进去会被上游拒。OpenAI 的 `/v1/images/edits`（multipart）也没做。

---

## 音频

三条独立的链路，都在 `design.minimax.io`，都用同一个 `token` 头：

| 能力 | 上游 | 形态 | 计费 |
| --- | --- | --- | --- |
| 语音合成 | `POST /api/v1/audio/tts` | **同步**，返回 `audio_url` | 约 3 额度/次 |
| 歌词 | `POST /api/v1/audio/lyrics/generate` | 同步，返回结构化歌词 | 免费 |
| 音乐 | `POST /api/v1/audio/music/minimax`（v2 异步） | 提交 + 轮询 | 约 23 额度/首 |

**只有 TTS 接进了 new-api**，因为它的 `/v1/audio/speech` 会原样透传音频字节，
正好对上。歌词和音乐 new-api 没有对应路由，只能直连本服务（`:18080`）。

### 音色映射

TTS 请求要 `voice_id`，而上游的 671 个音色没有一个是 OpenAI 那套名字，
所以 `audio.go` 里有一张 11 项的别名表，把 `alloy`/`nova`/`onyx` 等映射到
真实存在的 MiniMax 音色。**任何真实 `voice_id` 也能直接传**，整个目录都可达。

```bash
# 先看有哪些音色
curl "http://127.0.0.1:18080/v1/audio/voices?language=en&q=storyteller"

# 用 OpenAI 名字
curl -X POST http://127.0.0.1:18080/v1/audio/speech \
  -H 'content-type: application/json' \
  -d '{"model":"tts-1","input":"你好","voice":"nova"}' -o out.mp3

# 或用真实 id
curl -X POST http://127.0.0.1:18080/v1/audio/speech \
  -H 'content-type: application/json' \
  -d '{"model":"speech-2.8-hd","input":"你好","voice":"Chinese_wenrounvxing"}' -o out.mp3
```

> **别名表不能手写猜测的音色名。** 上游对未知 `voice_id` 的响应是
> **HTTP 500 + `code=2054 voice id not exist`**，从调用方看就是「TTS 接口挂了」，
> 很容易误判成路由或鉴权问题。而且上游的命名毫无规律可循——
> `English_CalmWoman` 在 Woman 前没有下划线，`English_Gentle-voiced_man`
> 带连字符——看起来「规范」的名字基本都是错的。
>
> 改了这张表就跑一下 `tools/check_voices.py`，它会把表里的每一项和线上目录对账：
>
> ```bash
> python tools/check_voices.py            # 校验映射表
> python tools/check_voices.py --list-en  # 列出全部英语音色
> python tools/check_voices.py --find calm
> ```
>
> 反过来，**不要**在服务里拿目录去校验用户传的 `voice_id`。目录不全：
> `Friendly_Person` 这类历史音色上游仍然认，但不在 671 条里。
> 拿目录当白名单会拒掉本来能用的音色。

---

## 接进 new-api

`tools/newapi_add_channel.py` 会往本机 new-api 里加三条渠道（幂等，重跑即更新）：

| profile | 渠道类型 | 用途 |
| --- | --- | --- |
| `llm` | 1（OpenAI 兼容） | 文本模型 + 音频模型（TTS 走 `/v1/audio/speech`） |
| `video` | 35（MiniMax / hailuo） | 视频模型，走 `/v1/video_generation` |
| `image` | 1（OpenAI 兼容） | 图像模型，走 `/v1/images/generations` |

```bash
python tools/newapi_add_channel.py --list          # 看现有渠道
python tools/newapi_add_channel.py --dry-run image # 预览
python tools/newapi_add_channel.py                 # 全部应用
```

> `image` 渠道的 `test_model` 是**故意留空**的：new-api 的渠道测试发的是 chat 请求，
> 而这几个模型只做图像，测试必然失败——不影响实际调用。

> 脚本同时处理了 POST 和 PUT 的**请求体形状差异**，这是个容易踩的坑：
> `POST /api/channel/` 要 `{mode, channel}` 包裹，而 `PUT /api/channel/` 直接把
> rawBody 解成 `PatchChannel`——渠道对象必须在**顶层**。用 POST 的形状去 PUT 会得到
> `{"message":"record not found"}`，看起来像 id 错了，其实是 body 没解出来。
>
> 三条渠道共用一个 `base_url`（`http://127.0.0.1:18080`），因为 new-api 是
> `baseURL + 原始请求路径` 直接拼接（`GetFullRequestURL`），不需要带 `/v1`。

new-api 有三条硬性门槛，脚本之外还需要处理：

1. **磁盘保护**：new-api 的性能监控默认阈值 95%，检测路径是
   `performance_setting.disk_cache_path`（空则回落 `os.TempDir()`，即 C 盘）。
   C 盘满到 95% 以上时**所有渠道**都会 503 `system_disk_overloaded`。
   用 `tools/newapi_option.py` 把缓存目录指到大盘即可。
2. **CPU 保护**：同族的 `monitor_cpu_threshold`（默认 90%）。编译、批量测试之类的
   瞬时负载会把它顶上去，表现为 503 `system_cpu_overloaded`。一般几秒就恢复，
   重试即可；要放宽就改 `performance_setting.monitor_cpu_threshold`。
3. **模型定价**：定价表按模型名精确匹配、没有通配，缺失就 `model_price_error`，
   请求根本发不出去。用 `tools/newapi_pricing.py` 补：
   - 文本模型走 `ModelRatio`（按 token 倍率）
   - 视频/图片类走 `ModelPrice`（按次，美元）

```bash
python tools/newapi_option.py get-prefix performance_setting
python tools/newapi_option.py set performance_setting.disk_cache_path 'E:\new-api-cache'
python tools/newapi_pricing.py set --ratio 0.15 --completion 4 gamma gamma_high
python tools/newapi_pricing.py setprice 0.3 MiniMax-Hailuo-2.3
python tools/newapi_pricing.py setprice 0.02 nano_banana_2_flash
```

> **`gpt-image-2` 是个例外，故意没给它加 `ModelPrice`。** 你的库里它已经有
> `ModelRatio=2.5`。new-api 的 `ModelPrice` 不止图像路径用——`ModelPriceHelper`
> （chat 路径）也优先读它，所以补上去会**连带改掉 chat 的计费方式**。
> 代价是经本服务生成的 `gpt-image-2` 只扣了 3 quota（≈免费）。如果你的
> `gpt-image-2` 只用于图像、没有别的渠道拿它当 chat 模型，可以放心补：
> `python tools/newapi_pricing.py setprice 0.06 gpt-image-2`。

调通后：

```bash
curl http://127.0.0.1:3000/v1/chat/completions \
  -H "Authorization: Bearer sk-<你的 new-api 令牌>" \
  -H 'content-type: application/json' \
  -d '{"model":"MiniMax-M3","messages":[{"role":"user","content":"你好"}]}'

curl http://127.0.0.1:3000/v1/video/generations \
  -H "Authorization: Bearer sk-<你的 new-api 令牌>" \
  -H 'content-type: application/json' \
  -d '{"model":"MiniMax-Hailuo-2.3","prompt":"...","duration":4,"size":"1280x720"}'
# 再 GET /v1/video/generations/{task_id} 查进度
# 完成后 GET /v1/videos/{task_id}/content 拿视频字节

curl http://127.0.0.1:3000/v1/images/generations \
  -H "Authorization: Bearer sk-<你的 new-api 令牌>" \
  -H 'content-type: application/json' \
  -d '{"model":"nano_banana_2_flash","prompt":"a sleepy ginger cat","n":1,"size":"1024x1024"}'
```

> 注意 new-api 的 `/v1/chat/completions` 不认 `users.access_token`（那是管理接口用的），
> 要用 `tokens` 表里的 key，前缀 `sk-`。

### 需要给 new-api 打的补丁（视频产物）

new-api 的 `hailuo` 适配器里有一处类型和上游对不上，**不修的话视频能生成、
但拿不到文件**：轮询明明返回 `SUCCESS / 100%`，`result_url` 却是指回
new-api 自己的 `http://localhost:3000/v1/videos/{id}/content`，
再取就是 `410 artifact_gone`。

根因在 `relay/channel/task/hailuo/models.go`：

```go
type FileObject struct {
    FileID int64 `json:"file_id"`   // 上游返回的是字符串 "L8ONzGzOpqzD"
}
```

适配器成功后要拿这个响应里的 `download_url` 当任务结果地址
（`buildVideoURL` → `GET {base}/v1/files/retrieve?file_id=X`）。
`int64` 反序列化字符串失败，而 `buildVideoURL` **把错误吞了**直接返回空串，
于是结果地址回落到自指的代理地址，内容代理再判定为环路 → 410。

修法（已应用在 `E:\new-api-main-copy-main (2)\new-api-main-copy-main`）：

```go
// LooseString 同时吃 JSON 字符串和数字
type LooseString string

func (s *LooseString) UnmarshalJSON(b []byte) error { /* trim 引号 */ }

type FileObject struct {
    FileID LooseString `json:"file_id"`
    ...
}
```

配套单测 `models_loose_test.go` 把两种形态都钉住了。
改完 `go build -o new-api.exe .` 重启即可（旧二进制备份在同目录
`new-api.exe.bak-20260919`）。

> 别改成 `string` —— 老部署返回的是数字，改成 `string` 会把另一种形态搞坏。
> 也别指望「反正是新任务才走这条路」：已存在的任务不会自愈，
> 它的 `private_data.result_url` 已经落库成环路地址了。

---

## 配置 `config.json`

跟 exe 同目录，首次启动自动生成。

```jsonc
{
  "listen": ":8080",
  "gateway": "https://design.minimax.io",   // 配置网关：拉模型目录
  "upstream": "https://hub.minimax.io",     // 模型网关：真正发推理请求
  "video_base": "https://design.minimax.io", // 媒体网关；留空则跟随 gateway
  "version_code": "3.0.16",                 // 上游硬性要求，缺了报 403
  "app_id": "3001",
  "device_platform": "desktop",
  "api_keys": [],                            // 空 = 不校验客户端；填了就要求 Bearer
  "default_model": "MiniMax-M3",
  "request_timeout_sec": 600,
  "tokens": [
    {
      "name": "local-557376998160859138",
      "token": "eyJhbGciOi...",              // MiniMax Design 的 access token
      "region": "overseas",
      "user_id": "557376998160859138",
      "expire_at": 1793210257
    }
  ]
}
```

号池调度：`least_inflight`。失败分三类处理：

- **凭证失效**（HTTP `401`，或 `403` 且响应体里出现 `invalid token` / `token expired`
  / `未登录` 之类的字样）→ 冷却 30 分钟，且惩罚**累加**。
- **积分耗尽**（余额不足 / `insufficient balance` / `quota exceeded` 之类）→
  冷却 15 分钟、**不累加**，并且**当场把请求转给下一个令牌**。
- **上游抖动**（传输错误 / 5xx）→ 固定 5 秒，**不累加**；而且池里只剩一个可用令牌时
  **完全不冷却**（没有别的号可换，罚它只是自己给自己降可用性）。

冷却原因会随 `/admin/api/state` 的 `pool[].cooling_reason` 返回
（`credential` / `billing` / `transient`），控制台直接显示出中文。

> 这里刻意**不**把 403 一律当凭证失效。网关的 403 是复用的：缺 `version_code`
> 是 403，账号没权限用某个模型也是 403（比如 `gpt-6-astra` 要会员）。
> 早期版本按 403 处理，结果一个没权限的模型就把整池令牌打进 30 分钟冷却，
> 对外表现为莫名其妙的 `no usable token in the pool`。
>
> 5xx 也一样：网关的 500 大多是**确定性的请求级拒绝**（模型不支持这组参数），
> 不是拥塞。早期版本对它做指数退避，于是「试一个不支持的模型」就能把服务
> 停 5s→10s→20s。现在这类错误只把错误原样返回给调用方。
>
> **积分耗尽单独成一类，是因为它曾经两者都不是。** 余额不足是 4xx 且不含凭证
> 关键字，于是落进「请求级错误」那条路——那条路**立即返回、不试下一个令牌**。
> 后果是：一个空号躺在健康的池子里，会白白吃掉本该由别的号处理的请求。
> 现在它会先冷却再把请求交出去。惩罚不累加也是有意的——令牌本身没坏，充值就能
> 恢复，让它等一场递增的 30 分钟惩罚说不过去。
>
> 池里只剩一个令牌时仍然不冷却，理由和 5xx 一样：把上游那句「余额不足」换成
> 我们自己编的「no usable token in the pool」是**信息量更少**。错误原样透传，
> 只是 type 标成 `insufficient_credit`，好让人一眼看出不是路由或鉴权的问题。
>
> 余额不足的识别靠 `billingMarkers`（`upstream.go`），和 `credentialMarkers`
> 刻意保持不相交：两边都不该误判成对方。
>
> 冷却卡住了可以 `POST /admin/api/pool/reset` 清掉，不用重启。

---

## 逆向结论

详细过程见 [`docs/reverse-engineering.md`](docs/reverse-engineering.md)，这里只放结论。

### 1. 客户端形态

Electron 应用，主进程代码在 `resources/app.asar`（47.8 MB / 608 个文件），
渲染层是 React，AI 运行时是内嵌的 **opencode**（Bun 编译的 `opencode.exe`），
另有本地 gateway（`:8001`）和 opencode runtime（`:4096`）。

```
E:\Minimax design\current\resources\
├── app.asar                  主进程 + 渲染层
├── opencode\opencode.exe     内嵌 AI agent 运行时
├── opencode-plugin-hilo\     opencode 插件（注入 X-Group-Id 等计费头）
├── gateway\dist\main.js      本地网关
└── conf\external_api_conf.yaml  视频/图像/语音等云端路由表
```

### 2. 凭据藏在哪

`%APPDATA%\@hilo\MiniMax Hub Global\hub-config-global.json`：

```json
{ "tokens": { "accessToken": "v2enc:<iv b64>:<tag b64>:<ciphertext b64>" } }
```

- 算法 **AES-256-GCM**，密钥是同目录的 `.token-key`（裸 32 字节）
- 解密出来是一个普通 HS256 JWT，`exp` 大约 40 天
- 三个坑：
  1. 字段名就叫 `token`，**不是** `Authorization`、**不是** `x-api-key`
  2. IV 是 **16 字节**；Go 的 `cipher.NewGCM` 只收 12 字节，得用 `NewGCMWithNonceSize`
  3. 缺 `version_code` 头会被上游 403 拒掉（`minimum supported version is "1.0.0"`）

### 3. 模型清单从哪来

客户端启动时 `GET {gateway}/api/v1/config`，带 `token` 头，返回 provider 矩阵：

```jsonc
{
  "provider": {
    "minimaxHub": { "npm": "@ai-sdk/anthropic", "options": {"baseURL": "https://hub.minimax.io/api/v1"},
                    "models": { "MiniMax-M3": {...} } },
    "gamma":      { "npm": "@ai-sdk/openai",    "options": {"baseURL": "https://hub.minimax.io/api/v1"},
                    "models": { "gamma_high": {...} } }
  }
}
```

`npm` 字段就是协议指纹：

| `npm` | 协议 | 路径 |
| --- | --- | --- |
| `@ai-sdk/anthropic` | Anthropic Messages | `POST /api/v1/messages` |
| `@ai-sdk/openai` | OpenAI **Responses** | `POST /api/v1/responses` |
| `@ai-sdk/google` | Google | `POST /api/v1/{model}:generateContent`（未定位） |

注意 gamma 用的是 **Responses API 不是 chat/completions**——
它的模型配置里带 `store:false` / `reasoningEffort` / `reasoningSummary` / `include`，
这些是 Responses 专有参数。直接打 `/api/v1/chat/completions` 是 404。

模型和协议是**锁死**的：`gamma_high` 打 `/messages` 报 400，`MiniMax-M3` 打 `/responses` 也报 400。

### 4. 最小可用请求

```bash
curl https://hub.minimax.io/api/v1/messages \
  -H 'token: <accessToken>' \
  -H 'version_code: 3.0.16' \
  -H 'content-type: application/json' \
  -d '{"model":"MiniMax-M3","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}'
```

就这两行头，别的都不需要。`token` 少了 → `401 token 为空`；
`version_code` 少了 → `403 client version_code is required`。

### 5. 账号体系：桌面端用的就是网页端那套

桌面端的登录**不是在客户端里做的**，它把用户导到网页登录页，带两个参数：

```
https://design.minimax.io/login?device_id=<设备id>&version_code=3.0.16     # 海外
https://design.minimax.cn/login?...                                        # 国内
```

（`HUB_WEB_LOGIN_DOMAINS`，dev/test 走 `hub-test.xaminim.com`。）

所以**没有"桌面端账号"和"网页端账号"之分**——`design.minimax.io` 上注册的账号就是它，
登录成功后网页侧把 access token 交回客户端，客户端加密存进
`hub-config-global.json`，也就是本服务读的那个。

**积分也是共享的**：应用内文案是「积分与海螺账号共享」，海螺（Hailuo）网页端/手机端的
贝壳余额还能按比例单向转入 MiniMax Design（`1 贝壳 = N 积分`，转入后有效期一年，
仅订阅积分与充值积分可转入，免费积分不能转）。

### 6. 积分：新账号的赠送积分只活 3 天

积分按来源分类型（`CreditType` 枚举，网关只回数字，得照着表读）：

| 值 | 类型 | 有效期 |
| --- | --- | --- |
| 0 | 充值积分（单独购买） | **一年** |
| 1 | 订阅积分 | **1 个月**，每月重置 |
| 2 | 赠送积分（新用户登录奖励 + 活动） | **3 天** |
| 4 / 5 / 6 | 创作者奖励 / 活动 / 登录奖励 | — |
| 7 | 团队转入 | 随原有效期 |

**新账号确实有积分**——新用户登录奖励，走 `CREDIT_TYPE_BONUS`。
但它是 3 天寿命的。另外还有一个视频免费试用：`hailuo03-video-trial`，
3 次，且**只限 `MiniMax-H3-Max`、480P/768P、文生视频或首尾帧**（会员不适用）。

```bash
python tools/check_credit.py                  # 查 config.json 里每个令牌
python tools/check_credit.py --token <jwt>    # 查还没加进池子的令牌
```

> **加号之前先查积分，别只查令牌。** 令牌能撑 40 天，赠送积分只活 3 天。
> 一个"有效但零积分"的账号会让所有生成失败，而报错看起来像服务坏了——
> 排查时容易往路由、鉴权方向找，其实答案是账号空了。

---

## 代码结构

```
main.go        启动、配置加载、-import-token（v2enc 解密）
config.go      config.json 读写
jwt.go         从 access token 里解 user.id / exp
catalog.go     模型目录：调 /api/v1/config 建路由表，失败回落内置表
pool.go        令牌池：least-inflight + 指数退避冷却
metrics.go     请求指标：计数器 + 最近请求环形列表 + /v1 埋点中间件
credit.go      账号积分：余额 / 积分类型 / 到期 / 免费试用（管理台按需查询）
oai.go         OpenAI 请求/响应类型
anth.go        Anthropic Messages 适配器（请求构造 + SSE 解析）
resp.go        OpenAI Responses 适配器（请求构造 + SSE 解析）
video.go       视频：MiniMax 平台形状门面 + 模型/分辨率/比例映射
image.go       图像：OpenAI images 形状门面 + 按 backend 分支的请求体
upstream.go    上游传输（POST/GET，hub 与 media 两个 host）+ SSE 读取器
server.go      HTTP 路由、流式/非流式渲染、管理接口
webui.go       内嵌管理台（go:embed web/，逐个文件提供）
web/index.html 管理台页面骨架
web/app.css    设计令牌与组件样式（复刻 grok2api 的 oklch 体系）
web/app.js     外壳：路由、API 客户端、对话框、菜单、主题、侧边栏
web/pages.js   仪表盘 / 模型 / 号池 / 文档 / 设置 / 关于
web/playground.js 调试台（聊天·图像·视频·语音）
tools/         逆向脚本 + new-api 渠道/定价/配置工具（check_credit.py 查账号积分与试用）
```

## 已知限制

- `omega-3.1-pro` 路由未定位，返回 501
- **Kling / Veo / Jimeng 视频要会员订阅**，非会员账号会收到上游 403
  `permission_error`。路由和请求体都已实现并验证到位（参数校验能过），只差权限
- 视频的 `callback_url` 被忽略（上游不支持回调，只能轮询）
- **视频按次定价是平价的，不随时长变**。new-api 的任务计费表达式需要适配器实现
  `TaskUsageFactsProvider`，而 `hailuo` 适配器没有（只有 `jsplugin` 有），所以
  时长计费走不通。后果：Wan 3.0 的 2 秒和 30 秒同价。想改的话得给那个适配器
  补一个 usage-facts 实现
- 图像只做了文生图 + URL/data-URI 参考图；本地文件上传（`/api/v1/files/upload`）
  与 OpenAI 的 `/v1/images/edits`（multipart）未做
- **音乐 / 歌词只有 2api 直连，没接进 new-api**——new-api 的中继路由里根本没有
  音乐这类端点（只有 `audio/speech` 和转写）。要用就直连 `127.0.0.1:18080`
- 语音克隆（`/api/v1/audio/voice_clone`）、音色设计（`voice_design`）、人声分离
  （`voice_isolation`）、`tts/batch` 都没做，请求体没逆
- **TTS 音色别用目录做白名单**。上游音色目录有 671 项，但 `Friendly_Person` 这类
  名字能直接用却**不在目录里**——拿目录校验会误判成非法。反过来目录里能查到的
  也未必都能用。映射表改完跑 `tools/check_voices.py` 对账即可
- `qwen` / `kling` 两个图像 backend 能路由但目录里没有模型，基本用不上
- `gpt-image-2` 在本服务里近乎免费（见「接进 new-api」一节的说明）
- 令牌有效期约 40 天，过期后需要重新 `-import-token`
- **额度会过期，比令牌早得多**。`python tools/check_credit.py` 或号池页的「查询积分」
  能一次看清余额、积分类型和到期时间。实测这个号是 **赠送积分**（新用户登录奖励，
  天生 3 天寿命），2026-09-22 01:57（北京时间）到期。到期后令牌还有效，但所有生成
  都会失败——排查时先看这里，别去翻配置。买来的积分活得更久：充值一年、订阅一个月。
  一个积分为零的号现在会被冷却 15 分钟并把请求转给别的号，但**它不会自己恢复**，
  充值或换号仍然要靠人。
- 号池没有自动刷新令牌的机制——MiniMax Design 的刷新流程还没逆
- 计费头（`X-Group-Id` 等）没带；目前看上游不强制，但如果哪天开始校验
  就会 4xx，届时需要接本地 gateway 的 request-group 接口
- `gpt-6-astra` 需要有效会员订阅，非会员账号会收到上游 403
