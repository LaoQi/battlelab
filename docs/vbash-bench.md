# vbash-bench：虚拟 bash 回归基准

> 归属（2026-11-04）：本文与 `testdata/vbash-bench/` 配套，依据见 `vbash.md`。
> 基准只依赖 `bash` 与 `python3`，**不参与 `go test`**，离线手动运行。

## 1 定位

给"虚拟 bash"这一演进方向一个**可量化的验收标准**，避免"感觉能跑"就上线：

- 用例取自真实会话语料的高频形态，不是凭空设计
- golden 由真 bash 录制，不是手写期望
- 按难度分层，可用 `--min-rates` 设门槛卡 CI
- 自带反面参照实现，验证基准本身的区分能力

## 2 目录

```
testdata/vbash-bench/
├── cases/            用例定义（*.case，人类可读）
│   ├── T0-simple.case        20 条  单命令+参数
│   ├── T1-pipeline.case      42 条  管道/串联/重定向/退出码/cd/exec-fd
│   ├── T2-expansion.case     53 条  变量/命令替换/参数展开/分词/大括号/printf 格式
│   ├── T3-heredoc.case       20 条  heredoc（嵌套/多终止符/python 内联）→ 层级 T3h
│   ├── T3-controlflow.case   28 条  for/if/while/case/函数/分组 → 层级 T3c
│   ├── C-corpus.case         30 条  真实语料高频形态（脱敏改写）
│   └── X-edge.case           30 条  边界鲁棒性（不崩不挂）
├── golden/           基准期望（228 个 json，由 run.py --record 生成，勿手改）
├── run.py            运行器：录制 / 回放 / 比对 / 门槛判定
└── tools/
    ├── gen_corpus_cases.py   从会话 jsonl 抽取高频形态并脱敏
    ├── reference-naive.py    反面参照（故意错误的实现）
    └── make_report.py        JSON 结果转 Markdown 报告
```

## 3 用例格式

一个 `.case` 文件是若干用例的集合，用 `--- name: <id>` 分隔，块字段（`setup`/`command`）
取公共缩进前缀，其余字段单行：

```
--- name: T3/003-heredoc-write-file-then-read
# 语料最高频形态：写文件后接一个校验命令
command:
  cat > out.md <<'EOF'
  # title
  body line
  EOF
  cat out.md
files: {}
```

| 字段 | 说明 |
|---|---|
| `name` | `层级/编号-短名`，层级决定汇总分组 |
| `setup` | 执行前用真 bash 跑的准备工作（建文件等），不参与比对 |
| `command` | 被测命令原文，等价 `bash -c "$command"` |
| `stdin` | 标准输入，缺省空 |
| `files` | 声明式建文件 `{路径: 内容}` |
| `env` | 附加环境变量 |
| `cwd` | 相对沙箱根的工作目录 |
| `expect_exit` | 期望退出码；缺省取 golden 的真实退出码 |
| `skip_golden` | true 时只比退出码（慎用，见 §5） |
| `timeout` | 秒，缺省 10 |

## 4 运行

```bash
cd testdata/vbash-bench

./run.py --record                       # 用真 bash 重新生成 golden
./run.py --target /bin/bash             # 自检：应 228/228
./run.py --target ../../vbash           # 测被测实现
./run.py --target ./mybash --filter T3  # 只跑 heredoc/控制流
./run.py --target ./mybash --verbose    # 打印失败详情与 diff
./run.py --target ./mybash \
    --min-rates "T0=100,T1=100,T2=95,T3h=90,C=100,X=100"  # CI 门槛
./run.py --target ./mybash --json out.json
```

调用约定是 `TARGET -c "$command"`（与 bash 一致）。若被测实现入口不同，用
`--target-cmd 'vbash run -c {cmd}'`。

## 5 设计要点

**沙箱隔离**：每条用例独立临时目录，`HOME`/`TMPDIR` 均指向沙箱内，结束即销毁。用例不依赖
本机状态，也不污染本机。副作用（新建文件）可被记录比对。

**`setup` 同样在沙箱内跑**：`setup` 块以 `cwd` = 沙箱根、`HOME`/`TMPDIR` = 沙箱内执行，
不继承调用者的家目录。写 `setup` 时仍应只用相对路径——越界的绝对路径不会被子目录隔离挡住。

**golden 确定性**：已验证重复录制 diff 为空。含绝对路径或时间戳的输出必须在用例里
用 `sed` 归一化后再输出（例如 `pwd | sed 's#.*/d/#d/#'`），**不要用 `skip_golden` 绕过**。

**比对口径**：默认比 stdout / stderr / 退出码三项。stderr 文案各家实现不同，
可用 `--no-stderr` 关掉；X 层用例本身只比 stdout + 退出码。

**反面参照**：`tools/reference-naive.py` 是故意写错的实现（盲切 `;|&`、忽略引号与 heredoc）。
它的分数是基准的健康指标，见 §6。

## 6 基线与门槛

| 被测 | 结果 |
|---|---|
| 真 bash 5.3 | **228/228（100%）** |
| `reference-naive.py` | 见 run.py `--target ./tools/reference-naive.py` 实跑 |

分层建议门槛（对应 `vbash.md` §7 的实施顺序）：

| 层级 | 语料占比 | 门槛 |
|---|---|---|
| T0 | 7.8% | 100% |
| T1 | 59.7% | 100% |
| T2 | 2.6% | 95% |
| T3h（heredoc） | 26.3% | 90% |
| T3c（控制流） | <6% | 一阶段不计入必过（建议 0%，逐步提到 80%） |
| C | — | 100% |
| X | — | 100% |

`T3` 曾把 heredoc（20 条）与控制流（25 条）合算一层，门槛 90% 与「控制流一阶段可不做」
矛盾（只做 heredoc 上限 44.4%）。现按用例名前缀拆为 `T3h` / `T3c` 两层分别统计。

`optional: true` 的用例（语料 0 次但基准收录的构造：`[[ ]]`/反引号/`case`）只校验
「不崩、不挂起、退出码合理」，不校验输出——实现可以选择报错而非支持。

## 7 语料抽取与脱敏

`tools/gen_corpus_cases.py` 从会话目录批量抽取：按"命令指纹"（数字/长标识符归一化）
统计频次，取高频且安全的形态，脱敏后改写为可移植用例。

脱敏规则：
- 家目录与用户名 → `d/`、`d/x` 等沙箱内相对路径
- 外部依赖命令（`ssh`/`adb`/`curl -X`/`git`/`go`/`python3`/`systemctl` 等）整条丢弃
- 内网地址与主机名在抽取阶段直接过滤

抽取结果只保留 `cat`/`head`/`tail`/`grep`/`sed`/`ls`/`wc`/`cd` 等可移植命令，
因此 `C-corpus.case` 中不含任何环境相关信息。

## 8 维护

- **改 bash 大版本后**：`./run.py --record` 复核 golden 是否漂移，有差异逐条确认是 bash 行为变化还是用例问题。
- **语料风格变化后**：重跑 `gen_corpus_cases.py` 看高频形态是否迁移，必要时补用例。
- **新增层级**：在 `cases/` 加文件，命名 `T4-xxx.case` 即可自动纳入；需同步 `vbash.md` §7 与本文件 §6 的门槛表。
