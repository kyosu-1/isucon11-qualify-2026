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

### 16:15〜16:40 ベースライン → 1 台での改善 → 3 台構成

スコアと根拠の数字は `scores/log.md` と各コミットの本文。流れだけ:

1. ベースライン 2,874 / 2,041（同一コード。タイムアウトの出方で ±17% ぶれる）
2. `isu_condition (jia_isu_uuid, timestamp)` の索引 → 18,000
3. trend を 500ms ごとの 1 クエリのキャッシュに → **9,150 に下がる**。閲覧者とユーザーが増えて負荷が増えただけなので revert せず進めた
4. 静的ファイルを nginx 直配信、アプリのアクセスログ停止 → 17,561
5. PK を (uuid, timestamp) に張り替え、レベル絞り込み・LIMIT・日付範囲を SQL へ、一覧の N+1 解消 → 20,485（1 台が飽和していて伸びない）
6. ISU・最新コンディション・ユーザーをメモリに、コンディションは 200ms ごとに bulk INSERT → 79,612
7. `http2_max_requests`（既定 1000 で GOAWAY → ベンチ側エラー 24 件）+ icon の Cache-Control → 108,374 / エラー 0
8. drop 率 0.9 → 0 → 228,052
9. 3 台構成（isu1: nginx+app / isu2: MariaDB / isu3: コンディション受信の nginx → isu1 の app）→ 415,536

ハマり:
- `make explain SQL='... `col` ...'` はバッククォートが make→bash で食われる（未修正。ファイルで渡す必要あり）
- `1_InitData.sql` が列の位置指定 INSERT。列を足すなら流した後に ALTER（`2_Migrate.sql`）
- アプリが起動時に全コンディションを読むようになり、bench の疎通確認が先に走って中止 → 200 を待つループに
- python の文字列置換で cache.go の一部を消してしまい vet で検知（`git show HEAD:` から復元）

### 16:44〜16:50 ベンチ機の飽和に気づく

コンディションの読み出しをメモリ化しても 435k（+5%）でグラフの質が変わらない。DB を集計:

```
ssh isucon11-qualify-2 'sudo mysql isucondition -e "select floor(unix_timestamp(created_at)/5)*5 t, count(*), count(distinct jia_isu_uuid) ... group by 1"'
```

- ISU は 1 仮想分に 1 件、10 件ずつ POST。定刻なら 1 時間 30 件
- 1 ISU あたりの受信: 開始〜25 秒は 30 件/時 → 以後 26 → 14 → 8 件/時。ISU 数は 51 → 518
- 全体の受信は 18.5 万件/5 秒で頭打ち。ベンチ機の vmstat は開始直後から us+sy 86〜95%
- ベンチのバイナリに race detector は入っていない（`go tool nm | grep tsan` = 0）。単にベンチ機（c4.xlarge）が先に飽和する

→ サーバーではなく**ベンチ機の CPU の使われ方**を最適化する問題になった。

### 16:48〜17:12 ユーザーを増やさない方が点が高い

| 変更 | スコア | user / isu | 備考 |
| --- | ---: | --- | --- |
| trend 間隔 500ms | 435,409 | 102 / 534 | G の Good 32% |
| 1000ms | 987,450 | 37 / 201 | |
| 2000ms | 2,070,376 | 17 / 91 | |
| 4000ms | 3,306,678 | 11 / 66 | |
| 60000ms（増えない） | 4,566,522 | 7 / 48 | |
| + trend の応答を 500ms 遅延 | 5,088,950 / 5,207,582 | 7 / 48 | trend 15k → 6k 回。同一コード 2 回で ±1.2% |
| 遅延 800ms | 5,241,602 | | ノイズ内。500ms に戻した |
| isu3 を HTTP/1.1 | 5,231,564 | | Good 比率は上がった。採用 |
| isu1 も HTTP/1.1 | 6,301,214 | | +20%。ベンチ機 busy 88 → 82% |
| 1 時間 21 件に絞る | 6,692,152 | | グラフ 51k → 56k リクエスト |
| trend 10s / 5s | 6,430,176 / 5,892,268 | 8 / 51, 10 / 60 | 1 人増えるだけで下がる → 60s に戻した |
| gzip_static | 6,006,434 | | -10%。伸長でベンチ機が重くなる。revert |

nginx ログ（conn / conn_req を追加）から: ユーザー向けは 49 接続が各 1000 リクエスト以上。1 接続あたり 78 req/s = 1 往復 12.8ms、
うちサーバー時間は 1ms 未満。**ユーザーのループはベンチ機側の処理時間で決まっている**ので、サーバーの応答時間を削っても伸びない。

### 17:15〜17:35 仕上げ

- isu2 の MariaDB 設定（buffer pool 2G など）。スコアには無関係（mysqld 16%）
- `make restart-test`（3 台 reboot）: サービスは全部自動起動、6,736,544
- スロークエリログ OFF（`make measure-off`）: 6,738,798 / 6,773,304（ON と差なし。DB はボトルネックでない）
- nginx ログから: ユーザーの訪問は「全レベルのコンディション（平均 3.9 ページ）/ warning,critical / グラフ（当日から負荷開始日まで 1 日ずつ遡る）」が 1/3 ずつ。
  点のほぼ全部はグラフの遡り（1 リクエスト 150 点）。閲覧者の trend は 1 リクエストごとに新しい TLS 接続
- app を isu3 に移す（isu1 は nginx だけ）: 6,909,950 / 6,876,034。再起動試験 7,024,602
- trend 遅延 900ms: 7,182,440 / 7,103,066（timeouts 0）

### 到達点と引き継ぎ

- 最終構成: isu1 nginx / isu2 MariaDB / isu3 nginx + app。約 710 万点、エラー 0
- ベンチ機（c4.xlarge）が busy 86% で律速。サーバーは 3 台とも 16〜44%
- 未解決: グラフの 1 割が Normal（ベンチ機の送信が遅れて 1 時間 20 件になる時間帯がある）。サーバー側からは手が無い
- 未修正: `make explain` がバッククォートを含む SQL で壊れる。`restart-test.sh` の `NOAPPRESTART` は bench.sh 側で未実装（毎回 app を再起動する）
- 次にやるなら: レギュレーション判断が要る 3 設定（README）を外した「保守的な構成」での最適点探し

### 18:00〜18:15 追加の実験（どちらも不採用）

| 実験 | スコア | 判断 |
| --- | --- | --- |
| nginx を外し isu3 の app が直接 TLS(443) を受ける | 6,856,746 / 6,982,501（timeouts 10） | 下がった。app が 62% → 91% になり、Normal のグラフも増える。revert |
| TLS 1.2 + AES128-GCM 固定 | 7,179,670 / 7,099,492 | 変化なし（直前 7.10〜7.19M）。revert |

ハマり: 直接 TLS の revert を deploy したとき、app が 443 を握ったまま nginx を `enable --now` して失敗 → nginx が上がらないままベンチして 1000 点
（`scores/log.md` の 18:04 の行は無効）。もう一度 deploy したら直った。**deploy が「失敗したノードがあります」と言ったらベンチしない。**
