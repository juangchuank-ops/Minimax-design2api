/* ==========================================================================
   MiniMax Design 2API — admin console · data pages
   --------------------------------------------------------------------------
   Dashboard, models, token pool, API reference, settings and about.
   The media playground lives in playground.js.

   Loaded after app.js; shares its globals (helpers, STATE, catalogs, …).
   ========================================================================== */

"use strict";

/* --- API reference ------------------------------------------------------ */

const DOCS = [
  {
    group: "Chat", icon: "message",
    items: [
      {
        slug: "chat/completions", method: "POST", path: "/v1/chat/completions", title: "Chat Completions",
        desc: "OpenAI 兼容入口。支持流式 SSE、工具调用、多模态与推理内容（reasoning_content）。gamma 系列上游实际走 Responses API，转换在这里完成，客户端无感知。",
        params: [
          ["model", "string", "模型 ID，取自 /v1/models；留空则用 config.json 的 default_model"],
          ["messages", "array", "标准 OpenAI 消息数组"],
          ["stream", "bool", "true 走 SSE；配合 stream_options.include_usage 会在末尾追加一个用量块"],
          ["tools", "array", "工具定义，会按上游协议转成 Anthropic 或 Responses 各自形状"],
        ],
        curl: "curl http://127.0.0.1:18080/v1/chat/completions \\\n  -H 'content-type: application/json' \\\n  -d '{\"model\":\"MiniMax-M3\",\"messages\":[{\"role\":\"user\",\"content\":\"你好\"}],\"stream\":true}'",
        notes: [
          "模型与协议是锁死的：gamma_high 打 /messages 报 400，MiniMax-M3 打 /responses 也报 400。",
          "omega-3.1-pro 走 Google 协议，上游路由未定位，会明确返回 501 而不是静默改道。",
        ],
      },
      {
        slug: "chat/messages", method: "POST", path: "/v1/messages", title: "Anthropic Messages",
        desc: "Anthropic 原生格式直通，给 Anthropic SDK / Claude Code 用，不经过 OpenAI 形状转换。",
        params: [
          ["model", "string", "必须是 anthropic 协议的模型（MiniMax-M3 / MiniMax-M2.7 / alpha…）"],
          ["stream", "bool", "上游 SSE 原样透传，不做重新封装"],
        ],
        curl: "curl http://127.0.0.1:18080/v1/messages \\\n  -H 'content-type: application/json' \\\n  -H 'anthropic-version: 2023-06-01' \\\n  -d '{\"model\":\"MiniMax-M3\",\"max_tokens\":256,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}'",
        notes: ["非 anthropic 协议的模型返回 404，不会静默改道。"],
      },
      {
        slug: "chat/models", method: "GET", path: "/v1/models", title: "Models",
        desc: "模型列表。每个模型都带 meta.protocol，可据此判断走哪个上游协议，以及 context / max_output / tools / vision 能力。",
        params: [["—", "—", "无参数"]],
        curl: "curl http://127.0.0.1:18080/v1/models",
        notes: ["同一个模型有裸名与 provider/name 两种写法，两者都可用。"],
      },
    ],
  },
  {
    group: "图像", icon: "image",
    items: [
      {
        slug: "image/generations", method: "POST", path: "/v1/images/generations", title: "Image Generations",
        desc: "OpenAI 图像格式。上游是异步的，本服务内部做「提交 + 轮询」，对调用方表现为同步，最长等 5 分钟。",
        params: [
          ["model", "string", "目录里的图像模型，如 nano_banana_2_flash"],
          ["prompt", "string", "提示词"],
          ["n", "int", "张数；midjourney 原生返回 4 张，new-api 会按 4 张计费"],
          ["size", "string", "如 1024x1024；不给则由 aspect_ratio 推导"],
          ["aspect_ratio", "string", "如 16:9（扩展字段）"],
          ["resolution", "string", "1K / 2K / 4K（扩展字段）"],
          ["image_paths", "array", "参考图 URL 或 data URI，做图生图（扩展字段）"],
          ["response_format", "string", "url（默认）或 b64_json（服务端代下再 base64）"],
          ["model_name", "string", "覆盖 backend 的上游模型名（扩展字段）"],
          ["stylize / chaos / weird / version", "int", "仅 midjourney，会拼成 prompt flag"],
        ],
        curl: "curl http://127.0.0.1:18080/v1/images/generations \\\n  -H 'content-type: application/json' \\\n  -d '{\"model\":\"nano_banana_2_flash\",\"prompt\":\"a sleepy ginger cat\",\"n\":1,\"size\":\"1024x1024\"}'",
        notes: [
          "请求体按 backend 分支构造：nano_banana / openai / qwen / seedream / kling / midjourney 各不相同。",
          "seedream 5-0 不能带 seed / guidance_scale，带了直接报错。",
          "图生图的 image_paths 只吃 URL 或 data URI；本地文件上传链路未实现，OpenAI 的 /v1/images/edits 也没做。",
        ],
      },
      {
        slug: "image/models", method: "GET", path: "/v1/image/models", title: "Image Models",
        desc: "图像模型目录，含所属 backend、上游模型名与各参数的可选值。",
        params: [["—", "—", "无参数"]],
        curl: "curl http://127.0.0.1:18080/v1/image/models",
        notes: ["qwen 与 kling 两个 backend 能路由，但客户端目录里没注册模型，实际用不上。"],
      },
    ],
  },
  {
    group: "视频", icon: "video",
    items: [
      {
        slug: "video/generations", method: "POST", path: "/v1/video_generation", title: "Video Generation",
        desc: "提交视频任务。这里是 MiniMax 平台格式，专门为了能被 new-api 的 MiniMax 渠道（type 35，hailuo 适配器）当作上游使用。",
        params: [
          ["model", "string", "hub 原生名（MiniMax-H3）与平台名（MiniMax-Hailuo-2.3 / T2V-01）都可"],
          ["prompt", "string", "提示词"],
          ["duration", "int", "按后端钳制：H3 4–15、H3-Max 5–15、Wan 2–30、Kling 3–15、Veo3 只能 8"],
          ["size", "string", "如 1280x720；分辨率会折算，512P/720P→768P，1080P→2K"],
          ["ratio", "string", "纯文本生成必须显式给且不能是 adaptive；不给则服务按 size 推导比例，兜底 16:9"],
        ],
        curl: "curl http://127.0.0.1:18080/v1/video_generation \\\n  -H 'content-type: application/json' \\\n  -d '{\"model\":\"MiniMax-Hailuo-2.3\",\"prompt\":\"a paper boat in a gutter\",\"duration\":4,\"size\":\"1280x720\"}'",
        notes: [
          "非 minimax_v3 后端的 task_id 会带 `{backend}~` 前缀，查询时原样传回即可。",
          "11 个模型分属 6 个 backend，路由按 model id 决定（不是目录里的 backend 字段）。",
        ],
      },
      {
        slug: "video/query", method: "GET", path: "/v1/query/video_generation", title: "Query Video",
        desc: "查询任务进度。上游状态是小写 processing/success/fail，这里转成平台要的首字母大写形式。",
        params: [["task_id", "string", "提交时返回的 id；带 backend 前缀的原样传"]],
        curl: "curl \"http://127.0.0.1:18080/v1/query/video_generation?task_id=7GyYK24V81Xr\"",
        notes: [
          "只有 minimax_v3 真的返回 file_id；其余后端把成品 CDN 地址百分号编码后塞进 file_id 槽。",
          "callback_url 被忽略——上游不支持回调，只能轮询。",
        ],
      },
      {
        slug: "video/files", method: "GET", path: "/v1/files/retrieve", title: "Retrieve File",
        desc: "用 file_id 换取下载地址。若 file_id 本身是绝对 URL（非 minimax_v3 后端），原样回吐。",
        params: [["file_id", "string", "任务返回的 file_id"]],
        curl: "curl \"http://127.0.0.1:18080/v1/files/retrieve?file_id=7GyYK24V81Xr\"",
        notes: ["百分号编码是必须的：未编码的 URL 会被调用方 query parser 在第一个 & 处截断，签名链接就废了。"],
      },
      {
        slug: "video/models", method: "GET", path: "/v1/video/models", title: "Video Models",
        desc: "11 个视频模型，含 backend 路由、是否会员专属，以及分辨率 / 比例 / 时长的可选值。",
        params: [["—", "—", "无参数"]],
        curl: "curl http://127.0.0.1:18080/v1/video/models",
        notes: ["Kling / Veo / Jimeng 是会员专属，非会员账号会收到上游 403 permission_error。这是账号权限，不是配置问题。"],
      },
    ],
  },
  {
    group: "语音", icon: "audio",
    items: [
      {
        slug: "voice/speech", method: "POST", path: "/v1/audio/speech", title: "Text to Speech",
        desc: "OpenAI TTS 格式，同步返回音频字节。new-api 的 type-1 渠道会原样透传这段响应，所以它是唯一接进了 new-api 的音频能力。",
        params: [
          ["model", "string", "tts-1，或真实的 speech-2.8-hd 之类"],
          ["input", "string", "要合成的文本"],
          ["voice", "string", "OpenAI 别名（nova / onyx / alloy…）或任意真实 voice_id"],
        ],
        curl: "curl -X POST http://127.0.0.1:18080/v1/audio/speech \\\n  -H 'content-type: application/json' \\\n  -d '{\"model\":\"tts-1\",\"input\":\"你好\",\"voice\":\"nova\"}' -o out.mp3",
        notes: [
          "别拿音色目录当白名单校验：Friendly_Person 这类历史音色上游仍然认，但不在 671 条目录里，拿目录校验会误判成非法。",
          "未知 voice_id 的响应是 HTTP 500 + code=2054 voice id not exist，从调用方看很像「TTS 接口挂了」，容易误判成路由或鉴权问题。",
        ],
      },
      {
        slug: "voice/voices", method: "GET", path: "/v1/audio/voices", title: "List Voices",
        desc: "音色目录，671 条、60 个语种，支持服务端过滤，省得调用方拉全表。",
        params: [
          ["language", "string", "按语言模糊匹配，如 en"],
          ["q", "string", "按 voice_id 或名称模糊匹配"],
        ],
        curl: "curl \"http://127.0.0.1:18080/v1/audio/voices?language=en&q=storyteller\"",
        notes: ["目录不全：能查到的未必都能用，能用的也未必查得到。"],
      },
      {
        slug: "voice/lyrics", method: "POST", path: "/v1/lyrics/generations", title: "Lyrics",
        desc: "歌词生成，同步返回结构化歌词，免费。",
        params: [["prompt", "string", "主题描述"]],
        curl: "curl -X POST http://127.0.0.1:18080/v1/lyrics/generations \\\n  -H 'content-type: application/json' \\\n  -d '{\"prompt\":\"a song about a lighthouse\"}'",
        notes: ["new-api 的中继路由里没有音乐类端点，只能直连本服务的 :18080。"],
      },
      {
        slug: "voice/music", method: "POST", path: "/v1/music/generations", title: "Music",
        desc: "提交音乐生成任务，异步，约 23 额度/首。",
        params: [
          ["prompt", "string", "音乐描述"],
          ["lyrics", "string", "可选，歌词文本"],
        ],
        curl: "curl -X POST http://127.0.0.1:18080/v1/music/generations \\\n  -H 'content-type: application/json' \\\n  -d '{\"prompt\":\"lo-fi hip hop, rainy night\"}'",
        notes: ["用 GET /v1/query/music_generation?task_id= 查询进度。"],
      },
    ],
  },
];

