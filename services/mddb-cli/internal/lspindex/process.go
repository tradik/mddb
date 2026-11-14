package lspindex

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// server is one running language server process.
type server struct {
	cmd    *exec.Cmd
	conn   *conn
	stdin  *os.File // our end of the server's stdin
	stdout *os.File // our end of the server's stdout
	stderr *ringBuffer

	waitDone chan struct{} // closed once the process is reaped
}

// osPipe is os.Pipe, replaceable so tests can make it fail (it only does
// when the process is out of file descriptors).
var osPipe = os.Pipe

// stderrBytes is how much of the server's stderr is kept for errors.
const stderrBytes = 64 << 10

// shutdownTimeout is how long a server gets to exit after shutdown/exit
// before it is killed (a variable so tests need not wait it out).
var shutdownTimeout = 5 * time.Second

// startServer runs argv in root with its stdio connected to a JSON-RPC conn.
// ctx ending kills the process. onLine, when set, also gets each line the
// server writes to stderr.
//
// Plain pipes (*os.File) rather than cmd.StdoutPipe: exec closes those in
// Wait, possibly before the read loop has read the server's last words, and
// the read loop must see EOF, not "file already closed", to report a crash.
func startServer(ctx context.Context, root string, argv []string, onLine func(string)) (*server, error) {
	inR, inW, err := osPipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := osPipe()
	if err != nil {
		closeAll(inR, inW)
		return nil, err
	}
	// #nosec G204 -- argv comes from the built-in server table or the
	// operator's MDDB_LSP_* variable, resolved with exec.LookPath; it is run
	// directly, never through a shell, and nothing in it comes from the network.
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Stdin, cmd.Stdout = inR, outW
	s := &server{cmd: cmd, stdin: inW, stdout: outR, stderr: &ringBuffer{limit: stderrBytes}, waitDone: make(chan struct{})}
	cmd.Stderr = s.stderr
	if onLine != nil {
		cmd.Stderr = io.MultiWriter(s.stderr, &lineWriter{fn: onLine})
	}
	// A server's children may inherit stderr and outlive it; Wait must not
	// block on them forever.
	cmd.WaitDelay = shutdownTimeout
	err = cmd.Start()
	// The child has its own copies now; ours would keep the pipes open
	// after the server exits and hide its death from the read loop.
	closeAll(inR, outW)
	if err != nil {
		closeAll(inW, outR)
		return nil, err
	}
	go func() {
		_ = cmd.Wait() // the exit status says nothing a caller can act on
		close(s.waitDone)
	}()
	s.conn = newConn(inW, outR, []workspaceFolder{{URI: fileURI(root), Name: "root"}})
	return s, nil
}

// stop asks the server to shut down and exit, kills it if it has not exited
// within shutdownTimeout, and waits until the process is reaped and the read
// loop has ended, so nothing outlives Index.
func (s *server) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	// Best effort: a server that crashed or hangs is killed below.
	_ = s.conn.call(ctx, "shutdown", nil, nil)
	_ = s.conn.notify("exit", nil)
	_ = s.stdin.Close()
	select {
	case <-s.waitDone:
	case <-ctx.Done():
		_ = s.cmd.Process.Kill()
		<-s.waitDone
	}
	// A grandchild holding the server's stdout would keep the read loop
	// alive; closing our end ends it.
	_ = s.stdout.Close()
	<-s.conn.done
}

// peakRssMb reads the server's VmHWM from /proc (Linux); 0 elsewhere or once
// the process is gone.
func (s *server) peakRssMb() int {
	return peakRssMb("/proc/" + strconv.Itoa(s.cmd.Process.Pid) + "/status")
}

func peakRssMb(statusFile string) int {
	data, err := os.ReadFile(statusFile) // #nosec G304 -- /proc path built from our child's pid
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmHWM:"); ok {
			f := strings.Fields(rest)
			if len(f) == 0 {
				return 0
			}
			kb, err := strconv.Atoi(f[0])
			if err != nil {
				return 0
			}
			return kb >> 10
		}
	}
	return 0
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

// ringBuffer keeps the last limit bytes written to it.
type ringBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if over := len(r.buf) - r.limit; over > 0 {
		r.buf = append(r.buf[:0], r.buf[over:]...)
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}
