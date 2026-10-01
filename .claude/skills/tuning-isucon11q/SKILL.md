---
name: tuning-isucon11q
description: Use when tuning ISUCON11 予選 (ISUCONDITION) for a benchmark score - running make bench, reading measurements/, choosing the next bottleneck, changing condition / graph / trend / isu logic, tuning user growth (trend 間隔), or deciding whether to keep or revert a change.
---

# ISUCON11 予選 (ISUCONDITION) 計測改善ループ

## 大原則

**計測なき改善は禁止。1改善 = 1コミット = 1ベンチ。**

ベンチは prepare 数秒 + 負荷 60 秒で**約 80 秒**（`make bench` 全体で 2 分強）。まとめて入れる理由はない。
汎用の定石・対応表・罠は isucon-tools の `docs/` にある。このスキルは**この問題で判断を間違えやすい点**と**実測して分かったこと**だけを扱う。

## 1 サイクル

```sh
make bench NOTE="何を確かめたいか"      # measurements/<ts>/ と scores/log.md に記録
# score.json（errors / timeouts / 内訳 / user増）→ alp-*.txt → slow.txt → cpu-*.txt → vmstat-*.txt（ベンチ機も）
# 実装 → git commit（本文に「根拠: measurements/<ts>/xxx の数字」）
make deploy && make bench
git add measurements scores docs && git commit -m "bench: <score> <何の計測か>"
```

## ISUCONDITION のスコアの構造を忘れない

```
1000 + コンディション既読（Info 50件=20 / Warning=8 / Critical=4）
     + 当日グラフ（Good 60 / Normal 40 / Bad 24 / Worst 4）+ 完成グラフ（Good 150 / Normal 100 / Bad 60 / Worst 10）
     - エラー 1 点（100 回で fail）- タイムアウト 0.1 点
グラフの等級 = その日の 24 データポイントのコンディション数の最小 n（Good は n > 20）
```

- **点の 9 割以上はグラフ**（実測: グラフ 1 リクエスト ≒ 125 点、コンディション 1 リクエスト ≒ 5 点）
- ISU は 1 仮想分に 1 件のコンディションを作り、10 件ずつ POST する。定刻どおりなら 1 時間 30 件（=Good）
- **ベンチ機（c4.xlarge = 公式どおり）が先に飽和する。** サーバーを速くしても、ベンチ機の CPU が点にならない仕事
  （増えたユーザーの ISU の POST、閲覧者の trend）に使われるとグラフが欠けて Worst になる
- 先行指標は `breakdown_short` の `G` / `T`（Good の比率）と `user` / `isu` / `viewer`、そして `vmstat-isucon11-qualify-4.txt`

## 判断を間違えやすい点

### 1. 「ユーザーが増える = 良い」ではない

閲覧者が trend の**変化**を見るとユーザーが増える。ユーザーが増えると ISU が増え、ベンチ機が全 ISU の POST を定刻に送れなくなり、
1 ISU あたりの受信が 30 件/時 → 8 件/時に落ちて全グラフが Worst になる（下の表）。
trend を作り直す間隔（`TREND_INTERVAL_MS`）がユーザー数のつまみ。**変えたら user / isu / G の Good 比率を見る。**

### 2. スコアのブレ

初期実装は ±17%（タイムアウトの出方次第）。高速化後のブレは下の表。効果がブレ幅より小さいときは同じコードでもう 1 回。

### 3. 反映の遅れは仮想時間で効く

仮想時間は 3 万倍速。DB 書き込みの 200ms の遅れ = 1.7 仮想時間。読み出しはメモリから返す（POST で即反映）。
`GET /api/condition` と `/graph` は 1 秒以内に整合している必要がある（どちらも同じメモリを読むので常に整合）。

### 4. 状態はメモリ、正は DB

- アプリは ISU・ユーザー・全コンディションをメモリに持つ**ので 1 プロセスだけ**（isu3）。isu1 の nginx はユーザー向けの TLS 終端と静的配信をして isu3 の app に流す
- 起動時と `POST /initialize` で DB から全件読み直す（184 万行で 2.2 秒）。コンディションは 200ms ごとに bulk INSERT
- 追試「負荷走行で書き込まれたデータは再起動後も取得できる」→ DB に入っていること。メモリだけに置かない

### 5. initialize は 20 秒以内

`init.sh` = `0_Schema.sql` + `1_InitData.sql` + `2_Migrate.sql`（主キー張り替え・生成列・isu の索引）。
`1_InitData.sql` は列の位置指定 INSERT なので**元の列構成のまま流してから ALTER する**。

### 6. MariaDB 10.3