function findDoc(slug) {
  for (const g of DOCS) {
    const item = g.items.find((i) => i.slug === slug);
    if (item) return { group: g, item };
  }
  return null;
}

/* --- page: dashboard ---------------------------------------------------- */

async function pageDashboard(token) {
  const view = $("view");
  view.innerHTML = loadingHtml(300);
  const [s, m] = await Promise.all([loadState(), api("/admin/api/metrics")]);
  if (token !== currentRender) return;

  renderFootStatus();

  const met = s.metrics;
  const bare = s.models.filter((x) => x.id.indexOf("/") < 0);
  const sourceLabel = s.catalog_source === "remote" ? "远程网关" : "内置兜底表";

  const cards = [
    { icon: "box", label: "模型", value: num(bare.length), detail: sourceLabel + " · " + fmtAgo(s.catalog_at * 1000) + "刷新" },
    { icon: "users", label: "可用令牌", value: num(s.pool_usable), detail: "共 " + num(s.pool.length) + " 个 · " + num(s.pool.length - s.pool_usable) + " 个不可用" },
    { icon: "activity", label: "请求总数", value: num(met.total), detail: "成功率 " + pct(met.success_rate) + " · 失败 " + num(met.failed) },
    { icon: "gauge", label: "平均耗时", value: met.total ? fmtMs(met.avg_ms) : "—", detail: "进程已运行 " + fmtDuration(met.uptime_seconds) },
    { icon: "layers", label: "当前并发", value: num(s.pool.reduce((a, t) => a + (t.inflight || 0), 0)), detail: "least-inflight 调度" },
  ];

  const cardsHtml = '<section class="metrics-grid">' + cards.map((c) =>
    '<article class="metric"><header class="metric-head"><span>' + esc(c.label) + "</span>" + icon(c.icon) + "</header>" +
    '<div class="metric-value">' + esc(c.value) + "</div>" +
    '<p class="metric-detail" title="' + esc(c.detail) + '">' + esc(c.detail) + "</p></article>").join("") + "</section>";

  const total = s.pool.length;
  const availability = total ? (s.pool_usable / total) * 100 : 0;
  const cooling = s.pool.filter((t) => t.enabled && !t.expired && t.cooling).length;
  const expired = s.pool.filter((t) => t.expired).length;
  const disabled = s.pool.filter((t) => !t.enabled).length;

  const r = 67, circ = 2 * Math.PI * r;
  const on = (circ * Math.min(100, Math.max(0, availability))) / 100;
  const donut = '<div class="donut-wrap"><svg viewBox="0 0 176 176" role="img" aria-label="号池可用率">' +
    '<circle cx="88" cy="88" r="' + r + '" fill="none" stroke="color-mix(in oklab, var(--muted-foreground) 35%, transparent)" stroke-width="22"/>' +
    '<circle cx="88" cy="88" r="' + r + '" fill="none" stroke="var(--ok)" stroke-width="22" ' +
    'stroke-dasharray="' + on.toFixed(2) + " " + (circ - on).toFixed(2) + '" transform="rotate(-90 88 88)"/>' +
    '</svg><div class="donut-center"><span class="pct">' + num(availability, 0) + "%</span>" +
    '<span class="cap">可用率</span></div></div>';

  const meters = [
    { color: "var(--ok)", label: "可用", value: s.pool_usable, cap: "可立即调度" },
    { color: "var(--warn)", label: "冷却中", value: cooling, cap: "失败退避，可一键清空" },
    { color: "var(--destructive)", label: "已过期", value: expired, cap: "有效期约 40 天，需重新导入" },
    { color: "color-mix(in oklab, var(--muted-foreground) 45%, transparent)", label: "已停用", value: disabled, cap: "手动停用" },
  ];
  const meterHtml = '<div class="meter-list">' + meters.map((x) =>
    '<div class="meter-row"><div class="name"><span class="dot" style="background:' + x.color + '"></span>' +
    '<div><p class="xs" style="margin:0">' + esc(x.label) + "</p>" +
    '<p class="xxs muted" style="margin:2px 0 0">' + esc(x.cap) + "</p></div></div>" +
    '<span class="value">' + num(x.value) + "</span></div>").join("") + "</div>";

  const kinds = [["chat", "对话"], ["image", "图像"], ["video", "视频"], ["audio", "语音"], ["other", "其他"]];
  const kindTotal = kinds.reduce((a, k) => a + ((met.by_kind || {})[k[0]] || 0), 0) || 1;
  const kindHtml = '<div class="bar-list">' + kinds.map((k) => {
    const v = (met.by_kind || {})[k[0]] || 0;
    return '<div class="bar-item"><span class="xs">' + k[1] + "</span>" +
      '<span class="xs muted tabular">' + num(v) + "</span>" +
      '<span class="bar"><i style="width:' + ((v / kindTotal) * 100).toFixed(1) + '%"></i></span></div>';
  }).join("") + "</div>";

  const byProto = {};
  bare.forEach((x) => { byProto[x.protocol] = (byProto[x.protocol] || 0) + 1; });
  const protoTotal = Object.keys(byProto).reduce((a, k) => a + byProto[k], 0) || 1;
  const protoColor = (k) => k === "anthropic" ? "oklch(0.65 0.15 300)" : k === "responses" ? "oklch(0.66 0.13 235)" : "var(--warn)";
  const protoHtml = '<div class="bar-list">' + Object.keys(byProto).sort().map((k) =>
    '<div class="bar-item"><span class="xs">' + esc(k) + "</span>" +
    '<span class="xs muted tabular">' + num(byProto[k]) + "</span>" +
    '<span class="bar"><i style="width:' + ((byProto[k] / protoTotal) * 100).toFixed(1) + "%;background:" + protoColor(k) + '"></i></span></div>'
  ).join("") + "</div>";

  const recent = m.recent || [];
  const recentHtml = recent.length
    ? '<div class="table-wrap viewport" style="max-height:400px"><table class="table"><thead><tr>' +
      '<th style="width:104px">时间</th><th>端点</th><th>模型</th>' +
      '<th class="right" style="width:64px">状态</th><th class="right" style="width:84px">耗时</th>' +
      "</tr></thead><tbody>" +
      recent.slice(0, 40).map((rec) =>
        '<tr><td class="mono xxs muted nowrap">' + esc(fmtDateTime(rec.time_ms)) + "</td>" +
        '<td class="mono xxs break">' + esc(rec.path) + (rec.stream ? ' <span class="badge outline">SSE</span>' : "") + "</td>" +
        '<td class="mono xxs">' + (rec.model ? esc(rec.model) : '<span class="muted">—</span>') + "</td>" +
        '<td class="right"><span class="badge ' + (rec.ok ? "ok" : "destructive") + ' mono">' + num(rec.status) + "</span></td>" +
        '<td class="right mono xxs tabular">' + esc(fmtMs(rec.ms)) + "</td></tr>").join("") +
      "</tbody></table></div>"
    : emptyHtml("还没有请求。去调试台发一条，这里就有数据了。");

  view.innerHTML =
    '<div class="page">' +
    pageHeader("仪表盘", "进程运行状态、号池健康度与推理流量。",
      '<button class="btn secondary sm" data-act="refresh">' + icon("refresh") + "刷新</button>") +
    (s.catalog_error
      ? '<div class="panel" style="padding:12px 16px"><div class="row gap-12">' +
        '<span style="display:flex">' + icon("alert") + "</span>" +
        '<span class="xs">模型目录上次拉取失败：' + esc(s.catalog_error) +
        "。当前在用内置兜底表，检查令牌是否可用后到设置页重新拉取。</span></div></div>"
      : "") +
    cardsHtml +
    '<div class="split">' +
      panel("pool-health", "号池健康", donut, null) +
      panel("pool-detail", "令牌构成", meterHtml, null) +
    "</div>" +
    '<div class="split">' +
      panel("traffic", "推理流量（本进程）", kindHtml +
        '<div class="divider" style="margin:16px 0"></div>' +
        '<div class="kv">' +
        '<span class="k">请求总数</span><span class="v">' + num(met.total) + "</span>" +
        '<span class="k">成功 / 失败</span><span class="v">' + num(met.success) + " / " + num(met.failed) + "</span>" +
        '<span class="k">平均耗时</span><span class="v">' + (met.total ? esc(fmtMs(met.avg_ms)) : "—") + "</span>" +
        '<span class="k">运行时长</span><span class="v">' + esc(fmtDuration(met.uptime_seconds)) + "</span>" +
        "</div>", null) +
      panel("protocol", "模型协议分布", protoHtml +
        '<div class="divider" style="margin:16px 0"></div>' +
        '<p class="hint">协议由上游 npm 包名决定：@ai-sdk/anthropic → /messages，' +
        "@ai-sdk/openai → /responses，@ai-sdk/google → :generateContent（路由未定位，返回 501）。</p>", null) +
    "</div>" +
    panel("recent", "最近请求", recentHtml,
      '<span class="xxs muted">保留最近 ' + num(recent.length) + " 条</span>") +
    "</div>";

  view.querySelector('[data-act="refresh"]').onclick = () => render();
}

