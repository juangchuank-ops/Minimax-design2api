#!/usr/bin/env python3
"""读写 new-api 的通用配置项（/api/option/）。

用法：
  python newapi_option.py get <key>
  python newapi_option.py get-prefix <prefix>
  python newapi_option.py set <key> <value>
  python newapi_option.py stats
"""
import json
import sys
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:3000"
TOKEN = open("recon/.natk").read().strip()
USER_ID = "1"


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


def cmd_get(key):
    res = call("GET", "/api/option/")
    opts = res.get("data") or []
    for o in opts:
        if o.get("key") == key:
            print(json.dumps(o, ensure_ascii=False, indent=1))
            return 0
    print(f"(未设置) {key}")
    return 1


def cmd_get_prefix(prefix):
    res = call("GET", "/api/option/")
    opts = res.get("data") or []
    hit = [o for o in opts if str(o.get("key", "")).startswith(prefix)]
    if not hit:
        print(f"(无匹配) {prefix}*")
        return 1
    for o in hit:
        print(f"{o['key']} = {json.dumps(o.get('value'), ensure_ascii=False)}")
    return 0


def cmd_set(key, value):
    res = call("PUT", "/api/option/", {"key": key, "value": value})
    print("PUT /api/option/ ->", json.dumps(res, ensure_ascii=False)[:400])
    return 0 if res.get("success") else 1


def cmd_stats():
    res = call("GET", "/api/performance/stats")
    print(json.dumps(res, ensure_ascii=False, indent=1)[:1200])
    return 0


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        return 2
    a = sys.argv
    if a[1] == "get" and len(a) == 3:
        return cmd_get(a[2])
    if a[1] == "get-prefix" and len(a) == 3:
        return cmd_get_prefix(a[2])
    if a[1] == "set" and len(a) == 4:
        return cmd_set(a[2], a[3])
    if a[1] == "stats":
        return cmd_stats()
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main())
