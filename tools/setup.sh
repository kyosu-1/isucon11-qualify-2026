#!/usr/bin/env bash
# 環境を作り直したら 1 回（冪等）。
#   - 計測ツール（alp / pt-query-digest / sysstat）
#   - unattended-upgrades 等を止める
#   - /etc/hosts の isucondition-N.t.isucon.dev を private IP に（AMI は 127.0.0.1x の 1 台構成。本番は 3 台の IP）
#   - ベンチ機の競技サービス停止
set -euo pipefail
source "$(dirname "$0")/lib.sh"
ALP_VERSION=v1.0.21
for HOST in "${APP_HOSTS_ARR[@]}" "$BENCH"; do
  (
  ssh "$HOST" "set -euo pipefail
    export DEBIAN_FRONTEND=noninteractive
    sudo systemctl disable --now unattended-upgrades apt-daily.timer apt-daily-upgrade.timer >/dev/null 2>&1 || true
    if ! command -v alp >/dev/null 2>&1; then
      cd /tmp && curl -fsSL -o alp.tar.gz https://github.com/tkuchiki/alp/releases/download/${ALP_VERSION}/alp_linux_amd64.tar.gz
      tar xf alp.tar.gz alp && sudo install -m 0755 alp /usr/local/bin/alp && rm -f alp alp.tar.gz
    fi
    if ! command -v pt-query-digest >/dev/null 2>&1 || ! command -v pidstat >/dev/null 2>&1; then
      sudo apt-get update -qq >/dev/null 2>&1 || true
      sudo apt-get install -y -qq percona-toolkit sysstat >/dev/null 2>&1
    fi
    sudo sed -i -e 's/^[0-9.]* isucondition-1.t.isucon.dev/$ISU1_IP isucondition-1.t.isucon.dev/' \
                -e 's/^[0-9.]* isucondition-2.t.isucon.dev/$ISU2_IP isucondition-2.t.isucon.dev/' \
                -e 's/^[0-9.]* isucondition-3.t.isucon.dev/$ISU3_IP isucondition-3.t.isucon.dev/' /etc/hosts
    sudo mkdir -p /var/log/mysql && sudo chown mysql:adm /var/log/mysql
    echo \"  [$HOST] alp=\$(alp --version 2>&1 | head -1) ptqd=\$(pt-query-digest --version 2>&1 | head -1) hosts=\$(grep -c isucondition /etc/hosts)\"
  "
  ) &
done
wait
echo "==> $BENCH : 競技用サービスを停止（ベンチ専用）"
ssh "$BENCH" 'sudo systemctl disable --now isucondition.go jiaapi-mock nginx mariadb >/dev/null 2>&1 || true
  for s in isucondition.go jiaapi-mock nginx mariadb; do printf "  %-18s %s\n" $s "$(systemctl is-active $s)"; done'
# DB 分離用: アプリは別ノードから isucon ユーザーで繋ぐ（AMI は localhost のみ）。全台に作っておけば DB をどこに置いてもよい
for HOST in "${APP_HOSTS_ARR[@]}"; do
  ssh "$HOST" "sudo mysql -e \"CREATE USER IF NOT EXISTS 'isucon'@'%' IDENTIFIED BY 'isucon'; GRANT ALL PRIVILEGES ON isucondition.* TO 'isucon'@'%'; FLUSH PRIVILEGES;\"" && echo "  [$HOST] mysql user isucon@% ok"
done
