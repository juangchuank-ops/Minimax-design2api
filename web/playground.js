/* ==========================================================================
   MiniMax Design 2API — admin console · media playground
   --------------------------------------------------------------------------
   Exercises the real /v1 endpoints over the same wire path an external
   client would use: chat (SSE), images, video (submit + poll) and TTS.

   Loaded after app.js and pages.js; shares their globals.
   ========================================================================== */

"use strict";

let pgTab = "chat";
let pgAbort = null;

async function pagePlayground(token) {
  const view = $("view");
  if (!STATE.data) { view.innerHTML = loadingHtml(300); await loadState(); }
  if (token !== currentRender) return;
  const s = STATE.data;

  const tabs = [
    { id: "chat", label: "聊天", icon: "message" },
    { id: "image", label: "图像", icon: "image" },
    { id: "video", label: "视频", icon: "video" },
    { id: "voice", label: "语音", icon: "audio" },
  ];
  const tabsHtml = '<div class="tabs">' + tabs.map((t) =>
    '<button class="tab' + (pgTab === t.id ? " active" : "") + '" data-tab="' + t.id + '">' +
    icon(t.icon) + esc(t.label) + "</button>").join("") + "</div>";

  view.innerHTML =
    '<div class="page">' +
    pageHeader("调试台", "走本服务真实的 /v1 接口，和外部客户端完全同一条链路。", tabsHtml) +
    panel("play-key", "客户端密钥",
      '<div class="row gap-12 wrap">' +
        '<input class="input mono" id="pgKey" style="max-width:400px" ' +
        'placeholder="sk-... （config.json 的 api_keys 为空时留空即可）" value="' + esc(clientKey()) + '">' +
        '<button class="btn secondary sm" data-act="save-key">保存</button>' +
        '<button class="btn ghost sm" data-act="clear-key">清除</button>' +
        '<span class="xxs muted">只存在本机 localStorage，不写进服务端配置。</span>' +
      "</div>", null) +
    '<div id="pgBody">' + loadingHtml(200) + "</div>" +
    "</div>";

  view.querySelector('[data-act="save-key"]').onclick = () => {
    setClientKey($("pgKey").value.trim());
    toast(clientKey() ? "客户端密钥已保存" : "客户端密钥已清空", "ok");
  };
  view.querySelector('[data-act="clear-key"]').onclick = () => {
    setClientKey("");
    $("pgKey").value = "";
    toast("已清空客户端密钥", "ok");
  };
  view.querySelectorAll("[data-tab]").forEach((b) => {
    b.onclick = () => { pgTab = b.dataset.tab; pagePlayground(currentRender); };
  });

  const body = view.querySelector("#pgBody");
  if (pgTab === "chat") renderPlayChat(body, s);
  else if (pgTab === "image") await renderPlayImage(body);
  else if (pgTab === "video") await renderPlayVideo(body);
  else await renderPlayVoice(body);
}

/* --- chat --------------------------------------------------------------- */

