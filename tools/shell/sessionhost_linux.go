//go:build linux

package shell

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/LaoQi/battlelab/ctty"
)

// 会话宿主：一次 run_shell 借出终端后启动一个**独立会话首进程**，由它持有借出的
// pts 并 fork 出所有外部命令。
//
// 为什么需要它：一个 pts 同一时刻只能属于一个会话。若解释器让每条命令各自
// setsid+TIOCSCTTY，管道各段（并发存活）会互相抢，第二段直接 EPERM，
// 表现为 fork/exec 失败（`cmd | cmd` 报 Permission denied）。真实 bash 不受此限，
// 是因为**它自己**就是会话首进程、各命令只 setpgid。解释器跑在宿主进程内，
// 不能让宿主 setsid（那会脱离 REPL 的终端），所以把「会话首进程」独立成一个子进程。
//
// 于是：任意多条命令（含并发管道各段）共享同一个控制终端，都能打开 /dev/tty，
// sudo/gpg/ssh 在管道任意位置都能与用户应答；同一 pty 也让 sudo 的按 tty
// 凭据缓存只生效一次。

const (
	// hostEnvVar 是宿主的自举开关：被设置时进程不跑业务，直接进入会话宿主循环。
	hostEnvVar = "BATTLELAB_SHELL_SESSION_HOST"
	// hostTTYEnvVar 传宿主应设为控制终端的设备路径。
	hostTTYEnvVar = "BATTLELAB_SHELL_SESSION_TTY"
	// hostFlagNoTTY 表示这条命令不该取得终端（后台作业）。
	hostFlagNoTTY uint32 = 1 << 0
	// hostReadyByte 是宿主进入循环前回给父进程的就绪字节。
	hostReadyByte byte = 0x5A
	// hostReadyTimeout 是等待就绪握手的时长：宿主没接自举就快速失败。
	hostReadyTimeout = 2 * time.Second
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

// sessionHost 是父进程侧对宿主的句柄。
type sessionHost struct {
	sock *os.File
	mu   sync.Mutex
	dead bool
}

// startSessionHost 启动会话宿主，dev 是借出的 pty slave（宿主据此建立会话）。
func startSessionHost(dev *os.File) (*sessionHost, error) {
	if dev == nil {
		return nil, errHostUnavailable
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	fds, err := unixSocketpair()
	if err != nil {
		return nil, err
	}
	parent := os.NewFile(uintptr(fds[0]), "sessionhost-parent")
	child := os.NewFile(uintptr(fds[1]), "sessionhost-child")

	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(),
		hostEnvVar+"=1",
		hostTTYEnvVar+"="+dev.Name(),
	)
	// child 落到子进程的 fd 3（os/exec 的第一个额外 fd）。
	cmd.ExtraFiles = []*os.File{child}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		parent.Close()
		child.Close()
		return nil, err
	}
	child.Close()
	// 等待宿主就绪握手；超时说明该可执行文件没有接宿主自举，明确失败而不是挂死。
	_ = parent.SetReadDeadline(time.Now().Add(hostReadyTimeout))
	var ready [1]byte
	if _, err := io.ReadFull(parent, ready[:]); err != nil || ready[0] != hostReadyByte {
		parent.Close()
		_ = cmd.Process.Kill()
		return nil, errHostUnavailable
	}
	_ = parent.SetReadDeadline(time.Time{})
	return &sessionHost{sock: parent}, nil
}

// run 让宿主执行一条命令，返回退出码。
func (h *sessionHost) run(argv []string, cwd string, env []string, flags uint32, fds []int) (int, error) {
	return h.runWithContext(context.Background(), argv, cwd, env, flags, fds)
}

// runWithContext 让宿主执行一条命令；ctx 取消时杀掉宿主里那条命令的进程组。
//
// 命令运行在宿主进程里，父进程手里没有它的 *exec.Cmd，CancelCmd 那条路走不通，
// 所以超时/中断必须靠宿主先回报 pgid、再由这里向该进程组发信号。
func (h *sessionHost) runWithContext(ctx context.Context, argv []string, cwd string, env []string, flags uint32, fds []int) (int, error) {
	if h == nil {
		return 1, errHostUnavailable
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dead {
		return 1, errHostUnavailable
	}
	if err := h.writeRequest(argv, cwd, env, flags, fds); err != nil {
		h.dead = true
		return 1, err
	}
	pgid, err := h.readStarted()
	if err != nil {
		return 1, err
	}
	done := make(chan struct{})
	defer close(done)
	if ctx != nil && ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				h.kill(pgid)
			case <-done:
			}
		}()
	}
	return h.readReply()
}

