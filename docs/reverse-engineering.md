# MiniMax Design 逆向记录

对象：**MiniMax Design** 桌面版（Windows），`@hilo/desktop` **3.0.16**，海外区（`com.minimax.hub.global`）。
目标：把它内置的模型反代成 OpenAI 兼容 API。

时间：2026-09-19。

---

## 0. 结论速览

| 问题 | 答案 |
| --- | --- |
| 凭据在哪 | `%APPDATA%\@hilo\MiniMax Hub Global\hub-config-global.json` → `tokens.accessToken` |
| 怎么加密的 | AES-256-GCM，`v2enc:<iv>:<tag>:<ct>`，密钥是同目录 `.token-key`（裸 32 字节） |
| 模型清单哪来 | `GET https://design.minimax.io/api/v1/config`，头 `token: <accessToken>` |
| 推理打哪 | `POST https://hub.minimax.io/api/v1/messages`（Anthropic）/ `/api/v1/responses`（OpenAI Responses） |
| 鉴权头 | `token: <accessToken>` + `version_code: 3.0.16`。**不是** `Authorization`，**不是** `x-api-key` |

---

## 1. 定位安装位置

AppData 里只有一个 `MiniMax` 目录，且是空的壳：

```
C:\Users\juang\AppData\Local\MiniMax\
├── install-ownership\com.minimax.hub.global\record.json   ← 真正的线索
├── install-telemetry\
└── velopack-generations\com.minimax.hub.global\           ← 空
```

`record.json` 是 Velopack（.NET 的安装器框架）写的安装记录：

```json
{"appId":"com.minimax.hub.global","rootPath":"E:\\Minimax design","createdAt":"2026-09-18T17:55:44.991Z"}
```

同目录的 `user-data-roots.json` 给出了数据目录：

```json
{"roots":[
  "C:\\Users\\juang\\AppData\\Roaming\\@hilo\\MiniMax Hub Global",
  "E:\\MiniMax Design (3) Data",
  ...]}
```

安装目录结构：

```
E:\Minimax design\
├── MiniMax Design.exe          启动器（631 KB）
├── Update.exe                  Velopack 更新器（4 MB）
└── current\                    当前版本
    ├── MiniMax Design.exe      Electron 主体（239 MB）
    └── resources\
        ├── app.asar            47.8 MB，608 个文件  ← 主战场
        ├── opencode\opencode.exe
        ├── opencode-plugin-hilo\dist\hilo.js
        ├── gateway\dist\main.js  （16 MB 本地网关）
        └── conf\external_api_conf.yaml
```

---

## 2. 解 app.asar

asar 格式：16 字节头 + pickle 编码的 JSON 目录树 + 文件数据。

**踩的坑**：按 Electron 官方文档的说法，offset 12 是「头总长」，offset 16 是 pickle，
pickle 开头还有 4 字节字符串长度。实际不是——`offset 12` 就是 **JSON 字符串长度本身**，
JSON 从 `offset 16` 直接开始。按文档写会少读 4 字节，`JSON.parse` 直接炸。

正确的读法：

```js
const head = Buffer.alloc(16);
fs.readSync(fd, head, 0, 16, 0);
const jsonLen = head.readUInt32LE(12);          // ← 不是 8，不是 16
const jb = Buffer.alloc(jsonLen);
fs.readSync(fd, jb, 0, jsonLen, 16);            // ← JSON 就在 16
const tree = JSON.parse(jb.toString('utf8'));
const dataStart = 16 + jsonLen;
```

完整脚本见 [`tools/extract-asar.js`](../tools/extract-asar.js)。

`package.json` 交代了身份：

```json
{"name":"@hilo/desktop","version":"3.0.16",
 "hiloRelease":{"region":"overseas","appId":"com.minimax.hub.global",
                "productName":"MiniMax Design",
                "updateBaseUrl":"https://file.cdn.minimax.io/public/minimax-hub/release/overseas"}}
```

---

## 3. 凭据

### 3.1 存储形态

`%APPDATA%\@hilo\MiniMax Hub Global\hub-config-global.json`：

```json
{
  "_version": 30,
  "tokens": {
    "accessToken": "v2enc:XNW53L1qwj7XzemJJW1r9A==:o8s6fbjZTxNbENJFOWPrTA==:AbZUFvLW+jeR..."
  },
  "user": { "userID": "557415087965073415", "userName": "林汐音" },
  "lkgProviderConfig": "v2enc:...",
  "deviceId": "d78498e5-c20d-45eb-b978-d23e9df3e65d"
}
```

同目录 `.token-key` 是 32 字节裸密钥：

```
00000000: 9d00 bb70 63e9 8ecc 55b3 c123 791f e635  ...pc...U..#y..5
00000010: e498 a26b 6a89 04af d9dd 0ba0 a4a4 6dbd  ...kj.........m.
```

### 3.2 解密实现

在 `out/main/chunks/index-C0Ixo6UY.js` 找到加解密实现（约 41486 行起）：

