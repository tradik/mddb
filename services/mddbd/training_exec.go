package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Starting an operator's command. There is no shell: the executable is the
// absolute path the profile names, the arguments an argv slice built from
// the profile's templates and checked values, the environment an allowlist
// plus the job's own MDDB_TRAIN_* / MDDB_EVAL_* variables. Output goes to the
// job's log, capped; a line with the profile's progress prefix updates the
// job's progress. Cancelling or a timeout ends the process group
// (training_proc_*.go).

// Limits of a command's log and its shutdown.
var (
	trainingLogCap   int64 = 8 << 20
	trainingGrace          = 10 * time.Second
	trainingLogTail        = 16 << 10
	maxProgressLine        = 64 << 10
	errTrainingLimit       = errors.New("timed out")
)

// commandRun is one start of a command.
type commandRun struct {
	spec       *commandSpec
	argv       []string
	env        map[string]string
	dir        string
	timeout    time.Duration
	progress   string
	log        *trainingLog
	onProgress func(pct float64, msg string)
}

// commandEnv is the allowlisted variables the server has, then the job's.
func commandEnv(names []string, own map[string]string) []string {
	env := make([]string, 0, len(names)+len(own))
	for _, n := range names {
		if v, ok := os.LookupEnv(n); ok {
			env = append(env, n+"="+v)
		}
	}
	for k, v := range own {
		env = append(env, k+"="+v)
	}
	return env
}

// run starts the command and waits for it. The exit code is -1 when the
// process did not start or was ended by a signal; the error says why it did
// not succeed: errTrainingLimit on a timeout, the context's cause when
// cancelled.
func (c *commandRun) run(ctx context.Context) (int, error) {
	ctx, stop := context.WithTimeoutCause(ctx, c.timeout, errTrainingLimit)
	defer stop()
	// #nosec G204 -- the executable and fixed arguments come from the operator's trainer profile file
	// (MDDB_TRAINERS_CONFIG, validated at startup: absolute path, regular executable file); request values
	// reach argv only as separate elements checked against the profile's declared types, ranges, enums
	// and character set (resolveParams), and no shell is involved.
	cmd := exec.CommandContext(ctx, c.spec.Command, c.argv...)
	cmd.Dir = c.dir
	cmd.Env = commandEnv(c.spec.trainerEnvNames(), c.env)
	out := &progressWriter{log: c.log, prefix: c.progress, onProgress: c.onProgress}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = trainingGrace
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	err := cmd.Wait()
	killProcessGroup(cmd)
	out.flush()
	code := cmd.ProcessState.ExitCode()
	if ctx.Err() != nil {
		return code, context.Cause(ctx)
	}
	if err != nil {
		return code, fmt.Errorf("the command exited with code %d", code)
	}
	return 0, nil
}

// trainingLog is a job's log file, kept to trainingLogCap bytes.
type trainingLog struct {
	mu      sync.Mutex
	f       *os.File
	written int64
	full    bool
}

// Write appends while under the cap; past it, one note and then nothing.
// It never fails, so a full log cannot stop the command.
func (l *trainingLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil || l.full {
		return len(p), nil
	}
	room := trainingLogCap - l.written
	if int64(len(p)) > room {
		_, _ = l.f.Write(p[:room])
		_, _ = l.f.WriteString("\n[log truncated: the job's log is capped]\n")
		l.full, l.written = true, trainingLogCap
		return len(p), nil
	}
	n, _ := l.f.Write(p)
	l.written += int64(n)
	return len(p), nil
}

// section starts a stage's part of the log.
func (l *trainingLog) section(name string) {
	_, _ = fmt.Fprintf(l, "\n=== %s · %s ===\n", name, time.Now().UTC().Format(time.RFC3339))
}

// close closes the file.
func (l *trainingLog) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
}

// progressWriter passes output to the log and reads progress lines.
type progressWriter struct {
	log        *trainingLog
	prefix     string
	onProgress func(pct float64, msg string)
	line       []byte
}

// Write logs p and scans its complete lines.
func (w *progressWriter) Write(p []byte) (int, error) {
	_, _ = w.log.Write(p)
	if w.prefix == "" || w.onProgress == nil {
		return len(p), nil
	}
	w.line = append(w.line, p...)
	for {
		i := bytes.IndexByte(w.line, '\n')
		if i < 0 {
			break
		}
		w.scan(string(w.line[:i]))
		w.line = w.line[i+1:]
	}
	if len(w.line) > maxProgressLine {
		w.line = w.line[:0] // a line this long is no progress line
	}
	return len(p), nil
}

// flush scans a last line without a line break.
func (w *progressWriter) flush() {
	if len(w.line) > 0 && w.onProgress != nil {
		w.scan(string(w.line))
	}
	w.line = nil
}

// scan reads "<prefix> <percent> [message]".
func (w *progressWriter) scan(line string) {
	rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), w.prefix+" ")
	if !ok {
		return
	}
	num, msg, _ := strings.Cut(strings.TrimSpace(rest), " ")
	pct, err := strconv.ParseFloat(strings.TrimSuffix(num, "%"), 64)
	if err != nil || pct < 0 || pct > 100 {
		return
	}
	w.onProgress(pct, cutText(strings.TrimSpace(msg), 200))
}
