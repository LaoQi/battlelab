package shell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/LaoQi/battlelab/vbash"
)

// 会话宿主：一次 run_shell 启动一个宿主进程，由它承载**所有**外部命令。
//
// 为什么需要独立进程：一个终端（真实或虚拟）同一时刻只能属于一个会话。
// 解释器跑在 tanya 进程内，不能让 tanya 自己 setsid（会脱离真实终端），
// 也不能让每条命令各自 setsid 去抢同一个终端（管道各段并发存活时会互相
// 抢而 EPERM，历史上 `echo x | cat | cat` 就报过 Permission denied）。
// 于是把「会话首进程」独立出来：会话只建立一次，各命令只建自己的进程组。
//
// 进程间只走宿主的 stdin/stdout（见 transport.go），不依赖 socketpair、
// ExtraFiles 或 /proc/<ppid>/fd —— 那三样在 Windows 上都不存在。
//
// 平台差异全部收在 hostPlatform（sessionhost_posix.go / sessionhost_windows.go）：
// POSIX 分配虚拟 pty 并让宿主成为其会话首进程与前台组；Windows 没有这套概念，
// 退化为「进程组 + taskkill 树杀」。

const (
	// hostEnvVar 是宿主的自举开关：被设置时进程不跑业务，直接进入宿主循环。
	hostEnvVar = "BATTLELAB_SHELL_SESSION_HOST"
	// hostReadyByte 是宿主进入循环前回给父进程的就绪字节。
	hostReadyByte byte = 0x5A
	// hostReadyTimeout 是等待就绪握手的时长：宿主没接自举就快速失败。
	hostReadyTimeout = 2 * time.Second
	// hostOutChunk 是输出切块大小：宿主按块发送，父进程边收边写，避免大输出攒在内存。
	hostOutChunk = 4096
)

// IsSessionHost 报告当前进程是否应作为会话宿主运行（由父进程自举时置位）。
func IsSessionHost() bool { return os.Getenv(hostEnvVar) == "1" }

// RunSessionHostIfRequested 在进程入口调用：若被父进程自举为会话宿主，
// 则进入宿主循环并**不返回**（结束时直接 os.Exit）。返回即表示不是宿主。
func RunSessionHostIfRequested() {
	if !IsSessionHost() {
		return
	}
	os.Exit(runSessionHost())
}

// hostJob 是宿主侧一条在跑的命令。
type hostJob struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	pgid int
	in   *os.File // 该 job 的 stdin 写端（父进程经 frameIn 灌数据）
	// pending 缓存 j.in 就绪前到达的 stdin 数据：内建命令（echo/printf）作为
	// 管道首段时，数据在 exec 尚未建好管道前就已发出，丢了就永远等不到 EOF。
	pending [][]byte
	eof     bool
	done    chan struct{}
}

// feedStdin 把数据交给该 job 的 stdin；写端尚未就绪时先缓存。
func (j *hostJob) feedStdin(p []byte) {
	j.mu.Lock()
	if j.in == nil {
		if len(p) == 0 {
			j.eof = true
		} else {
			j.pending = append(j.pending, append([]byte(nil), p...))
		}
		j.mu.Unlock()
		return
	}
	w := j.in
	if len(p) == 0 {
		j.in = nil
		j.mu.Unlock()
		_ = w.Close()
		return
	}
	j.mu.Unlock()
	_, _ = w.Write(p)
}

// attachStdin 安装 stdin 写端，并冲掉此前缓存的数据。
func (j *hostJob) attachStdin(w *os.File) {
	j.mu.Lock()
	j.in = w
	bufs := j.pending
	j.pending = nil
	eof := j.eof
	j.mu.Unlock()
	for _, b := range bufs {
		_, _ = w.Write(b)
	}
	if eof {
		j.mu.Lock()
		if j.in == w {
			j.in = nil
		}
		j.mu.Unlock()
		_ = w.Close()
	}
}

// sessionHost 是父进程侧对宿主的句柄。
type sessionHost struct {
	reqW *frameWriter // 写宿主 stdin
	res  *frameWriter // 备用（宿主侧用）
	mu   sync.Mutex
	jobs map[uint32]*parentJob
	next uint32
	dead bool

	respR  io.ReadCloser
	waiter chan error
	// onTTY 处理虚拟 tty 上的字节（命令直接写 /dev/tty 的内容）。
	onTTY func(p []byte)
	// 内建 read 的输入缓冲：虚拟 tty 的回显按行切分后存这里。
	lineMu   sync.Mutex
	lineRaw  []byte
	lineBuf  []string
	lineWait chan struct{}
}

// parentJob 是父进程侧一条在跑的命令。
type parentJob struct {
	pgid chan int
	exit chan int
	job  uint32
	out  io.Writer
	err  io.Writer
}