/* --- page: models ------------------------------------------------------- */

const modelFilter = { q: "", protocol: "", bareOnly: true };
const mediaFilter = { q: "" };

const MODEL_TABS = [
  { id: "chat", label: "对话", icon: "message" },
  { id: "image", label: "图像", icon: "image" },
  { id: "video", label: "视频", icon: "video" },
  { id: "voice", label: "音色", icon: "audio" },
];

// Media catalogues come from the media gateway and are keyed differently from
// the LLM catalogue: `voices` is plural while the tab is `voice`.
const MEDIA_META = {
  image: { key: "image", title: "图像模型", columns: ["模型 ID", "Backend", "上游模型", "比例", "分辨率", "尺寸", "质量"] },
  video: { key: "video", title: "视频模型", columns: ["模型 ID", "Backend", "上游模型", "分辨率", "比例", "时长(秒)", "会员"] },
  voice: { key: "voices", title: "音色", columns: ["音色 ID", "名称", "语言", "性别", "年龄", "口音", "试听"] },
};

const VOICE_ROW_CAP = 120;

let modelsTab = "chat";

function fmtOptions(values) {
  return values && values.length ? values.join(" / ") : "—";
}

// Duration ladders arrive as a list of legal values; "4–15" reads far better
// than "4 / 5 / 6 / 7 / 8 / 9 / 10 / 11 / 12 / 13 / 14 / 15".
function fmtRange(values) {
  const list = values || [];
  const nums = list.map(Number);
  if (!list.length || nums.some((n) => isNaN(n))) return fmtOptions(list);
  const min = Math.min.apply(null, nums);
  const max = Math.max.apply(null, nums);
  return min === max ? String(min) : min + "–" + max;
}

function mediaCount(tab) {
  if (tab === "chat") {
    return STATE.data ? STATE.data.models.filter((m) => m.id.indexOf("/") < 0).length : null;
  }
  const c = CATALOGS.data;
  if (!c) return null;
  const meta = MEDIA_META[tab];
  return c[meta.key + "_error"] ? null : (c[meta.key] || []).length;
}

function modelTabsHtml() {
  return '<div class="tabs" id="modelTabs">' + MODEL_TABS.map((t) => {
    const n = mediaCount(t.id);
    return '<button class="tab' + (modelsTab === t.id ? " active" : "") + '" data-mtab="' + t.id + '">' +
      icon(t.icon) + esc(t.label) +
      (n == null ? "" : ' <span class="xxs tabular" style="opacity:.6">' + num(n) + "</span>") + "</button>";
  }).join("") + "</div>";
}

// The media counts only become known once catalogs() resolves, so the header
// rendered before that would show bare labels. Re-render just the tab bar.
function refreshModelTabCounts(view) {
  const el = view.querySelector("#modelTabs");
  if (!el) return;
  el.outerHTML = modelTabsHtml();
  bindModelTabs(view);
}

function bindModelTabs(view) {
  view.querySelectorAll("[data-mtab]").forEach((b) => {
    b.onclick = () => {
      if (modelsTab === b.dataset.mtab) return;
      modelsTab = b.dataset.mtab;
      mediaFilter.q = "";
      pageModels(currentRender);
    };
  });
}

async function pageModels(token) {
  const view = $("view");
  if (!STATE.data) { view.innerHTML = loadingHtml(300); await loadState(); }
  if (token !== currentRender) return;
  renderFootStatus();

  const header = pageHeader("模型",
    "对话模型来自云端网关目录；图像 / 视频 / 音色来自媒体网关，需单独拉取。",
    modelTabsHtml());

  if (modelsTab === "chat") {
    renderChatModels(view, header, token);
    return;
  }
  await renderMediaModels(view, header, token);
}

/* --- tab: 对话模型 ------------------------------------------------------ */