```js
const ENCRYPTED_V2_PREFIX = "v2enc:";
const ALGORITHM = "aes-256-gcm";
const IV_LENGTH = 16;
const KEY_LENGTH = 32;

function encryptValue(plaintext) {
  const key = getEncryptionKey();                    // 读 .token-key
  const iv = randomBytes(IV_LENGTH);
  const cipher = createCipheriv(ALGORITHM, key, iv);
  const encrypted = Buffer.concat([cipher.update(plaintext, "utf8"), cipher.final()]);
  const authTag = cipher.getAuthTag();
  return `${ENCRYPTED_V2_PREFIX}${iv.toString("base64")}:${authTag.toString("base64")}:${encrypted.toString("base64")}`;
}

function decryptValue(stored) {
  const parts = stored.slice(ENCRYPTED_V2_PREFIX.length).split(":");
  const [ivB64, authTagB64, ciphertextB64] = parts;
  const decipher = createDecipheriv(ALGORITHM, key, Buffer.from(ivB64, "base64"));
  decipher.setAuthTag(Buffer.from(authTagB64, "base64"));
  return Buffer.concat([decipher.update(Buffer.from(ciphertextB64, "base64")),
                        decipher.final()]).toString("utf8");
}
```

**坑**：`IV_LENGTH = 16`。Go 的 `cipher.NewGCM()` 硬性要求 nonce 是 12 字节，
直接传 16 字节会 `panic: crypto/cipher: incorrect nonce length given to GCM`。
要用 `cipher.NewGCMWithNonceSize(block, len(iv))`。

解密出来是一个普通 HS256 JWT（签名没法验，也不需要验）：

```json
{"exp":1793210257,
 "user":{"id":"557376998160859138","name":"","avatar":"","deviceID":"","isAnonymous":false}}
```

`exp` 距签发约 40 天。

### 3.3 顺带解出的 `lkgProviderConfig`

`lkgProviderConfig` 是「上次成功的 provider 配置」，同样加密。解密后能看到完整的
provider / 模型矩阵——这是后面所有推理的起点。

注意源码里 `saveLkgProviderConfig` 会先调 `stripProviderSecrets()`，
把 `options.apiKey` 和 `options.headers` 删掉再落盘。所以本地这份**没有密钥**，
密钥只存在于运行时内存里。

---

## 4. 模型清单从哪来

`out/main/chunks/python-runtime-g_0Tz6er.js:4710`：

```js
async function fetchRemoteProviderConfigResult(cloudGatewayUrl, token, logger, options) {
  const url = `${cloudGatewayUrl}/api/v1/config`;
  const headers = { ...(token ? { token } : {}) };
  const resp = await fetch(url, { headers });
  ...
}
```

网关地址在 `out/main/chunks/js-yaml-B0IoXaZA.js:334`：

```js
const CLOUD_GATEWAY_URLS = {
  domestic: { dev: "https://hub-pre.xaminim.com", prod: "https://design.minimax.cn" },
  overseas: { dev: "https://hilo-test.xaminim.com", prod: "https://design.minimax.io" }
};
```

所以海外区就是 `GET https://design.minimax.io/api/v1/config`，头 `token: <accessToken>`。

实测返回 200 / 8029 字节，provider 矩阵：

```jsonc
{
  "model": "minimaxHub/MiniMax-M3",
  "enabled_providers": ["minimaxHub", "alpha", "gamma", "omega"],
  "agent_model": { "media-agent": "gamma/gamma_high", "executor": "gamma/gamma_mid" },
  "provider": {
    "minimaxHub": { "npm": "@ai-sdk/anthropic", "options": {"baseURL": "https://hub.minimax.io/api/v1"},
                    "models": { "MiniMax-M2.7": {...}, "MiniMax-M3": {...} } },
    "alpha":      { "npm": "@ai-sdk/anthropic", "options": {"baseURL": "https://hub.minimax.io/api/v1"},
                    "models": { "alpha": {...}, "alpha_high": {...} } },
    "gamma":      { "npm": "@ai-sdk/openai",    "options": {"baseURL": "https://hub.minimax.io/api/v1"},
                    "models": { "gamma": {...}, "gamma_high": {...}, "gamma_mid": {...}, "gpt-6-astra": {...} } },
    "omega":      { "npm": "@ai-sdk/google",    "options": {"baseURL": "https://hub.minimax.io/api/v1"},
                    "models": { "omega-3.1-pro": {...} } }
  }
}
```

**`npm` 字段就是协议指纹**，`baseURL` 就是主机。这套映射直接驱动了 2api 的路由表。

---

## 5. 鉴权方式：一步步撞出来的

先猜最常见的两种，都不对：

| 尝试 | 请求头 | 结果 |
| --- | --- | --- |
| A | `x-api-key: <token>` + `anthropic-version` | `401 {"message":"token 为空"}` |
| B | `Authorization: Bearer <token>` | `401 {"message":"token 为空"}` |

