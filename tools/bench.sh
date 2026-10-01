#!/usr/bin/env bash
# 計測ループの本体。「計測状態の適用 → ログ初期化 → 疎通確認 → ベンチ → 解析 → 記録」。
# 出力: measurements/<ts>/ {score.json, bench.log, alp-<host>.txt, slow.txt, cpu-<host>.txt, vmstat-<host>.txt, app-errors-<host>.txt, meta.txt}
# usage: tools/bench.sh ["メモ"]
set -euo pipefail
source "$(dirname "$0")/lib.sh"
NOTE="${1:-}"
n="${ENTRY##*-}"; v="ISU${n}_IP"; TARGET_IP="${!v}"
TS="$(date +%Y%m%d-%H%M%S)"; OUT="measurements/$TS"; mkdir -p "$OUT"
MEASURE="$(cat .measure 2>/dev/null || echo on)"
{
  echo "timestamp : $TS"
  echo "commit    : $(git rev-parse --short HEAD 2>/dev/null || echo -)  $(git log -1 --format=%s 2>/dev/null || echo -)"
  echo "entry     : $ENTRY ($TARGET_IP)"
  echo "db_host   : $DB_HOST"
  echo "bench     : $BENCH ($BENCH_IP)"
  echo "slowlog   : $MEASURE"
  echo "note      : $NOTE"
  echo "dirty     : $(git status --porcelain -- webapp etc | wc -l | tr -d ' ') files uncommitted (webapp/ etc/)"
} > "$OUT/meta.txt"

echo "==> [1/5] 計測状態の適用・ログ初期化・リソース記録開始"
./tools/measure.sh apply
START_EPOCH="$(date +%s)"
ssh "$DB_HOST" "sudo truncate -s 0 /var/log/mysql/slow.log 2>/dev/null || true"
for h in "${APP_HOSTS_ARR[@]}"; do
  ssh "$h" "sudo truncate -s 0 /var/log/nginx/access.log /var/log/nginx/error.log 2>/dev/null || true
            systemctl is-enabled --quiet $APP_SVC 2>/dev/null && sudo systemctl restart $APP_SVC || true
            sudo pkill -x vmstat; sudo pkill -x pidstat
            nohup sudo sh -c 'vmstat -t 1 100 > /tmp/vmstat.txt 2>&1' >/dev/null 2>&1 &
            nohup sudo sh -c 'LC_ALL=C pidstat -u 1 95 > /tmp/pidstat.txt 2>&1' >/dev/null 2>&1 &
            sleep 0.1" &
done
ssh "$BENCH" "sudo pkill -x vmstat; nohup sudo sh -c 'vmstat -t 1 100 > /tmp/vmstat.txt 2>&1' >/dev/null 2>&1 & sleep 0.1" &
wait

echo "==> [2/5] 疎通確認"
code="$(ssh "$ENTRY" "curl -sk -o /dev/null -w '%{http_code}' --retry 5 --retry-delay 1 --retry-all-errors https://127.0.0.1/api/trend" || true)"
echo "    GET /api/trend -> $code"
[ "$code" = "200" ] || { echo "!! アプリに届いていません。ベンチを中止します。" >&2; exit 1; }

echo "==> [3/5] ベンチ実行 -> $TARGET_IP:443 (all: $ISU1_IP,$ISU2_IP,$ISU3_IP / jia: $BENCH_IP:4999)"
set +e
ssh "$BENCH" "cd /home/isucon/bench && sudo -u isucon ./bench -all-addresses $ISU1_IP,$ISU2_IP,$ISU3_IP -target $TARGET_IP:443 -tls -jia-service-url http://$BENCH_IP:4999" \
  > "$OUT/bench.log" 2> "$OUT/bench-debug.log"
BENCH_RC=$?
set -e
cat "$OUT/bench-debug.log" >> "$OUT/bench.log"; rm -f "$OUT/bench-debug.log"
python3 tools/parse.py "$OUT/bench.log" > "$OUT/score.json"

