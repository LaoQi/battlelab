#!/usr/bin/env python3
"""把 --json 结果转成 Markdown 报告。"""
import json, sys

d = json.load(open(sys.argv[1] if len(sys.argv) > 1 else '/dev/stdin'))
stats = d['stats']
print('# vbash-bench 报告\n')
print('| 层级 | 通过 | 失败 | 通过率 |')
print('|---|---|---|---|')
tp = tt = 0
for tier in sorted(stats):
    p, t, e = stats[tier]
    tp += p; tt += t
    print('| %s | %d | %d | %.1f%% |' % (tier, p, t - p - e, p / t * 100 if t else 0))
print('| **合计** | **%d** | **%d** | **%.1f%%** |' % (tp, tt - tp, tp / tt * 100 if tt else 0))
fails = [r for r in d['results'] if r['status'] != 'PASS']
if fails:
    print('\n## 失败用例\n')
    for r in fails:
        print('- `%s`' % r['name'])
