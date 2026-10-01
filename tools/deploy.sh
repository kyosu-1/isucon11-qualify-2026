#!/usr/bin/env bash
# ローカルの内容をサーバーへ配布し、必要なものだけ再起動する（唯一の反映経路。ローカルの git が正）。
#
#   build/isucondition       -> /home/isucon/webapp/go/isucondition
#   webapp/sql/              -> /home/isucon/webapp/sql/      (POST /initialize が init.sh を叩く)
#   etc/isuN/nginx/          -> /etc/nginx/
#   etc/isuN/mysql/          -> /etc/mysql/                   (差分があるときだけ mariadb を再起動)
#   etc/isuN/systemd/        -> /etc/systemd/system/
#   etc/isuN/sysctl.d/       -> /etc/sysctl.d/
#   etc/isuN/home/env.sh     -> /home/isucon/env.sh
#   etc/isuN/services        そのノードで enable するサービス（それ以外の管理対象は disable --now）
#
# etc/ 配下の __ISU1_IP__ 等は hosts.generated.mk の private IP に置換して配る。
# DB_HOST を先に配ってから残りを並列に配る。
set -euo pipefail
source "$(dirname "$0")/lib.sh"
REMOTE=/home/isucon/webapp
MANAGED="nginx mariadb $APP_SVC"

[ -x build/isucondition ] || { echo "build/isucondition がありません" >&2; exit 1; }
[ -n "$ISU1_IP" ] && [ -n "$ISU2_IP" ] && [ -n "$ISU3_IP" ] || { echo "hosts.generated.mk がありません。make hosts" >&2; exit 1; }
if [ -n "$(git status --porcelain -- webapp etc 2>/dev/null)" ]; then
  echo "warning: webapp/ か etc/ に未コミットの変更があります。ベンチ前に commit してください。" >&2
fi

STAGE="$(mktemp -d)"; trap 'rm -rf "$STAGE"' EXIT