function renderChatModels(view, header, token) {
  const s = STATE.data;

  const rows = s.models.filter((m) => {
    if (modelFilter.bareOnly && m.id.indexOf("/") >= 0) return false;
    if (modelFilter.protocol && m.protocol !== modelFilter.protocol) return false;
    if (modelFilter.q) {
      const q = modelFilter.q.toLowerCase();
      if (m.id.toLowerCase().indexOf(q) < 0 &&
        String(m.display || "").toLowerCase().indexOf(q) < 0 &&
        String(m.provider || "").toLowerCase().indexOf(q) < 0) return false;
    }
    return true;
  });
  const protocols = Array.from(new Set(s.models.map((m) => m.protocol))).sort();

  const table = rows.length
    ? '<div class="table-wrap"><table class="table"><thead><tr>' +
      "<th>模型 ID</th><th>协议</th><th>上游模型</th><th>Provider</th>" +
      '<th class="right">上下文</th><th class="right">最大输出</th><th class="center">能力</th>' +
      '<th class="right" style="width:56px">复制</th></tr></thead><tbody>' +
      rows.map((m) =>
        '<tr><td><code class="mono xs">' + esc(m.id) + "</code></td>" +
        "<td>" + protocolBadge(m.protocol) + "</td>" +
        '<td class="mono xxs muted">' + esc(m.upstream) + "</td>" +
        '<td class="xs">' + esc(m.provider) + "</td>" +
        '<td class="right mono xxs tabular">' + fmtTokens(m.context) + "</td>" +
        '<td class="right mono xxs tabular">' + fmtTokens(m.max_output) + "</td>" +
        '<td class="center">' +
          (m.tools ? '<span class="badge outline">工具</span> ' : "") +
          (m.vision ? '<span class="badge outline">视觉</span>' : "") +
          (!m.tools && !m.vision ? '<span class="muted xs">—</span>' : "") + "</td>" +
        '<td class="right"><button class="btn ghost sm" data-copy="' + esc(m.id) + '" title="复制模型 ID">' +
        icon("copy") + "</button></td></tr>").join("") +
      "</tbody></table></div>"
    : emptyHtml("没有匹配的模型");

  view.innerHTML =
    '<div class="page">' + header +
    panel("models", "对话模型路由",
      '<div class="toolbar" style="padding-top:0">' +
        '<div class="toolbar-group">' +
          '<div class="search">' + icon("search") +
            '<input class="input" id="modelQ" placeholder="搜索模型 / provider" value="' + esc(modelFilter.q) + '">' +
          "</div>" +
          selectHtml("modelProto",
            [{ value: "", label: "全部协议" }].concat(protocols.map((p) => ({ value: p, label: p }))),
            modelFilter.protocol) +
          '<label class="check"><input type="checkbox" class="checkbox" id="modelBare"' +
            (modelFilter.bareOnly ? " checked" : "") + ">隐藏 provider/ 前缀写法</label>" +
        "</div>" +
        '<span class="xxs muted">' + num(rows.length) + " / " + num(s.models.length) + " 条</span>" +
      "</div>" + table +
      '<div class="divider" style="margin:16px 0 12px"></div>' +
      '<div class="kv">' +
      '<span class="k">目录来源</span><span class="v">' + esc(s.catalog_source) + "（" +
        (s.catalog_source === "remote" ? "远程网关" : "内置兜底表") + "）</span>" +
      '<span class="k">拉取时间</span><span class="v">' + esc(fmtDateTime(s.catalog_at * 1000)) + " · " +
        esc(fmtAgo(s.catalog_at * 1000)) + "</span>" +
      '<span class="k">网关地址</span><span class="v break">' + esc(s.config.gateway + "/api/v1/config") + "</span>" +
      (s.catalog_error ? '<span class="k">上次错误</span><span class="v" style="color:var(--destructive)">' +
        esc(s.catalog_error) + "</span>" : "") +
      "</div>",
      '<button class="btn secondary sm" data-act="reload">' + icon("refresh") + "重新拉取目录</button>") +
    "</div>";

  bindModelTabs(view);

  // The media counts are only knowable once the catalogues land. Fetch them in
  // the background so the tab bar fills in without making the operator click
  // through first. A failure is harmless — the tabs just stay label-only.
  catalogs().then(() => {
    if (token === currentRender) refreshModelTabCounts(view);
  }).catch(() => {});

  const q = view.querySelector("#modelQ");
  let timer = null;
  q.oninput = () => {
    clearTimeout(timer);
    timer = setTimeout(() => {
      modelFilter.q = q.value.trim();
      const caret = q.selectionStart;
      pageModels(currentRender).then(() => {
        const el = view.querySelector("#modelQ");
        if (el) { el.focus(); el.setSelectionRange(caret, caret); }
      });
    }, 220);
  };
  view.querySelector("#modelProto").onchange = (e) => {
    modelFilter.protocol = e.target.value;
    pageModels(currentRender);
  };
  view.querySelector("#modelBare").onchange = (e) => {
    modelFilter.bareOnly = e.target.checked;
    pageModels(currentRender);
  };
  view.querySelector('[data-act="reload"]').onclick = async (e) => {
    const btn = e.currentTarget;
    btn.disabled = true;
    try {
      await apiJSON("/admin/api/reload", "POST");
      toast("已重新拉取模型目录", "ok");
      await loadState();
      renderSidebar();
      pageModels(currentRender);
    } catch (err) {
      toast("拉取失败：" + err.message, "err");
      btn.disabled = false;
    }
  };
}

/* --- tabs: 图像 / 视频 / 音色 ------------------------------------------ */

async function renderMediaModels(view, header, token) {
  const tab = modelsTab;
  const meta = MEDIA_META[tab];

  view.innerHTML = '<div class="page">' + header +
    '<div id="mediaBody">' + catalogLoadingHtml() + "</div></div>";
  bindModelTabs(view);

  let cat;
  try {
    cat = await catalogs();
  } catch (e) {
    if (token !== currentRender) return;
    view.querySelector("#mediaBody").innerHTML =
      panel("media", meta.title, errorStateHtml("目录拉取失败：" + e.message), null);
    return;
  }
  if (token !== currentRender) return;

  const body = view.querySelector("#mediaBody");
  refreshModelTabCounts(view);
  const errKey = meta.key + "_error";
  if (cat[errKey]) {
    body.innerHTML = panel("media", meta.title,
      errorStateHtml("目录拉取失败：" + cat[errKey]) +
      '<p class="hint" style="margin-top:12px">这类目录来自媒体网关，需要号池里有可用令牌。',
      '<button class="btn secondary sm" data-act="refetch">' + icon("refresh") + "重试</button>");
    wireRefetch(body, tab);
    return;
  }

  const all = cat[meta.key] || [];
  body.innerHTML = renderMediaPanel(tab, meta, all);
  wireMediaPanel(body, tab, meta, all);
}

function filterMedia(tab, all) {
  const q = mediaFilter.q.toLowerCase();
  if (!q) return all;
  return all.filter((m) => {
    const hay = tab === "voice"
      ? [m.id, m.name, m.language, m.gender, m.accent]
      : [m.id, m.name, m.owned_by, m.backend, m.hub_model, m.backend_model];
    return hay.some((v) => String(v == null ? "" : v).toLowerCase().indexOf(q) >= 0);
  });
}

function renderMediaPanel(tab, meta, all) {
  const rows = filterMedia(tab, all);
  const shown = tab === "voice" ? rows.slice(0, VOICE_ROW_CAP) : rows;
  const colCount = meta.columns.length;

  let head = "<tr>" + meta.columns.map((c, i) =>
    "<th" + (i > 2 ? ' class="right"' : "") + ">" + esc(c) + "</th>").join("") + "</tr>";

  let cells;
  if (tab === "image") {
    cells = shown.map((m) =>
      "<tr><td><code class=\"mono xs\">" + esc(m.id) + "</code></td>" +
      '<td><span class="badge outline mono">' + esc(m.owned_by || "—") + "</span></td>" +
      '<td class="mono xxs muted">' + esc(m.backend_model || "—") + "</td>" +
      '<td class="right mono xxs">' + esc(fmtOptions(m.aspect_ratio)) + "</td>" +
      '<td class="right mono xxs">' + esc(fmtOptions(m.resolution)) + "</td>" +
      '<td class="right mono xxs">' + esc(fmtOptions(m.size)) + "</td>" +
      '<td class="right mono xxs">' + esc(fmtOptions(m.quality)) + "</td></tr>");
  } else if (tab === "video") {
    cells = shown.map((m) =>
      "<tr><td><code class=\"mono xs\">" + esc(m.id) + "</code></td>" +
      '<td><span class="badge outline mono">' + esc(m.backend || "—") + "</span></td>" +
      '<td class="mono xxs muted">' + esc(m.backend_model || m.hub_model || "—") + "</td>" +
      '<td class="right mono xxs">' + esc(fmtOptions(m.resolution)) + "</td>" +
      '<td class="right mono xxs">' + esc(fmtOptions(m.ratio)) + "</td>" +
      '<td class="right mono xxs tabular">' + esc(fmtRange(m.duration)) + "</td>" +
      '<td class="right">' + (m.members_only
        ? '<span class="badge warn">会员专属</span>'
        : '<span class="muted xs">—</span>') + "</td></tr>");
  } else {
    cells = shown.map((m) =>
      "<tr><td><code class=\"mono xs\">" + esc(m.id) + "</code></td>" +
      '<td class="xs">' + esc(m.name || "—") + "</td>" +
      '<td class="right xs muted">' + esc(m.language || "—") + "</td>" +
      '<td class="right xs muted">' + esc(m.gender || "—") + "</td>" +
      '<td class="right xs muted">' + esc(m.age || "—") + "</td>" +
      '<td class="right xs muted">' + esc(m.accent || "—") + "</td>" +
      '<td class="right">' + (m.sample_url
        ? '<a class="btn-link" href="' + esc(m.sample_url) + '" target="_blank" rel="noreferrer">试听</a>'
        : '<span class="muted xs">—</span>') + "</td></tr>");
  }

  const table = shown.length
    ? '<div class="table-wrap"><table class="table"><thead>' + head + "</thead><tbody>" +
      cells.join("") + "</tbody></table></div>"
    : emptyHtml("没有匹配的模型");

  const notes = {
    image: "上游按 backend 分端点，各 backend 的请求体不同，由服务端按 backend 分支构造。"
      + "qwen / kling 能路由，但客户端目录里没有注册模型。"
      + "size 列为空表示目录没声明可选值——用比例 + 分辨率指定画面即可。",
    video: "11 个模型分属 6 个 backend，路由按 model id 决定（不是目录里的 backend 字段）。"
      + "Kling / Veo / Jimeng 是会员专属，非会员账号会收到上游 403 permission_error。"
      + "时长会按后端钳制，各家合法区间不同。"
      + "比例里的 adaptive 上游会拒绝，服务端会忽略它、改从 size 推导并兜底 16:9。",
    voice: "目录共 671 条、40 个语种。目录不全：能查到的未必都能用，能用的也未必查得到；"
      + "TTS 时可以直接传任意真实 voice_id，不要拿它当白名单校验。",
  };

  const capped = shown.length < rows.length
    ? '<span class="xxs muted">只显示前 ' + num(shown.length) + " 条，用搜索缩小范围</span>"
    : '<span class="xxs muted">' + num(rows.length) + " 条</span>";

  return panel("media", meta.title,
    '<div class="toolbar" style="padding-top:0">' +
      '<div class="toolbar-group">' +
        '<div class="search">' + icon("search") +
          '<input class="input" id="mediaQ" placeholder="搜索 ID / 名称' +
          (tab === "voice" ? " / 语言" : " / backend") + '" value="' + esc(mediaFilter.q) + '">' +
        "</div>" +
      "</div>" + capped +
    "</div>" + table +
    '<div class="divider" style="margin:16px 0 12px"></div>' +
    '<p class="hint" style="margin:0">' + esc(notes[tab]) + "</p>",
    '<button class="btn secondary sm" data-act="refetch">' + icon("refresh") + "重新拉取</button>");
}

