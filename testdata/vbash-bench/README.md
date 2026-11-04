# vbash-bench

虚拟 bash 的语法/语义回归基准。193 条用例，golden 由真 bash 5.3 录制。

**完整说明见 [`docs/vbash-bench.md`](../../docs/vbash-bench.md)，依据与语料分析见 [`docs/vbash.md`](../../docs/vbash.md)。**

```bash
./run.py --target /bin/bash          # 自检，应 193/193
./run.py --record                    # 重新生成 golden
./run.py --target ./mybash -v        # 测被测实现
./run.py --target ./mybash --min-rates "T0=100,T1=100,T2=95,T3h=90,C=100,X=100"
```

或从仓库根：`make vbash-check`。

依赖：`bash` + `python3`。不参与 `go test`。