deploy_one() {
  local HOST="$1"; local n="${HOST##*-}"; local src="etc/isu$n"; local dir="$STAGE/isu$n"
  mkdir -p "$dir"; rsync -a "$src/" "$dir/"
  grep -rlE '__ISU[123]_IP__' "$dir" 2>/dev/null | while read -r f; do
    sed -i.bak -e "s/__ISU1_IP__/$ISU1_IP/g" -e "s/__ISU2_IP__/$ISU2_IP/g" -e "s/__ISU3_IP__/$ISU3_IP/g" "$f" && rm -f "$f.bak"
  done || true

  local RS_ISUCON='sudo -u isucon rsync'
  rsync -az --rsync-path="$RS_ISUCON" build/isucondition "$HOST:$REMOTE/go/isucondition"
  rsync -az --rsync-path="$RS_ISUCON" webapp/sql/ "$HOST:$REMOTE/sql/"

  local changed_nginx="" changed_mysql="" changed_systemd="" changed_env="" changed_sysctl=""
  [ -d "$dir/nginx" ]   && changed_nginx="$(rsync -rlpcz -i --rsync-path='sudo rsync' "$dir/nginx/" "$HOST:/etc/nginx/" | grep -v '^\.' || true)"
  [ -d "$dir/mysql" ]   && changed_mysql="$(rsync -rlpcz -i --rsync-path='sudo rsync' "$dir/mysql/" "$HOST:/etc/mysql/" | grep -v '^\.' || true)"
  [ -d "$dir/systemd" ] && changed_systemd="$(rsync -rlpcz -i --rsync-path='sudo rsync' "$dir/systemd/" "$HOST:/etc/systemd/system/" | grep -v '^\.' || true)"
  if [ -d "$dir/sysctl.d" ]; then
    changed_sysctl="$(rsync -rlpcz -i --rsync-path='sudo rsync' "$dir/sysctl.d/" "$HOST:/etc/sysctl.d/" | grep -v '^\.' || true)"
    [ -n "$changed_sysctl" ] && { ssh "$HOST" 'sudo chown root:root /etc/sysctl.d/*; sudo sysctl -q --system' >/dev/null 2>&1 || true; }
  fi
  [ -f "$dir/home/env.sh" ] && changed_env="$(rsync -lpcz -i --rsync-path="$RS_ISUCON" "$dir/home/env.sh" "$HOST:/home/isucon/env.sh" | grep -v '^\.' || true)"
  local want="$MANAGED"
  [ -f "$dir/services" ] && want="$(grep -v '^#' "$dir/services" | xargs || true)"

  {
    [ -n "$changed_nginx$changed_mysql$changed_systemd$changed_env$changed_sysctl" ] && \
      echo "    [$HOST] 設定変更: ${changed_nginx:+nginx }${changed_mysql:+mysql }${changed_systemd:+systemd }${changed_env:+env.sh }${changed_sysctl:+sysctl}"
    ssh "$HOST" "set -euo pipefail
      sudo chown root:root -R /etc/nginx /etc/mysql 2>/dev/null || true
      ${changed_systemd:+sudo systemctl daemon-reload}
      for s in $MANAGED; do
        case ' $want ' in
          *\" \$s \"*) systemctl is-enabled --quiet \$s 2>/dev/null || sudo systemctl enable --now \$s >/dev/null 2>&1 ;;
          *)           if systemctl is-enabled --quiet \$s 2>/dev/null || systemctl is-active --quiet \$s; then sudo systemctl disable --now \$s >/dev/null 2>&1 || true; fi ;;
        esac
      done
      if [ -n '${changed_mysql:+x}' ] && systemctl is-enabled --quiet mariadb; then sudo systemctl restart mariadb; fi
      if [ -n '${changed_nginx:+x}' ] && systemctl is-enabled --quiet nginx; then
        sudo nginx -t -q || { echo '!! nginx -t 失敗（旧設定のまま動いています）'; exit 1; }
        sudo systemctl restart nginx
      fi
      if systemctl is-enabled --quiet $APP_SVC 2>/dev/null; then sudo systemctl restart $APP_SVC; fi
      sleep 1
      printf '    [%s] ' '$HOST'
      for s in $MANAGED; do
        systemctl is-enabled --quiet \$s 2>/dev/null && printf '%s=%s ' \$s \$(systemctl is-active \$s)
      done
      if systemctl is-active --quiet $APP_SVC; then printf 'app:trend=%s ' \$(curl -s -o /dev/null -w '%{http_code}' --retry 5 --retry-connrefused --retry-delay 1 http://127.0.0.1:3000/api/trend); fi
      if systemctl is-active --quiet nginx; then printf 'nginx:/=%s' \$(curl -sk -o /dev/null -w '%{http_code}' https://127.0.0.1/); fi
      echo
    "
  } > "$STAGE/out-$HOST.log" 2>&1
}

ORDERED=()
for h in "${APP_HOSTS_ARR[@]}"; do [ "$h" = "$DB_HOST" ] && ORDERED+=("$h"); done
FAILED=0
if [ "${#ORDERED[@]}" -gt 0 ]; then
  echo "==> $DB_HOST : deploy (DB)"
  deploy_one "$DB_HOST" || FAILED=1
  cat "$STAGE/out-$DB_HOST.log"
fi
pids=(); REST=()
for h in "${APP_HOSTS_ARR[@]}"; do [ "$h" != "$DB_HOST" ] && REST+=("$h"); done
for HOST in "${REST[@]}"; do deploy_one "$HOST" & pids+=($!); done
for p in "${pids[@]}"; do wait "$p" || FAILED=1; done
for HOST in "${REST[@]}"; do cat "$STAGE/out-$HOST.log"; done
[ "$FAILED" = 0 ] || { echo "!! deploy に失敗したノードがあります" >&2; exit 1; }
echo "==> deployed: ${APP_HOSTS_ARR[*]} ($(git rev-parse --short HEAD 2>/dev/null || echo -))"