function wireMediaPanel(body, tab, meta, all) {
  const q = body.querySelector("#mediaQ");
  let timer = null;
  q.oninput = () => {
    clearTimeout(timer);
    timer = setTimeout(() => {
      mediaFilter.q = q.value.trim();
      const caret = q.selectionStart;
      body.innerHTML = renderMediaPanel(tab, meta, all);
      wireMediaPanel(body, tab, meta, all);
      const el = body.querySelector("#mediaQ");
      if (el) { el.focus(); el.setSelectionRange(caret, caret); }
    }, 220);
  };
  wireRefetch(body, tab);
}

function wireRefetch(body, tab) {
  body.querySelectorAll('[data-act="refetch"]').forEach((b) => {
    b.onclick = async () => {
      b.disabled = true;
      try {
        await catalogs(true);
        toast("目录已重新拉取", "ok");
      } catch (e) {
        toast("拉取失败：" + e.message, "err");
      }
      pageModels(currentRender);
    };
  });
}


/* --- page: pool --------------------------------------------------------- */

// Cooldown reasons mirror the reason* constants in pool.go. "冷却中" alone is
// not actionable — which of the three it is decides what you do about it.
const COOL_REASON = {
  credential: "凭证失效",
  transient: "上游抖动",
  billing: "积分耗尽",
};

function fmtCooldown(seconds) {
  const v = Math.max(0, Math.floor(seconds || 0));
  if (v < 60) return v + "s";
  return Math.floor(v / 60) + ":" + String(v % 60).padStart(2, "0");
}

function fmtExpiry(ms) {
  if (!ms) return "—";
  const days = (ms - Date.now()) / 86400000;
  const when = fmtDateTime(ms);
  if (days < 0) return when + "（已过期 " + Math.abs(days).toFixed(1) + " 天）";
  return when + "（还有 " + days.toFixed(1) + " 天）";
}

function tokenStatus(t) {
  if (!t.enabled) return '<span class="badge secondary">已停用</span>';
  if (t.expired) return '<span class="badge destructive">已过期</span>';
  if (t.cooling) {
    const why = COOL_REASON[t.cooling_reason] || "";
    const title = "剩余 " + num(t.cooldown_seconds) + " 秒" + (why ? " · " + why : "");
    return '<span class="badge warn" title="' + esc(title) + '">冷却 ' +
      esc(fmtCooldown(t.cooldown_seconds)) + (why ? " · " + esc(why) : "") + "</span>";
  }
  return '<span class="badge ok">可用</span>';
}

// --- credits column ---------------------------------------------------------

// Billing state is fetched on demand and cached briefly, because each token
// costs three upstream calls. The pool page renders whatever it has and fills
// the column in when the fetch lands; the「查询积分」button forces a refresh.
//
// Keyed by user_id rather than row index: pool rows are addressed by position,
// which shifts every time a token is added or removed, while user_id comes out
// of the JWT and does not move.
const CREDITS_TTL = 5 * 60 * 1000;
const CREDITS_CACHE = { at: 0, loading: false, accounts: [], byKey: {} };

function creditsKey(a) { return a.user_id || a.name; }

// fetchCredits returns { accounts, refreshed }. `refreshed` tells the caller
// whether a real fetch happened, so a page that re-renders itself on new data
// does not loop: a cache hit resolves with the cached rows and refreshed=false.
async function fetchCredits(force) {
  const fresh = CREDITS_CACHE.at && Date.now() - CREDITS_CACHE.at < CREDITS_TTL;
  if (CREDITS_CACHE.loading || (!force && fresh)) {
    return { accounts: fresh ? CREDITS_CACHE.accounts : null, refreshed: false };
  }
  CREDITS_CACHE.loading = true;
  try {
    const d = await api("/admin/api/credits");
    CREDITS_CACHE.accounts = d.accounts || [];
    CREDITS_CACHE.byKey = {};
    CREDITS_CACHE.accounts.forEach((a) => { CREDITS_CACHE.byKey[creditsKey(a)] = a; });
    CREDITS_CACHE.at = Date.now();
    return { accounts: CREDITS_CACHE.accounts, refreshed: true };
  } finally {
    CREDITS_CACHE.loading = false;
  }
}

function creditExpiryCell(ms) {
  if (!ms) return '<span class="xxs muted">—</span>';
  const days = (ms - Date.now()) / 86400000;
  const when = fmtDateTime(ms);
  if (days < 0) {
    return '<span class="xxs" style="color:var(--destructive)">' + esc(when) + "（已过期）</span>";
  }
  const label = when + "（还有 " + days.toFixed(1) + " 天）";
  // Bonus credits live 3 days, so "soon" here is the common case for a fresh
  // account rather than an anomaly worth alarming over.
  if (days <= 3) {
    return '<span class="xxs" style="color:var(--warn)">' + esc(label) + "</span>";
  }
  return '<span class="xxs muted">' + esc(label) + "</span>";
}

function creditCell(t) {
  const c = CREDITS_CACHE.byKey[t.user_id || t.name];
  if (CREDITS_CACHE.loading && !c) {
    return '<span style="display:inline-flex;vertical-align:middle">' + spinner() + "</span>";
  }
  if (!c) {
    return '<span class="xxs muted" title="打开本页时自动查询；也可点「查询积分」">—</span>';
  }
  if (c.error) {
    return '<span class="xxs" style="color:var(--destructive)" title="' + esc(c.error) + '">查询失败</span>';
  }
  const empty = Number(c.balance || 0) <= 0;
  return '<div class="col" style="gap:2px">' +
    '<span class="mono xs tabular" style="' + (empty ? "color:var(--destructive);font-weight:500" : "") + '">' +
    esc(c.balance || "0") +
    (c.credit_name ? ' <span class="badge outline">' + esc(c.credit_name) + "</span>" : "") +
    "</span>" +
    creditExpiryCell(c.expire_at) +
    "</div>";
}

// showCreditsDialog shows the full billing breakdown from data that has already
// been fetched; the refresh itself lives in fetchCredits.
function showCreditsDialog(accounts) {
  const overlay = openDialog({
    title: "账号积分",
    desc: "令牌有效期和积分有效期是两个时钟——令牌约 40 天，新号赠送积分只有 3 天。" +
      "打开号池页时会自动查一次，「查询积分」强制刷新。",
    wide: true,
    body: loadingHtml(160),
    actions: [{ label: "关闭", variant: "secondary" }],
  });
  const body = overlay.querySelector(".dialog-body");

  const all = accounts || [];
  const rows = all.filter((a) => !a.error || a.balance);
  const failed = all.filter((a) => a.error && !a.balance);

  if (!rows.length && !failed.length) {
    body.innerHTML = emptyHtml("号池是空的");
    return;
  }

  const table = rows.length
    ? '<div class="table-wrap"><table class="table"><thead><tr>' +
      "<th>令牌</th><th>余额</th><th>积分类型</th><th>到期</th><th>免费试用</th></tr></thead><tbody>" +
      rows.map((a) =>
        '<tr><td class="xs">' + esc(a.name) + "</td>" +
        '<td class="mono xs tabular">' + esc(a.balance || "—") + "</td>" +
        '<td class="xs">' + (a.credit_name
          ? '<span class="badge outline">' + esc(a.credit_name) + "</span>" +
            (a.lifetime ? '<div class="xxs muted" style="margin-top:4px">' + esc(a.lifetime) + "</div>" : "")
          : '<span class="muted">—</span>') + "</td>" +
        '<td class="xs mono">' + esc(fmtExpiry(a.expire_at)) + "</td>" +
        '<td class="xs">' + (a.free_trial
          ? '<span class="badge ok">' + num(a.free_trial) + " 次</span>"
          : '<span class="muted">—</span>') + "</td></tr>").join("") +
      "</tbody></table></div>"
    : "";

  const failedBlock = failed.length
    ? '<div class="divider" style="margin:14px 0"></div>' +
      '<p class="label" style="margin:0 0 6px">查询失败（' + num(failed.length) + " 个）</p>" +
      '<ul class="about-list" style="margin:0">' + failed.map((a) =>
        "<li>" + esc(a.name) + "：" + esc(a.error) + "</li>").join("") + "</ul>"
    : "";

  body.innerHTML = table + failedBlock +
    '<div class="divider" style="margin:14px 0"></div>' +
    '<p class="hint" style="margin:0">余额只说明还剩多少，不能说明能用多久。' +
    "图像约 40–80 积分/张、H3 视频 70 积分/秒计算，一条 6 秒视频就能吃掉一个纯赠送积分的号。" +
    "订阅与充值积分活得久得多（一个月 / 一年），想长期跑就别依赖新号赠送。</p>";
}

