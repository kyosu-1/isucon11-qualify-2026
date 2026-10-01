#!/usr/bin/env bash
# pidstat の出力をプロセス別に平均して降順に並べる（Average: 行は二重計上なので捨て、ヘッダ数=秒数で割る）。
awk '
  $1 == "Average:" { next }
  $NF == "Command" { secs++; next }
  NF >= 10 && $(NF-2) ~ /^[0-9.]+$/ { cpu[$NF] += $(NF-2) }
  END { for (c in cpu) printf "%-24s %9.1f\n", c, cpu[c]/(secs ? secs : 1) }
' "${1:?usage: cpu-by-process.sh <pidstat-raw.txt>}" | sort -k2 -rn | head -12 | { printf "%-24s %9s\n" "COMMAND" "AVG_CPU%"; cat; }
