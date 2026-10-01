# 共通: isuenv の ssh 設定は known_hosts を持たないので "Permanently added" を黙らせる
ssh() { command ssh -o LogLevel=ERROR -o ConnectTimeout=10 "$@"; }
scp() { command scp -o LogLevel=ERROR "$@"; }
export RSYNC_RSH="ssh -o LogLevel=ERROR"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
mk() { awk -F':= *' -v k="$1" '$1 ~ "^"k" *$" {print $2}' hosts.generated.mk 2>/dev/null; }
ISU1_IP="${ISU1_IP:-$(mk ISU1_IP)}"; ISU2_IP="${ISU2_IP:-$(mk ISU2_IP)}"; ISU3_IP="${ISU3_IP:-$(mk ISU3_IP)}"
BENCH="${BENCH:-$(mk BENCH)}"; BENCH_IP="${BENCH_IP:-$(mk BENCH_IP)}"
PREFIX=isucon11-qualify
ENTRY="${ENTRY:-$PREFIX-1}"
DB_HOST="${DB_HOST:-$PREFIX-1}"
read -r -a APP_HOSTS_ARR <<< "${APP_HOSTS:-$PREFIX-1 $PREFIX-2 $PREFIX-3}"
APP_SVC=isucondition.go
