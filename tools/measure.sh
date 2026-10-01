#!/usr/bin/env bash
# スロークエリログの ON/OFF（MariaDB 10.3 は SET PERSIST が無いので SET GLOBAL。恒久設定は OFF のまま）。
# 状態は .measure（on/off）に持ち、make bench が毎回これを DB に適用してアプリを再起動する
# （mariadb を再起動すると SET GLOBAL は消える / long_query_time は既存接続に効かない）。
# usage: measure.sh on|off|apply
set -euo pipefail
source "$(dirname "$0")/lib.sh"
MODE="${1:-apply}"
case "$MODE" in on|off) echo "$MODE" > .measure ;; apply) ;; *) echo "usage: measure.sh on|off|apply" >&2; exit 1 ;; esac
STATE="$(cat .measure 2>/dev/null || echo on)"
if [ "$STATE" = on ]; then
  SQL="SET GLOBAL slow_query_log_file='/var/log/mysql/slow.log'; SET GLOBAL long_query_time=0; SET GLOBAL slow_query_log=1;"
else
  SQL="SET GLOBAL slow_query_log=0; SET GLOBAL long_query_time=10;"
fi
ssh "$DB_HOST" "sudo mysql -e \"$SQL\"; sudo mysql -N -e 'SELECT CONCAT(\"    slow_query_log=\", @@global.slow_query_log, \" long_query_time=\", @@global.long_query_time)'"
