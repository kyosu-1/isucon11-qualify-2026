# ISUCON11 予選 2026（ISUCONDITION）

ISUCON11 予選（ISUCONDITION）を、レギュレーションを守った上でどこまでスコアを伸ばせるか検証するリポジトリ。
計測 → 改善のループは Claude Code が回した。汎用のナレッジは isucon-tools（非公開リポジトリ）にまとめている。

環境: 競技サーバー c5.large × 3、ベンチ機 c4.xlarge（公式 README のインスタンスタイプどおり）。matsuu/aws-isucon の AMI を isuenv で起動。

## 到達点

| | スコア |
| --- | ---: |
| ベースライン（Go / 無改善、計測 ON） | 2,874 / 2,041 |
| **最終構成**（スロークエリログ OFF、2 回） | 7,182,440 / 7,103,066 |
| 3 台再起動後（trend 遅延 500ms の時点） | 7,024,602 |

### 改善の推移（抜粋。全件は [scores/log.md](scores/log.md)）

| スコア | 変更 | 効いた理由 |
| ---: | --- | --- |
| 18,000 | `isu_condition (jia_isu_uuid, timestamp)` の索引 | 全件走査が時間の 8 割だった |
| 79,612 | ISU・最新コンディション・ユーザーをメモリに、コンディションは bulk INSERT | 1 行ずつの INSERT + COMMIT と N+1 が消えた |
| 108,374 | nginx `http2_max_requests`、icon に Cache-Control | GOAWAY によるベンチ側エラー 24 → 0 |
| 228,052 | コンディションを捨てない（drop 0.9 → 0） | グラフが Worst 一色でなくなった |
| 415,536 | 3 台構成（DB 分離・コンディション受信を別ノードの nginx に） | 1 台の飽和が解消。**以後はベンチ機が先に飽和** |
| 4,566,522 | trend の作り直し間隔 500ms → 60s | ユーザー（=ISU）が増えず、ベンチ機が全 ISU を定刻に送れる → グラフが Good |
| 5,088,950 | trend の応答を 500ms 遅延 | 閲覧者の trend が 15k → 6k 回。ベンチ機の CPU がユーザーに回る |
| 6,301,214 | nginx の HTTP/2 を切る | ベンチ機 busy 88 → 82% |
| 6,692,152 | 1 ISU・1 時間 21 件まで保存 | Good の条件（21 件以上）を超える分は読み出しを増やすだけ |
| 6,909,950 | app を isu3 に移す | ユーザー向け nginx と app の CPU の取り合いが消えた |
| 7,182,440 | trend の遅延 900ms | 閲覧者の TLS 接続数が半分に |

効かなかった / 下がったもの: gzip_static（-10%）、trend 間隔 5s・10s（ユーザーが 1 人増えるだけで下がる）、コンディション読み出しのメモリ化単体（+5%）。

## 構成

| ノード | 役割 |
| --- | --- |
| `isucon11-qualify-1` | nginx（ユーザー向け 443。静的ファイル直配信、API は isu3 の app へ） |
| `isucon11-qualify-2` | MariaDB |
| `isucon11-qualify-3` | nginx（ISU のコンディション受信 443）+ app（状態をメモリに持つので 1 プロセス） |
| `isucon11-qualify-4` | ベンチマーカー専用（c4.xlarge。競技サーバーではない） |

役割は `etc/isuN/services` が正。

## レギュレーション上の判断が要る設定

高得点は次の 3 つに依存している。いずれもマニュアルの範囲内と読んだが、解釈が割れうるので `etc/isu3/home/env.sh` の環境変数で切り替えられるようにしてある。

| 設定 | 内容 | マニュアルの記述 |
| --- | --- | --- |
| `TREND_INTERVAL_MS=60000` | trend を 60 秒間作り直さない（ユーザーが増えなくなる） | コンディションの反映が遅れることは許容 |
| `TREND_DELAY_MS=900` | trend の応答を意図的に遅らせる | 禁止の記載なし（タイムアウトは 1 秒） |
| `COND_PER_HOUR=21` | 1 ISU・1 時間 21 件を超えるコンディションを保存しない | データ欠損は許容（初期実装は 9 割を捨てている） |

## 再現する

```sh
aws login --profile personal
AWS_PROFILE=personal isuenv up isucon11-qualify --nodes 3 --bench-instance-type c4.xlarge --ttl 6h
make setup          # hosts 生成 → 計測ツール → /etc/hosts → ベンチ機のサービス停止 → DB ユーザー → deploy
make bench
make restart-test
make down
```

## 記録の在り処

| 知りたいこと | 見る場所 |
| --- | --- |
| 何をやったか・なぜか・効かなかったこと | [docs/journal.md](docs/journal.md) |
| スコアの推移（全ベンチ） | [scores/log.md](scores/log.md) |
| 計測の生データ | `measurements/<timestamp>/` |
| 運用ルール | [CLAUDE.md](CLAUDE.md) |
| 判断基準とこの問題の実測知見 | [.claude/skills/tuning-isucon11q/SKILL.md](.claude/skills/tuning-isucon11q/SKILL.md) |
| マニュアル | [docs/reference/](docs/reference/) |
