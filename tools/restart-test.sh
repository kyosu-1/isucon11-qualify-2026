#!/usr/bin/env bash
# 再起動試験: 3 台同時 reboot → boot_id が変わるまで待つ → サービス確認 → ベンチ（最終スコアは「全サーバー再起動後の負荷走行」）
set -euo pipefail
source "$(dirname "$0")/lib.sh"
SSH_OPTS=(-o ConnectTimeout=5 -o BatchMode=yes)
BOOT_IDS=()
for i in "${!APP_HOSTS_ARR[@]}"; do BOOT_IDS[$i]="$(ssh "${SSH_OPTS[@]}" "${APP_HOSTS_ARR[$i]}" 'cat /proc/sys/kernel/random/boot_id')"; done
for HOST in "${APP_HOSTS_ARR[@]}"; do echo "==> $HOST : reboot"; ssh "${SSH_OPTS[@]}" "$HOST" 'sudo systemctl reboot' || true; done
echo "==> 起動待ち（boot_id が変わるまで）"
for i in "${!APP_HOSTS_ARR[@]}"; do
  HOST="${APP_HOSTS_ARR[$i]}"
  for attempt in $(seq 1 90); do
    now="$(ssh "${SSH_OPTS[@]}" "$HOST" 'cat /proc/sys/kernel/random/boot_id' 2>/dev/null || true)"
    if [ -n "$now" ] && [ "$now" != "${BOOT_IDS[$i]}" ]; then echo "    $HOST rebooted"; break; fi
    sleep 5
    [ "$attempt" = 90 ] && { echo "!! $HOST が再起動しませんでした" >&2; exit 1; }
  done
  ssh "${SSH_OPTS[@]}" "$HOST" 'systemctl is-system-running --wait >/dev/null 2>&1 || true'
done
echo "==> サービス確認（enabled なものの is-active）"
for HOST in "${APP_HOSTS_ARR[@]}"; do
  ssh "${SSH_OPTS[@]}" "$HOST" "printf '  %-19s ' $HOST; for s in nginx mariadb $APP_SVC; do systemctl is-enabled --quiet \$s 2>/dev/null && printf '%s=%s ' \$s \$(systemctl is-active \$s); done; echo"
done
# 再起動で SET GLOBAL は消える = 本番の追試と同じ「計測 OFF」状態でそのまま測る
NOAPPRESTART=1 ./tools/bench.sh "再起動試験後 ${1:-}"
