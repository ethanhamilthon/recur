package repl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const executionTimeout = 2 * time.Minute

type Result struct {
	Images []Image `json:"images,omitempty"`
	Output string  `json:"output"`
	Error  bool    `json:"error"`
}
type REPL struct {
	mu           sync.Mutex
	cmd          *exec.Cmd
	input        io.WriteCloser
	output       *os.File
	scanner      *bufio.Scanner
	Dir          string
	Prefix       string
	PrefixOutput string
	Session      func() map[string]any
}

func (r *REPL) stop() {
	if r.cmd == nil {
		return
	}
	_ = syscall.Kill(-r.cmd.Process.Pid, syscall.SIGKILL)
	_ = r.input.Close()
	_ = r.output.Close()
	_ = r.cmd.Wait()
	r.cmd = nil
}
func (r *REPL) Close() { r.mu.Lock(); defer r.mu.Unlock(); r.stop() }

// Ensure starts a fresh interpreter only when its state has been lost.
func (r *REPL) Ensure(ctx context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil {
		return false, nil
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return false, err
	}
	executable, err := os.Executable()
	if err != nil {
		reader.Close()
		writer.Close()
		return false, err
	}
	cmd := exec.Command(executable, "--recur-worker")
	cmd.Dir = r.Dir
	cmd.ExtraFiles = []*os.File{writer}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, err := cmd.StdinPipe()
	if err != nil {
		reader.Close()
		writer.Close()
		return false, err
	}
	if err = cmd.Start(); err != nil {
		input.Close()
		reader.Close()
		writer.Close()
		return false, err
	}
	writer.Close()
	r.cmd, r.input, r.output = cmd, input, reader
	r.scanner = bufio.NewScanner(reader)
	r.scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	result, err := r.execute(ctx, r.Prefix, true)
	if err != nil {
		return false, err
	}
	if result.Error {
		r.stop()
		return false, fmt.Errorf("REPL prefix failed: %s", result.Output)
	}
	r.PrefixOutput = result.Output
	return true, nil
}
func (r *REPL) Execute(ctx context.Context, code string) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd == nil {
		return Result{}, fmt.Errorf("REPL unavailable")
	}
	return r.execute(ctx, code, false)
}
func (r *REPL) execute(ctx context.Context, code string, prefix bool) (Result, error) {
	// Deadline starts for each snippet, including the prefix, never for the
	// lifetime of the persistent worker. Earlier caller deadlines still win.
	ctx, cancel := context.WithTimeout(ctx, executionTimeout)
	defer cancel()
	type reply struct {
		result Result
		err    error
	}
	done := make(chan reply, 1)
	go func() {
		var session map[string]any
		if r.Session != nil {
			session = r.Session()
		}
		data, err := json.Marshal(map[string]any{"code": code, "prefix": prefix, "session": session})
		if err == nil {
			_, err = r.input.Write(append(data, '\n'))
		}
		var result Result
		if err == nil {
			if !r.scanner.Scan() {
				err = r.scanner.Err()
				if err == nil {
					err = io.EOF
				}
			} else {
				err = json.Unmarshal(r.scanner.Bytes(), &result)
			}
		}
		done <- reply{result, err}
	}()
	select {
	case v := <-done:
		if v.err != nil {
			r.stop()
			return Result{}, fmt.Errorf("REPL state lost: %w", v.err)
		}
		return v.result, nil
	case <-ctx.Done():
		r.stop()
		<-done
		return Result{}, ctx.Err()
	}
}
