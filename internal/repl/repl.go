package repl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

const (
	DefaultTimeout = 120 * time.Second
	MaxTimeout     = 600 * time.Second
)

type Result struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Error    bool   `json:"error,omitempty"`
}

// REPL is a persistent in-process shell. Shell state survives timeouts; only
// Close or an interpreter panic discards it.
type REPL struct {
	mu           sync.Mutex
	runner       *interp.Runner
	jobs         context.Context // lifetime of top-level background jobs
	stopJobs     context.CancelFunc
	callMu       sync.Mutex
	call         *call
	Dir          string
	Prefix       string
	PrefixOutput string
	// Env returns variables exported before every call, such as session metadata.
	Env func() map[string]string
	// Observe, when set, is told about prefix_start and prefix_end.
	Observe func(event string, kv ...string)
}

type call struct{ output cappedBuffer }

// sink routes shell output to one call; output written after it ends is dropped.
type sink struct {
	r *REPL
	c *call
}

func (s sink) Write(p []byte) (int, error) {
	s.r.callMu.Lock()
	defer s.r.callMu.Unlock()
	if c := s.c; c != nil && s.r.call == c {
		c.output.Write(p)
	} else if c == nil && s.r.call != nil {
		s.r.call.output.Write(p)
	}
	return len(p), nil
}

func (r *REPL) stop() {
	if r.runner == nil {
		return
	}
	r.stopJobs()
	r.runner = nil
}
func (r *REPL) Close() { r.mu.Lock(); defer r.mu.Unlock(); r.stop() }

// Ensure starts a fresh shell and runs the prefix only when no shell exists.
func (r *REPL) Ensure(ctx context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runner != nil {
		return false, nil
	}
	env := append(os.Environ(), "PAGER=cat", "GIT_PAGER=cat")
	// Put this recur binary first so recur notify, view and docs always resolve.
	if executable, err := os.Executable(); err == nil {
		env = append(env, "PATH="+filepath.Dir(executable)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	runner, err := interp.New(interp.Dir(r.Dir), interp.Env(expand.ListEnviron(env...)),
		interp.StdIO(nil, sink{r: r}, sink{r: r}), interp.ExecHandlers(func(interp.ExecHandlerFunc) interp.ExecHandlerFunc { return external }))
	if err != nil {
		return false, err
	}
	r.runner = runner
	r.jobs, r.stopJobs = context.WithCancel(context.Background())
	started := time.Now()
	if r.Observe != nil {
		r.Observe("prefix_start")
	}
	result, broken := r.run(ctx, r.Prefix, DefaultTimeout, NoOutputLimit)
	if r.Observe != nil {
		r.Observe("prefix_end", "RECUR_PREFIX_OK", strconv.FormatBool(!broken), "RECUR_DURATION_MS", strconv.FormatInt(time.Since(started).Milliseconds(), 10))
	}
	if broken {
		r.stop()
		return false, fmt.Errorf("shell prefix failed: %s", result.Output)
	}
	r.PrefixOutput = result.Output
	return true, nil
}

func (r *REPL) Execute(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	return r.ExecuteLimit(ctx, command, timeout, DefaultOutputLimit)
}

// ExecuteLimit runs a command keeping at most limit output bytes (first and last
// halves); NoOutputLimit keeps everything.
func (r *REPL) ExecuteLimit(ctx context.Context, command string, timeout time.Duration, limit int) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runner == nil {
		return Result{}, fmt.Errorf("shell unavailable")
	}
	result, _ := r.run(ctx, command, timeout, limit)
	return result, nil
}

// run reports broken when the source did not run to completion: a syntax
// error, a timeout, cancellation or an interpreter failure.
func (r *REPL) run(ctx context.Context, source string, timeout time.Duration, limit int) (Result, bool) {
	file, err := syntax.NewParser().Parse(strings.NewReader(source), "")
	if err != nil {
		return Result{Output: "[recur: syntax error, nothing was run] " + err.Error() + "\n", ExitCode: 2, Error: true}, true
	}
	c := &call{output: cappedBuffer{limit: limit}}
	r.callMu.Lock()
	r.call = c
	r.callMu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err = r.statements(callCtx, c, file)
	r.callMu.Lock()
	r.call = nil
	r.callMu.Unlock()
	result := Result{}
	var status interp.ExitStatus
	broken := true
	switch {
	case ctx.Err() != nil:
		result.ExitCode = 130
		c.output.Write([]byte("\n[recur: cancelled; shell state was kept]\n"))
	case callCtx.Err() != nil:
		result.ExitCode = 124
		fmt.Fprintf(&c.output, "\n[recur: timed out after %s; shell state was kept]\n", timeout)
	case err == nil || errors.As(err, &status):
		result.ExitCode, broken = int(status), false
	default:
		result.ExitCode = 1
		fmt.Fprintf(&c.output, "\n[recur: %v]\n", err)
	}
	result.Output, result.Error = c.output.Text(), result.ExitCode != 0
	return result, broken
}

// statements runs top-level statements one by one so that a top-level & job
// is detached from the call deadline and lives until the shell is closed.
func (r *REPL) statements(ctx context.Context, c *call, file *syntax.File) (err error) {
	defer func() {
		if p := recover(); p != nil {
			r.stop()
			err = fmt.Errorf("shell interpreter crashed, state was lost: %v", p)
		}
	}()
	if err = r.runner.Run(ctx, r.metadata()); err != nil {
		return err
	}
	for _, stmt := range file.Stmts {
		if stmt.Background {
			job := *stmt
			job.Background = false
			shell := r.runner.Subshell()
			if err = interp.StdIO(nil, sink{r, c}, sink{r, c})(shell); err != nil {
				return err
			}
			go func() {
				defer func() { _ = recover() }()
				_ = shell.Run(r.jobs, &job)
			}()
			err = nil
			continue
		}
		err = r.runner.Run(ctx, stmt)
		if ctx.Err() != nil || r.runner.Exited() {
			break
		}
	}
	return err
}

// metadata exports Env before every call.
func (r *REPL) metadata() *syntax.File {
	script := "true"
	if r.Env != nil {
		for name, value := range r.Env() {
			if quoted, err := syntax.Quote(value, syntax.LangBash); err == nil {
				script += "; export " + name + "=" + quoted
			}
		}
	}
	file, _ := syntax.NewParser().Parse(strings.NewReader(script), "")
	return file
}

// external runs a program in its own process group, killed on cancellation.
func external(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	path, err := interp.LookPathDir(hc.Dir, hc.Env, args[0])
	if err != nil {
		fmt.Fprintln(hc.Stderr, err)
		return interp.ExitStatus(127)
	}
	cmd := exec.Command(path)
	cmd.Args, cmd.Dir = args, hc.Dir
	for name, v := range hc.Env.Each {
		if v.Exported && v.Kind == expand.String {
			cmd.Env = append(cmd.Env, name+"="+v.String())
		}
	}
	if hc.Stdin != nil {
		cmd.Stdin = hc.Stdin
	}
	cmd.Stdout, cmd.Stderr = hc.Stdout, hc.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Orphaned grandchildren may keep output pipes open after the program exits.
	cmd.WaitDelay = time.Second
	if err = cmd.Start(); err != nil {
		fmt.Fprintln(hc.Stderr, err)
		return interp.ExitStatus(126)
	}
	stop := context.AfterFunc(ctx, func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	err = cmd.Wait()
	stop()
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.As(err, &exit):
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return interp.ExitStatus(128 + int(status.Signal()))
		}
		return interp.ExitStatus(exit.ExitCode())
	case errors.Is(err, exec.ErrWaitDelay):
		return nil
	}
	return err
}