// kill 杀掉宿主里正在运行的命令进程组（超时/中断时用）。
func (h *sessionHost) kill(pgid int) {
	if h == nil || pgid <= 0 {
		return
	}
	// 先 CONT 唤醒可能被 SIGTTIN/SIGTTOU 停住的进程组，再 KILL。
	_ = syscall.Kill(-pgid, syscall.SIGCONT)
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// writeRequest 发送一条命令请求。
//
// 帧格式：blob(argv) blob(cwd) blob(env) u32(flags) u32(fdCount) fdCount×u32(fd)
// stdio 只传**fd 编号**，宿主用 /proc/<ppid>/fd/<n> 打开同一个文件描述。
// 不用 SCM_RIGHTS：那要求 fd 与数据严格配对，一旦帧边界与附着消息错位就会静默挂死。
func (h *sessionHost) writeRequest(argv []string, cwd string, env []string, flags uint32, fds []int) error {
	var buf []byte
	buf = appendBlob(buf, argv)
	buf = appendBlob(buf, []string{cwd})
	buf = appendBlob(buf, env)
	var fix [4]byte
	binary.BigEndian.PutUint32(fix[:], flags)
	buf = append(buf, fix[:]...)
	binary.BigEndian.PutUint32(fix[:], uint32(len(fds)))
	buf = append(buf, fix[:]...)
	for _, fd := range fds {
		binary.BigEndian.PutUint32(fix[:], uint32(fd))
		buf = append(buf, fix[:]...)
	}
	_, err := h.sock.Write(buf)
	return err
}

func appendBlob(dst []byte, parts []string) []byte {
	total := 0
	for _, p := range parts {
		total += len(p) + 1
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(total))
	dst = append(dst, n[:]...)
	for _, p := range parts {
		dst = append(dst, p...)
		dst = append(dst, 0)
	}
	return dst
}

// readStarted 读宿主回报的 pgid（启动失败时为 0）。
func (h *sessionHost) readStarted() (int, error) {
	var raw [4]byte
	if _, err := io.ReadFull(h.sock, raw[:]); err != nil {
		h.dead = true
		return 0, err
	}
	return int(binary.BigEndian.Uint32(raw[:])), nil
}

// readReply 读宿主回报的退出码。
func (h *sessionHost) readReply() (int, error) {
	var raw [4]byte
	if _, err := io.ReadFull(h.sock, raw[:]); err != nil {
		h.dead = true
		return 1, err
	}
	code := binary.BigEndian.Uint32(raw[:])
	if code == 0xFFFFFFFF {
		var n [4]byte
		if _, err := io.ReadFull(h.sock, n[:]); err != nil {
			return 1, err
		}
		msg := make([]byte, binary.BigEndian.Uint32(n[:]))
		if _, err := io.ReadFull(h.sock, msg); err != nil {
			return 1, err
		}
		return 1, fmt.Errorf("%s", msg)
	}
	return int(code), nil
}

func (h *sessionHost) close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sock != nil {
		h.sock.Close()
		h.sock = nil
	}
	h.dead = true
}

// runSessionHost 是宿主主循环：建立会话、接管控制终端，然后逐条执行请求。
func runSessionHost() int {
	ttyPath := os.Getenv(hostTTYEnvVar)
	dev, err := os.OpenFile(ttyPath, os.O_RDWR, 0)
	if err != nil {
		return 90
	}
	if err := ctty.BecomeSessionLeader(dev); err != nil {
		return 91
	}
	// 宿主管理前台组时会做终端操作，必须忽略 SIGTTOU，否则会被停住。
	ctty.IgnoreTTOU()
	sock := os.NewFile(3, "sessionhost")
	if sock == nil {
		return 92
	}
	// 就绪握手：父进程据此判断「当前可执行文件确实支持宿主模式」。
	// 不支持自举的宿主（例如库自身的测试二进制没有 TestMain 接线）会走别的路径，
	// 不能让它一直等一个永远不来的请求。
	if _, err := sock.Write([]byte{hostReadyByte}); err != nil {
		return 93
	}
	for {
		argv, cwd, env, flags, fds, err := readRequest(sock)
		if err != nil {
			return 0
		}
		code := hostExec(sock, dev, argv, cwd, env, flags, fds)
		var out [4]byte
		binary.BigEndian.PutUint32(out[:], uint32(code))
		if _, err := sock.Write(out[:]); err != nil {
			return 0
		}
	}
}

