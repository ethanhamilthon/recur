package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
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

// Runner executes commands in stateless system Bash processes.
type Runner struct {
	mu          sync.Mutex
	active      map[*exec.Cmd]struct{}
	Dir         string
	Environment map[string]string // base [env] configuration
	// Env returns variables exported before every call, such as session metadata.
	Env func() map[string]string
}

// Close terminates any active managed processes.
func (r *Runner) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for cmd := range r.active {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	r.active = make(map[*exec.Cmd]struct{})
}

func (r *Runner) track(cmd *exec.Cmd) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.active == nil {
		r.active = make(map[*exec.Cmd]struct{})
	}
	r.active[cmd] = struct{}{}
	r.mu.Unlock()
}

func (r *Runner) untrack(cmd *exec.Cmd) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.active, cmd)
	r.mu.Unlock()
}

// Command builds `bash -c script` in its own process group. It is the only place
// that decides how recur starts shell commands: BASH_ENV is always dropped,
// pagers are disabled, and cancelling ctx kills the whole process group.
// A nil env inherits the current environment.
func Command(ctx context.Context, dir string, env []string, script string) *exec.Cmd {
	bash := "/bin/bash"
	if _, err := os.Stat(bash); err != nil {
		if p, err := exec.LookPath("bash"); err == nil {
			bash = p
		}
	}
	if env == nil {
		env = os.Environ()
	}
	cmd := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", script)
	cmd.Dir = dir
	for _, e := range env {
		if !strings.HasPrefix(e, "BASH_ENV=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "PAGER=cat", "GIT_PAGER=cat")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	return cmd
}

func (r *Runner) Execute(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	return r.ExecuteLimit(ctx, command, timeout, DefaultOutputLimit)
}

// ExecuteLimit runs a command keeping at most limit output bytes (first and last
// halves); NoOutputLimit keeps everything.
func (r *Runner) ExecuteLimit(ctx context.Context, command string, timeout time.Duration, limit int) (Result, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dir := ""
	if r != nil {
		dir = r.Dir
	}
	cmd := Command(callCtx, dir, r.buildEnv(), command)
	cmd.Stdin = strings.NewReader("")
	// Orphaned descendants that keep pipes open are cut off after WaitDelay.
	cmd.WaitDelay = 250 * time.Millisecond

	var out syncBuffer
	out.buf.limit = limit
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return Result{Output: "\n[recur: cancelled]\n", ExitCode: 130, Error: true}, nil
		}
		out.WriteString(err.Error() + "\n")
		return Result{Output: out.Text(), ExitCode: 127, Error: true}, nil
	}

	r.track(cmd)
	defer r.untrack(cmd)

	err := cmd.Wait()

	result := Result{}
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		result.ExitCode = 130
		out.WriteString("\n[recur: cancelled]\n")
	case callCtx.Err() != nil:
		result.ExitCode = 124
		out.WriteString(fmt.Sprintf("\n[recur: timed out after %s]\n", timeout))
	case errors.Is(err, exec.ErrWaitDelay):
		if cmd.ProcessState != nil {
			result.ExitCode = cmd.ProcessState.ExitCode()
		}
	case errors.As(err, &exit):
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			result.ExitCode = 128 + int(status.Signal())
		} else {
			result.ExitCode = exit.ExitCode()
		}
	case err != nil:
		result.ExitCode = 1
		out.WriteString(fmt.Sprintf("\n[recur: %v]\n", err))
	default:
		result.ExitCode = 0
	}
	result.Output = out.Text()
	result.Error = result.ExitCode != 0
	return result, nil
}

// buildEnv merges inherited environment, configured [env], recur binary
// directory first in PATH, and per-call metadata. Command applies the final policy.
func (r *Runner) buildEnv() []string {
	envMap := make(map[string]string)
	for _, entry := range os.Environ() {
		if k, v, ok := strings.Cut(entry, "="); ok {
			envMap[k] = v
		}
	}
	if r != nil {
		for k, v := range r.Environment {
			envMap[k] = v
		}
	}
	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		currPath := envMap["PATH"]
		if currPath == "" {
			envMap["PATH"] = execDir
		} else {
			envMap["PATH"] = execDir + string(os.PathListSeparator) + currPath
		}
	}
	if r != nil && r.Env != nil {
		for k, v := range r.Env() {
			envMap[k] = v
		}
	}
	envList := make([]string, 0, len(envMap))
	for k, v := range envMap {
		envList = append(envList, k+"="+v)
	}
	sort.Strings(envList)
	return envList
}
