#!/usr/bin/env python3
"""vbash-bench runner: 用真 bash 录制 golden，用被测实现回放比对。"""
import argparse, json, os, re, shlex, shutil, subprocess, sys, tempfile, difflib, glob

CASE_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'cases')
GOLDEN_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'golden')
FIELDS = ('name', 'setup', 'command', 'stdin', 'files', 'env', 'cwd', 'expect_exit', 'skip_golden', 'check_files', 'timeout', 'note')

def parse_case_file(path):
    """解析 .case。

    格式：
        --- name: <id>
        # 注释行（在第一个字段之前忽略）
        field: value          简单字段
        blockfield:           块字段（setup/command），后续所有行原样收集，直到下一个
          ...                 顶格 `field:` 出现
    """
    BLOCK = ('setup', 'command')
    cases, cur = [], None
    block_key = None
    with open(path, encoding='utf-8') as f:
        lines = f.read().split('\n')
    for line in lines:
        if line.startswith('--- name:'):
            if cur is not None:
                cases.append(cur)
            cur = {'name': line.split(':', 1)[1].strip(), '_file': path}
            block_key = None
            continue
        if cur is None:
            continue
        if block_key:
            # 块字段进行中：顶格且形如 field: 才结束块
            m = re.match(r'^([a-z_]+):', line)
            if m and m.group(1) in FIELDS:
                block_key = None
                # 落到下面统一处理
            else:
                cur[block_key] += ('\n' + line) if cur[block_key] else line
                continue
        stripped = line.strip()
        if not stripped or stripped.startswith('#'):
            continue
        m = re.match(r'^([a-z_]+):\s*(.*)$', line) or re.match(r'^([a-z_]+):\s*(.*)$', stripped)
        if not m:
            continue
        key, rest = m.group(1), m.group(2)
        if key not in FIELDS:
            continue
        if key in BLOCK:
            cur[key] = rest if rest not in ('', '|') else ''
            if rest in ('', '|'):
                block_key = key
        else:
            cur[key] = rest.strip()
    if cur is not None:
        cases.append(cur)
    for c in cases:
        for k in BLOCK:
            if k in c:
                c[k] = dedent_block(c[k]).strip('\n')
    return cases


def dedent_block(text):
    """剥掉块字段的公共前置缩进，使 heredoc 终止符能回到第 0 列。

    只剥公共前缀，保留相对缩进（用例里刻意写的缩进语义不丢）。
    """
    lines = text.split('\n')
    indents = [len(l) - len(l.lstrip(' ')) for l in lines if l.strip()]
    if not indents:
        return text
    common = min(indents)
    if common == 0:
        return text
    return '\n'.join(l[common:] if len(l) >= common else '' for l in lines)

def load_cases(files=None):
    paths = files or sorted(glob.glob(os.path.join(CASE_DIR, '*.case')))
    cases = []
    for p in paths:
        cases.extend(parse_case_file(p))
    out = []
    for c in cases:
        c.setdefault('setup', '')
        c.setdefault('command', '')
        c.setdefault('stdin', '')
        c.setdefault('cwd', '.')
        c.setdefault('timeout', '10')
        c.setdefault('env', '{}')
        try:
            c['env'] = json.loads(c['env']) if isinstance(c['env'], str) else c['env']
        except Exception:
            c['env'] = {}
        try:
            c['files'] = json.loads(c['files']) if isinstance(c.get('files'), str) else c.get('files', {})
        except Exception:
            c['files'] = {}
        c['skip_golden'] = str(c.get('skip_golden', '')).lower() in ('true', '1', 'yes')
        if 'expect_exit' in c and c['expect_exit'] != '':
            c['expect_exit'] = int(c['expect_exit'])
        else:
            c.pop('expect_exit', None)
        out.append(c)
    return out

