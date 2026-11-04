#!/usr/bin/env python3
"""反面参照实现（故意错误）：仅按 && ; || | 切分，不处理引号与 heredoc。

用途：验证 vbash-bench 的区分能力。跑一遍应大量失败；若它也能全员通过，
说明用例太松，需要补强。
"""
import re, subprocess, sys

def main():
    if len(sys.argv) >= 3 and sys.argv[1] == '-c':
        cmd = sys.argv[2]
    elif len(sys.argv) >= 2:
        cmd = sys.argv[1]
    else:
        return 2
    rc = 0
    for seg in [x.strip() for x in re.split(r'&&|\|\||;|\|', cmd) if x.strip()]:
        rc = subprocess.run(seg, shell=True).returncode
    return rc

if __name__ == '__main__':
    sys.exit(main())
