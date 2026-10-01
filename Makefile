.DEFAULT_GOAL := help
SHELL := /bin/bash

# isuenv が振った ssh 設定(~/.ssh/isuenv_config)の Host 名をそのまま使う。
#   ENTRY     : ベンチマーカーがユーザーのリクエストを送る入口（-target）
#   APP_HOSTS : 競技サーバー 3 台
#   DB_HOST   : MariaDB が動くノード（スロークエリ収集先。deploy はここを先に配る）
#   BENCH     : ベンチマーカー専用ノード（hosts.generated.mk）
ENTRY     ?= isucon11-qualify-1
APP_HOSTS ?= isucon11-qualify-1 isucon11-qualify-2 isucon11-qualify-3
DB_HOST   ?= isucon11-qualify-2
-include hosts.generated.mk
export ENTRY APP_HOSTS DB_HOST BENCH ISU1_IP ISU2_IP ISU3_IP BENCH_IP

help: ## このヘルプ
	@grep -hE '^[a-zA-Z0-9_.-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: hosts status info down setup measure-on measure-off build deploy bench restart-test logs explain pprof
hosts: ## private IP を hosts.generated.mk に書き出す
	@./tools/gen-hosts.sh
status: ## 稼働中の環境と TTL
	@AWS_PROFILE=$${AWS_PROFILE:-personal} isuenv list
info: ## 現在の対象ホスト
	@echo "ENTRY=$(ENTRY) DB_HOST=$(DB_HOST) BENCH=$(BENCH)($(BENCH_IP)) ISU=$(ISU1_IP),$(ISU2_IP),$(ISU3_IP) slowlog=$$(cat .measure 2>/dev/null || echo on)"
down: ## 環境を破棄する（要確認）
	@AWS_PROFILE=$${AWS_PROFILE:-personal} isuenv down isucon11-qualify
setup: ## 環境を作り直したあとの初期化（冪等）: hosts → ツール導入 → /etc/hosts → ベンチ機 → deploy
	@./tools/gen-hosts.sh && ./tools/setup.sh && $(MAKE) deploy
measure-on: ## スロークエリログ ON（以降の make bench で適用）
	@./tools/measure.sh on
measure-off: ## スロークエリログ OFF（最終計測前に必ず）
	@./tools/measure.sh off
build: ## Go アプリを linux/amd64 にクロスビルド
	@./tools/build.sh
deploy: build ## ローカルの変更を全台に配布して再起動
	@./tools/deploy.sh
bench: ## ログ初期化 → ベンチ → 解析 → 記録（NOTE="..."）
	@./tools/bench.sh "$(NOTE)"
restart-test: ## 3 台再起動 → ベンチ
	@./tools/restart-test.sh
logs: ## アプリのログ（HOST=...）
	@ssh -o LogLevel=ERROR $(or $(HOST),$(ENTRY)) 'sudo journalctl -u isucondition.go -n 80 --no-pager -o cat'
explain: ## 実行計画（SQL="SELECT ..."）。MariaDB 10.3 なので EXPLAIN + ANALYZE
	@printf 'EXPLAIN %s;\nANALYZE %s;\n' "$(SQL)" "$(SQL)" > /tmp/isu11-explain.sql && scp -q -o LogLevel=ERROR /tmp/isu11-explain.sql $(DB_HOST):/tmp/explain.sql && ssh -o LogLevel=ERROR $(DB_HOST) 'sudo mysql isucondition -t < /tmp/explain.sql'
pprof: ## ベンチ中に CPU プロファイル 30 秒（HOST=...）
	@ssh -o LogLevel=ERROR $(or $(HOST),$(ENTRY)) "curl -fsS -o /tmp/cpu.pprof 'http://127.0.0.1:6060/debug/pprof/profile?seconds=30'" && scp -q -o LogLevel=ERROR $(or $(HOST),$(ENTRY)):/tmp/cpu.pprof /tmp/isu11-cpu.pprof && go tool pprof -top -nodecount=40 /tmp/isu11-cpu.pprof 2>/dev/null
