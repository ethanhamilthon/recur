package repl

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

// REPL executes commands in stateless system Bash processes.
type REPL struct {
	mu          sync.Mutex
	active      map[*exec.Cmd]struct{}
	Dir         string
	Environment map[string]string // base [env] configuration
	// Env returns variables exported before every call, such as session metadata.
	Env func() map[string]string
}

// Close terminates any active managed processes.
func (r *REPL) Close() {
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

func (r *REPL) track(cmd *exec.Cmd) {
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

func (r *REPL) untrack(cmd *exec.Cmd) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.active, cmd)
	r.mu.Unlock()
}

func (r *REPL) Execute(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	return r.ExecuteLimit(ctx, command, timeout, DefaultOutputLimit)
}

// ExecuteLimit runs a command keeping at most limit output bytes (first and last
// halves); NoOutputLimit keeps everything.
func (r *REPL) ExecuteLimit(ctx context.Context, command string, timeout time.Duration, limit int) (Result, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	bashPath := "/bin/bash"
	if _, err := os.Stat(bashPath); err != nil {
		if p, err := exec.LookPath("bash"); err == nil {
			bashPath = p
		}
	}

	cmd := exec.Command(bashPath, "--noprofile", "--norc", "-c", command)
	if r != nil && r.Dir != "" {
		cmd.Dir = r.Dir
	}
	cmd.Stdin = strings.NewReader("")
	cmd.Env = r.buildEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Orphaned descendants that keep pipes open are cut off after WaitDelay.
	cmd.WaitDelay = 250 * time.Millisecond

	var out syncBuffer
	out.buf.limit = limit
	cmd.Stdout = &out
	cmd.Stderr = &out

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := cmd.Start(); err != nil {
		out.WriteString(err.Error() + "\n")
		return Result{Output: out.Text(), ExitCode: 127, Error: true}, nil
	}

	pid := cmd.Process.Pid
	r.track(cmd)
	defer r.untrack(cmd)

	stop := context.AfterFunc(callCtx, func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	})
	err := cmd.Wait()
	stop()

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

// buildEnv merges inherited environment, configured [env], default pagers,
// recur binary directory first in PATH, and per-call metadata. BASH_ENV is excluded.
func (r *REPL) buildEnv() []string {
	envMap := make(map[string]string)
	for _, entry := range os.Environ() {
		if k, v, ok := strings.Cut(entry, "="); ok {
			if k == "BASH_ENV" {
				continue
			}
			envMap[k] = v
		}
	}
	if r != nil {
		for k, v := range r.Environment {
			if k == "BASH_ENV" {
				continue
			}
			envMap[k] = v
		}
	}
	envMap["PAGER"] = "cat"
	envMap["GIT_PAGER"] = "cat"
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
			if k == "BASH_ENV" {
				continue
			}
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