echo "==> [4/5] 解析"
ALP_M='^/api/isu/[0-9a-f-]+/icon$,^/api/isu/[0-9a-f-]+/graph,^/api/isu/[0-9a-f-]+$,^/api/condition/[0-9a-f-]+,^/isu/[0-9a-f-]+/condition,^/isu/[0-9a-f-]+/graph,^/isu/[0-9a-f-]+$,^/assets/.+'
for h in "${APP_HOSTS_ARR[@]}"; do
  (
    ssh "$h" "cat /tmp/pidstat.txt" > "$OUT/raw-pidstat-$h.txt" 2>/dev/null || true
    ./tools/cpu-by-process.sh "$OUT/raw-pidstat-$h.txt" > "$OUT/cpu-$h.txt" 2>/dev/null || true
    rm -f "$OUT/raw-pidstat-$h.txt"
    ssh "$h" "cat /tmp/vmstat.txt" > "$OUT/vmstat-$h.txt" 2>/dev/null || true
    ssh "$h" "sudo test -s /var/log/nginx/access.log && sudo alp ltsv --file /var/log/nginx/access.log --sort sum -r -m '$ALP_M' -o count,method,uri,min,avg,max,sum,p99,2xx,3xx,4xx,5xx" > "$OUT/alp-$h.txt" 2>/dev/null || true
    [ -s "$OUT/alp-$h.txt" ] || rm -f "$OUT/alp-$h.txt"
    ssh "$h" "sudo journalctl -u $APP_SVC --since @$START_EPOCH --no-pager -o cat 2>/dev/null \
      | grep -aiE 'error|panic|fatal' | grep -av '\"error\":\"\"' | sed -E 's/[0-9a-f]{8}-[0-9a-f-]{27}/<UUID>/g; s/\"time\":\"[^\"]+\",?//; s/[0-9]{4}[-\/][0-9]{2}[-\/][0-9]{2}[T ][0-9:.+Z-]+//g' \
      | cut -c1-220 | sort | uniq -c | sort -rn | head -30" > "$OUT/app-errors-$h.txt" 2>/dev/null || true
    [ -s "$OUT/app-errors-$h.txt" ] || rm -f "$OUT/app-errors-$h.txt"
  ) &
done
ssh "$BENCH" "cat /tmp/vmstat.txt" > "$OUT/vmstat-$BENCH.txt" 2>/dev/null &
ssh "$DB_HOST" "sudo test -s /var/log/mysql/slow.log && sudo pt-query-digest --limit 15 /var/log/mysql/slow.log 2>/dev/null || echo 'slow log is empty (measure off)'" > "$OUT/slow.txt" 2>&1 &
wait

echo "==> [5/5] 記録"
./tools/record.sh "$OUT" "$NOTE"
echo; echo "==> measurements: $OUT"
python3 tools/parse.py --show "$OUT/score.json"
for h in "${APP_HOSTS_ARR[@]}" "$BENCH"; do
  busy="$(awk '$15 ~ /^[0-9]+$/ && $15 < 90 {n++; b += 100 - $15} END {if (n) printf "%.0f%% (%ds)", b/n, n; else print "-"}' "$OUT/vmstat-$h.txt" 2>/dev/null)"
  printf '  %-19s busy=%-10s ' "$h" "$busy"; { sed -n '2,5p' "$OUT/cpu-$h.txt" 2>/dev/null || true; } | awk '{printf "%s=%s%% ", $1, $2}'; echo
done
for h in "${APP_HOSTS_ARR[@]}"; do [ -f "$OUT/alp-$h.txt" ] && { echo "--- alp $h (上位) ---"; head -13 "$OUT/alp-$h.txt"; }; done
[ "$BENCH_RC" = 0 ] || echo "!! bench exit code = $BENCH_RC" >&2