`token 为空` 这个中文报错很关键——它说的是「token 这个字段是空的」，
暗示上游期望的是一个**字面叫 `token` 的头**（跟 `/api/v1/config` 一样）：

| 尝试 | 请求头 | 结果 |
| --- | --- | --- |
| C | `token: <token>` | `403 client version_code is required; minimum supported version is "1.0.0"` |

方向对了。日志里客户端发的 query string 带了 `version_code=3.0.16`，补上：

| 尝试 | 请求头 | 结果 |
| --- | --- | --- |
| D | `token` + `version_code: 3.0.16` | **200，真实补全** |

最终确认的最小头集就是这两个（`content-type` 由 curl/SDK 自带）。

```bash
curl https://hub.minimax.io/api/v1/messages \
  -H 'token: eyJhbGciOi...' \
  -H 'version_code: 3.0.16' \
  -H 'content-type: application/json' \
  -d '{"model":"MiniMax-M3","max_tokens":64,"messages":[{"role":"user","content":"say hi"}]}'
```

```json
{"id":"06fcb14b37c459237f6eabedb0044892","type":"message","role":"assistant",
 "model":"MiniMax-M3","content":[{"text":"Hi there! How can I help you today?","type":"text"}],
 "usage":{"input_tokens":37,"output_tokens":11,"cache_read_input_tokens":128},
 "stop_reason":"end_turn","base_resp":{"status_code":0,"status_msg":""}}
```

---

## 6. 协议矩阵：模型和协议是锁死的

先测 `/chat/completions` → **404**。这不是 OpenAI 的 chat 端点。

回头看 gamma 的模型配置：

```jsonc
"gamma_high": { "options": { "store": false, "reasoningEffort": "medium",
                             "reasoningSummary": "auto",
                             "include": ["reasoning.encrypted_content"] } }
```

`store` / `reasoningEffort` / `reasoningSummary` / `include` 全是 **OpenAI Responses API**
的专有参数。所以 `@ai-sdk/openai` 在这里走的是 `/responses`：

| 路径 | 结果 |
| --- | --- |
| `/api/v1/chat/completions` | 404 |
| `/api/v1/responses` | **200**，标准 Responses 事件流 |

### 6.1 交叉测试

拿全部 9 个模型 × 2 个端点跑一遍：

```
--- POST /api/v1/messages (Anthropic) ---
200  MiniMax-M2.7      200  MiniMax-M3      200  alpha       200  alpha_high
400  gamma             400  gamma_high      400  gamma_mid   400  gpt-6-astra
400  omega-3.1-pro

--- POST /api/v1/responses (OpenAI Responses) ---
400  MiniMax-M2.7      400  MiniMax-M3      400  alpha       400  alpha_high
200  gamma_high
400  omega-3.1-pro
```

**完全互斥**——每个模型只认自己那一个协议，打错了统一回
`{"type":"error","error":{"type":"client_error","message":"Request failed. Please try again later or contact support."}}`
（这个报错信息本身没有任何诊断价值，纯属噪音）。

### 6.2 Google 协议没定位到（已知缺口）

`omega-3.1-pro`（= Gemini 3.1 Pro Preview）声明为 `@ai-sdk/google`。
从 `opencode.exe` 里抠出 SDK 的 URL 模板：

```js
url: `${this.config.baseURL}/${OQ(this.modelId)}:generateContent`
```

即 `https://hub.minimax.io/api/v1/omega-3.1-pro:generateContent`。试过的组合全 404：

```
/api/v1/models/omega-3.1-pro:generateContent
/api/v1/models/omega-3.1-pro:streamGenerateContent?alt=sse
/api/v1/omega-3.1-pro:generateContent
/api/v1/omega-3.1-pro:generateContent?alt=sse
/api/v1/v1beta/models/omega-3.1-pro:generateContent
/api/v1beta/models/omega-3.1-pro:generateContent
/api/v1beta/omega-3.1-pro:generateContent
/v1beta/models/omega-3.1-pro:generateContent
/api/v1/gemini/models/omega-3.1-pro:generateContent
/api/v1/omega/models/omega-3.1-pro:generateContent
/api/v1/google/generateContent
```

大概率是 opencode 在运行时对 google provider 的 baseURL 做了改写（比如补 `v1beta`），
或者这条路由根本不在这个网关上。**没有继续深挖**，2api 里明确返回 501 而不是假装支持。

想补上的话，最直接的路子是给 opencode 子进程挂 `HTTPS_PROXY` 抓一次真实请求，
或者让 App 实际用一次 omega 然后翻 opencode 的 debug 日志。

---

## 7. 流式格式

两个协议都是标准 SSE，直接透传解析即可。

**Anthropic** `/api/v1/messages`（`stream: true`）：

```
event: message_start
data: {"type":"message_start","message":{"id":"...","usage":{"input_tokens":0,...}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"1"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":N}}

event: message_stop
```

工具调用是 `content_block_start` 里 `content_block.type == "tool_use"`，
后面跟 `input_json_delta` 的 `partial_json` 分片。