function renderPlayChat(body, s) {
  // Only the bare id is offered: the provider-qualified spelling is the same
  // model, and listing both would just double the dropdown.
  const models = s.models.filter((m) => m.id.indexOf("/") < 0);
  const usable = models.filter((m) => m.protocol !== "gemini");
  const unsupported = models.filter((m) => m.protocol === "gemini");
  const options = usable.map((m) => ({ value: m.id, label: m.id + " · " + m.protocol }))
    .concat(unsupported.map((m) => ({ value: m.id, label: m.id + " · 未支持（501）", disabled: true })));

  body.innerHTML = panel("pg-chat", "对话",
    '<div class="col gap-16">' +
      '<div class="row gap-12 wrap">' +
        '<div class="field" style="width:260px">' + selectHtml("pcModel", options, s.config.default_model) + "</div>" +
        '<label class="check"><input type="checkbox" class="checkbox" id="pcStream" checked>流式</label>' +
        '<label class="check"><input type="checkbox" class="checkbox" id="pcReasoning">显示推理内容</label>' +
        '<div class="field" style="width:130px"><input class="input" id="pcMaxTokens" type="number" min="1" value="4096"></div>' +
        '<button class="btn sm" data-act="send">' + icon("play") + "发送</button>" +
        '<button class="btn secondary sm" data-act="stop" disabled>停止</button>' +
      "</div>" +
      '<div class="field"><label class="label" for="pcSystem">System（可选）</label>' +
      '<textarea class="textarea" id="pcSystem" style="min-height:56px" placeholder="留空则不发送 system 消息"></textarea></div>' +
      '<div class="field"><label class="label" for="pcPrompt">用户消息</label>' +
      '<textarea class="textarea" id="pcPrompt" style="min-height:96px">用一句话介绍一下这个服务是什么。</textarea></div>' +
      '<div class="field"><label class="label">输出</label><pre class="result-box" id="pcOut">—</pre></div>' +
    "</div>", null);

  const out = body.querySelector("#pcOut");
  const sendBtn = body.querySelector('[data-act="send"]');
  const stopBtn = body.querySelector('[data-act="stop"]');

  sendBtn.onclick = async () => {
    const model = body.querySelector("#pcModel").value;
    const prompt = body.querySelector("#pcPrompt").value;
    if (!model) return toast("先选一个模型", "err");
    if (!prompt.trim()) return toast("先写点内容", "err");

    const stream = body.querySelector("#pcStream").checked;
    const showReasoning = body.querySelector("#pcReasoning").checked;
    const maxTokens = parseInt(body.querySelector("#pcMaxTokens").value, 10) || 0;
    const system = body.querySelector("#pcSystem").value.trim();

    const messages = [];
    if (system) messages.push({ role: "system", content: system });
    messages.push({ role: "user", content: prompt });

    const payload = { model, messages, stream };
    if (maxTokens > 0) payload.max_tokens = maxTokens;

    sendBtn.disabled = true;
    stopBtn.disabled = false;
    out.textContent = "";
    const started = performance.now();
    pgAbort = new AbortController();

    try {
      const res = await fetch("/v1/chat/completions", {
        method: "POST",
        headers: v1Headers({ "content-type": "application/json" }),
        body: JSON.stringify(payload),
        signal: pgAbort.signal,
      });

      if (!res.ok) {
        const text = await res.text();
        let msg = "HTTP " + res.status;
        try { const j = JSON.parse(text); msg = (j.error && j.error.message) || msg; } catch (e) {}
        out.textContent = "[错误] " + msg;
        toast(msg, "err");
        return;
      }

      if (!stream) {
        const j = await res.json();
        const msg = (j.choices && j.choices[0] && j.choices[0].message) || {};
        const usage = j.usage
          ? "\n\n[tokens] prompt " + num(j.usage.prompt_tokens) +
            " · completion " + num(j.usage.completion_tokens) +
            " · total " + num(j.usage.total_tokens)
          : "";
        out.textContent = (msg.reasoning_content ? "[推理]\n" + msg.reasoning_content + "\n\n[正文]\n" : "") +
          (msg.content || "") + usage;
        return;
      }

      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = "", text = "", reasoning = "";
      for (;;) {
        const chunk = await reader.read();
        if (chunk.done) break;
        buf += dec.decode(chunk.value, { stream: true });
        const lines = buf.split("\n");
        buf = lines.pop();
        for (const line of lines) {
          if (line.indexOf("data: ") !== 0) continue;
          const raw = line.slice(6).trim();
          if (!raw || raw === "[DONE]") continue;
          let j;
          try { j = JSON.parse(raw); } catch (e) { continue; }
          if (j.error) { out.textContent += "\n[错误] " + j.error.message; continue; }
          const d = j.choices && j.choices[0] && j.choices[0].delta;
          if (!d) continue;
          if (d.reasoning_content) reasoning += d.reasoning_content;
          if (d.content) text += d.content;
          out.textContent = (showReasoning && reasoning ? "[推理]\n" + reasoning + "\n\n[正文]\n" : "") + text;
          out.scrollTop = out.scrollHeight;
        }
      }
      out.textContent += "\n\n[" + fmtMs(performance.now() - started) + " · " + num(text.length) + " 字符]";
    } catch (e) {
      if (e.name === "AbortError") out.textContent += "\n\n[已停止]";
      else out.textContent += "\n[异常] " + e.message;
    } finally {
      sendBtn.disabled = false;
      stopBtn.disabled = true;
      pgAbort = null;
    }
  };

  stopBtn.onclick = () => { if (pgAbort) pgAbort.abort(); };
}

