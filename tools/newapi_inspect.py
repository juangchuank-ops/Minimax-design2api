#!/usr/bin/env python3
"""只读检查 new-api 的渠道与 abilities 表。

用法： python newapi_inspect.py [db_path]
"""
import sqlite3
import sys

DB = sys.argv[1] if len(sys.argv) > 1 else \
    r"E:/new-api-main-copy-main (2)/new-api-main-copy-main/one-api.db"


def main():
    c = sqlite3.connect("file:" + DB + "?mode=ro", uri=True)

    print("=== channels ===")
    for r in c.execute(
            "select id,name,type,status,base_url,models from channels order by id"):
        print(f"  {r[0]:>3} | {r[1]} | type={r[2]} | st={r[3]} | {r[4]}")
        print(f"        models: {(r[5] or '')[:150]}")

    print()
    print("=== abilities ===")
    cols = [d[1] for d in c.execute("pragma table_info(abilities)")]
    print("  columns:", ", ".join(cols))
    gcol = '"group"'  # SQLite 保留字，必须引起来
    q = (f"select {gcol},model,channel_id,enabled,priority,weight "
         f"from abilities order by channel_id,model")
    for r in c.execute(q):
        print(f"  grp={r[0]:<10} model={r[1]:<20} ch={r[2]:<4} "
              f"enabled={r[3]} prio={r[4]} w={r[5]}")

    print()
    print("=== 渠道 4 的模型能否被 abilities 命中（大小写敏感实测）===")
    for m in ["MiniMax-M3", "minimax-m3", "gamma_high", "GAMMA_HIGH"]:
        n = c.execute(
            f"select count(*) from abilities where model=? and channel_id=4",
            (m,)).fetchone()[0]
        print(f"  {m:<14} -> {n}")


if __name__ == "__main__":
    main()