**OpenAI Responses** `/api/v1/responses`：只有 `data:` 行，没有 `event:` 行。
事件类型在 JSON 的 `type` 字段里：`response.created`、`response.output_item.added`、
`response.output_text.delta`、`response.reasoning_summary_text.delta`、
`response.function_call_arguments.delta`、`response.completed`。

`reasoning_summary_text.delta` 就是推理内容，2api 把它映射成 OpenAI 生态里常见的
`reasoning_content` 字段。

---

## 8. 客户端运行时（旁证）

opencode 的日志确认了整条链路：

```
message=stream providerID=gamma modelID=gamma_high session.id=ses_... agent=media-agent
message="llm runtime selected" llm.runtime=ai-sdk llm.provider=gamma llm.model=gamma_high
```

配置通过 `OPENCODE_CONFIG_CONTENT` 环境变量注入，主进程会把它外化成临时文件
（`%TEMP%\hilo-opencode-config-<pid>-<uuid>.json`）来缩小 env 体积，退出时删掉。
想看内容可以在 App 运行时读那个进程的环境块，或者改 `cleanupConfigFile` 前抢一份。

opencode 插件 `hilo.js` 的 `chat.headers` 钩子会往每次模型请求里塞：

| 头 | 内容 |
| --- | --- |
| `X-Group-Id` | 从本地 gateway `:8001` 的 request-group 接口拿的计费分组 |
| `x-hilo-chat-turn-id` | 本轮对话 ID |
| `X-Hilo-Attachment-Refs` | 附件引用 |
| `x-hilo-model-trace-context` | 模型埋点 |

**2api 没有带这些头**，目前实测上游不强制。如果哪天开始校验，需要接
`http://127.0.0.1:8001` 的 request-group 接口补上。

---

## 9. 云端媒体路由（视频 + 图像 + TTS 已实现）

`resources\conf\external_api_conf.yaml` 列了全部云端能力（视频 / 图像 / 语音 / 音乐），
走 `cloud_gateway`：

```yaml
cloud_gateway:
  video:
    minimax:    { generate: /api/v1/video/minimax/generate, query: /api/v1/video/minimax/tasks }
    minimax_v3: { generate: /api/v1/video/minimax-v3/generate, ... }
    kling:      { generate: /api/v1/video/kling/generate, ... }
  image:
    nano_banana: { generate: /api/v1/image/nano_banana/generate,
                   generate_v2: /api/v2/image/nano_banana/generate, query_v2: ... }
    openai:      { generate_v2: /api/v2/image/openai/generate,   query_v2: ... }
    qwen:        { generate_v2: /api/v2/image/qwen/generate,     query_v2: ... }
    seedream:    { generate_v2: /api/v2/image/seedream/generate, query_v2: ... }
    kling:       { generate: /api/v1/image/kling/generate,       query: ... }
    midjourney:  { generate: /api/v1/image/midjourney/generate,  query: ... }
  speech:
    minimax: { tts: /api/v1/audio/tts, voice_clone: /api/v1/audio/voice_clone,
               speech_model: speech-2.8-hd }
  music:
    minimax: { generate: /api/v1/audio/music/minimax, lyrics: /api/v1/audio/lyrics/generate }
```

> yaml 里的 `generate_v2` / `query_v2` 是**默认值**，真实路径由代码里的
> `str3(raw, key, default)` 取——所以 yaml 没写的 key 不代表没有那个端点。

### 9.1 媒体模型目录（新的，不在 `/api/v1/config` 里）

文本模型走 `/api/v1/config`，但媒体模型是**另一个端点**：

```
GET https://design.minimax.io/api/v1/models/config   （头：token）
→ { textModels: [...], videoModels: [11 个], imageModels: [...], audioModels: [...] }
```

`videoModels[]` 每项带 `id` / `backend` / `model_name` 和 `params`
（`ratio` / `resolution` / `duration` / `generate_audio` / `prompt_expansion_mode`
各自有 `default` 和 `options`），所以参数校验可以在本地做，不用撞上游。

### 9.2 视频端点：11 个模型、6 个 backend

**和 LLM 不是同一个 host**：LLM 在 `hub.minimax.io`，媒体在 `design.minimax.io`。
**但鉴权是同一套**：`token: <jwt>` + `version_code`，不需要别的头。

`minimax_v3`（H3 线）三个端点：

```
POST {media}/api/v1/video/minimax-v3/generate       -> {task_id, base_resp}
GET  {media}/api/v1/video/minimax-v3/tasks/{id}     -> {task_id, file_id, status, base_resp}
GET  {media}/api/v1/video/minimax/files/{file_id}   -> {file: {download_url, file_id}}
```

三个反直觉的地方：

1. **查询是路径参数**（`/tasks/{id}`、`/files/{id}`），不是 query string。
   写成 `?task_id=` 一律 404 —— 极容易误判成「接口不存在」，我在这上面绕了很久。