`SET PERSIST` 無し（計測の ON/OFF は `.measure` + `make bench` が毎回適用）。`EXPLAIN ANALYZE` 無し → `ANALYZE SELECT`。
`make explain SQL=...` はバッククォートを含む SQL だとシェルに食われる（ファイルで渡す）。

### 7. 観測を原因と取り違えない

「サーバーに余裕があるのに伸びない」ときはベンチ機の vmstat を見る。ここではずっと busy 90% 前後。

## 実測して分かったこと

| 観測 | 意味・打った手 | 根拠 |
| --- | --- | --- |
| 初期状態 2,874 / 2,041（同一コード） | mysqld 97%。`isu_condition` に索引なしで全件走査 | measurements/20261001-161540, -161806 |
| 索引で 18,000 | まだ「ユーザーは増えませんでした」。trend が 1 秒でタイムアウト | -162007 |
| trend をキャッシュしたら 9,150 に**下がった** | 閲覧者とユーザーが増え始め、負荷が増えて他がタイムアウト。方向は正しいので進めた | -162307 |
| nginx の `http2_max_requests` 既定 1000 | 接続が GOAWAY されベンチ側でエラー（1 回 1 点）。上げたら 0 | -163400 → -163607 |
| drop 率 0.9 → 0 で 108k → 228k | グラフが Worst 一色から Good が出始める | -163751 |
| 3 台構成（DB 分離・コンディション TLS を isu3）で 415k | サーバーは 3 台とも 50% 前後に。以後ベンチ機が busy 90% | -164003 |
| コンディション読み出しをメモリ化しても 435k（+5%） | グラフの質は変わらず。原因は反映遅れではなく**受信件数そのもの** | -164409 |
| 1 ISU の受信は開始 25 秒まで 30 件/時、その後 14 → 8 件/時 | ベンチ機が ISU の増加（51 → 518）に追いつけない。全体の受信は 3.7 万件/秒で頭打ち | journal 16:46 |
| trend 間隔 500ms → 1s / 2s / 4s / 60s で 435k → 987k / 2.07M / 3.31M / 4.57M | ユーザー 102 → 37 / 17 / 11 / 7。**増やさないほど高い** | -165021, -164837, -165157, -165358 |
| trend の応答を 500ms 遅らせて 5.09M | 閲覧者 21 人の trend が 15,325 → 6,168 回に減り、ベンチ機の CPU がユーザーに回る | -165620 |
| isu1 も HTTP/1.1 にして 6.30M（+20%） | Go の HTTP/2 クライアントが重い。ベンチ機 busy 88 → 82% | -170402 |
| 1 ISU・1 時間 21 件に絞って 6.69M | 超過分はコンディションのページ数とグラフ JSON を増やすだけ | -170624 |
| gzip_static で 6.01M（-10%） | ベンチ機が伸長で重くなる。revert | -171242 |
| trend 10s / 5s で 6.43M / 5.89M | ユーザーが 1〜3 人増えるだけで下がる（新しい ISU は履歴が短くグラフの遡りが短い） | -170853, -171027 |
| app を isu3 に移して 6.9M、trend 遅延 900ms で 7.1〜7.2M | サーバー側の待ちと閲覧者の TLS 接続を減らした | -172528, -173204 |
| 1 ログイン（約 1 秒）ごとに全 assets を取り直す（vendor.js だけで 2.3GB/分） | セッションごとにキャッシュが空。サーバー側では減らせない | journal 16:55 |
| icon の `Cache-Control: private, max-age` | 57k → 13k 回。ただしログインし直すと取り直す | -163607 |

### 踏んだ罠

- AMI の curl に `--retry-all-errors` が無い
- アプリの起動（全件読み込み）中に疎通確認して「届いていません」→ 200 が返るまで待つループに
- pt-query-digest が 225MB の slow log に 53 秒かかる（ベンチ後なのでスコアには無関係だが 1 サイクルが伸びる）

## ベンチマーカーはブラックボックス

ソースを読まない。プロファイルも取らない（`-profile` フラグはあるが使わない）。判断材料はスコア・内訳・alp・自分のサーバーのログ・ベンチ機の vmstat だけ。

## Red Flags

- 「サーバーに余裕があるからユーザーを増やそう」→ ベンチ機が飽和している。G の Good 比率が落ちる
- 「fail / エラーは数件だから許容」→ 1 回 1 点、100 回で 0 点
- trend の遅延は 900ms（timeouts 0 を確認済み）。これ以上は 1 秒のタイムアウトに掛かる。タイムアウトは 10 回 1 点で fail にはならない
- 「メモリにあるから DB は後で」→ 再起動で消える。追試で fail

## 締め

1. `make restart-test` — 3 台再起動後もベンチが通るか（最終スコアは再起動後の負荷走行）
2. `make measure-off` — スロークエリログを切って最終ベンチ
3. 新規の大きい変更をしない
