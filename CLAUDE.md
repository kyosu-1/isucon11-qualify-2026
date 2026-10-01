# CLAUDE.md — ISUCON11 予選 攻略の運用ルール

このリポジトリは **ISUCON11 予選の問題（ISUCONDITION）で、レギュレーションを守った上で最高スコアを取る**ためのもの。
AI エージェント（Claude Code）が主体で「計測 → 改善」のループを回す前提で書いてある。実装言語は **Go**（`webapp/go/`）。

汎用のナレッジ（計測の読み方・改善の定石・罠）は isucon-tools（`../../Dorayaki-World/isucon-tools/docs/`）にある。
このファイルには**この問題固有のこと**だけ書く。

## 0. まずこれを読む

| 知りたいこと | 見る場所 |
| --- | --- |
| 何をやったか（再現手順） | `docs/journal.md` |
| スコアの推移 | `scores/log.md` |
| 計測の生データ | `measurements/<timestamp>/` |
| 当日マニュアル（レギュレーション・スコア計算） | `docs/reference/manual.md` |
| アプリケーション仕様 | `docs/reference/isucondition.md` |
| 判断基準・この問題で実測して分かったこと | `.claude/skills/tuning-isucon11q/SKILL.md` |
| 汎用の定石・罠 | isucon-tools `docs/`（02-measurement / 03-improvements / 07-pitfalls） |

## 1. レギュレーション（違反したら全部無意味）

`docs/reference/manual.md` が原典。要点:

### 構成

- 競技サーバーは **c5.large（2 vCPU / 4GB）× 3 台**。この 3 台だけで処理する。
  - `isucon11-qualify-1..3` が競技サーバー、`isucon11-qualify-4`（**c4.xlarge**。公式 README のベンチ VM と同じ）は**ベンチマーカー専用**
  - 役割分担は `etc/isuN/services` が正（`make deploy` がそのとおりに enable/disable する）
- ベンチマーカーはユーザーのリクエストを `ENTRY`（`-target`）の 443 に送る。
  ISU のコンディション送信（`POST /api/condition/:uuid`）は、アプリが JIA に登録した `POST_ISUCONDITION_TARGET_BASE_URL`
  （`https://isucondition-[1-3].t.isucon.dev` のどれか。ポート指定不可・https のみ）のノードの 443 に直接来る
- `/etc/hosts` の `isucondition-N.t.isucon.dev` は 3 台の private IP（`make setup` が書く。AMI 初期値は 127.0.0.1x）

### スコア

```
開始 1000 点
+ コンディション既読（GET /api/condition で前回より新しいものを確認）: Info 50件=20 / Warning 50件=8 / Critical 50件=4
+ 当日グラフ（GET /api/isu/:id/graph）: データポイントごとのコンディション数の最小 n で Good(n>20)=60 / Normal=40 / Bad=24 / Worst=4
+ 完成グラフ（当日以外。同日 1 回だけ）: Good=150 / Normal=100 / Bad=60 / Worst=10
- エラー 1 回 1 点（100 回で fail）、タイムアウト 10 回 1 点（POST /api/condition は減点なし・100ms でタイムアウト）
```

- **コンディションを落とさず保存する**ほどグラフが Good になり、既読件数も増える（初期実装は 9 割を捨てている）
- ユーザーはベンチが「負荷に応答できている」と判断すると増える。タイムアウトが多いと「評判が悪くなり」以降増えない
- 閲覧者（未ログインで `/api/trend` を見る）はトレンドの変化を見て増える/離脱する

### FAIL（スコア 0）

- `POST /initialize` が **20 秒**以内に終わらない / `language` が空
- アプリケーション互換性チェック失敗、エラー 100 回

### 猶予時間・仕様

- `POST /api/condition` の反映は `GET /api/isu` `/graph` `GET /api/condition` `/api/trend` で**遅れてよい**。
  ただし `GET /api/condition` と `/graph` の間は **1 秒以内に整合**
- Conditional GET 対応（`Cache-Control` / 304）。ユーザー間でキャッシュ共有はしない
- それ以外は POST 直後の GET に即時反映

### 変更してはいけない

