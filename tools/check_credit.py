#!/usr/bin/env python3
"""Check the credit balance / trial status of MiniMax Design accounts.

Answers the two questions you actually have when adding a token to the pool:
  - how many credits does this account have, and what kind are they
  - when do they expire (bonus credits die in 3 days, tokens last ~40)

Usage:
    python tools/check_credit.py                 # every token in config.json
    python tools/check_credit.py --token <jwt>   # a token you have not added yet
    python tools/check_credit.py --region domestic

Why this exists: a token being valid says nothing about whether generation will
work. Credits expire far sooner than the token, and a brand-new account's credits
are BONUS credits with a 3-day life. Checking a token without checking its
credits is how you end up debugging "the API is broken" when the real answer is
"the account is empty".
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone

GATEWAYS = {
    "overseas": "https://design.minimax.io",
    "domestic": "https://design.minimax.cn",
}

# Decoded from the desktop bundle's CreditType enum — the gateway returns the
# raw integer, so without this table the wallet is unreadable.
CREDIT_TYPES = {
    0: ("充值", "单独购买的积分，有效期一年"),
    1: ("订阅", "会员订阅积分，有效期 1 个月，每月重置"),
    2: ("赠送", "新用户登录奖励以及活动获得积分，新用户免费积分有效期 3 天"),
    3: ("默认", ""),
    4: ("创作者", "投稿被选中发放的积分奖励"),
    5: ("活动", "活动获得积分"),
    6: ("登录奖励", "新用户登录奖励"),
    7: ("团队转入", "从团队转入的积分"),
}

WALLET_SOURCES = {0: "默认", 1: "运营"}


def call(gateway: str, path: str, token: str, version_code: str, timeout: int = 25):
    """GET a gateway path. Returns (ok, parsed_or_error_text)."""
    req = urllib.request.Request(gateway + path)
    req.add_header("token", token)
    req.add_header("version_code", version_code)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read()
    except urllib.error.HTTPError as e:
        return False, "HTTP %d %s" % (e.code, e.read()[:200].decode("utf-8", "replace"))
    except Exception as e:  # transport, DNS, timeout
        return False, str(e)
    try:
        return True, json.loads(body)
    except ValueError:
        return False, "response is not JSON: %s" % body[:200]


def ms_to_local(ms) -> str:
    try:
        n = int(ms)
    except (TypeError, ValueError):
        return "—"
    if n <= 0:
        return "—"
    dt = datetime.fromtimestamp(n / 1000, tz=timezone.utc).astimezone()
    delta = dt - datetime.now(tz=dt.tzinfo)
    days = delta.total_seconds() / 86400
    if days < 0:
        return "%s（已过期 %.1f 天前）" % (dt.strftime("%Y-%m-%d %H:%M"), -days)
    return "%s（还有 %.1f 天）" % (dt.strftime("%Y-%m-%d %H:%M"), days)


def report(gateway: str, token: str, version_code: str, label: str) -> bool:
    print("=" * 72)
    print(f"{label}   gateway={gateway}")
    print("=" * 72)

    ok, balance = call(gateway, "/api/v1/credit/balance", token, version_code)
    if not ok:
        print("  ✗ 余额查询失败:", balance)
        print("    （令牌无效 / 过期 / 区域选错，都会走到这里）")
        return False
    print("  可用积分:", balance.get("total_credit", "?"))

    ok, wallet = call(gateway, "/api/v1/credit/wallet", token, version_code)
    if ok:
        for w in wallet.get("wallets") or []:
            src = WALLET_SOURCES.get(w.get("source"), w.get("source"))
            print(f"  钱包（来源 {src}，套餐 {w.get('plan_name') or '无'}）")
            for sc in w.get("sub_credits") or []:
                ctype = sc.get("credit_type")
                name, note = CREDIT_TYPES.get(ctype, ("未知类型 %s" % ctype, ""))
                print(f"    · {name}积分 {sc.get('credit')}｜到期 {ms_to_local(sc.get('end_time'))}")
                if note:
                    print(f"      {note}")
        if wallet.get("url"):
            print("  充值入口:", wallet["url"])
    else:
        print("  钱包查询失败:", wallet)

    ok, trial = call(gateway, "/api/v1/promotions/hailuo03-video-trial/status", token, version_code)
    if ok:
        elig = trial.get("eligibility") or {}
        print(f"  免费视频试用: 剩余 {trial.get('free_count')} 次"
              f"（限 {'/'.join(elig.get('models') or []) or '—'}，"
              f"分辨率 {'/'.join(elig.get('resolutions') or []) or '—'}）")
        if trial.get("claim_hint"):
            print("    " + trial["claim_hint"])
    else:
        print("  试用状态查询失败:", trial)

    return True


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--token", help="直接给一个 JWT（不从 config.json 读）")
    ap.add_argument("--region", choices=sorted(GATEWAYS), default="overseas")
    ap.add_argument("--config", default="config.json")
    args = ap.parse_args()

    gateway = GATEWAYS[args.region]

    tokens: list[tuple[str, str]] = []
    if args.token:
        tokens.append(("命令行令牌", args.token))
    elif os.path.exists(args.config):
        with open(args.config, encoding="utf-8") as f:
            cfg = json.load(f)
        for i, t in enumerate(cfg.get("tokens") or []):
            name = t.get("name") or t.get("user_id") or ("token#%d" % i)
            if t.get("token"):
                tokens.append((f"[{i}] {name}", t["token"]))
        version_code = str(cfg.get("version_code") or "3.0.16")
        # A token's own region wins over the command-line default when the
        # config records one, since the two gateways have separate accounts.
        regions = [t.get("region") or args.region for t in (cfg.get("tokens") or [])]
    else:
        print("找不到 %s，也没有 --token" % args.config, file=sys.stderr)
        return 2

    if not args.token:
        if not tokens:
            print("config.json 里没有令牌")
            return 0
        ok = True
        for i, (label, tok) in enumerate(tokens):
            region = regions[i] if regions[i] in GATEWAYS else args.region
            ok = report(GATEWAYS[region], tok, version_code, label) and ok
        print()
        print("提示：积分余额只是一个数字，还要看有效期。"
              "确认能不能真的出片，用 tools/newapi_add_channel.py 之后的渠道试一次最小请求。")
        return 0 if ok else 1

    report(gateway, tokens[0][1], "3.0.16", tokens[0][0])
    return 0


if __name__ == "__main__":
    sys.exit(main())