// readRequest 读取一条命令请求（纯字节流，帧格式见 writeRequest）。
func readRequest(sock *os.File) (argv []string, cwd string, env []string, flags uint32, fds []int, err error) {
	argv, err = readBlob(sock)
	if err != nil {
		return
	}
	c, cerr := readBlob(sock)
	if cerr != nil {
		err = cerr
		return
	}
	if len(c) > 0 {
		cwd = c[0]
	}
	env, err = readBlob(sock)
	if err != nil {
		return
	}
	var raw [4]byte
	if _, err = io.ReadFull(sock, raw[:]); err != nil {
		return
	}
	flags = binary.BigEndian.Uint32(raw[:])
	if _, err = io.ReadFull(sock, raw[:]); err != nil {
		return
	}
	n := binary.BigEndian.Uint32(raw[:])
	for i := uint32(0); i < n; i++ {
		if _, err = io.ReadFull(sock, raw[:]); err != nil {
			return
		}
		fds = append(fds, int(binary.BigEndian.Uint32(raw[:])))
	}
	return
}

// readBlob 读一个以 \0 连接、带 u32 长度的字符串块。
func readBlob(r io.Reader) ([]string, error) {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(n[:])
	if size == 0 {
		return nil, nil
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	var out []string
	cur := make([]byte, 0, 64)
	for _, b := range buf {
		if b == 0 {
			out = append(out, string(cur))
			cur = cur[:0]
			continue
		}
		cur = append(cur, b)
	}
	return out, nil
}

// openParentFd 打开父进程（解释器所在进程）的某个 fd 对应的同一个文件。
func openParentFd(fd int) (*os.File, error) {
	path := "/proc/" + strconv.Itoa(os.Getppid()) + "/fd/" + strconv.Itoa(fd)
	return os.OpenFile(path, os.O_RDWR, 0)
}

// hostExec 在宿主里执行一条命令：新进程组 + 设为前台组（非后台时）。
// 启动成功后先把 pgid 回报给父进程（父进程据此在超时时杀组），执行完再回报退出码。
func hostExec(sock *os.File, dev *os.File, argv []string, cwd string, env []string, flags uint32, fds []int) int {
	if len(argv) == 0 {
		return 0
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	if len(env) > 0 {
		cmd.Env = env
	}
	// 有传入的 fd 就按 0/1/2 装上（管道/重定向已由解释器建好），否则都接控制终端。
	if len(fds) >= 3 {
		in, e1 := openParentFd(fds[0])
		out, e2 := openParentFd(fds[1])
		errw, e3 := openParentFd(fds[2])
		if e1 != nil || e2 != nil || e3 != nil {
			fmt.Fprintf(dev, "%s: cannot inherit stdio\n", argv[0])
			return 127
		}
		defer in.Close()
		defer out.Close()
		defer errw.Close()
		cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, errw
	} else {
		cmd.Stdin = dev
		cmd.Stdout = dev
		cmd.Stderr = dev
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(dev, "%s: %s\n", argv[0], err)
		return 127
	}
	var pg [4]byte
	binary.BigEndian.PutUint32(pg[:], uint32(cmd.Process.Pid))
	if _, werr := sock.Write(pg[:]); werr != nil {
		_ = cmd.Process.Kill()
		return 1
	}
	front := flags&hostFlagNoTTY == 0
	if front {
		// 设为前台进程组：非前台组读终端会被 SIGTTIN 停住。
		_ = ctty.SetForeground(dev, cmd.Process.Pid)
	}
	werr := cmd.Wait()
	if front {
		_ = ctty.SetForeground(dev, os.Getpid())
	}
	if werr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(werr, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return 128 + int(ws.Signal())
			}
			return ws.ExitStatus()
		}
		return exitErr.ExitCode()
	}
	return 1
}