/* --- image -------------------------------------------------------------- */

async function renderPlayImage(body) {
  body.innerHTML = panel("pg-image", "图像生成", catalogLoadingHtml(), null);
  let cat;
  try {
    cat = await catalogs();
  } catch (e) {
    body.innerHTML = panel("pg-image", "图像生成", errorStateHtml(e.message), null);
    return;
  }
  const list = cat.image || [];
  if (!list.length) {
    body.innerHTML = panel("pg-image", "图像生成",
      emptyHtml(cat.image_error ? "目录拉取失败：" + cat.image_error : "目录里没有图像模型"), null);
    return;
  }

  const options = list.map((m) => ({ value: m.id, label: m.id + " · " + m.backend }));
  const current = () => list.find((x) => x.id === body.querySelector("#piModel").value) || list[0];

  const paramFields = () => {
    const m = current();
    const ratios = paramOptions(m.params, "aspect_ratio");
    const resolutions = paramOptions(m.params, "resolution");
    const sizes = paramOptions(m.params, "size");
    // No leading "" here: selectHtml already emits the placeholder option, and
    // a duplicated empty value would claim the selection and render blank.
    return '<div class="form-grid">' +
      '<div class="field"><label class="label">比例 aspect_ratio</label>' +
        selectHtml("piRatio", ratios, paramDefault(m.params, "aspect_ratio"), "不指定") + "</div>" +
      '<div class="field"><label class="label">分辨率 resolution</label>' +
        selectHtml("piRes", resolutions, paramDefault(m.params, "resolution"), "不指定") + "</div>" +
      // `size` is a real OpenAI field but the catalogue rarely declares options
      // for it, so it is a free-text input with the catalogue values offered as
      // suggestions — a plain select would either lose the field or invent
      // options the hub never confirmed.
      '<div class="field"><label class="label">尺寸 size</label>' +
        '<input class="input mono" id="piSize" list="piSizeOptions" value="' +
        esc(paramDefault(m.params, "size")) + '" placeholder="' +
        esc(sizes.length ? sizes.join(" / ") : "如 1024x1024") + '">' +
        '<datalist id="piSizeOptions">' +
        sizes.map((s) => '<option value="' + esc(s) + '"></option>').join("") +
        "</datalist></div>" +
      '<div class="field"><label class="label">张数 n</label>' +
        '<input class="input" id="piN" type="number" min="1" max="4" value="1"></div>' +
      "</div>" +
      '<p class="hint" style="margin-top:8px">当前 backend：<code class="mono">' + esc(m.backend) +
      "</code>；上游模型名 <code class=\"mono\">" + esc(m.model_name || "—") +
      "</code>。各 backend 的请求体不同，由服务端按 backend 分支构造。</p>";
  };

  body.innerHTML = panel("pg-image", "图像生成",
    '<div class="col gap-16">' +
      '<div class="row gap-12 wrap">' +
        '<div class="field" style="width:260px">' + selectHtml("piModel", options, list[0].id) + "</div>" +
        '<div class="field" style="width:160px"><label class="label">返回格式</label>' +
          selectHtml("piFormat", [
            { value: "url", label: "url（默认）" },
            { value: "b64_json", label: "b64_json" },
          ], "url") + "</div>" +
        '<div style="align-self:flex-end"><button class="btn sm" data-act="send">' + icon("play") + "生成</button></div>" +
      "</div>" +
      '<div class="field"><label class="label" for="piPrompt">提示词</label>' +
        '<textarea class="textarea" id="piPrompt" style="min-height:80px">一只在午后阳光里打盹的橘猫，胶片质感</textarea></div>' +
      '<div class="field"><label class="label" for="piRef">参考图（可选，URL 或 data URI，每行一个）</label>' +
        '<textarea class="textarea mono" id="piRef" style="min-height:52px" placeholder="https://..."></textarea></div>' +
      '<div id="piParams"></div>' +
      '<div class="field"><label class="label">输出</label><div id="piOut"><p class="hint">尚未生成。</p></div></div>' +
    "</div>", null);

  const paintParams = () => {
    body.querySelector("#piParams").innerHTML = paramFields();
    body.querySelector("#piModel").onchange = paintParams;
  };
  paintParams();

  body.querySelector('[data-act="send"]').onclick = async (e) => {
    const btn = e.currentTarget;
    const out = body.querySelector("#piOut");
    const prompt = body.querySelector("#piPrompt").value.trim();
    if (!prompt) return toast("先写提示词", "err");
    btn.disabled = true;
    out.innerHTML = loadingHtml(140);
    const started = performance.now();

    const payload = {
      model: body.querySelector("#piModel").value,
      prompt,
      n: parseInt(body.querySelector("#piN").value, 10) || 1,
    };
    const ratio = body.querySelector("#piRatio").value;
    const resolution = body.querySelector("#piRes").value;
    const size = body.querySelector("#piSize").value;
    const format = body.querySelector("#piFormat").value;
    if (ratio) payload.aspect_ratio = ratio;
    if (resolution) payload.resolution = resolution;
    if (size) payload.size = size;
    if (format) payload.response_format = format;
    const refs = body.querySelector("#piRef").value.split(/[\r\n]+/).map((x) => x.trim()).filter(Boolean);
    if (refs.length) payload.image_paths = refs;

    try {
      const j = await api("/v1/images/generations", {
        method: "POST",
        headers: v1Headers({ "content-type": "application/json" }),
        body: JSON.stringify(payload),
      });
      const items = (j.data || []).map((d) => {
        const src = d.url || (d.b64_json ? "data:image/png;base64," + d.b64_json : "");
        if (!src) return "";
        return '<div class="media-item"><img src="' + esc(src) + '" alt="生成结果" loading="lazy">' +
          '<div class="cap"><a class="btn-link" href="' + esc(src) + '" target="_blank" rel="noreferrer">打开原图</a>' +
          (d.revised_prompt ? '<div class="xxs muted" style="margin-top:4px">' + esc(d.revised_prompt) + "</div>" : "") +
          "</div></div>";
      }).join("");
      out.innerHTML = (items ? '<div class="media-grid">' + items + "</div>" : emptyHtml("响应里没有图像")) +
        '<p class="hint" style="margin-top:12px">耗时 ' + esc(fmtMs(performance.now() - started)) +
        " · 共 " + num((j.data || []).length) + " 张</p>";
      toast("生成完成", "ok");
    } catch (err) {
      out.innerHTML = '<pre class="result-box" style="color:var(--destructive)">' + esc(err.message) + "</pre>";
      toast(err.message, "err");
    } finally {
      btn.disabled = false;
    }
  };
}

