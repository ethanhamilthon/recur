// Package hooks runs user commands when Recur emits lifecycle events.
// Hooks are asynchronous: they never delay or fail the agent.
package hooks

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Events lists every event name a hook may subscribe to; "*" matches all.
var Events = []string{
	"recur_start", "recur_exit",
	"session_start", "session_switch", "session_end", "session_title_change",
	"turn_start", "turn_end", "idle",
	"steer_queued", "steer_accepted", "notify_received", "image_attached",
	"model_change", "model_request_start", "model_request_end", "model_retry",
	"shell_start", "shell_end", "prefix_start", "prefix_end",
	"compact_start", "compact_end", "error",
}

const (
	defaultTimeout = 10
	queueSize      = 64
)

// Hook is one [[hook]] table of hooks.toml.
type Hook struct {
	Event   string   `toml:"event"`
	Events  []string `toml:"events"`
	Command string   `toml:"command"`
	Timeout int      `toml:"timeout"` // seconds; 0 means 10
}

// Parse reads and validates hooks.toml text.
func Parse(text string) ([]Hook, error) {
	var f struct {
		Hook []Hook `toml:"hook"`
	}
	if err := toml.NewDecoder(strings.NewReader(text)).DisallowUnknownFields().Decode(&f); err != nil {
		return nil, err
	}
	for i, h := range f.Hook {
		names := append([]string(nil), h.Events...)
		if h.Event != "" {
			names = append(names, h.Event)
		}
		if len(names) == 0 || strings.TrimSpace(h.Command) == "" {
			return nil, fmt.Errorf("hook %d needs event (or events) and command", i+1)
		}
		for _, n := range names {
			if n != "*" && !slices.Contains(Events, n) {
				return nil, fmt.Errorf("hook %d: unknown event %q; see recur docs hooks", i+1, n)
			}
		}
		if h.Timeout < 0 || h.Timeout > 600 {
			return nil, fmt.Errorf("hook %d: timeout must be 1..600 seconds", i+1)
		}
		f.Hook[i].Events, f.Hook[i].Event = names, ""
	}
	return f.Hook, nil
}

type job struct {
	env  []string
	dir  string
	name string
	done func()
}

type worker struct {
	hook   Hook
	events map[string]bool
	jobs   chan job
}

// Runner delivers events to hooks. Each hook has its own queue, so one hook sees
// events in order while a slow hook never delays another one.
type Runner struct {
	Mode string // "tui" or "headless", exported as RECUR_MODE
	Log  string // failures are appended here

	mu      sync.Mutex
	list    []Hook
	workers []*worker
	wg      sync.WaitGroup
}

var sequence atomic.Uint64

// Set replaces the active hooks. Identical hooks keep their queues, so event
// order survives /new and /reload; queued events of replaced hooks still run.
func (r *Runner) Set(list []Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(list) == 0 && len(r.list) == 0 || reflect.DeepEqual(list, r.list) {
		return
	}
	r.list = list
	for _, w := range r.workers {
		close(w.jobs)
	}
	r.workers = nil
	for _, h := range list {
		w := &worker{hook: h, events: map[string]bool{}, jobs: make(chan job, queueSize)}
		for _, n := range h.Events {
			w.events[n] = true
		}
		r.workers = append(r.workers, w)
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			for j := range w.jobs {
				r.run(w.hook, j)
				j.done()
			}
		}()
	}
}

// Wait stops accepting events and gives queued hooks up to d to finish.
func (r *Runner) Wait(d time.Duration) {
	if r == nil {
		return
	}
	r.Set(nil)
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

// Fire queues event for every matching hook and returns immediately. Variables
// whose name ends in _FILE carry text: it is written to a temporary file that
// exists while the hooks run, and the variable holds the file path.
func (r *Runner) Fire(event string, vars map[string]string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var targets []*worker
	for _, w := range r.workers {
		if w.events[event] || w.events["*"] {
			targets = append(targets, w)
		}
	}
	if len(targets) == 0 {
		return
	}
	env := append(os.Environ(), "RECUR_EVENT="+event, "RECUR_MODE="+r.Mode,
		"RECUR_SEQ="+strconv.FormatUint(sequence.Add(1), 10), "RECUR_TS="+strconv.FormatInt(time.Now().UnixMilli(), 10))
	dir := ""
	for k, v := range vars {
		if !strings.HasSuffix(k, "_FILE") {
			env = append(env, k+"="+v)
			continue
		}
		if dir == "" {
			dir, _ = os.MkdirTemp("", "recur-hook-")
		}
		path := filepath.Join(dir, strings.ToLower(k))
		if os.WriteFile(path, []byte(v), 0600) == nil {
			env = append(env, k+"="+path)
		}
	}
	left := atomic.Int32{}
	left.Store(int32(len(targets)))
	done := func() {
		if left.Add(-1) == 0 && dir != "" {
			os.RemoveAll(dir)
		}
	}
	for _, w := range targets {
		select {
		case w.jobs <- job{env: env, dir: vars["RECUR_CWD"], name: event, done: done}:
		default:
			r.fail(w.hook, event, "queue full, event dropped")
			done()
		}
	}
}

type tail struct{ buf bytes.Buffer }

func (t *tail) Write(p []byte) (int, error) {
	if room := 2048 - t.buf.Len(); room > 0 {
		t.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// run executes one hook in its own process group; output never reaches the terminal.
func (r *Runner) run(h Hook, j job) {
	seconds := h.Timeout
	if seconds == 0 {
		seconds = defaultTimeout
	}
	var out tail
	cmd := exec.Command("/bin/sh", "-c", h.Command)
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = j.env, j.dir, &out, &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		r.fail(h, j.name, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	err := cmd.Wait()
	stop()
	if ctx.Err() != nil {
		err = fmt.Errorf("timed out after %ds", seconds)
	}
	if err != nil {
		r.fail(h, j.name, err.Error()+": "+strings.TrimSpace(out.buf.String()))
	}
}

func (r *Runner) fail(h Hook, event, problem string) {
	if r.Log == "" {
		return
	}
	f, err := os.OpenFile(r.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s event=%s command=%q: %s\n", time.Now().Format(time.RFC3339), event, h.Command, strings.ReplaceAll(problem, "\n", " "))
}
