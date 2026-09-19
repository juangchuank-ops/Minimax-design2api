#!/usr/bin/env bash
# 扫描 hub.minimax.io 的 /api/v1/* 路由表：用 POST + 空 body，
# 存在的路由会返回 400/401/405，不存在的是 404。
# 目的：找出 Google 协议（omega-3.1-pro）到底挂在哪个路径下。
set -u
cd "$(dirname "$0")/.." || exit 1
JWT=$(cat recon/.jwt)
BASE=https://hub.minimax.io

WORDS=(
  messages responses chat completions models config video image audio files tasks
  user credits equity gemini google vertex v1beta generateContent streamGenerateContent
  interactions converse invoke predict generate embed embeddings tts asr music lyrics
  voice voices conversation agents assistants batches uploads assets search tools
  health status version
)

for w in "${WORDS[@]}"; do
  for p in "/api/v1/$w" "/api/v1/models/$w"; do
    code=$(curl -sS -m 6 -o /tmp/.scan.json -w "%{http_code}" "$BASE$p" \
      -X POST -H "content-type: application/json" -H "token: $JWT" \
      -H "version_code: 3.0.16" -d '{}' 2>/dev/null)
    if [ "$code" != "404" ] && [ -n "$code" ]; then
      printf "%-4s %-40s %s\n" "$code" "$p" "$(head -c 120 /tmp/.scan.json 2>/dev/null | tr -d '\n')"
    fi
  done
done
echo "--- scan done ---"