async function pagePool(token) {
  const view = $("view");
  if (!STATE.data) { view.innerHTML = loadingHtml(300); await loadState(); }
  if (token !== currentRender) return;
  const s = STATE.data;
  renderFootStatus();

  const table = s.pool.length
    ? '<div class="table-wrap"><table class="table"><thead><tr>' +
      "<th>名称</th><th>区域</th><th style=\"width:158px\">积分</th><th>状态</th><th>过期</th>" +
      '<th class="right">失败</th><th class="right">并发</th>' +
      '<th class="right" style="width:56px">操作</th></tr></thead><tbody>' +
      s.pool.map((t, i) =>
        '<tr><td><div class="col" style="gap:2px"><span class="xs">' + esc(t.name) + "</span>" +
        (t.user_id ? '<span class="mono xxs muted">' + esc(t.user_id) + "</span>" : "") +
        (t.note ? '<span class="xxs muted">' + esc(t.note) + "</span>" : "") + "</div></td>" +
        '<td class="mono xxs muted">' + esc(t.region) + "</td>" +
        "<td>" + creditCell(t) + "</td>" +
        "<td>" + tokenStatus(t) + "</td>" +
        '<td class="mono xxs muted nowrap">' + esc(fmtDate(t.expire_at)) + "</td>" +
        '<td class="right mono xs tabular">' + num(t.fails) + "</td>" +
        '<td class="right mono xs tabular">' + num(t.inflight) + "</td>" +
        '<td class="right"><button class="btn ghost sm" data-token="' + i + '" aria-label="操作">' +
        icon("more") + "</button></td></tr>").join("") +
      "</tbody></table></div>"
    : emptyHtml("号池是空的。先导入一个令牌——所有推理都要靠它。");

  const summary = [
    { label: "全部", value: s.pool.length },
    { label: "可用", value: s.pool_usable },
    { label: "冷却中", value: s.pool.filter((t) => t.enabled && !t.expired && t.cooling).length },
    { label: "已过期", value: s.pool.filter((t) => t.expired).length },
    { label: "已停用", value: s.pool.filter((t) => !t.enabled).length },
  ];

  view.innerHTML =
    '<div class="page">' +
    pageHeader("号池", "MiniMax Design 账号令牌池，least-inflight 调度。",
      '<button class="btn secondary sm" data-act="credits">' + icon("dollar") + "查询积分</button>" +
      '<button class="btn secondary sm" data-act="reset-pool">' + icon("clockOff") + "清空全部冷却</button>" +
      '<button class="btn secondary sm" data-act="import-local">' + icon("download") + "从本机客户端导入</button>" +
      '<button class="btn sm" data-act="add-token">' + icon("plus") + "添加令牌</button>") +
    '<section class="metrics-grid" style="grid-template-columns:repeat(auto-fit,minmax(140px,1fr))">' +
      summary.map((x) =>
        '<article class="metric" style="min-height:84px"><header class="metric-head"><span>' + esc(x.label) +
        '</span></header><div class="metric-value" style="margin-top:8px;font-size:22px">' + num(x.value) + "</div></article>").join("") +
    "</section>" +
    panel("pool", "令牌", table, null) +
    '<div class="two-col">' +
      panel("pool-rules", "调度与失败策略",
        '<ul class="about-list">' +
        "<li><strong>调度</strong>：least-inflight，永远挑当前并发最低的令牌。</li>" +
        "<li><strong>凭证失效</strong>（401，或 403 且响应体含 invalid token / token expired / 未登录）→ 冷却 30 分钟，且惩罚累加。</li>" +
        "<li><strong>积分耗尽</strong>（余额不足 / insufficient balance 之类）→ 冷却 15 分钟、<strong>不累加</strong>，并且<strong>当场把请求转给下一个令牌</strong>。令牌本身是好的，充值就能恢复，所以不能按凭证失效罚它；但也不能当请求级错误直接返回——一个空号在健康的池子里会白白吃掉本该由别的号处理的请求。</li>" +
        "<li><strong>上游抖动</strong>（传输错误 / 5xx）→ 固定 5 秒、不累加。池里只剩一个可用令牌时完全不冷却：没有别的号可换，罚它只是自己给自己降可用性。</li>" +
        "<li><strong>403 不一律当凭证失效</strong>：网关复用了 403——缺 version_code 是 403，账号没权限用某个模型也是 403。按 403 处理会让一个没权限的模型把整池令牌打进 30 分钟冷却，对外表现为莫名其妙的 no usable token。</li>" +
        "<li><strong>5xx 也不做指数退避</strong>：网关的 500 大多是确定性的请求级拒绝（模型不支持这组参数），不是拥塞。早期版本对它退避，于是「试一个不支持的模型」就能把服务停 5s→10s→20s。</li>" +
        "</ul>", null) +
      panel("pool-config", "服务配置",
        '<div class="kv">' +
        '<span class="k">监听</span><span class="v">' + esc(s.config.listen) + "</span>" +
        '<span class="k">配置网关</span><span class="v break">' + esc(s.config.gateway) + "</span>" +
        '<span class="k">模型网关</span><span class="v break">' + esc(s.config.upstream) + "</span>" +
        '<span class="k">媒体网关</span><span class="v break">' + esc(s.config.video_base) + "</span>" +
        '<span class="k">version_code</span><span class="v">' + esc(s.config.version_code) + "</span>" +
        '<span class="k">默认模型</span><span class="v">' + esc(s.config.default_model) + "</span>" +
        '<span class="k">客户端鉴权</span><span class="v">' +
        ((s.config.api_keys || []).length ? num(s.config.api_keys.length) + " 个 key" : "关闭（任何人可调用）") + "</span>" +
        '<span class="k">超时</span><span class="v">' + num(s.config.timeout_sec) + "s</span>" +
        '<span class="k">配置文件</span><span class="v break">' + esc(s.config_path) + "</span>" +
        "</div>" +
        '<div class="divider" style="margin:14px 0 12px"></div>' +
        '<p class="hint">这些字段可以在「设置」页直接改并保存，不用手改 config.json。</p>', null) +
    "</div></div>";

  view.querySelector('[data-act="add-token"]').onclick = () => addTokenDialog();
  view.querySelector('[data-act="credits"]').onclick = async () => {
    const r = await fetchCredits(true).catch((e) => {
      toast("查询失败：" + e.message, "err");
      return null;
    });
    if (token === currentRender) pagePool(currentRender);
    if (r && r.accounts) showCreditsDialog(r.accounts);
  };

  // Fill the credits column without making the operator click. Cached, so
  // revisiting the page within the TTL is free; a failure leaves the dash and
  // stays quiet rather than nagging on every visit.
  fetchCredits(false).then((r) => {
    if (r && r.refreshed && token === currentRender) pagePool(currentRender);
  }).catch(() => {});
  view.querySelector('[data-act="import-local"]').onclick = async (e) => {
    const btn = e.currentTarget;
    btn.disabled = true;
    try {
      await apiJSON("/admin/api/import-local", "POST");
      toast("已从本机 MiniMax Design 客户端导入令牌", "ok");
      await loadState();
      pagePool(currentRender);
    } catch (err) {
      toast("导入失败：" + err.message, "err");
      btn.disabled = false;
    }
  };
  view.querySelector('[data-act="reset-pool"]').onclick = () => {
    confirmDialog("清空全部冷却？",
      "会把所有令牌的失败计数与冷却时间清零，令牌本身不动。适用于一个没权限的模型把整池令牌卡住的情况。",
      "清空冷却", async () => {
        try {
          await apiJSON("/admin/api/pool/reset", "POST");
          toast("已清空全部冷却", "ok");
          await loadState();
          pagePool(currentRender);
        } catch (err) { toast(err.message, "err"); }
      });
  };

  view.querySelectorAll("[data-token]").forEach((btn) => {
    btn.onclick = () => {
      const i = Number(btn.dataset.token);
      const t = s.pool[i];
      openMenu(btn, [
        { heading: t.name },
        {
          label: t.enabled ? "停用" : "启用",
          icon: t.enabled ? "ban" : "check",
          onClick: async () => {
            try {
              await apiJSON("/admin/api/tokens/" + i, "PUT", { enabled: !t.enabled });
              await loadState();
              pagePool(currentRender);
            } catch (e) { toast(e.message, "err"); }
          },
        },
        { label: "编辑", icon: "edit", onClick: () => editTokenDialog(i) },
        { separator: true },
        {
          label: "删除", icon: "trash", variant: "destructive",
          onClick: () => confirmDialog("删除令牌？", t.name + " 将从号池移除，不可撤销。", "删除", async () => {
            try {
              await apiJSON("/admin/api/tokens/" + i, "DELETE");
              toast("已删除", "ok");
              await loadState();
              pagePool(currentRender);
            } catch (e) { toast(e.message, "err"); }
          }),
        },
      ], { align: "right" });
    };
  });
}

