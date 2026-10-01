# 作業ログ（ISUCON11 予選 / ISUCONDITION）

スコアは `scores/log.md` が正。ここには「何をしたか・なぜか・ハマった点」。

### 10-01 16:08 環境作成（1 回目。ベンチ機 c5.2xlarge）→ 作り直し

```
AWS_PROFILE=personal isuenv up isucon11-qualify --nodes 3 --bench-instance-type c5.2xlarge --ttl 6h
```
ベンチ機のサイズは isucon14 の練習メモを流用しただけで、この問題の推奨ではなかった。
公式 README（isucon/isucon11-qualify）に「競技者 VM c5.large × 3 / ベンチ VM c4.xlarge」と明記があるので、
ベースラインを取る前に作り直した。

### 16:14 環境作成（2 回目。ベンチ機 c4.xlarge = 公式どおり）

```
AWS_PROFILE=personal isuenv down isucon11-qualify
AWS_PROFILE=personal isuenv up isucon11-qualify --nodes 3 --bench-instance-type c4.xlarge --ttl 6h   # 22:14 に自動終了
make setup
```

サーバーの初期状態（3 台とも同一）:
- Ubuntu 20.04 / 2 vCPU / 3.6GB / swap 0 / ディスク 16GB 中 7.9GB 空き
- nginx(443, http2) → isucondition.go(3000) → MariaDB 10.3.34(127.0.0.1:3306)。jiaapi-mock(5000) も enabled
- innodb_buffer_pool_size 128MB / flush_log_at_trx_commit=1 / query_cache 16MB
- テーブル: isu(画像 LONGBLOB) / isu_condition(インデックスは PK のみ) / user / isu_association_config
- `/etc/hosts` の isucondition-N.t.isucon.dev が 127.0.0.11〜13（1 台構成用）→ setup で private IP に書き換え
- ベンチ: `./bench -all-addresses <3台> -target <isu1>:443 -tls -jia-service-url http://<bench>:4999`

ハマり: サーバーの curl が古く `--retry-all-errors` が無い → `--retry-connrefused`。
