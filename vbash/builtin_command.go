package vbash

import (
	"fmt"
	"os"
	"strings"
)

// command 内建：查询命令（-v / -V）与「跳过函数查找」执行。
//
// 与 bash 对齐的口径（逐条实测 bash 5.3 得出，差异见 docs/vbash-capability.md §3.3）：
//
//   - 查找顺序：关键字 → 函数 → 内建 → 参数含 `/` 直接查该路径 → PATH 逐条目。
//   - `-v` 输出「名字」或「条目/名字」；`-V` 输出一句话说明，路径形态为绝对化但**不 clean**。
//   - 任一名字命中即退出码 0，全 miss 才 1；`-v` miss 静默，`-V` miss 打 stderr。
//   - 无 `-v`/`-V` 时执行该命令并跳过函数查找（同名函数存在时直取内建/外部程序）。
//
// 刻意不复刻 bash 的 hash 表：bash 的 `command -v` 会返回陈旧的缓存路径（程序删掉后仍报旧
// 路径），那是实现细节不是契约，vbash 每次都新鲜查找。

// commandKeywords 是 `-v`/`-V` 应答 "is a shell keyword" 的词表。
// 与解析器用的 reservedWords 刻意分开：这张表的唯一用途是回答查询，`{` 与 `!` 在
// reservedWords 里没有（它们是分组/取反的引导符而非独立关键字），但 bash 对它们同样应答关键字。
// `[[`/`]]` 不在此列——vbash 不支持该构造，保持 127 语义。
var commandKeywords = map[string]bool{
	"if": true, "then": true, "elif": true, "else": true, "fi": true,
	"for": true, "while": true, "until": true, "do": true, "done": true,
	"case": true, "esac": true, "select": true, "function": true,
	"coproc": true, "time": true, "in": true, "{": true, "}": true, "!": true,
}

// commandUsage 是非法选项时打到 stderr 的第二行（bash 不带 shell 名前缀）。
const commandUsage = "command: usage: command [-pVv] command [arg ...]"

// commandOpts 是 `command` 解析后的选项与参数。
type commandOpts struct {
	usePath bool // -p：PATH 查找改用 plat.DefaultPath
	mode    byte // 0 = 执行态；'v' = -v；'V' = -V
	args    []string
}

// commandHit 是一个名字的两种呈现：短形态给 -v，长形态给 -V。
type commandHit struct {
	short   string
	verbose string
}

func builtinCommand(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	opts, code, ok := parseCommandOpts(r, argv[1:], io, pos)
	if !ok {
		return code, nil
	}
	if opts.mode != 0 {
		return r.commandQuery(opts, io, pos), nil
	}
	return r.commandExecute(opts, io, pos)
}

// parseCommandOpts 解析 `[-p] [-v|-V] [--] [name ...]`。
// 选项在遇到第一个非选项参数时终止（`command echo -v` 里 -v 是 echo 的参数）。
func parseCommandOpts(r *Runner, args []string, io *IO, pos Pos) (commandOpts, int, bool) {
	var opts commandOpts
	for len(args) > 0 {
		a := args[0]
		if a == "--" {
			args = args[1:]
			break
		}
		if len(a) < 2 || a[0] != '-' {
			break
		}
		// `--xxx`（长度 > 2 的双横线）在 bash 里报的是 `--` 而非整个参数。
		if strings.HasPrefix(a, "--") {
			r.diagfName(io.Err, pos, "command", "%s: invalid option", "--")
			fmt.Fprintln(io.Err, commandUsage)
			return opts, 2, false
		}
		for i := 1; i < len(a); i++ {
			switch a[i] {
			case 'p':
				opts.usePath = true
			case 'v':
				opts.mode = 'v'
			case 'V':
				opts.mode = 'V'
			default:
				r.diagfName(io.Err, pos, "command", "-%c: invalid option", a[i])
				fmt.Fprintln(io.Err, commandUsage)
				return opts, 2, false
			}
		}
		args = args[1:]
	}
	opts.args = args
	return opts, 0, true
}

// commandQuery 处理 `-v` / `-V`：逐个名字打印命中，任一命中即返回 0。
func (r *Runner) commandQuery(opts commandOpts, io *IO, pos Pos) int {
	if len(opts.args) == 0 {
		return 0 // bash：无名字时 `command -v` / `command -V` 均返回 0、不输出
	}
	code := 1
	for _, name := range opts.args {
		hit, ok := r.commandLookup(name, opts.usePath)
		if !ok {
			if opts.mode == 'V' {
				r.diagfName(io.Err, pos, "command", "%s: not found", name)
			}
			continue
		}
		code = 0
		if opts.mode == 'V' {
			fmt.Fprintln(io.Out, hit.verbose)
		} else {
			fmt.Fprintln(io.Out, hit.short)
		}
	}
	return code
}