function addTokenDialog() {
  openDialog({
    title: "添加令牌",
    desc: "令牌就是 MiniMax Design 的 access token（JWT）。一行一个，可以批量粘贴。",
    wide: true,
    body: '<div class="col gap-16">' +
      '<div class="field"><label class="label" for="tokInput">令牌</label>' +
      '<textarea class="textarea mono" id="tokInput" placeholder="eyJhbGciOi..."></textarea></div>' +
      '<div class="form-grid">' +
        '<div class="field"><label class="label" for="tokName">名称（可选）</label>' +
        '<input class="input" id="tokName" placeholder="留空则用 JWT 里的 user_id"></div>' +
        '<div class="field"><label class="label" for="tokRegion">区域</label>' +
        selectHtml("tokRegion", [
          { value: "overseas", label: "overseas（海外区）" },
          { value: "domestic", label: "domestic（国内）" },
        ], "overseas") + "</div>" +
      "</div>" +
      '<label class="check"><input type="checkbox" class="checkbox" id="tokReplace">覆盖现有号池（先清空再写入）</label>' +
      '<p class="hint">也可以直接用「从本机客户端导入」：它从 %APPDATA%\\@hilo\\MiniMax Hub Global 解密读取，不用手动复制。</p>' +
      "</div>",
    actions: [
      { label: "取消", variant: "secondary" },
      {
        label: "添加", variant: "",
        onClick: async () => {
          const raw = $("tokInput").value.trim();
          if (!raw) { toast("先粘一个令牌", "err"); return false; }
          const tokens = raw.split(/[\r\n,]+/).map((x) => x.trim()).filter(Boolean);
          try {
            const r = await apiJSON("/admin/api/tokens", "POST", {
              tokens,
              name: $("tokName").value.trim(),
              region: $("tokRegion").value,
              replace: $("tokReplace").checked,
            });
            toast("已加入 " + r.added + " 个令牌", "ok");
            await loadState();
            pagePool(currentRender);
          } catch (e) {
            toast("添加失败：" + e.message, "err");
            return false;
          }
        },
      },
    ],
  });
}

function editTokenDialog(index) {
  const t = STATE.data.pool[index];
  if (!t) return;
  openDialog({
    title: "编辑令牌",
    desc: t.name,
    body: '<div class="col gap-16">' +
      '<div class="field"><label class="label" for="edName">名称</label>' +
      '<input class="input" id="edName" value="' + esc(t.name) + '"></div>' +
      '<div class="field"><label class="label" for="edRegion">区域</label>' +
      selectHtml("edRegion", [
        { value: "overseas", label: "overseas（海外区）" },
        { value: "domestic", label: "domestic（国内）" },
      ], t.region) + "</div>" +
      '<div class="field"><label class="label" for="edToken">替换令牌（留空则不改）</label>' +
      '<input class="input mono" id="edToken" placeholder="eyJhbGciOi..."></div>' +
      '<p class="hint">过期 ' + esc(fmtDate(t.expire_at)) + " · 失败 " + num(t.fails) + " 次 · 并发 " + num(t.inflight) + "</p>" +
      "</div>",
    actions: [
      { label: "取消", variant: "secondary" },
      {
        label: "保存", variant: "",
        onClick: async () => {
          const body = { name: $("edName").value.trim(), region: $("edRegion").value };
          const tk = $("edToken").value.trim();
          if (tk) body.token = tk;
          try {
            await apiJSON("/admin/api/tokens/" + index, "PUT", body);
            toast("已保存", "ok");
            await loadState();
            pagePool(currentRender);
          } catch (e) {
            toast(e.message, "err");
            return false;
          }
        },
      },
    ],
  });
}

/* --- page: API reference ------------------------------------------------ */

function renderDocs(slug) {
  const view = $("view");
  const found = findDoc(slug);
  if (!found) {
    view.innerHTML = '<div class="page">' +
      pageHeader("文档", "接口参考", "") + errorStateHtml("没有这个端点：" + slug) + "</div>";
    return;
  }
  const { group: groupObj, item } = found;
  const hasParams = item.params && item.params[0] && item.params[0][0] !== "—";

  const paramTable = hasParams
    ? '<div class="table-wrap"><table class="table"><thead><tr>' +
      '<th style="width:200px">字段</th><th style="width:110px">类型</th><th>说明</th></tr></thead><tbody>' +
      item.params.map((p) =>
        '<tr><td class="mono xs">' + esc(p[0]) + '</td><td class="mono xxs muted">' + esc(p[1]) + "</td>" +
        '<td class="xs muted">' + esc(p[2]) + "</td></tr>").join("") +
      "</tbody></table></div>"
    : '<p class="hint" style="margin:0">无参数。</p>';

  const notes = (item.notes || []).length
    ? '<div class="panel" style="background:color-mix(in oklab, var(--secondary) 45%, transparent)">' +
      '<div class="row gap-12" style="align-items:flex-start">' +
      '<span style="display:flex;flex:none">' + icon("info") + "</span>" +
      '<div><p class="label" style="margin:0 0 6px">注意事项</p>' +
      '<ul class="about-list" style="margin:0">' + item.notes.map((n) => "<li>" + esc(n) + "</li>").join("") + "</ul>" +
      "</div></div></div>"
    : "";

  view.innerHTML =
    '<div class="page">' +
    pageHeader(item.title, groupObj.group + " · " + item.method + " " + item.path, "") +
    panel("doc-main", "说明", '<p class="xs muted" style="margin:0">' + esc(item.desc) + "</p>", null) +
    panel("doc-params", "请求参数", paramTable, null) +
    panel("doc-curl", "请求示例",
      '<div class="copy-row" style="margin-bottom:10px">' +
        '<span class="badge ' + item.method.toLowerCase() + '">' + esc(item.method) + "</span>" +
        '<code class="mono xs">' + esc(item.path) + "</code>" +
        '<button class="btn ghost sm" data-copy="' + esc(item.curl) + '">' + icon("copy") + "复制</button>" +
      "</div>" +
      '<pre class="code">' + esc(item.curl) + "</pre>", null) +
    notes +
    "</div>";
}

/* --- page: settings ----------------------------------------------------- */