// startSessionHost 启动会话宿主并返回句柄。
func startSessionHost() (*sessionHost, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// 协议的两条流：父进程写 → 宿主 stdin；宿主 stdout → 父进程读。
	reqR, reqW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	resR, resW, err := os.Pipe()
	if err != nil {
		reqR.Close()
		reqW.Close()
		return nil, err
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), hostEnvVar+"=1")
	cmd.Stdin = reqR
	cmd.Stdout = resW
	cmd.Stderr = nil
	// 宿主自己不接管终端：它内部再为命令分配（POSIX 虚拟 pty / Windows 进程组）。
	if err := cmd.Start(); err != nil {
		reqR.Close()
		reqW.Close()
		resR.Close()
		resW.Close()
		return nil, err
	}
	reqR.Close()
	resW.Close()

	h := &sessionHost{
		reqW:     newFrameWriter(reqW),
		respR:    resR,
		jobs:     make(map[uint32]*parentJob),
		waiter:   make(chan error, 1),
		lineWait: make(chan struct{}),
	}
	// 就绪握手：宿主若没接自举，会在这里超时失败而不是挂死。
	_ = resR.SetReadDeadline(time.Now().Add(hostReadyTimeout))
	var ready [1]byte
	if _, err := io.ReadFull(resR, ready[:]); err != nil || ready[0] != hostReadyByte {
		reqW.Close()
		resR.Close()
		_ = cmd.Process.Kill()
		return nil, errHostUnavailable
	}
	_ = resR.SetReadDeadline(time.Time{})

	go func() {
		h.waiter <- cmd.Wait()
		close(h.waiter)
	}()
	go h.readLoop()
	return h, nil
}

// readLoop 是父进程侧的响应分发循环：按 jobID 把输出与退出码投给对应 job。
func (h *sessionHost) readLoop() {
	for {
		f, err := readFrame(h.respR)
		if err != nil {
			h.markDead()
			return
		}
		h.mu.Lock()
		j := h.jobs[f.job]
		h.mu.Unlock()
		switch f.typ {
		case frameStart:
			pgid, _ := getU32(f.payload)
			if j != nil {
				select {
				case j.pgid <- int(pgid):
				default:
				}
			}
		case frameData:
			if len(f.payload) > 0 && j != nil {
				fd := int(f.payload[0])
				w := j.out
				if fd == 2 {
					w = j.err
				}
				if w != nil {
					_, _ = w.Write(f.payload[1:])
				}
			}
		case frameExit:
			code, _ := getU32(f.payload)
			if j != nil {
				select {
				case j.exit <- int(code):
				default:
				}
			}
		case frameTTY:
			if len(f.payload) > 0 {
				h.feedTTY(f.payload)
				if h.onTTY != nil {
					h.onTTY(f.payload)
				}
			}

		}
	}
}

func (h *sessionHost) markDead() {
	h.mu.Lock()
	h.dead = true
	h.mu.Unlock()
}

// run 让宿主执行一条命令；ctx 取消时向该命令的进程组投 SIGKILL。
func (h *sessionHost) run(ctx context.Context, req reqPayload, in *vbash.IO) (int, error) {
	if h == nil {
		return 1, errHostUnavailable
	}
	h.mu.Lock()
	if h.dead {
		h.mu.Unlock()
		return 1, errHostUnavailable
	}
	h.next++
	id := h.next
	j := &parentJob{pgid: make(chan int, 1), exit: make(chan int, 1), job: id, out: in.Out, err: in.Err}
	h.jobs[id] = j
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.jobs, id)
		h.mu.Unlock()
	}()

	if err := h.reqW.write(frameReq, id, encodeReq(req)); err != nil {
		h.markDead()
		return 1, err
	}
	// stdin 搬运：解释器给的 In 可能是管道读端、重定向文件或空读取器，
	// 一律由父进程读出来发 frameIn，宿主再写进命令的 stdin 管道。
	stdinDone := make(chan struct{})
	go func() {
		defer close(stdinDone)
		h.pumpStdin(id, in.In)
	}()
	var pgid int
	select {
	case pgid = <-j.pgid:
	case <-time.After(hostReadyTimeout):
		return 1, errHostUnavailable
	}
	if pgid <= 0 {
		return 1, errHostUnavailable
	}
	if ctx != nil && ctx.Done() != nil {
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			select {
			case <-ctx.Done():
				h.signal(pgid, sigKillNum)
			case <-stop:
			}
		}()
	}
	code := <-j.exit
	// 不等 pumpStdin：它的来源可能是虚拟 tty（hostTTYReader 阻塞等用户输入），
	// 命令已退出时没有理由再等它。它会在下一次读失败或进程结束时自行退出。
	_ = stdinDone
	return code, nil
}

// pumpStdin 把解释器侧的 stdin 搬进宿主；读完后发一个空 frameIn 表示 EOF。
func (h *sessionHost) pumpStdin(id uint32, r io.Reader) {
	if r == nil {
		_ = h.reqW.write(frameIn, id, nil)
		return
	}
	buf := make([]byte, hostOutChunk)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if werr := h.reqW.write(frameIn, id, buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			break
		}
	}
	_ = h.reqW.write(frameIn, id, nil)
}