2. **纯文本生成必须显式给 `ratio`，且不能是 `adaptive`**。不给的话上游会返回
   「分辨率/比例校验失败」并**自动退还额度**（`refund_status` 字段）。平台请求体里
   没有 ratio，得从 `size`（`1280x720`）推导。
3. **状态字大小写不同**：上游是 `processing/success/fail`，MiniMax 平台要的是
   `Preparing/Queueing/Processing/Success/Fail`。

**其余 5 个 backend 的路径**（`external_api_conf.yaml` 的 `cloud_gateway.video`）：

| backend | 提交 | 查询 |
| --- | --- | --- |
| `wan_i2v` | `/api/v1/video/wan3/generate` | `/api/v1/video/wan3/tasks/{id}` |
| `veo3` | `/api/v1/video/veo3/generate?model={model}` | `/api/v1/video/veo3/tasks/{id}` |
| `kling` | `/api/v1/video/kling/generate` | `/api/v1/video/kling/tasks/{id}` |
| `kling_omni` | `/api/v1/video/kling-omni/generate` | `/api/v1/video/kling-omni/tasks/{id}` |
| `kling`（avatar） | `/api/v1/video/kling/avatar` | `/api/v1/video/kling/avatar/{id}` |
| `kling`（motion） | `/api/v1/video/kling/motion-control` | `/api/v1/video/kling/motion-control/{id}` |
| `jimeng` | `/api/v1/video/jimeng/generate` | `/api/v1/video/jimeng/tasks/{id}` |

四个坑：

1. **`backend` 字段不足以决定端点**。Kling 三个模型在目录里都是 `backend: "kling"`，
   实际走三组不同路径，靠 **model id** 分（客户端源码里就是
   `if (modelName === "kling-v3-omni") return this.generateOmniVideo(...)`）。
   照抄 `backend` 会把 avatar 的请求发到 `/kling/generate` 上去。
2. **只有 `minimax_v3` 有 `file_id`**。其余后端在任务结果里直接给成品 CDN 地址
   （`output.video_url` / `data.video_result.videos[0].url` / `data.task_result.videos[0].url`），
   位置各家不同。平台形状只有一个产物槽，所以要把 URL 编码后塞进 `file_id`。
3. **Veo3 的模型名在 query string 里，不在 body 里**，而且 body 是 Google 风格的信封
   （`{instances: [{prompt}], parameters: {...}}`），不是扁平字段。
4. **时长区间各家不同，且 Veo3 只能 8 秒**。见 `params.duration.options`：
   H3 4–15、H3-Max/Turbo 5–15、Wan 2–30、Kling 3–15、Veo3 固定 8。
   注意 H3 的判定不能写成 `strings.Contains(model, "Max")` —— 厂商前缀是
   **MiniMax**，那个子串对每个 H3 模型都成立（这个 bug 是单测抓出来的）。

**会员墙**：`veo3` / `kling` / `jimeng` 的**全部**模型和 `gpt-6-astra` 一样，
非会员账号在**参数校验之后**收到 403 `permission_error`
（`video generation is members-only; please upgrade membership and try again`）。
判据是「空 body 也返回 403 而不是参数错误」——说明权限检查在参数校验之前或独立于它。
`wan3` 和 `minimax_v3` 不需要会员。

### 9.3 图像端点（按 backend 分，不按模型分）

图像和视频一样在 `design.minimax.io`、同一个 `token`，但组织方式不同：
**端点属于 backend（厂商），不属于模型**。目录里的 `imageModels[].backend`
就是选端点的键。

```
POST {media}/api/v2/image/{backend}/generate      -> {task_id, status, base}
GET  {media}/api/v2/image/{backend}/tasks/{id}    -> {status, image_url, width, height, base}
```

`v2` 这套是异步的。`/api/v1/image/{backend}/...` 也有一套：

| backend | 路径 | 备注 |
| --- | --- | --- |
| `openai` | v2 | |
| `qwen` | v2 | |
| `seedream` | v2 | |
| `nano_banana` | v2 **和** v1 | v1 是**同步**的，直接返回 `image_url` |
| `kling` | v1 | 响应把 task_id 藏在 `data.task_id` |
| `midjourney` | v1 | 参数全走 prompt flag |

**每个 backend 的请求体都不一样**，只能逐个从客户端的 Service 类里抠：

```js
// NanoBananaService.buildBananaBody
{prompt, model_name, image_paths, aspect_ratio, resolution}
// OpenAIService.buildOpenAIBody   —— size 由 resolveSize(model, resolution, aspect_ratio, size) 算
{prompt, model, size, image_paths, quality, n, background?}
// QwenService.buildQwenBody
{prompt, image_paths, aspect_ratio}
// SeedreamService.buildSeedreamBody
{prompt, image_paths, aspect_ratio, model, size?, seed?, guidance_scale?}
//   ↑ 只有非 5-0 才带 seed/guidance_scale，5-0 带了报错
// KlingService.submitLegacyImage
{prompt, model_name, aspect_ratio, n, image?, image_reference?}
// MidjourneyService.submitMidjourney
{prompt: "<prompt> --ar 16:9 --stylize N --chaos N --weird N --v 8.2", params: {}}
```