- URI・レスポンスの JSON 構造・静的ファイルの内容（ベンチがハッシュを検証する）
- `POST /initialize` のレスポンスの `language`
- （本番のみ）`isucon-env-checker` 関連、セキュリティグループ

### 守らないといけない性質

- **最終スコアは全サーバー再起動後の負荷走行。** enable していないサービス・手で起動したプロセス・起動順序依存は全部アウト → `make restart-test`
- **負荷走行で書き込まれたデータは再起動後も取得できること**（追試）。メモリだけに持たない。DB/ファイルが正で、起動時と initialize で復元する
- ブラウザ上の挙動を初期状態と同様に保つ

## 2. 大原則

1. **計測なき改善は禁止。** 根拠（alp / slow / pidstat / ベンチの指標）を `measurements/` から示す
2. **1 改善 = 1 コミット = 1 ベンチ**
3. **下がったら revert**（ノイズ幅は SKILL.md）
4. **サーバー上で直接編集しない。** ローカルの git が正、反映は `make deploy` だけ
5. **やったことは `docs/journal.md` に残す**（失敗も）
6. **計測記録はコードと別コミット**（`bench: <score> ...`）
7. **終盤は守り**: `make restart-test` → `make measure-off` → 最終ベンチ
8. **ベンチマーカーはブラックボックス。** ソースを読まない（CLI フラグの確認は可）

## 3. 改善ループ

```
make bench NOTE="何を確かめたいか"
  ↓ measurements/<ts>/: score.json（エラー・timeout・内訳・user増）→ alp-*.txt → slow.txt → cpu-*.txt → vmstat-*.txt
  ↓ 支配的なボトルネックを 1 つ → 最小の変更 → git commit（本文に根拠）
make deploy && make bench
git add measurements scores docs && git commit -m "bench: <score> <何の計測か>"
```

| 症状 | 見るもの | 疑うこと |
| --- | --- | --- |
| 「ユーザーは増えませんでした」が並ぶ / timeout が多い | `score.json` | 1 秒を超える GET（alp の max/p99） |
| G/T が Worst ばかり | `breakdown_short` | コンディションを捨てている・保存が遅れている |
| post 件数が少ない | `breakdown_short` の post | POST /api/condition が 100ms を超えている |
| 特定 URI の SUM が突出 | `alp-*.txt` | N+1・全件取得 |
| `Rows examine` ≫ `Rows sent` | `slow.txt` | インデックス |
| mysqld が CPU を支配 | `cpu-*.txt` | DB。クエリ/インデックス/キャッシュ/DB 分離 |
| ベンチ機の idle が小さい | `vmstat-isucon11-qualify-4.txt` | ベンチ機が先に飽和（c4.xlarge は公式どおりなので受け入れる） |

## 4. 自律で進めてよい範囲

承認なし: 計測・解析・実装・コミット・`make deploy` / `make bench` / `make restart-test`・revert・3 台の役割変更。
必ず先に確認: `make down`・環境の作り直し・ベンチ機のインスタンスタイプ変更・レギュレーション解釈が割れる変更。

## 5. やってはいけないこと

- ベンチマーカーのソースを読んで先回りする／ベンチ機に処理を載せる
- 静的ファイルの内容・レスポンス構造を変える
- 再起動で消える状態に依存する
- スロークエリログ ON のまま最終ベンチ（`make measure-off`）

## 6. 環境

```sh
aws login --profile personal     # セッション切れのとき
AWS_PROFILE=personal isuenv up isucon11-qualify --nodes 3 --bench-instance-type c4.xlarge --ttl 6h
make setup      # hosts 生成 → 計測ツール → /etc/hosts → ベンチ機のサービス停止 → deploy
make bench
make down       # 破棄（要確認）
```

- ssh の Host 名: `isucon11-qualify-1..4`（`~/.ssh/isuenv_config`）。IP が変わったら `AWS_PROFILE=personal isuenv ssh isucon11-qualify`
- ベンチは約 80 秒（prepare 数秒 + 負荷 60 秒）
- DB は **MariaDB 10.3**（`SET PERSIST` 無し・`EXPLAIN ANALYZE` 無し → `ANALYZE SELECT`）。スロークエリの ON/OFF は `.measure` に持ち `make bench` が毎回適用する
