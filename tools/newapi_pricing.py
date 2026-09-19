#!/usr/bin/env python3
"""给 new-api 的模型补定价条目（走 /api/option/pricing/patch）。

new-api 的定价表是「按模型名精确匹配」的，没有通配。模型不在表里时，
`SelfUseModeEnabled=false` 会直接报 model_price_error，请求根本发不出去。

用法：
  python newapi_pricing.py list <model> [model...]     # 查看指定模型当前定价
  python newapi_pricing.py set <ratio> <model> [model...]
      # 为模型补 ModelRatio=<ratio> / CompletionRatio=4（仅在缺失时写入）
  python newapi_pricing.py set --ratio X --completion Y model...
  python newapi_pricing.py setprice <usd_per_call> <model> [model...]
      # 按次计价（图片/视频/音乐类走这条），单位是美元/次
"""
import json
import sys
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:3000"
TOKEN = open("recon/.natk").read().strip()
USER_ID = "1"

PRICING_KEYS = (
    "ModelRatio", "ModelPrice", "CompletionRatio", "CacheRatio",
    "CreateCacheRatio", "ImageRatio", "AudioRatio", "AudioCompletionRatio",
    "billing_setting.billing_mode", "billing_setting.billing_expr",
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
        with urllib.request.urlopen(req, timeout=60) as r:
            return json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        return {"_http_error": e.code, "_body": e.read().decode()[:800]}


def fetch_entries():
    res = call("GET", "/api/option/model_pricing")
    if not res.get("success"):
        raise SystemExit("拉取定价失败: " + json.dumps(res, ensure_ascii=False)[:400])
    return {e.get("model_name"): e for e in (res["data"].get("entries") or [])}


def cmd_list(models):
    entries = fetch_entries()
    for m in models:
        e = entries.get(m)
        if not e:
            print(f"{m:<14} ✗ 定价表中无条目 -> 会报 model_price_error")
            continue
        print(f"{m:<14} ✓ configured={json.dumps(e.get('configured') or {}, ensure_ascii=False)}")
        print(f"{'':<14}   effective ={json.dumps(e.get('effective') or {}, ensure_ascii=False)[:160]}")
    return 0


def cmd_set(ratio, completion, models):
    ops = []
    for m in models:
        ops.append({"key": "ModelRatio", "model": m,
                    "action": "set_if_missing", "value": ratio})
        ops.append({"key": "CompletionRatio", "model": m,
                    "action": "set_if_missing", "value": completion})
    res = call("PATCH", "/api/option/pricing/patch", {"operations": ops})
    if not res.get("success"):
        print("PATCH 失败:", json.dumps(res, ensure_ascii=False)[:500])
        return 1
    print(f"已为 {len(models)} 个模型写入 ModelRatio={ratio} / CompletionRatio={completion}"
          f"（set_if_missing，不覆盖已有值）")
    return cmd_list(models)


def cmd_setprice(price, models):
    """按次计价的模型（图片/视频/音乐类）用 ModelPrice，单位是「每次请求多少美元」。"""
    ops = [{"key": "ModelPrice", "model": m, "action": "set_if_missing", "value": price}
           for m in models]
    res = call("PATCH", "/api/option/pricing/patch", {"operations": ops})
    if not res.get("success"):
        print("PATCH 失败:", json.dumps(res, ensure_ascii=False)[:500])
        return 1
    print(f"已为 {len(models)} 个模型写入 ModelPrice={price}（set_if_missing）")
    return cmd_list(models)


def main():
    a = sys.argv[1:]
    if not a:
        print(__doc__)
        return 2
    if a[0] == "list" and len(a) >= 2:
        return cmd_list(a[1:])
    if a[0] == "set":
        ratio, completion, models = 0.15, 4, []
        i = 1
        while i < len(a):
            if a[i] == "--ratio":
                ratio = float(a[i + 1]); i += 2
            elif a[i] == "--completion":
                completion = float(a[i + 1]); i += 2
            else:
                models.append(a[i]); i += 1
        if not models:
            print("没给模型名")
            return 2
        return cmd_set(ratio, completion, models)
    if a[0] == "setprice" and len(a) >= 3:
        return cmd_setprice(float(a[1]), a[2:])
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main())
