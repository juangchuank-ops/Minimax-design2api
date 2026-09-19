#!/usr/bin/env python3
"""校验 audio.go 里的音色映射表，确保每个目标 id 都在上游目录里。

上游对未知 voice_id 的响应是 HTTP 500 + `code=2054 voice id not exist`，
而且这个错会以「整个 TTS 接口挂了」的形态出现，很容易误判成路由或鉴权问题。
所以映射表不能靠猜——跑这个脚本对账。

用法：
  python tools/check_voices.py                 # 只检查映射表
  python tools/check_voices.py --list-en       # 顺便列出全部英语音色
  python tools/check_voices.py --find 关键词    # 按名称/描述搜音色
"""
import json
import re
import sys
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
GATEWAY = "https://design.minimax.io"
VOICES_PATH = "/api/v1/audio/voices"

# 2api 自己的 /v1/audio/voices 也能用，且不用解令牌
LOCAL = "http://127.0.0.1:18080/v1/audio/voices"


def load_token() -> str:
    """从本机 MiniMax Design 客户端读令牌（复用 tools/decrypt-v2enc.js）。"""
    import subprocess

    node = "node"
    appdata = Path.home() / "AppData" / "Roaming" / "@hilo" / "MiniMax Hub Global"
    cfg = appdata / "hub-config-global.json"
    if not cfg.exists():
        raise SystemExit(f"找不到客户端配置: {cfg}")
    enc = json.loads(cfg.read_text(encoding="utf-8"))["tokens"]["accessToken"]
    out = subprocess.run(
        [node, str(ROOT / "tools" / "decrypt-v2enc.js"),
         str(appdata / ".token-key"), enc],
        capture_output=True, text=True, check=True)
    return out.stdout.strip()


def fetch_voices() -> list:
    """优先走本机 2api（免令牌），退回直连上游。

    两种来源的字段名不同：本机门面归一化成 `id`，上游是 `voice_id`。
    统一成 `voice_id` 再返回。
    """
    raw = None
    try:
        with urllib.request.urlopen(LOCAL, timeout=30) as r:
            raw = json.load(r)["data"]
    except Exception:
        token = load_token()
        req = urllib.request.Request(
            GATEWAY + VOICES_PATH,
            headers={"token": token, "version_code": "3.0.16"})
        with urllib.request.urlopen(req, timeout=40) as r:
            raw = json.load(r)["voices"]

    out = []
    for v in raw:
        v = dict(v)
        if "voice_id" not in v:
            v["voice_id"] = v.get("id", "")
        out.append(v)
    return out


def parse_aliases() -> dict:
    """从 audio.go 里抠出映射表，避免手抄。"""
    src = (ROOT / "audio.go").read_text(encoding="utf-8")
    block = re.search(
        r"var openAIVoiceAliases = map\[string\]string\{(.*?)\n\}",
        src, re.S)
    if not block:
        raise SystemExit("audio.go 里找不到 openAIVoiceAliases")
    out = {}
    for m in re.finditer(r'"([^"]+)"\s*:\s*"([^"]+)"', block.group(1)):
        out[m.group(1)] = m.group(2)
    default = re.search(r'const defaultVoice = "([^"]+)"', src)
    if default:
        out["(默认)"] = default.group(1)
    return out


def main() -> int:
    args = sys.argv[1:]
    voices = fetch_voices()
    ids = {v["voice_id"] for v in voices}
    print(f"上游目录共 {len(voices)} 个音色")

    if "--list-en" in args:
        en = [v for v in voices if str(v.get("language", "")).lower().startswith("en")
              or str(v["voice_id"]).startswith("English")]
        print(f"\n英语音色 {len(en)} 个:")
        for v in en:
            print(f"  {v['voice_id']:<38}{v.get('gender',''):<8}{v.get('age',''):<12}"
                  f"{str(v.get('name',''))[:44]}")
        return 0

    if "--find" in args:
        kw = args[args.index("--find") + 1].lower()
        hits = [v for v in voices
                if kw in str(v.get("voice_id", "")).lower()
                or kw in str(v.get("name", "")).lower()
                or kw in str(v.get("description", "")).lower()]
        print(f"\n匹配 {len(hits)} 个:")
        for v in hits:
            print(f"  {v['voice_id']:<40}{v.get('gender',''):<8}{str(v.get('name',''))[:40]}")
        return 0

    aliases = parse_aliases()
    print(f"\n映射表 {len(aliases)} 项:")
    bad = []
    for name, target in aliases.items():
        ok = target in ids
        print(f"  {name:<12} -> {target:<38}{'OK' if ok else '❌ 不在目录里'}")
        if not ok:
            bad.append((name, target))

    if bad:
        print(f"\n❌ {len(bad)} 个映射指向了不存在的音色，"
              f"上游会返回 500 code=2054 voice id not exist")
        return 1
    print("\n✅ 全部有效")
    return 0


if __name__ == "__main__":
    sys.exit(main())
