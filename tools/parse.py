#!/usr/bin/env python3
"""bench.log -> score.json（暫定: 生出力を見てから拾う項目を増やす）"""
import json, re, sys
if sys.argv[1] == "--show":
    s = json.load(open(sys.argv[2]))
    print(f"  score={s['score']} pass={s['pass']} raw={s.get('raw')} deduction={s.get('deduction')} errors={s.get('error_count')} timeouts={s.get('timeout_count')}")
    for k, v in (s.get("breakdown") or {}).items():
        print(f"    {k}: {v}")
    for e in s.get("errors", [])[:8]:
        print(f"  ERR x{e['count']}: {e['msg'][:170]}")
    sys.exit(0)
text = open(sys.argv[1], errors="replace").read()
out = {"score": 0, "pass": False}
print(json.dumps(out, ensure_ascii=False, indent=1))
