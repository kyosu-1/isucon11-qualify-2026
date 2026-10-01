# スコア推移

`make bench` が自動で 1 行ずつ追記する。判断（採用 / revert）はメモ列に書く。

- スコア = 加点（raw）− 減点（deduction）。`pass=FAIL` のスコアは無効（エラー 100 回で fail）
- timeout は 10 回で 1 点減点（POST /api/condition は対象外）
- 内訳 = `G` 完成グラフ Good/Normal/Bad/Worst、`T` 当日グラフ 同、`R` コンディション既読 Info/Warning/Critical（50 件単位）、user ユーザー数、viewer 閲覧者 参加-離脱、isu 登録 ISU 数、post 受信コンディション Info/Warning/Critical

| 時刻 | スコア | 前回比 | pass | raw | 減点 | err | timeout | 内訳 | 変更 | commit | 計測 | メモ |
| --- | ---: | ---: | --- | ---: | ---: | ---: | ---: | --- | --- | --- | --- | --- |
| 10-01 16:17 | 2874 | - | ok | 2894 | 20 | 0 | 209 | G 0/0/0/77 T 0/0/0/28 R 21/63/22 user 7 viewer 21-21 isu 48 post 977/4591/8211 | chore: bench の疎通確認を古い curl でも動くようにする | `8ae8d05` | 20261001-161540 | ベースライン1（無改善・slowlog ON） |
| 10-01 16:19 | 2041 | -29.0% | ok | 2072 | 31 | 0 | 311 | G 0/0/0/34 T 0/0/0/23 R 23/21/3 user 7 viewer 21-21 isu 48 post 1033/3711/8799 | docs: CLAUDE.md と journal を書く | `a03ce0a` | 20261001-161806 | ベースライン2（ノイズ幅） |