/* --- video -------------------------------------------------------------- */

async function renderPlayVideo(body) {
  body.innerHTML = panel("pg-video", "视频生成", catalogLoadingHtml(), null);
  let cat;
  try {
    cat = await catalogs();
  } catch (e) {
    body.innerHTML = panel("pg-video", "视频生成", errorStateHtml(e.message), null);
    return;
  }
  const list = cat.video || [];
  if (!list.length) {
    body.innerHTML = panel("pg-video", "视频生成",
      emptyHtml(cat.video_error ? "目录拉取失败：" + cat.video_error : "目录里没有视频模型"), null);
    return;
  }

  // `members_only` comes from the catalogue mirror, which reads it off the
  // server's backend table. Do not re-derive it from backend names here — the
  // real ids (kling_avatar, kling_motion_control, jimeng_motion_control) are
  // not what a hand-written list would guess.
  const options = list.map((m) => ({ value: m.id, label: m.id + " · " + (m.backend || "?") }));
  const current = () => list.find((x) => x.id === body.querySelector("#pvModel").value) || list[0];

  const paramFields = () => {
    const m = current();
    const resolutions = paramOptions(m.params, "resolution");
    const ratios = paramOptions(m.params, "ratio");
    const durations = paramOptions(m.params, "duration");
    const members = !!m.members_only;
    // The catalogue advertises `adaptive`, but the generator rejects it for
    // text-to-video and the server silently rewrites it to 16:9. Offering a
    // choice that is quietly overridden is worse than not offering it, so it
    // is dropped and the placeholder ("derive from size") takes over.
    const usableRatios = ratios.filter((r) => String(r).toLowerCase() !== "adaptive");
    // Only preselect the catalogue default when it survived that filter.
    const catalogueRatio = paramDefault(m.params, "ratio");
    const defaultRatio = usableRatios.indexOf(catalogueRatio) >= 0 ? catalogueRatio : "";
    return '<div class="form-grid">' +
      '<div class="field"><label class="label">分辨率 resolution</label>' +
        selectHtml("pvRes", resolutions, paramDefault(m.params, "resolution"), "默认") + "</div>" +
      '<div class="field"><label class="label">比例 ratio</label>' +
        selectHtml("pvRatio", usableRatios, defaultRatio, "不指定（由 size 推导，兜底 16:9）") + "</div>" +
      '<div class="field"><label class="label">时长 duration（秒）</label>' +
        selectHtml("pvDur", durations, paramDefault(m.params, "duration"), "默认 6") + "</div>" +
      '<div class="field"><label class="label">size（可选）</label>' +
        '<input class="input" id="pvSize" placeholder="1280x720"></div>' +
      "</div>" +
      (members
        ? '<p class="hint" style="margin-top:10px">⚠️ <code class="mono">' + esc(m.backend) +
          "</code> 是会员专属后端，非会员账号会收到上游 403 permission_error。这是账号权限，不是配置问题——本服务会把上游的 403 原样透传。</p>"
        : "");
  };

  body.innerHTML = panel("pg-video", "视频生成",
    '<div class="col gap-16">' +
      '<div class="row gap-12 wrap">' +
        '<div class="field" style="width:280px">' + selectHtml("pvModel", options, list[0].id) + "</div>" +
        '<div style="align-self:flex-end"><button class="btn sm" data-act="send">' + icon("play") + "提交任务</button></div>" +
        '<span class="xxs muted">提交后每 5 秒轮询一次，最长等 10 分钟。</span>' +
      "</div>" +
      '<div class="field"><label class="label" for="pvPrompt">提示词</label>' +
        '<textarea class="textarea" id="pvPrompt" style="min-height:80px">一条纸船在雨后的排水沟里漂向远处</textarea></div>' +
      '<div id="pvParams"></div>' +
      '<div class="field"><label class="label">任务状态</label><div id="pvOut"><p class="hint">尚未提交。</p></div></div>' +
    "</div>", null);

  const paintParams = () => {
    body.querySelector("#pvParams").innerHTML = paramFields();
    body.querySelector("#pvModel").onchange = paintParams;
  };
  paintParams();

  const out = body.querySelector("#pvOut");
  const log = [];
  const paint = (busy) => {
    out.innerHTML = '<div class="col" style="gap:10px">' +
      log.map((l) =>
        '<div class="row gap-12" style="align-items:flex-start"><span class="mono xxs muted" style="width:66px;flex:none">' +
        esc(l.t) + '</span><span class="xs">' + esc(l.m) + "</span></div>").join("") +
      (busy ? '<div class="row gap-12"><span style="width:66px;flex:none;display:flex">' + spinner() +
        '</span><span class="xs muted">' + esc(busy) + "</span></div>" : "") +
      "</div>";
  };

  body.querySelector('[data-act="send"]').onclick = async (e) => {
    const btn = e.currentTarget;
    const prompt = body.querySelector("#pvPrompt").value.trim();
    if (!prompt) return toast("先写提示词", "err");
    btn.disabled = true;
    log.length = 0;

    const payload = { model: body.querySelector("#pvModel").value, prompt };
    const resolution = body.querySelector("#pvRes").value;
    const ratio = body.querySelector("#pvRatio").value;
    const duration = body.querySelector("#pvDur").value;
    const size = body.querySelector("#pvSize").value.trim();
    if (resolution) payload.resolution = resolution;
    if (ratio) payload.ratio = ratio;
    if (duration) payload.duration = parseInt(duration, 10);
    if (size) payload.size = size;

    const started = performance.now();
    try {
      log.push({ t: fmtDateTime(Date.now()), m: "提交 " + payload.model });
      paint("提交中…");

      const submit = await api("/v1/video_generation", {
        method: "POST",
        headers: v1Headers({ "content-type": "application/json" }),
        body: JSON.stringify(payload),
      });
      const taskId = submit.task_id;
      log.push({ t: fmtDateTime(Date.now()), m: "已受理 task_id = " + taskId });
      paint("轮询中…");

      let fileId = "";
      let lastStatus = "";
      for (let i = 0; i < 120; i++) {
        await new Promise((r) => setTimeout(r, 5000));
        const q = await api("/v1/query/video_generation?task_id=" + encodeURIComponent(taskId), {
          headers: v1Headers(),
        });
        const status = q.status || "";
        if (status !== lastStatus) {
          log.push({ t: fmtDateTime(Date.now()), m: "状态 " + status + (q.progress ? "（" + q.progress + "）" : "") });
          lastStatus = status;
        }
        paint("轮询中…（已等待 " + fmtMs(performance.now() - started) + "）");
        if (status === "Success") { fileId = q.file_id || ""; break; }
        if (status === "Fail") {
          const detail = q.base_resp && q.base_resp.status_msg ? "：" + q.base_resp.status_msg : "";
          throw new Error("上游返回任务失败" + detail);
        }
      }
      if (!fileId) throw new Error("等待超时（10 分钟）仍未出片");

      log.push({ t: fmtDateTime(Date.now()), m: "取下载地址…" });
      paint("取文件…");
      const file = await api("/v1/files/retrieve?file_id=" + encodeURIComponent(fileId), { headers: v1Headers() });
      const url = (file.file && file.file.download_url) || "";
      log.push({ t: fmtDateTime(Date.now()), m: "完成，总耗时 " + fmtMs(performance.now() - started) });
      paint("");

      out.innerHTML =
        '<div class="col gap-16">' +
          '<div class="col" style="gap:10px">' + log.map((l) =>
            '<div class="row gap-12" style="align-items:flex-start">' +
            '<span class="mono xxs muted" style="width:66px;flex:none">' + esc(l.t) + "</span>" +
            '<span class="xs">' + esc(l.m) + "</span></div>").join("") + "</div>" +
          (url
            ? '<div class="media-item" style="max-width:420px"><video src="' + esc(url) +
              '" controls preload="metadata"></video><div class="cap">' +
              '<a class="btn-link" href="' + esc(url) + '" target="_blank" rel="noreferrer">打开视频</a></div></div>'
            : "") +
          '<div class="kv">' +
            '<span class="k">task_id</span><span class="v break">' + esc(taskId) + "</span>" +
            '<span class="k">file_id</span><span class="v break">' + esc(fileId) + "</span>" +
          "</div>" +
        "</div>";
      toast("视频已生成", "ok");
    } catch (err) {
      log.push({ t: fmtDateTime(Date.now()), m: "失败：" + err.message });
      out.innerHTML = '<div class="col" style="gap:10px">' + log.map((l) =>
        '<div class="row gap-12" style="align-items:flex-start">' +
        '<span class="mono xxs muted" style="width:66px;flex:none">' + esc(l.t) + "</span>" +
        '<span class="xs" style="color:var(--destructive)">' + esc(l.m) + "</span></div>").join("") +
        '<p class="hint">提示：同一个 task_id 可以再用 GET /v1/query/video_generation 查，任务在上游继续跑。</p></div>';
      toast(err.message, "err");
    } finally {
      btn.disabled = false;
    }
  };
}

