package shell

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

// 宿主协议：父进程与宿主进程之间只走宿主的 stdin/stdout 两条标准流，
// 不用 socketpair、不用 ExtraFiles、不用 /proc/<ppid>/fd —— 这三者在
// Windows 上都不存在（os/exec 明写 ExtraFiles is not supported on Windows），
// 而 vbash 要求跨平台一致，故协议必须建立在「所有平台都有的东西」之上。
//
// 并发命令的帧在同一条流上交织，靠 jobID 多路复用；每帧自带类型与长度，
// 因此顺序无关、可交错。
//
// 帧格式（9 字节头 + payload）：
//
//	type(1) | jobID(4, 大端) | payloadLen(4, 大端) | payload
const frameHeaderSize = 9

const (
	// 父进程 → 宿主
	frameReq   byte = 1 // 请求执行一条命令：argv/cwd/env/前台标记
	frameIn    byte = 2 // 喂给某 job 的 stdin 数据；payload 为空表示 EOF（关闭该 job 的 stdin）
	frameSig   byte = 3 // 向某 job 的进程组投信号：payload 为 4 字节信号号
	frameTTYIn byte = 7 // 写进虚拟 tty 的数据（用户按键转发）

	// 宿主 → 父进程
	frameStart byte = 4 // 命令已启动：payload 为 4 字节 pgid（启动失败为 0）
	frameData  byte = 5 // 输出数据：payload 首字节为 fd 号（1=stdout、2=stderr），其后是数据
	frameExit  byte = 6 // 命令已退出：payload 为 4 字节退出码
	frameTTY   byte = 8 // 虚拟 tty 上的输出（命令直接写 /dev/tty 的内容）
)

// errHostProtocol 表示帧格式非法（长度溢出、类型未知等）。
var errHostProtocol = errors.New("shell: 会话宿主协议错误")

type frame struct {
	typ     byte
	job     uint32
	payload []byte
}

// frameWriter 串行化写帧：并发命令的输出会同时到达，必须保证每帧原子写入。
type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newFrameWriter(w io.Writer) *frameWriter { return &frameWriter{w: w} }

func (fw *frameWriter) write(typ byte, job uint32, payload []byte) error {
	var hdr [frameHeaderSize]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:5], job)
	binary.BigEndian.PutUint32(hdr[5:9], uint32(len(payload)))
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if _, err := fw.w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := fw.w.Write(payload)
	return err
}

// readFrame 从流中读出一帧；流结束返回 io.EOF。
func readFrame(r io.Reader) (frame, error) {
	var hdr [frameHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return frame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[5:9])
	if n > maxFramePayload {
		return frame{}, errHostProtocol
	}
	var payload []byte
	if n > 0 {
		payload = make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			return frame{}, err
		}
	}
	return frame{typ: hdr[0], job: binary.BigEndian.Uint32(hdr[1:5]), payload: payload}, nil
}

// maxFramePayload 是单帧上限：输出按 4 KiB 左右切块发送，超过这个量说明
// 流已错位（帧头被当成数据），立刻失败而不是分配巨量内存。
const maxFramePayload = 1 << 20

// ---- payload 编解码 ----

func putU32(v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return b[:]
}

func getU32(p []byte) (uint32, bool) {
	if len(p) < 4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(p[:4]), true
}

// encodeStrings 把字符串列表编成「数量 + 逐条长度前缀」的 payload。
func encodeStrings(parts []string) []byte {
	size := 4
	for _, p := range parts {
		size += 4 + len(p)
	}
	out := make([]byte, 0, size)
	out = append(out, putU32(uint32(len(parts)))...)
	for _, p := range parts {
		out = append(out, putU32(uint32(len(p)))...)
		out = append(out, p...)
	}
	return out
}

// decodeStrings 还原 encodeStrings 的 payload。
func decodeStrings(p []byte) ([]string, bool) {
	n, ok := getU32(p)
	if !ok {
		return nil, false
	}
	p = p[4:]
	out := make([]string, 0, n)
	for i := uint32(0); i < n; i++ {
		sz, ok := getU32(p)
		if !ok {
			return nil, false
		}
		p = p[4:]
		if uint32(len(p)) < sz {
			return nil, false
		}
		out = append(out, string(p[:sz]))
		p = p[sz:]
	}
	return out, true
}

// reqPayload 是一条命令请求的完整内容。
type reqPayload struct {
	argv  []string
	cwd   string
	env   []string
	front bool // 是否把该命令的进程组设为终端前台组
	// stdinTTY 表示该命令的 stdin 未被重定向：宿主把它接到虚拟 tty，
	// 与真实 shell 里「命令的 stdin 就是终端」一致。
	stdinTTY bool
}

// encodeReq 编码请求帧 payload：argv 列表 + cwd + env 列表 + 前台标记。
func encodeReq(r reqPayload) []byte {
	out := encodeStrings(r.argv)
	out = append(out, encodeStrings([]string{r.cwd})...)
	out = append(out, encodeStrings(r.env)...)
	var flags byte
	if r.front {
		flags |= 1
	}
	if r.stdinTTY {
		flags |= 2
	}
	out = append(out, flags)
	return out
}

// decodeReq 还原请求帧 payload。
func decodeReq(p []byte) (reqPayload, bool) {
	argv, ok := decodeStrings(p)
	if !ok {
		return reqPayload{}, false
	}
	rest := p[len(encodeStrings(argv)):]
	cwdList, ok := decodeStrings(rest)
	if !ok {
		return reqPayload{}, false
	}
	rest = rest[len(encodeStrings(cwdList)):]
	env, ok := decodeStrings(rest)
	if !ok {
		return reqPayload{}, false
	}
	rest = rest[len(encodeStrings(env)):]
	if len(rest) < 1 {
		return reqPayload{}, false
	}
	cwd := ""
	if len(cwdList) > 0 {
		cwd = cwdList[0]
	}
	return reqPayload{argv: argv, cwd: cwd, env: env, front: rest[0]&1 != 0, stdinTTY: rest[0]&2 != 0}, true
}