响应解析反而都一样：取 `image_url`（midjourney 优先取 `image_urls[]`，一次 4 张）。

Midjourney 的版本号不是请求字段，是**拼进 prompt 的 flag**，而且由
`resolveMidjourneyVersion(modelId, version)` 从**目录 id** 推：

```js
midjourney-niji7 -> "--niji 7"    midjourney-7   -> "--v 7"
midjourney-8.1  -> "--v 8.1"      midjourney-8.2 -> "--v 8.2"
```

另外 `appendVersion` 会先检查 prompt 里有没有 `--v/--version/--niji`，
已经有就不重复加。

### 9.4 音频端点（TTS 已实现，音乐/歌词直连）

同一个 host、同一个 `token` 头。`external_api_conf.yaml` 里的 `speech:` / `music:`
两段列了十几条路径，实际有用的这几条：

```
POST {media}/api/v1/audio/tts              -> {audio_url, subtitle_url, extra_info}  同步
GET  {media}/api/v1/audio/voices           -> {voices:[671 条]}                      同步
POST {media}/api/v1/audio/lyrics/generate  -> {lyrics, title, style_tags}            同步、免费
POST {media}/api/v2/audio/music/minimax    -> {task_id}                              异步
GET  {media}/api/v2/audio/music/minimax/{id} -> {audio_url, ...}
```

四个值得记的点：

1. **TTS 是同步的**，直接返回 `audio_url`（CDN 上的 mp3），不用轮询。
   唯一支持的模型是 `speech-2.8-hd`（客户端里写死，没有 turbo 变体）。
   一次约 3 额度。
2. **`voice_id` 不能用音色目录做白名单**。目录 `/audio/voices` 有 671 项，
   但 `Friendly_Person` 这种名字能直接用、却**不在目录里**。反过来，
   目录里查得到的不一定都能用（`Young_Female` 就不存在 → `2054 voice id not exist`）。
   拿目录校验会两头误判。真实有效的英文音色是 `English_*` 前缀那 118 个。
3. **空 text 会先于音色校验失败**，所以没法用「空 body 探音色有效性」这招——
   要么真发一次（花钱），要么拿线上目录对账。
4. **音乐是 v2 异步**（`v1` 也存在且同步，但客户端走 v2）。
   歌词接口**完全免费且同步**，空 body 都能返回一首完整歌词——
   这是最省事的连通性探针。

### 9.5 探测媒体路由的正确姿势

**别用 GET 探**：这些路由都是 POST-only，Express 对未匹配的 GET 一律 404，
和「路由不存在」长得一模一样，探不出任何信息。

**用空 body POST**：上游会因校验失败拒绝，**不扣额度**（失败会
`refund_status: refunded`）。响应形状本身就是情报：

```
POST /api/v2/image/seedream/generate  -d '{}'
-> 200 {"task_id":"dADqOor4VAbj","status":1,"base":{"message":"success"}}
   任务建了但立刻失败（prompt 为空），查询后确认 refunded
POST /api/v2/image/openai/generate    -d '{}'
-> 500 {"type":"error","error":{"message":"model gpt-image-2 does not support
        this parameter combination"}}
   同步校验，连任务都没建
```

对照一个不存在的 backend（`/api/v2/image/nope/generate`）返回 `404 Not Found`
纯文本——**这个区别就是判定路由存在的依据**。

### 9.6 账户额度

余额和明细在**配置网关**上，不在模型网关。两个端点，分工不同：

```
GET https://design.minimax.io/api/v1/credit/balance
    token: <jwt>   version_code: 3.0.16
→ {"total_credit":"211"}                      # 只有一个数字

GET https://design.minimax.io/api/v1/credit/wallet
    token: <jwt>   version_code: 3.0.16
→ {"can_migrate":false,
   "wallets":[{"source":1,"total_credit":"211",
     "sub_credits":[{"credit_type":2,"end_time":"1790013469697","credit":"211"}]}],
   "url":"https://design.minimax.io/media-plan/subscribe?group_id=..."}
```

**到期时间只在 wallet 里**（`sub_credits[].end_time`，毫秒时间戳），
`balance` 那一个数字看不出什么时候没。早先这里记的是 `{"credits":…, "expire_at":…}`，
与线上实际返回对不上，已按实测订正。

`credit_type` 是整数，网关不给标签，得照着桌面端 bundle 里的 `CreditType` 枚举读：

| 值 | 含义 | 有效期 |
| --- | --- | --- |
| 0 | `CREDIT_TYPE_TOP_UP` 充值 | **一年** |
| 1 | `CREDIT_TYPE_MEMBERSHIP` 订阅 | **1 个月**，每月重置 |
| 2 | `CREDIT_TYPE_BONUS` 赠送 | **3 天** |
| 3 | `CREDIT_TYPE_DEFAULT` | — |
| 4 | `CREDIT_TYPE_CREATOR` 创作者奖励 | — |
| 5 | `CREDIT_TYPE_ACTIVITY` 活动 | — |
| 6 | `CREDIT_TYPE_LOGIN` 登录奖励 | — |
| 7 | `CREDIT_TYPE_TRANSFER` 团队转入 | 随原有效期 |