/* --- voice -------------------------------------------------------------- */

async function renderPlayVoice(body) {
  body.innerHTML = panel("pg-voice", "语音合成", catalogLoadingHtml(), null);
  let cat;
  try {
    cat = await catalogs();
  } catch (e) {
    body.innerHTML = panel("pg-voice", "语音合成", errorStateHtml(e.message), null);
    return;
  }
  const voices = cat.voices || [];
  const languages = Array.from(new Set(voices.map((v) => v.language).filter(Boolean))).sort();

  const ALIASES = ["alloy", "nova", "onyx", "echo", "fable", "shimmer"];

  body.innerHTML = panel("pg-voice", "语音合成",
    '<div class="col gap-16">' +
      '<div class="row gap-12 wrap">' +
        '<div class="field" style="width:170px"><label class="label">模型</label>' +
          selectHtml("paModel", ["tts-1", "speech-2.8-hd", "speech-2.6-turbo"], "tts-1") + "</div>" +
        '<div class="field" style="width:150px"><label class="label">语言过滤</label>' +
          selectHtml("paLang", languages, "", "全部") + "</div>" +
        '<div class="field" style="width:200px"><label class="label">搜索音色</label>' +
          '<input class="input" id="paQuery" placeholder="voice_id / 名称"></div>' +
        '<div class="field" style="width:290px"><label class="label">音色</label>' +
          selectHtml("paVoice", ALIASES, "nova") + "</div>" +
      "</div>" +
      '<div class="field"><label class="label" for="paText">文本</label>' +
        '<textarea class="textarea" id="paText" style="min-height:80px">你好，这里是 MiniMax Design 反向代理服务。</textarea></div>' +
      '<div class="row gap-12 wrap">' +
        '<button class="btn sm" data-act="send">' + icon("play") + "合成</button>" +
        '<span class="xxs muted">' +
        (voices.length
          ? "目录里共 " + num(voices.length) + " 个音色。也可以直接改成一个真实 voice_id——目录不全，能查到的未必能用，能用也未必查得到。"
          : "音色目录不可用，仍可直接填 voice_id 试验。") +
        "</span>" +
      "</div>" +
      '<div class="field"><label class="label">输出</label><div id="paOut"><p class="hint">尚未合成。</p></div></div>' +
    "</div>", null);

  const voiceSel = body.querySelector("#paVoice");
  const qInput = body.querySelector("#paQuery");
  const langSel = body.querySelector("#paLang");
  let searchTimer = null;

  async function reloadVoices() {
    const params = new URLSearchParams();
    if (langSel.value) params.set("language", langSel.value);
    if (qInput.value.trim()) params.set("q", qInput.value.trim());
    try {
      const d = await api("/admin/api/catalogs" + (params.toString() ? "?" + params.toString() : ""));
      // Cap the list: the full table is 671 entries and a huge <select> is
      // worse than a filtered one.
      const list = (d.voices || []).slice(0, 400);
      const previous = voiceSel.value;
      voiceSel.innerHTML = ALIASES
        .map((v) => '<option value="' + v + '">' + v + "（OpenAI 别名）</option>").join("") +
        list.map((v) => '<option value="' + esc(v.id) + '">' + esc(v.id) +
          (v.name ? " · " + esc(v.name) : "") + (v.language ? " · " + esc(v.language) : "") + "</option>").join("");
      const stillThere = Array.prototype.some.call(voiceSel.options, (o) => o.value === previous);
      voiceSel.value = stillThere ? previous : "nova";
    } catch (e) {
      toast("音色目录拉取失败：" + e.message, "err");
    }
  }

  qInput.oninput = () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(reloadVoices, 320);
  };
  langSel.onchange = reloadVoices;

  body.querySelector('[data-act="send"]').onclick = async (e) => {
    const btn = e.currentTarget;
    const out = body.querySelector("#paOut");
    const text = body.querySelector("#paText").value.trim();
    if (!text) return toast("先写文本", "err");
    btn.disabled = true;
    out.innerHTML = loadingHtml(120);
    const started = performance.now();

    try {
      const res = await fetch("/v1/audio/speech", {
        method: "POST",
        headers: v1Headers({ "content-type": "application/json" }),
        body: JSON.stringify({
          model: body.querySelector("#paModel").value,
          input: text,
          voice: voiceSel.value,
        }),
      });
      if (!res.ok) {
        const text2 = await res.text();
        let msg = "HTTP " + res.status;
        try { const j = JSON.parse(text2); msg = (j.error && j.error.message) || msg; } catch (e2) {}
        throw new Error(msg);
      }
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      out.innerHTML = '<div class="col gap-12">' +
        '<audio controls src="' + url + '" style="width:100%;max-width:420px"></audio>' +
        '<div class="kv">' +
        '<span class="k">音色</span><span class="v">' + esc(voiceSel.value) + "</span>" +
        '<span class="k">大小</span><span class="v">' + num(blob.size / 1024, 1) + " KB</span>" +
        '<span class="k">耗时</span><span class="v">' + esc(fmtMs(performance.now() - started)) + "</span>" +
        "</div>" +
        '<div><a class="btn secondary sm" href="' + url + '" download="speech.mp3">' +
        icon("download") + "下载音频</a></div>" +
        "</div>";
      toast("合成完成", "ok");
    } catch (err) {
      out.innerHTML = '<pre class="result-box" style="color:var(--destructive)">' + esc(err.message) + "</pre>";
      toast(err.message, "err");
    } finally {
      btn.disabled = false;
    }
  };
}
