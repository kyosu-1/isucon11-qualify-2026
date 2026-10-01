#!/usr/bin/env bash
# ベンチ結果を scores/log.md に 1 行追記する。 usage: record.sh <measurement_dir> [note]
set -euo pipefail
source "$(dirname "$0")/lib.sh"
DIR="${1:?usage: record.sh <measurement_dir> [note]}"; NOTE="${2:-}"; LOG=scores/log.md
if [ ! -f "$LOG" ]; then
  cat > "$LOG" <<'EOT'
# スコア推移

`make bench` が自動で 1 行ずつ追記する。判断（採用 / revert）はメモ列に書く。

- スコア = 加点（raw）− 減点（deduction）。`pass=FAIL` のスコアは無効（エラー 100 回で fail）
- timeout は 10 回で 1 点減点（POST /api/condition は対象外）
- 内訳 = `G` 完成グラフ Good/Normal/Bad/Worst、`T` 当日グラフ 同、`R` コンディション既読 Info/Warning/Critical（50 件単位）、user ユーザー数、viewer 閲覧者 参加-離脱、isu 登録 ISU 数、post 受信コンディション Info/Warning/Critical

| 時刻 | スコア | 前回比 | pass | raw | 減点 | err | timeout | 内訳 | 変更 | commit | 計測 | メモ |
| --- | ---: | ---: | --- | ---: | ---: | ---: | ---: | --- | --- | --- | --- | --- |
EOT
fi
IFS=$'\t' read -r SCORE PASS RAW DED ERR TMO BRK < <(python3 - "$DIR/score.json" <<'PY'
import json, sys
s = json.load(open(sys.argv[1]))
b = s.get("breakdown_short") or "-"
print("\t".join(str(x) for x in (s["score"], s["pass"], s.get("raw", "-"), s.get("deduction", "-"), s.get("error_count", "-"), s.get("timeout_count", "-"), b)))
PY
)
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo '-')"; SUBJECT="$(git log -1 --format=%s 2>/dev/null || echo '-')"
PREV="$(grep -E '^\| [0-9-]+ [0-9:]+ \|' "$LOG" | tail -1 | awk -F'|' '{gsub(/ /,"",$3); print $3}' || true)"
DELTA="-"
if [[ "$PREV" =~ ^[0-9]+$ ]] && [ "$PREV" -gt 0 ]; then DELTA="$(awk -v a="$SCORE" -v b="$PREV" 'BEGIN{printf "%+.1f%%", (a-b)*100.0/b}')"; fi
PASSMARK="**FAIL**"; [ "$PASS" = "True" ] && PASSMARK="ok"
printf '| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | `%s` | %s | %s |\n' \
  "$(date '+%m-%d %H:%M')" "$SCORE" "$DELTA" "$PASSMARK" "$RAW" "$DED" "$ERR" "$TMO" "$BRK" "${SUBJECT//|/\\|}" "$COMMIT" "${DIR#measurements/}" "$NOTE" >> "$LOG"
echo "recorded: score=$SCORE (prev=${PREV:-none}, $DELTA) pass=$PASS"
