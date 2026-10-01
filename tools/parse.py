#!/usr/bin/env python3
"""ISUCON11 予選ベンチの出力 (bench.log) -> score.json

拾うもの:
  score: N(raw - deduction) : pass|fail   最後の 1 行が最終結果
  deduction: E / timeout: T               エラー数（1 回 1 点・100 回で fail）とタイムアウト数（10 回 1 点）
  SCORE: <tag> : <count>                  加点タグ別の件数（グラフ Good/Normal/Bad/Worst、コンディション既読、ユーザー数など）
  ユーザーが増える / 増えない             負荷レベルが上がったか（先行指標）
  それ以外の行                            エラーメッセージ（UUID と数字を潰して uniq）
usage: parse.py bench.log > score.json / parse.py --show score.json
"""
import collections, json, re, sys

if sys.argv[1] == "--show":
    s = json.load(open(sys.argv[2]))
    print(f"  score={s['score']} pass={s['pass']} raw={s['raw']} deduction={s['deduction']} errors={s['error_count']} timeouts={s['timeout_count']} user増={s['user_added']} 増えず={s['user_not_added']}")
    print(f"  {s['breakdown_short']}")
    for e in s["errors"][:8]:
        print(f"  MSG x{e['count']}: {e['msg'][:170]}")
    sys.exit(0)

lines = open(sys.argv[1], errors="replace").read().splitlines()
out = {"score": 0, "pass": False, "raw": 0, "deduction": 0, "error_count": 0, "timeout_count": 0,
       "user_added": 0, "user_not_added": 0, "breakdown": {}, "breakdown_short": "-", "errors": []}
msgs = collections.Counter()
for ln in lines:
    body = re.sub(r"^\d\d:\d\d:\d\d\.\d+ ", "", ln)
    m = re.match(r"score: (-?\d+)\((-?\d+) - (-?\d+)\) : (\w+)", body)
    if m:
        out.update(score=int(m[1]), raw=int(m[2]), deduction=int(m[3]), **{"pass": m[4] == "pass"})
        continue
    m = re.match(r"deduction: (\d+) / timeout: (\d+)", body)
    if m:
        out.update(error_count=int(m[1]), timeout_count=int(m[2]))
        continue
    m = re.match(r"SCORE: (\S+)\s*: (\d+)", body)
    if m:
        out["breakdown"][re.sub(r"^[_\d]+\.", "", m[1])] = int(m[2])
        continue
    if "ユーザーは増えませんでした" in body:
        out["user_not_added"] += 1
        continue
    if "増やします" in body or "増えました" in body or "ユーザーが増" in body:
        out["user_added"] += 1
        continue
    if "loadWaitGroup" in body or body.startswith(("===>", "<===", "LOAD ", "---", "start:", "finish:", "ISUCON11", "  Language", "  Campaign")) or not body.strip():
        continue
    msgs[re.sub(r"\d+", "N", re.sub(r"[0-9a-f]{8}-[0-9a-f-]{27}", "<UUID>", body))[:300]] += 1
b = out["breakdown"]
if b:
    g = lambda k: b.get(k, 0)
    out["breakdown_short"] = (f"G {g('GraphGood')}/{g('GraphNormal')}/{g('GraphBad')}/{g('GraphWorst')} "
        f"T {g('TodayGraphGood')}/{g('TodayGraphNormal')}/{g('TodayGraphBad')}/{g('TodayGraphWorst')} "
        f"R {g('ReadInfoCondition')}/{g('ReadWarningCondition')}/{g('ReadCriticalCondition')} "
        f"user {g('NormalUserInitialize')} viewer {g('ViewerInitialize')}-{g('ViewerDropout')} isu {g('IsuInitialize')} "
        f"post {g('PostInfoCondition')}/{g('PostWarningCondition')}/{g('PostCriticalCondition')}")
out["errors"] = [{"count": c, "msg": m} for m, c in msgs.most_common(30)]
print(json.dumps(out, ensure_ascii=False, indent=1))
