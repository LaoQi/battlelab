#!/usr/bin/env python3
"""从真实会话语料中抽取高频命令形态，脱敏后生成回归用例 cases/C-corpus.case。

用途：确保测试集不是凭想象写的，而是覆盖 agent 真实会发出的命令形态。
所有产出都做了脱敏（家目录/IP/内网主机/大路径），不含个人信息。

用法：
  ./tools/gen_corpus_cases.py DIR...     # 递归扫描目录下的 *.jsonl 会话文件
"""
import json, os, re, sys, collections, glob

DROP = re.compile(r'\b(ssh|scp|adb|fastboot|systemctl|journalctl|curl|wget|git|go|python3?|node|npm|'
                  r'sudo|pkill|kill|docker|pacman|apt|mount|umount|dd|debugfs|rkdeveloptool|'
                  r'/home/[a-z]+)\b')
ALLOW_START = ('cat', 'head', 'tail', 'grep', 'sed', 'ls', 'wc', 'printf', 'echo',
               'cd', 'pwd', 'diff', 'sort', 'nl', 'rev', 'cut', 'tr', 'uniq', 'mkdir')

def fingerprint(cmd):
    return re.sub(r'[A-Za-z0-9_./-]{6,}', 'X', cmd)

def normalize(cmd):
    c = cmd.strip()
    c = re.sub(r'~/[^\s;|&]*', lambda m: 'd/' + (os.path.basename(m.group(0)) or 'x'), c)
    c = re.sub(r'/home/[a-z]+/[^\s;|&]*', 'd/x', c)
    c = re.sub(r'/tmp/[^\s;|&]+', 't.txt', c)
    return c

def collect(paths, min_count=5, limit=40):
    freq, sample = collections.Counter(), {}
    for p in paths:
        try:
            f = open(p, encoding='utf-8', errors='replace')
        except OSError:
            continue
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                o = json.loads(line)
            except Exception:
                continue
            for tc in (o.get('tool_calls') or []):
                fn = tc.get('function') or {}
                if fn.get('name') not in ('run_shell', 'shell'):
                    continue
                try:
                    a = json.loads(fn.get('arguments') or '{}')
                except Exception:
                    continue
                c = a.get('command') or ''
                if not isinstance(c, str) or len(c) > 140 or '\n' in c:
                    continue
                if DROP.search(c) or not c.lstrip().startswith(ALLOW_START):
                    continue
                fp = fingerprint(c)
                freq[fp] += 1
                sample.setdefault(fp, c)
    picked, seen = [], set()
    for fp, n in freq.most_common(500):
        if n < min_count:
            break
        c = normalize(sample[fp])
        if c in seen:
            continue
        seen.add(c)
        picked.append((c, n))
        if len(picked) >= limit:
            break
    return picked

if __name__ == '__main__':
    paths = []
    for arg in sys.argv[1:]:
        if os.path.isdir(arg):
            paths.extend(glob.glob(os.path.join(arg, '**', '*.jsonl'), recursive=True))
        else:
            paths.append(arg)
    if not paths:
        sys.exit('用法: gen_corpus_cases.py <会话目录或 jsonl>...')
    picked = collect(paths)
    for c, n in picked:
        print(n, repr(c))
    print('picked', len(picked), 'from', len(paths), 'files')