def prepare_sandbox(case, root):
    """建一个干净沙箱：files 声明 + setup 脚本。"""
    os.makedirs(root, exist_ok=True)
    for rel, content in (case.get('files') or {}).items():
        p = os.path.join(root, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True) if os.path.dirname(p) else None
        mode = 'wb' if isinstance(content, str) and content.startswith('b64:') else 'w'
        if mode == 'wb':
            import base64
            with open(p, 'wb') as f:
                f.write(base64.b64decode(content[4:]))
        else:
            with open(p, 'w', encoding='utf-8') as f:
                f.write(content)
    if case.get('setup'):
        home = os.path.join(root, 'home')
        os.makedirs(home, exist_ok=True)
        env = dict(os.environ)
        env.update({'HOME': home, 'TMPDIR': root, 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8'})
        subprocess.run(['bash', '-c', case['setup']], cwd=root, env=env, timeout=30,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    return os.path.join(root, case['cwd'])

def invoke(target, target_cmd, command, cwd, env, stdin, timeout):
    if target_cmd:
        argv = shlex.split(target_cmd.replace('{cmd}', '@@CMD@@'))
        if '@@CMD@@' in argv:
            argv[argv.index('@@CMD@@')] = command
        else:
            argv.append(command)
    else:
        argv = [target, '-c', command]
    try:
        p = subprocess.run(argv, cwd=cwd, env=env, input=stdin.encode(),
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
        return {'stdout': p.stdout.decode('utf-8', 'replace'),
                'stderr': p.stderr.decode('utf-8', 'replace'),
                'exit': p.returncode, 'timeout': False}
    except subprocess.TimeoutExpired:
        return {'stdout': '', 'stderr': '', 'exit': None, 'timeout': True}
    except FileNotFoundError as e:
        return {'stdout': '', 'stderr': 'TARGET_NOT_FOUND: %s' % e, 'exit': None, 'timeout': False}

def run_case(case, target, target_cmd, base_env):
    tmp = tempfile.mkdtemp(prefix='vbash-')
    try:
        cwd = prepare_sandbox(case, tmp)
        env = dict(base_env)
        env.update({k: str(v) for k, v in (case.get('env') or {}).items()})
        env['HOME'] = os.path.join(tmp, 'home')
        os.makedirs(env['HOME'], exist_ok=True)
        # 记录沙箱文件清单（用于比对副作用）
        before = snapshot(tmp)
        res = invoke(target, target_cmd, case['command'], cwd, env, case['stdin'], float(case['timeout']))
        res['files'] = sorted(set(snapshot(tmp)) - set(before))
        return res
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

def snapshot(root):
    out = []
    for dirpath, dirnames, filenames in os.walk(root):
        for f in filenames:
            out.append(os.path.relpath(os.path.join(dirpath, f), root))
    return out

def golden_path(name):
    safe = name.replace('/', '__')
    return os.path.join(GOLDEN_DIR, safe + '.json')

def record(cases, only=None):
    env = {'PATH': '/usr/local/bin:/usr/bin:/bin', 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8', 'TERM': 'dumb'}
    n = 0
    for c in cases:
        if only and only not in c['name']:
            continue
        res = run_case(c, '/bin/bash', None, env)
        if 'delimited by end-of-file' in res.get('stderr', '') and 'unterminated' not in c['name']:
            print('WARN %s: heredoc 未闭合（用例格式问题，终止符必须顶格）' % c['name'])
        gp = golden_path(c['name'])
        os.makedirs(GOLDEN_DIR, exist_ok=True)
        with open(gp, 'w', encoding='utf-8') as f:
            json.dump(res, f, ensure_ascii=False, indent=1, sort_keys=True)
        n += 1
    print('recorded %d golden' % n)

def diff_text(a, b, label):
    d = list(difflib.unified_diff(a.splitlines(True), b.splitlines(True),
                                  fromfile='golden:' + label, tofile='actual:' + label, n=2))
    return ''.join(d[:40])

def compare(case, res, gold, no_stderr):
    fails = []
    if res.get('timeout'):
        fails.append('TIMEOUT')
        return fails
    exp_exit = case.get('expect_exit', gold.get('exit'))
    if exp_exit is not None and res.get('exit') != exp_exit:
        fails.append('exit: want %r got %r' % (exp_exit, res.get('exit')))
    if not case['skip_golden']:
        if res['stdout'] != gold['stdout']:
            fails.append('stdout differs\n' + diff_text(gold['stdout'], res['stdout'], 'stdout'))
        if not no_stderr and res['stderr'] != gold['stderr']:
            fails.append('stderr differs\n' + diff_text(gold['stderr'], res['stderr'], 'stderr'))
    if 'files' in gold and case.get('check_files'):
        if res['files'] != gold['files']:
            fails.append('side-effect files: want %r got %r' % (gold['files'], res['files']))
    return fails

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--record', action='store_true')
    ap.add_argument('--target', help='被测 bash 可执行文件（以 TARGET -c CMD 调用）')
    ap.add_argument('--target-cmd', help='自定义调用模板，{cmd} 占位，如 "mybash run -c {cmd}"')
    ap.add_argument('--filter', help='只跑名字含该子串的用例')
    ap.add_argument('--files', nargs='*', help='只用指定 .case 文件')
    ap.add_argument('--verbose', '-v', action='store_true')
    ap.add_argument('--json', help='把结果写成 JSON')
    ap.add_argument('--no-stderr', action='store_true')
    ap.add_argument('--max-fail-print', type=int, default=10)
    ap.add_argument('--min-rates', help='各层级门槛，如 "T0=100,T1=100,T3=90"，未达标则退出 1')
    args = ap.parse_args()

    cases = load_cases(args.files)
    if args.filter:
        cases = [c for c in cases if args.filter in c['name']]
    if not cases:
        print('no cases'); return 1

    if args.record:
        record(cases, args.filter)
        return 0

    if not args.target and not args.target_cmd:
        print('need --target or --target-cmd (or --record)'); return 1
    target = args.target or 'bash'
    # 相对路径的 target 必须绝对化，否则沙箱切换 cwd 后找不到
    if '/' in target or os.path.sep in target:
        target = os.path.abspath(target)
    env = {'PATH': '/usr/local/bin:/usr/bin:/bin', 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8', 'TERM': 'dumb'}
    env['PATH'] = os.path.dirname(target) + ':' + env['PATH'] if os.path.dirname(target) else env['PATH']

    results, stats = [], {}
    printed = 0
    for c in cases:
        gp = golden_path(c['name'])
        if not os.path.exists(gp):
            results.append({'name': c['name'], 'status': 'NO_GOLDEN'})
            stats.setdefault(c['name'].split('/')[0], [0, 0, 0])[2] += 1
            continue
        gold = json.load(open(gp, encoding='utf-8'))
        res = run_case(c, target, args.target_cmd, env)
        fails = compare(c, res, gold, args.no_stderr)
        tier = c['name'].split('/')[0]
        st = stats.setdefault(tier, [0, 0, 0])
        st[1] += 1
        if fails:
            results.append({'name': c['name'], 'status': 'FAIL', 'detail': fails})
            if args.verbose and printed < args.max_fail_print:
                printed += 1
                print('FAIL %s' % c['name'])
                print('  cmd: %s' % c['command'].replace('\n', '\\n')[:160])
                for f in fails:
                    print('  ' + f.replace('\n', '\n  '))
        else:
            st[0] += 1
            results.append({'name': c['name'], 'status': 'PASS'})

    print('\n%-6s %6s %6s %6s %7s' % ('TIER', 'PASS', 'FAIL', 'NOGOLD', 'RATE'))
    tp = tf = 0
    for tier in sorted(stats):
        p, t, e = stats[tier]
        tp += p; tf += t
        fail = t - p - e
        print('%-6s %6d %6d %6d %6.1f%%' % (tier, p, fail, e, (p / t * 100) if t else 0))
    print('%-6s %6d %6d' % ('TOTAL', tp, tf))
    if args.json:
        json.dump({'results': results, 'stats': stats}, open(args.json, 'w'), ensure_ascii=False, indent=1)

    bad = [] if tp == tf else ['存在失败用例']
    if args.min_rates:
        for item in args.min_rates.split(','):
            kv = [x for x in re.split(r'[=:]', item.strip()) if x]
            if len(kv) != 2:
                continue
            tier, need = kv[0], float(kv[1])
            p, t, _ = stats.get(tier, [0, 0, 0])
            rate = (p / t * 100) if t else 0.0
            if rate < need:
                bad.append('%s 通过率 %.1f%% < %g%%' % (tier, rate, need))
        if bad and '存在失败用例' not in bad:
            for b in bad:
                print('GATE ' + b)
    return 1 if bad else 0

if __name__ == '__main__':
    sys.exit(main())