async function pageSettings(token) {
  const view = $("view");
  if (!STATE.data) { view.innerHTML = loadingHtml(300); await loadState(); }
  if (token !== currentRender) return;
  const s = STATE.data;
  const c = s.config;

  view.innerHTML =
    '<div class="page">' +
    pageHeader("设置", "改完直接写回 config.json。除监听地址外都立即生效。",
      '<button class="btn secondary sm" data-act="reload">' + icon("refresh") + "重新拉取目录</button>" +
      '<button class="btn sm" data-act="save">' + icon("check") + "保存配置</button>") +
    panel("cfg", "服务配置",
      '<div class="form-grid">' +
        '<div class="field"><label class="label" for="cfListen">监听地址</label>' +
          '<input class="input mono" id="cfListen" value="' + esc(c.listen) + '">' +
          '<p class="hint">改这个需要重启进程才生效。</p></div>' +
        '<div class="field"><label class="label" for="cfVersion">version_code</label>' +
          '<input class="input mono" id="cfVersion" value="' + esc(c.version_code) + '">' +
          '<p class="hint">上游硬性要求，缺了会被 403 拒掉。</p></div>' +
        '<div class="field"><label class="label" for="cfGateway">配置网关</label>' +
          '<input class="input mono" id="cfGateway" value="' + esc(c.gateway) + '">' +
          '<p class="hint">拉模型目录的地址。</p></div>' +
        '<div class="field"><label class="label" for="cfUpstream">模型网关</label>' +
          '<input class="input mono" id="cfUpstream" value="' + esc(c.upstream) + '">' +
          '<p class="hint">真正发推理请求的地址。</p></div>' +
        '<div class="field"><label class="label" for="cfVideo">媒体网关</label>' +
          '<input class="input mono" id="cfVideo" value="' + esc(c.video_base) + '">' +
          '<p class="hint">视频 / 图像 / 音频所在主机，留空则跟随配置网关。</p></div>' +
        '<div class="field"><label class="label" for="cfModel">默认模型</label>' +
          '<input class="input mono" id="cfModel" value="' + esc(c.default_model) + '">' +
          '<p class="hint">请求里没带 model 时用它。</p></div>' +
        '<div class="field"><label class="label" for="cfAppID">app_id</label>' +
          '<input class="input mono" id="cfAppID" value="' + esc(c.app_id) + '"></div>' +
        '<div class="field"><label class="label" for="cfDevice">device_platform</label>' +
          '<input class="input mono" id="cfDevice" value="' + esc(c.device_platform) + '"></div>' +
        '<div class="field"><label class="label" for="cfTimeout">请求超时（秒）</label>' +
          '<input class="input" id="cfTimeout" type="number" min="1" value="' + esc(c.timeout_sec) + '"></div>' +
        '<div class="field span-2"><label class="label" for="cfKeys">客户端 API Key（每行一个）</label>' +
          '<textarea class="textarea mono" id="cfKeys" style="min-height:72px" ' +
          'placeholder="留空 = 不校验，任何人都能调用">' + esc((c.api_keys || []).join("\n")) + "</textarea>" +
          '<p class="hint">填了就要求调用方带 Authorization: Bearer &lt;key&gt;。调试台里要填同一个 key。</p>' +
        "</div>" +
      "</div>", null) +
    panel("paths", "运行信息",
      '<div class="kv">' +
      '<span class="k">配置文件</span><span class="v break">' + esc(s.config_path) + "</span>" +
      '<span class="k">服务版本</span><span class="v">' + esc(s.version) + "</span>" +
      '<span class="k">启动时间</span><span class="v">' + esc(fmtDateTime(s.started_at * 1000)) + "</span>" +
      '<span class="k">目录来源</span><span class="v">' + esc(s.catalog_source) + "</span>" +
      '<span class="k">目录时间</span><span class="v">' + esc(fmtDateTime(s.catalog_at * 1000)) + "</span>" +
      "</div>", null) +
    panel("danger", "维护操作",
      '<div class="row gap-12 wrap">' +
        '<button class="btn secondary sm" data-act="reset-pool">' + icon("clockOff") + "清空全部冷却</button>" +
        '<button class="btn secondary sm" data-act="reload">' + icon("refresh") + "重新拉取模型目录</button>" +
        '<span class="xxs muted">清空冷却只重置失败计数与退避，不动令牌本身。</span>' +
      "</div>", null) +
    "</div>";

  const saveBtn = view.querySelector('[data-act="save"]');
  saveBtn.onclick = async () => {
    saveBtn.disabled = true;
    const keys = $("cfKeys").value.split(/[\r\n]+/).map((x) => x.trim()).filter(Boolean);
    try {
      await apiJSON("/admin/api/config", "PUT", {
        listen: $("cfListen").value,
        gateway: $("cfGateway").value,
        upstream: $("cfUpstream").value,
        video_base: $("cfVideo").value,
        version_code: $("cfVersion").value,
        app_id: $("cfAppID").value,
        device_platform: $("cfDevice").value,
        default_model: $("cfModel").value,
        timeout_sec: parseInt($("cfTimeout").value, 10) || 600,
        api_keys: keys,
      });
      toast("配置已保存到 " + STATE.data.config_path, "ok");
      await loadState();
      pageSettings(currentRender);
    } catch (e) {
      toast("保存失败：" + e.message, "err");
    } finally {
      saveBtn.disabled = false;
    }
  };

  view.querySelectorAll('[data-act="reload"]').forEach((b) => {
    b.onclick = async () => {
      b.disabled = true;
      try {
        const r = await apiJSON("/admin/api/reload", "POST");
        if (r.error) toast("拉取失败：" + r.error, "err");
        else toast("目录已刷新：" + r.catalog_source, "ok");
        await loadState();
        pageSettings(currentRender);
      } catch (e) {
        toast(e.message, "err");
        b.disabled = false;
      }
    };
  });

  view.querySelector('[data-act="reset-pool"]').onclick = () => {
    confirmDialog("清空全部冷却？", "会把所有令牌的失败计数与冷却时间清零，令牌本身不动。", "清空冷却", async () => {
      try {
        await apiJSON("/admin/api/pool/reset", "POST");
        toast("已清空全部冷却", "ok");
        await loadState();
        pageSettings(currentRender);
      } catch (e) { toast(e.message, "err"); }
    });
  };
}

/* --- page: about -------------------------------------------------------- */

async function pageAbout(token) {
  const view = $("view");
  if (!STATE.data) { view.innerHTML = loadingHtml(300); await loadState(); }
  if (token !== currentRender) return;
  const s = STATE.data;

  const endpoints = [
    ["POST", "/v1/chat/completions", "OpenAI 对话，流式 / 非流式 / 工具 / 多模态"],
    ["POST", "/v1/messages", "Anthropic 原生直通"],
    ["GET", "/v1/models", "模型列表"],
    ["POST", "/v1/images/generations", "OpenAI 图像，内部提交 + 轮询"],
    ["GET", "/v1/image/models", "图像模型目录"],
    ["POST", "/v1/video_generation", "视频任务提交（MiniMax 平台格式）"],
    ["GET", "/v1/query/video_generation", "视频任务查询"],
    ["GET", "/v1/files/retrieve", "视频下载地址"],
    ["GET", "/v1/video/models", "视频模型目录"],
    ["POST", "/v1/audio/speech", "OpenAI TTS"],
    ["GET", "/v1/audio/voices", "音色目录"],
    ["POST", "/v1/lyrics/generations", "歌词生成"],
    ["POST", "/v1/music/generations", "音乐任务提交"],
    ["GET", "/v1/query/music_generation", "音乐任务查询"],
    ["GET", "/health", "健康检查 + 号池概览"],
  ];

  view.innerHTML =
    '<div class="page">' +
    pageHeader("关于", "这个服务做了什么、怎么来的、以及还没做什么。", "") +
    panel("about-what", "它是什么",
      '<div class="kv">' +
      '<span class="k">目标</span><span class="v" style="font-family:inherit">把 MiniMax Design 桌面版内置的模型反代成 OpenAI 兼容 API</span>' +
      '<span class="k">服务版本</span><span class="v">' + esc(s.version) + "</span>" +
      '<span class="k">依赖</span><span class="v" style="font-family:inherit">纯 Go 标准库，单二进制 + 单 config.json</span>' +
      '<span class="k">管理台</span><span class="v" style="font-family:inherit">单文件 HTML/CSS/JS，无构建步骤，go:embed 进二进制</span>' +
      '<span class="k">UI 参考</span><span class="v" style="font-family:inherit">chenyme/grok2api 的设计语言</span>' +
      "</div>", null) +
    panel("about-endpoints", "接口清单",
      '<div class="table-wrap"><table class="table"><thead><tr>' +
      '<th style="width:80px">方法</th><th style="width:280px">路径</th><th>说明</th></tr></thead><tbody>' +
      endpoints.map((e) =>
        "<tr><td><span class=\"badge " + e[0].toLowerCase() + "\">" + esc(e[0]) + "</span></td>" +
        '<td class="mono xs">' + esc(e[1]) + "</td>" +
        '<td class="xs muted">' + esc(e[2]) + "</td></tr>").join("") +
      "</tbody></table></div>" +
      '<div class="divider" style="margin:16px 0 12px"></div>' +
      '<p class="hint">管理台接口：/admin/api/state、/admin/api/metrics、/admin/api/catalogs、' +
      "/admin/api/config、/admin/api/tokens、/admin/api/tokens/{i}、/admin/api/import-local、" +
      "/admin/api/reload、/admin/api/pool/reset</p>", null) +
    '<div class="two-col">' +
      panel("about-re", "逆向结论",
        '<ul class="about-list">' +
        "<li><strong>客户端形态</strong>：Electron 应用，主进程代码在 resources/app.asar；渲染层 React，AI 运行时是内嵌的 opencode。</li>" +
        "<li><strong>凭据位置</strong>：%APPDATA%\\@hilo\\MiniMax Hub Global\\hub-config-global.json 里的 tokens.accessToken，格式 v2enc:&lt;iv&gt;:&lt;tag&gt;:&lt;密文&gt;。</li>" +
        "<li><strong>加密</strong>：AES-256-GCM，密钥是同目录的 .token-key（裸 32 字节）。IV 是 16 字节，Go 的 cipher.NewGCM 只收 12 字节，得用 NewGCMWithNonceSize。</li>" +
        "<li><strong>模型清单</strong>：客户端启动时 GET {gateway}/api/v1/config 拿 provider 矩阵，npm 字段就是协议指纹。</li>" +
        "<li><strong>最小请求头</strong>：只需 token 与 version_code。少了 token 报 401「token 为空」，少了 version_code 报 403。注意不是 Authorization，也不是 x-api-key。</li>" +
        "<li><strong>gamma 用 Responses 而非 chat/completions</strong>：它的模型配置里带 store:false / reasoningEffort / include，直接打 /chat/completions 是 404。</li>" +
        "</ul>", null) +
      panel("about-limits", "已知限制",
        '<ul class="about-list">' +
        "<li>omega-3.1-pro 路由未定位，返回 501。</li>" +
        "<li>Kling / Veo / Jimeng 视频要会员订阅，非会员会收到上游 403 permission_error。</li>" +
        "<li>视频 callback_url 被忽略，上游不支持回调，只能轮询。</li>" +
        "<li>视频按次定价是平价的、不随时长变——new-api 的 hailuo 适配器没实现 TaskUsageFactsProvider。</li>" +
        "<li>图像只做了文生图 + URL/data-URI 参考图；本地文件上传与 /v1/images/edits 没做。</li>" +
        "<li>音乐 / 歌词只能直连本服务，new-api 没有对应中继路由。</li>" +
        "<li>语音克隆、音色设计、人声分离、tts/batch 都没做。</li>" +
        "<li>令牌有效期约 40 天；额度过期比令牌更早，到期后所有生成都会失败，排查时先看余额。</li>" +
        "<li>号池没有自动刷新令牌的机制。</li>" +
        "</ul>", null) +
    "</div>" +
    "</div>";
}