// readLine 取一行用户输入（内建 read 用）。
//
// 输入从虚拟 tty 的回显流里取：终端在 canonical 模式下会把用户敲的行回显出来，
// 父进程已在 onTTY 收到这些字节，这里按行切分即可。不另开一条读通道，
// 因为宿主的 master 已被输出泵独占、slave 读不到按键。
func (h *sessionHost) readLine(ctx context.Context) (string, bool) {
	if h == nil {
		return "", false
	}
	for {
		h.lineMu.Lock()
		if len(h.lineBuf) > 0 {
			line := h.lineBuf[0]
			h.lineBuf = h.lineBuf[1:]
			h.lineMu.Unlock()
			return line, true
		}
		// 在同一把锁内取等待通道，避免「检查为空 → 数据到达 → 才开始等」的丢失唤醒。
		wait := h.lineWait
		h.lineMu.Unlock()
		select {
		case <-wait:
			// 有新数据到达，回到循环顶部重新检查。
		case <-ctx.Done():
			return "", false
		}
	}
}

// feedTTY 把虚拟 tty 的输出字节按行切分，供内建 read 取用。
func (h *sessionHost) feedTTY(p []byte) {
	h.lineMu.Lock()
	defer h.lineMu.Unlock()
	h.lineRaw = append(h.lineRaw, p...)
	for {
		i := bytes.IndexByte(h.lineRaw, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSuffix(string(h.lineRaw[:i]), "\r")
		h.lineBuf = append(h.lineBuf, line)
		h.lineRaw = h.lineRaw[i+1:]
	}
	// 有新字节就唤醒等待者：它可能正等首行，也可能只是要观察一次进度。
	if h.lineWait != nil {
		close(h.lineWait)
	}
	h.lineWait = make(chan struct{})
}

// writeTTY 把用户按键转发进宿主的虚拟 tty（命令的 /dev/tty 因此可应答）。
func (h *sessionHost) writeTTY(p []byte) error {
	if h == nil || len(p) == 0 {
		return nil
	}
	return h.reqW.write(frameTTYIn, 0, p)
}

// writeStdin 把数据喂给某 job 的 stdin；空 payload 表示关闭（EOF）。
func (h *sessionHost) writeStdin(id uint32, p []byte) error {
	return h.reqW.write(frameIn, id, p)
}

// signal 向某命令的进程组投信号。
func (h *sessionHost) signal(pgid, sig int) {
	_ = hostSignalGroup(pgid, sig)
}

func (h *sessionHost) close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	dead := h.dead
	h.dead = true
	h.mu.Unlock()
	if !dead {
		if f, ok := h.reqW.w.(io.Closer); ok {
			_ = f.Close()
		}
	}
	_ = h.respR.Close()
}

// ---- 宿主侧 ----

// runSessionHost 是宿主主循环：建立平台会话，然后并发执行父进程发来的请求。
func runSessionHost() int {
	in := newFrameReader(os.Stdin)
	out := newFrameWriter(os.Stdout)
	hp, err := hostPlatformSetup(out)
	if err != nil {
		return 90
	}
	defer hp.close()
	// 就绪握手：父进程据此判断「当前可执行文件确实支持宿主模式」。
	if _, err := os.Stdout.Write([]byte{hostReadyByte}); err != nil {
		return 93
	}

	var wg sync.WaitGroup
	jobs := make(map[uint32]*hostJob)
	var jobsMu sync.Mutex

	for {
		f, err := in.next()
		if err != nil {
			break
		}
		switch f.typ {
		case frameReq:
			req, ok := decodeReq(f.payload)
			if !ok {
				continue
			}
			j := &hostJob{done: make(chan struct{})}
			jobsMu.Lock()
			jobs[f.job] = j
			jobsMu.Unlock()
			wg.Add(1)
			go func(id uint32, req reqPayload, j *hostJob) {
				defer wg.Done()
				code := hp.exec(id, req, j, out)
				_ = out.write(frameExit, id, putU32(uint32(code)))
				jobsMu.Lock()
				delete(jobs, id)
				jobsMu.Unlock()
				close(j.done)
			}(f.job, req, j)
		case frameIn:
			jobsMu.Lock()
			j := jobs[f.job]
			jobsMu.Unlock()
			if j == nil {
				continue
			}
			j.feedStdin(f.payload)
		case frameTTYIn:
			hp.writeTTY(f.payload)

		case frameSig:
			sig, _ := getU32(f.payload)
			jobsMu.Lock()
			j := jobs[f.job]
			jobsMu.Unlock()
			if j == nil {
				continue
			}
			j.mu.Lock()
			pgid := j.pgid
			j.mu.Unlock()
			if pgid > 0 {
				_ = hostSignalGroup(pgid, int(sig))
			}
		}
	}
	wg.Wait()
	return 0
}

// frameReader 是宿主侧的读帧器（单 goroutine 顺序读）。
type frameReader struct{ r io.Reader }

func newFrameReader(r io.Reader) *frameReader { return &frameReader{r: r} }

func (fr *frameReader) next() (frame, error) { return readFrame(fr.r) }

// hostUnavailableMsg 用于诊断。
func hostUnavailableMsg() string { return fmt.Sprintf("%v", errHostUnavailable) }