应用内文案把这条讲得很直白：`credits.bonusTip` =
「新用户登录奖励以及活动获得积分，**新用户免费积分有效期 3 天**」。

所以**新账号是有积分的**——新用户登录奖励，落在 `CREDIT_TYPE_BONUS`。
但只有 3 天。账号本身（令牌）还能撑 40 天，两件事是分开的：
额度用完/过期后令牌仍然有效，所有生成都会失败，排查时容易往错的方向找。

另有视频免费试用，端点独立：

```
GET /api/v1/promotions/hailuo03-video-trial/status
→ {"free_count":3, "eligibility":{"models":["MiniMax-H3-Max"],
     "resolutions":["480P","768P"], "allow_reference_images":true, ...}}
POST /api/v1/promotions/hailuo03-video-trial/claim
```

3 次，**只限 `MiniMax-H3-Max`、480P/768P、文生视频或首尾帧**（会员不适用）。

`python tools/check_credit.py` 把上面三个端点串起来跑，一行看清余额、类型、到期与试用。

### 9.6.1 账号体系：桌面端登录用的是网页端

桌面端的登录**不在客户端里做**，它把用户导到网页登录页：

```js
const HUB_WEB_LOGIN_DOMAINS = {
  domestic: { prod: "https://design.minimax.cn/login", ... },
  overseas: { prod: "https://design.minimax.io/login", ... }   // dev/test → hub-test.xaminim.com
};
function getHubWebLoginUrl(region, channel, params) {
  return `${base}?device_id=${params.deviceId}&version_code=${normalizeVersionCodeForCloud(params.versionCode)}`;
}
```

带 `device_id` + `version_code` 去网页登录，成功后网页侧把 access token 交回客户端，
客户端加密写进 `hub-config-global.json` —— 也就是本服务读的那个文件。

**结论：不存在"桌面端账号"和"网页端账号"的区分。**
`design.minimax.io` 上注册的账号就是它，在网页注册完再登录桌面端即可。

**积分也是共享的**：`credits.sharedTooltipPrefix` =「积分与海螺账号共享」。
海螺（Hailuo）网页端/手机端的贝壳余额可按比例单向转入 MiniMax Design
（`1 贝壳 = N 积分`，转入后有效期一年；仅订阅积分与充值积分可转入，
免费积分不支持转入）。应用里还有 `account/hailuo-web`、
`account/cancel/hailuo-check` 这类接口，服务的是**账号注销**链路——
注销 Design 账号会连带处理海螺侧，文案明说
「将无法登录当前海螺视频（网页端和手机端）及 MiniMax Design 的所有账号」
「海螺 AI 以及 MiniMax Design 账号中的积分都将全部清零」。这条也反证了两边是同一账号。

顺带一提，域名矩阵（`CLOUD_GATEWAY_URLS`）：海外 `design.minimax.io`，
国内 `design.minimax.cn`，dev/test `hub-pre.xaminim.com` / `hilo-test.xaminim.com`；
历史遗留 `hub.minimax.io` / `hub.minimaxi.com` / `design.minimaxi.com`
仍要认，否则 provider 的 baseURL 会漏掉导致 401。

### 9.7 没做的

- 语音（`tts` / `voice_clone` / `voice_design`）、音乐、超分、媒体分析 —— 请求体没逆
- 图像的上传链路（`/api/v1/files/upload`）：客户端做图生图时先把本地文件传上去
  再引用返回的 URL，本服务只接受 URL / data URI
- OpenAI 的 `/v1/images/edits`（multipart 形状）
- `qwen` / `kling` 两个图像 backend 路由是通的，但客户端这一版没在目录里注册模型，
  且 qwen 上游默认模型是 `qwen-image-edit`（图生图），纯文生图被上游拒
- 视频的 `callback_url`：上游不支持回调，只能轮询

---

## 10. 复现步骤

```bash
# 1. 找安装目录
cat "$APPDATA/MiniMax/install-ownership/com.minimax.hub.global/record.json"

# 2. 解 asar
node tools/extract-asar.js "E:/Minimax design/current/resources/app.asar" recon/app-asar

# 3. 解密 access token（也可直接用主程序的 -import-token）
node tools/decrypt-v2enc.js "$APPDATA/@hilo/MiniMax Hub Global/.token-key" \
  "$(node -e 'console.log(require(process.env.APPDATA+"/@hilo/MiniMax Hub Global/hub-config-global.json").tokens.accessToken)')"

# 4. 验通
curl https://hub.minimax.io/api/v1/messages \
  -H "token: $TOKEN" -H "version_code: 3.0.16" \
  -H 'content-type: application/json' \
  -d '{"model":"MiniMax-M3","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}'
```

---

## 11. 踩过的坑清单