// commandLookup 按 bash 的顺序解析一个名字。
func (r *Runner) commandLookup(name string, usePath bool) (commandHit, bool) {
	if commandKeywords[name] {
		return commandHit{short: name, verbose: name + " is a shell keyword"}, true
	}
	if _, ok := r.funcs[name]; ok {
		return commandHit{short: name, verbose: name + " is a function"}, true
	}
	if canBuiltin(name) {
		return commandHit{short: name, verbose: name + " is a shell builtin"}, true
	}
	if isPathLike(name) {
		// 参数自带 `/`：直接查该路径，命中即原样回显（不做绝对化）。
		host := r.hostPath(name)
		st, err := os.Stat(host)
		if err != nil || !plat.IsExec(host, st) {
			return commandHit{}, false
		}
		return commandHit{short: name, verbose: name + " is " + name}, true
	}
	return r.commandPathLookup(name, usePath)
}

// commandPathLookup 是 bash 兼容的 PATH 查找，与 lookPath 的三处关键差异：
//
//  1. 只跳过目录，**非可执行文件也算命中**（先找可执行，全都没有可执行时才回落到
//     第一个非目录文件）；
//  2. 拼出的路径**不 clean**（`sub//`、`sub/./` 原样保留），因为 -v 要回显这个形态；
//  3. 命中的判定口径独立于 `lookPath`：后者对「带 `/` 的参数」走 `hostPath` 且要求
//     `IsExec`，而 `-v` 要看的是「PATH 里有没有这个名字」的 bash 口径。
func (r *Runner) commandPathLookup(name string, usePath bool) (commandHit, bool) {
	pathVar, ok := r.Env.Lookup("PATH")
	if usePath || !ok {
		pathVar = plat.DefaultPath
	}
	suffixes := plat.ExecSuffixes
	if hasAnySuffix(name, suffixes) {
		suffixes = []string{""}
	}
	// fallback 记住第一个「非目录但不可执行」的候选，供全无命中可执行文件时回落。
	var fallbackRel, fallbackAbs string
	for _, dir := range strings.Split(pathVar, ":") {
		entry := dir
		if entry == "" {
			entry = "." // 空条目等价于当前目录
		}
		// 只削掉末尾**一个** `/` 再补 `/`：`sub//` 因此产出 `sub//name`，与 bash 一致。
		base := strings.TrimSuffix(entry, "/")
		for _, suffix := range suffixes {
			rel := base + "/" + name + suffix
			abs := commandAbsPath(r.Env.Dir(), rel)
			host := plat.ToHost(r.Env.Dir(), abs)
			st, err := os.Stat(host)
			if err != nil || st.IsDir() {
				continue
			}
			if plat.IsExec(host, st) {
				return commandHit{short: rel, verbose: name + " is " + abs}, true
			}
			if fallbackRel == "" {
				fallbackRel, fallbackAbs = rel, abs
			}
		}
	}
	if fallbackRel != "" {
		return commandHit{short: fallbackRel, verbose: name + " is " + fallbackAbs}, true
	}
	return commandHit{}, false
}

// commandAbsPath 把 PATH 命中的相对形态绝对化，**不做 clean**（bash 的 -V 保留 `//`、`/.`）。
//
// 唯一的规整是削掉开头的一个 `./`：`PATH=./sub` 报 `/cwd/sub/t1`，而 `PATH=.//sub`
// 削完 `./` 还剩 `/sub`，拼出的 `/cwd//sub/t1` 双斜杠照旧保留（逐条实测 bash 5.3）。
func commandAbsPath(dir, rel string) string {
	if strings.HasPrefix(rel, "/") {
		return rel
	}
	rel = strings.TrimPrefix(rel, "./")
	if dir == "" {
		return "/" + rel
	}
	return strings.TrimSuffix(dir, "/") + "/" + rel
}

// commandExecute 处理无 `-v`/`-V` 的形态：执行命令，跳过函数查找。
func (r *Runner) commandExecute(opts commandOpts, io *IO, pos Pos) (int, error) {
	if len(opts.args) == 0 {
		return 0, nil
	}
	name := opts.args[0]
	if canBuiltin(name) {
		code, handled, err := r.runBuiltin(name, opts.args, io, pos)
		if handled {
			return code, err
		}
	}
	if opts.usePath {
		// -p：只在默认 PATH 里解析，**不回落到自定义 PATH**（子进程的 PATH 仍是原来的）。
		// 解析不到就是 127，与 bash 一致：实测 `PATH=/tmp/pt:$PATH; command -p ptool`
		// 在 ptool 只存在于 /tmp/pt 时报 command not found。
		if full, ok := r.commandResolveDefault(opts.args[0]); ok {
			return r.spawn(full, opts.args, io, pos)
		}
		r.diagf(io.Err, pos, "%s: command not found", opts.args[0])
		return 127, nil
	}
	return r.runExternal(opts.args, io, pos)
}

// commandResolveDefault 用 plat.DefaultPath 解析出宿主路径，供 -p 执行态使用。
func (r *Runner) commandResolveDefault(name string) (string, bool) {
	hit, ok := r.commandPathLookup(name, true)
	if !ok {
		return "", false
	}
	return plat.ToHost(r.Env.Dir(), commandAbsPath(r.Env.Dir(), hit.short)), true
}
