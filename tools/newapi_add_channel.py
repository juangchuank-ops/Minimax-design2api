#!/usr/bin/env python3
"""把本地的 MiniMax Design 2api 加成 new-api 的渠道。

内置三个 profile：

  llm    渠道类型 1  (OpenAI 兼容)      —— 文本模型 + TTS，走 /v1/chat/completions
                                          和 /v1/audio/speech
  video  渠道类型 35 (MiniMax/hailuo)   —— 视频模型，走 /v1/video_generation
  image  渠道类型 1  (OpenAI 兼容)      —— 图像模型，走 /v1/images/generations

用法：
  python newapi_add_channel.py --list
  python newapi_add_channel.py --dry-run [profile]
  python newapi_add_channel.py [profile]

不加 profile 时按内置顺序全部处理。已存在同名渠道会改为更新（幂等）。

注：image profile 的 test_model 留空是有意的 —— new-api 的渠道测试发的是
chat 请求，而这几个模型只做图像，测试必然失败（不影响实际调用）。
"""
import json
import sys
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:3000"
TOKEN = open("recon/.natk").read().strip()
USER_ID = "1"

TEMPLATE_ID = 2  # 一个健康的 type=1 (OpenAI) 渠道，克隆它的字段结构

LOCAL_BASE = "http://127.0.0.1:18080"  # 不带 /v1，new-api 自己拼路径

TEXT_MODELS = [
    "MiniMax-M3",
    "MiniMax-M2.7",
    "alpha",
    "alpha_high",
    "gamma",
    "gamma_high",
    "gamma_mid",
    "gpt-6-astra",
]

# new-api 的 hailuo 适配器按平台模型名发请求；2api 会把这些别名映射到 hub 的 H3 线。
# 同时列出 hub 原生名，方便直接调用。
VIDEO_MODELS = [
    # minimax_v3（H3 线）——平台别名与 hub 原生名并存
    "MiniMax-Hailuo-2.3",
    "MiniMax-Hailuo-2.3-Fast",
    "MiniMax-Hailuo-02",
    "T2V-01",
    "I2V-01",
    "MiniMax-H3",
    "MiniMax-H3-Max",
    "MiniMax-H3-Max-Turbo",
    # 其余 backend：名字直接用 catalogue id，2api 按 id 找 backend
    "wan3.0-video",
    "wan3.0-video-prime",
    "kling-v3-omni-video",
    "kling-avatar",
    "kling-motion-control",
    "jimeng_motion_control",
    "veo-3.1-fast-generate-001",
    "veo-3.1-generate-001",
]

# 音频（TTS）。必须挂在 type=1 渠道上 —— new-api 的 /v1/audio/speech 只由
# OpenAI 适配器处理（按路径前缀判定 RelayModeAudioSpeech），别的类型不认。
# `tts-1` 是 new-api 在请求没带 model 时的默认值，务必保留。
AUDIO_MODELS = [
    "tts-1",
    "tts-1-hd",
    "gpt-4o-mini-tts",
    "speech-2.8-hd",
]

# 来自 /api/v1/models/config 的 imageModels。这些名字会被原样当作 model 传下去，
# 2api 再按 catalogue 映射到对应 backend（openai / nano_banana / seedream / midjourney）。
IMAGE_MODELS = [
    "gpt-image-2.5-sunburst",
    "gpt-image-2.5-flare",
    "gpt-image-2",
    "nano_banana_2_flash",
    "nano_banana_2",
    "doubao-seedream-5-0-pro-260628",
    "doubao-seedream-4-5-251128",
    "midjourney-8.1",
    "midjourney-8.2",
    "midjourney-7",
    "midjourney-niji7",
]

PROFILES = {
    "llm": {
        "name": "MiniMax Design (本地反代)",
        "type": 1,
        "base_url": LOCAL_BASE,
        "key": "sk-minimax-design-local",
        "models": ",".join(TEXT_MODELS + AUDIO_MODELS),
        "test_model": "MiniMax-M3",
    },
    "video": {
        "name": "MiniMax Design 视频 (本地反代)",
        "type": 35,  # ChannelTypeMiniMax -> relay/channel/task/hailuo
        "base_url": LOCAL_BASE,
        "key": "sk-minimax-design-video",
        "models": ",".join(VIDEO_MODELS),
        "test_model": "",
    },
    "image": {
        "name": "MiniMax Design 图像 (本地反代)",
        "type": 1,  # OpenAI 兼容，因为 2api 的门面就是 OpenAI 的 images 形状
        "base_url": LOCAL_BASE,
        "key": "sk-minimax-design-image",
        "models": ",".join(IMAGE_MODELS),
        "test_model": "",
    },
}