| # | 坑 | 现象 | 解法 |
| --- | --- | --- | --- |
| 1 | asar 头部偏移 | `JSON.parse` 报 `Unexpected token 'l', "les":{"nod"...` | JSON 从 offset 16 直接开始，offset 12 就是长度 |
| 2 | Go GCM nonce | `panic: incorrect nonce length given to GCM` | Node 用 16 字节 IV，Go 要用 `NewGCMWithNonceSize` |
| 3 | 鉴权头名 | `401 token 为空` | 头就叫 `token`，不是 `Authorization` / `x-api-key` |
| 4 | 版本头 | `403 client version_code is required` | 加 `version_code: 3.0.16` |
| 5 | 端点猜错 | `/chat/completions` 404 | gamma 走 `/responses`，靠 `store`/`reasoningEffort` 参数认出来 |
| 6 | 协议串台 | 400 + 无信息量的 `client_error` | 模型和协议锁死，必须按 provider 的 `npm` 字段路由 |
| 7 | APPDATA 缺失 | `-import-token` 找不到目录 | Git Bash 不导出 `APPDATA`，回落到 `~` 推导 |
| 8 | 空 `content` 语义 | OpenAI 工具调用时 content 是 `null` 不是 `""` | 有 tool_calls 且无文本时显式输出 `null` |
| 9 | 工具调用续帧 | 后续 delta 不该带 `id`/`type` 空串 | 加 `omitempty`，只首帧带 |
| 10 | 视频查询写成 query string | `/tasks?task_id=x` 404 | 是**路径参数** `/tasks/{id}`，写错了会误判成「接口不存在」 |
| 11 | 纯文本生成没给 `ratio` | 上游报比例校验失败（额度自动退还） | 必须显式给 `ratio`，且不能是 `adaptive`；平台请求体没这个字段，要从 `size` 推导 |
| 12 | 403 一律当凭证失效 | 一个没权限的模型把整池令牌打进 30 分钟冷却 | 403 是复用的（缺版本头 / 没权限 / 真失效），只有响应体含凭证字样才算 |
| 13 | `file_id` 当数字解 | 任务成功但取不到文件（410 `artifact_gone`） | 上游给的是字符串 `"L8ONzGzOpqzD"`；适配器吞了 unmarshal 错误，现象是「静默没地址」 |
| 14 | 用 GET 探媒体路由 | 全 404，误判成「接口不存在」 | 这些路由是 POST-only，Express 对未匹配的 GET 也返回 404。改用**空 body POST**——会因校验失败而不扣费，响应形状本身就是情报 |
| 15 | 以为图像也按模型分端点 | 拼 `/api/v2/image/gpt-image-2/generate` 找不到 | 图像按 **backend** 分端点，模型名是请求体里的字段 |
| 16 | 以为图像响应有统一形状 | 有的返回 `image_url`，有的 `image_urls[]`，kling 还藏在 `data.task_id` | 各 backend 的提交/查询响应都不一样，解析要按 backend 分支 + 宽容 |
| 17 | 5xx 也做指数退避 | 试一个「参数组合不支持」的模型，服务自停 5s→10s→20s | 网关的 5xx 大多是确定性请求级拒绝，不是拥塞；只该冷却真正可疑的失败 |
| 18 | 单号池还给失败的令牌上冷却 | 唯一令牌被罚，期间所有请求 `no usable token` | 池里只有一个可用令牌时没有可换的号，罚它纯属自降可用性 |
| 19 | 照抄目录里的 `backend` 字段选视频端点 | Kling avatar / motion-control 被发到 `/kling/generate` | 三个 Kling 模型目录里都是 `backend: "kling"`，实际走三组路径，靠 **model id** 分 |
| 20 | 用 `strings.Contains(model, "Max")` 判 H3-Max | 每个 H3 模型都被当成 Max，时长下限从 4 抬到 5 | 厂商前缀是 **MiniMax**，那个子串恒真；要用 `"-Max"` |
| 21 | 以为任务类模型能按时长计费 | 配了 `billing_expr` 也没用，只能按次 | new-api 的任务计费表达式要求适配器实现 `TaskUsageFactsProvider`，`hailuo` 没有（只有 `jsplugin` 有） |
| 22 | 用 POST 的 body 形状去 PUT 渠道 | `{"message":"record not found"}`，看起来像 id 错了 | `POST /api/channel/` 要 `{mode, channel}`；`PUT` 直接把 rawBody 解成 `PatchChannel`，渠道对象必须在**顶层** |
| 23 | 照着音色目录做映射表 | 6 个音色 5 个报 `2054 voice id not exist` | 目录 671 项**不是**白名单：`Friendly_Person` 能用但不在目录里，`Young_Female` 在猜测里但根本不存在。别猜，从 `English_*` 里挑 |
| 24 | 用空 text 探音色有效性 | 永远返回「text 不能为空」，探不出音色对错 | 上游的校验顺序是 text → voice，空 text 先挂。要免费验证只能拿目录对账，或者真发一次 |