# 服务端管理的字段，POST 时必须剔除，否则会被判为非法参数
READONLY = (
    "id", "created_time", "test_time", "response_time",
    "balance", "balance_updated_time", "used_quota",
    "channel_info", "setting", "other_info", "status_only",
)


def call(method, path, body=None):
    req = urllib.request.Request(
        BASE + path,
        method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={
            "Authorization": "Bearer " + TOKEN,
            "New-Api-User": USER_ID,
            "Content-Type": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        return {"_http_error": e.code, "_body": e.read().decode()[:600]}


def list_channels():
    res = call("GET", "/api/channel/?p=0&page_size=200")
    if not res.get("success"):
        return []
    data = res.get("data") or {}
    return (data.get("items") if isinstance(data, dict) else data) or []


def find_existing(name):
    for c in list_channels():
        if c.get("name") == name:
            return c.get("id")
    return None


def build_payload(profile):
    tpl = call("GET", f"/api/channel/{TEMPLATE_ID}")
    if not tpl.get("success"):
        raise SystemExit("拿模板失败: " + json.dumps(tpl, ensure_ascii=False)[:400])

    channel = tpl["data"]
    for k in READONLY:
        channel.pop(k, None)
    channel.update(profile)

    # 模板带了个 claude 的 client_identity。对 type=1 它只多一个 claude-cli 的
    # User-Agent；对 type=35 更是毫无意义。清掉，免得误导后续排查。
    try:
        st = json.loads(channel.get("settings") or "{}")
        st.pop("client_identity", None)
        channel["settings"] = json.dumps(st, separators=(",", ":"))
    except (ValueError, TypeError):
        pass

    return {"mode": "single", "channel": channel}


def apply_profile(key, dry):
    profile = PROFILES[key]
    payload = build_payload(profile)
    channel = payload["channel"]
    existing = find_existing(profile["name"])

    if existing:
        # PUT 和 POST 的 body 形状不一样，这点很容易踩：
        #   POST /api/channel/  -> AddChannel 解析 {mode, channel}
        #   PUT  /api/channel/  -> UpdateChannel 直接把 rawBody 解成 PatchChannel，
        #                          也就是渠道对象必须在**顶层**
        # 另外 UpdateChannel 会遍历顶层 key 逐个比对只读表，任何只读字段出现
        # 就直接判非法参数；status 也必须剔除（改状态走 POST /:id/status）。
        channel["id"] = existing
        channel.pop("status", None)
        put_body = channel
    else:
        put_body = payload

    if dry:
        shown = {k: v for k, v in channel.items() if k != "key"}
        print(f"--- {key} ---")
        print(json.dumps(shown, ensure_ascii=False, indent=1)[:1600])
        print("目标接口:", f"PUT /api/channel/ (id={existing})" if existing
              else "POST /api/channel/")
        return 0

    if existing:
        res = call("PUT", "/api/channel/", put_body)
        verb = f"更新 id={existing}"
    else:
        res = call("POST", "/api/channel/", put_body)
        verb = "新建"
    ok = bool(res.get("success"))
    print(f"[{key}] {verb} -> {'OK' if ok else 'FAIL'} "
          f"{json.dumps(res, ensure_ascii=False)[:300]}")
    return 0 if ok else 1


def main():
    args = [a for a in sys.argv[1:] if a != "--dry-run"]
    dry = "--dry-run" in sys.argv

    if "--list" in sys.argv:
        for c in list_channels():
            print(f"  {c.get('id'):>3} | type={c.get('type'):<3} | {c.get('name')} "
                  f"| {c.get('base_url')}")
        return 0

    keys = args or list(PROFILES)
    for k in keys:
        if k not in PROFILES:
            print(f"未知 profile: {k}（可选: {', '.join(PROFILES)}）")
            return 2
    return max(apply_profile(k, dry) for k in keys)


if __name__ == "__main__":
    sys.exit(main())
