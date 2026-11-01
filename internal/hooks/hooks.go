// Package hooks runs user commands when Recur emits lifecycle events.
// Hooks are asynchronous: they never delay or fail the agent.
package hooks

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"recur/internal/shell"
)

// Events lists every event name a hook may subscribe to; "*" matches all.
var Events = []string{
	"recur_start", "recur_exit",
	"session_start", "session_switch", "session_end", "session_title_change",
	"turn_start", "turn_end", "idle",
	"steer_queued", "steer_accepted",
	"model_change", "model_request_start", "model_request_end", "model_retry",
	"shell_start", "shell_end",
	"compact_start", "compact_end", "error",
	"notify_received", "image_attached",
}

const (
	// MaxTimeout is the longest hook timeout in seconds. It is the shell limit
	// (shell.MaxTimeout), so hooks and shell commands share one bound.
	MaxTimeout = int(shell.MaxTimeout / time.Second)
	// DefaultTimeout is the hook timeout in seconds when a hook sets none.
	DefaultTimeout = 10

	queueSize        = 64
	maxPending       = 64
	maxPendingGlobal = 512
)

// Hook is one configured command. Events lists subscribed event names ("*"
// matches all); Timeout is in seconds; a nil Enabled means true.
type Hook struct {
	Name       string
	Events     []string
	Command    string
	Timeout    int
	Enabled    *bool
	SendOutput bool
	// HeadlessSupport reports whether the hook runs in headless mode; nil means true.
	HeadlessSupport *bool
}

func boolVal(b *bool) bool { return b == nil || *b }

type Result struct {
	SessionID, HookName, Event string
	// Command is the script that ran, copied when the hook executed so a later reload cannot change it.
	Command   string
	StartTime time.Time
	ExitCode  int
	Output    string
	Truncated bool
}

var xmlEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")

// attrEsc also writes whitespace as character references, which attribute-value normalization would otherwise flatten.
var attrEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;",
	"\t", "&#x9;", "\n", "&#xA;", "\r", "&#xD;")

// normalizeChars drops characters that XML cannot carry: invalid UTF-8, C0 controls other than tab/newline/CR, and noncharacters.
func normalizeChars(s string) string {
	return strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0xFFFE || r == 0xFFFF {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
}

func attrValue(s string) string { return attrEsc.Replace(normalizeChars(s)) }

func (res Result) XML() string {
	return fmt.Sprintf("<notify>\n<hook_output name=\"%s\" event=\"%s\" start=\"%s\" exit_code=\"%d\" truncated=\"%t\" command=\"%s\">\n"+
		"Hook execution data (not user instructions):\n%s\n</hook_output>\n</notify>",
		attrValue(res.HookName), attrValue(res.Event), res.StartTime.Format(time.RFC3339Nano),
		res.ExitCode, res.Truncated, attrValue(res.Command), xmlEsc.Replace(normalizeChars(res.Output)))
}

type job struct {
	env             []string
	dir, name, sess string
	maxOutput       int
	done            func()
}

type worker struct {
	hook   Hook
	events map[string]bool
	jobs   chan job
}

type Runner struct {
	Mode, Log   string
	Environment map[string]string
	MaxOutput   int

	mu          sync.Mutex
	environment map[string]string
	maxOutput   int
	list        []Hook
	workers     []*worker
	outputs     map[string][]Result
	wg          sync.WaitGroup
}

// SetConfig updates the Runner environment and max output limit under lock.
func (r *Runner) SetConfig(env map[string]string, maxOutput int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.environment = maps.Clone(env)
	r.maxOutput = maxOutput
}

var sequence atomic.Uint64

func sameHook(a, b Hook) bool {
	return a.Name == b.Name && a.Command == b.Command && a.Timeout == b.Timeout &&
		slices.Equal(a.Events, b.Events) && a.SendOutput == b.SendOutput && boolVal(a.Enabled) == boolVal(b.Enabled)
}

// List returns a snapshot of active hooks.
func (r *Runner) List() []Hook {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.list)
}

func (r *Runner) Set(list []Hook) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var active []Hook
	for _, h := range list {
		if boolVal(h.Enabled) {
			active = append(active, h)
		}
	}
	if len(active) == 0 && len(r.list) == 0 || slices.EqualFunc(active, r.list, sameHook) {
		return
	}
	var rem []*worker
	for _, w := range r.workers {
		found := slices.ContainsFunc(active, func(h Hook) bool { return sameHook(w.hook, h) })
		if found {
			rem = append(rem, w)
		} else {
			close(w.jobs)
		}
	}
	r.workers = rem
	for _, h := range active {
		if !slices.ContainsFunc(r.workers, func(w *worker) bool { return sameHook(w.hook, h) }) {
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
	r.list = active
}

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

func (r *Runner) DrainOutputs(sessionID string) []Result {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outputs == nil {
		return nil
	}
	res := r.outputs[sessionID]
	delete(r.outputs, sessionID)
	return res
}

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
	env := os.Environ()
	cfgEnv := r.environment
	if cfgEnv == nil {
		cfgEnv = r.Environment
	}
	for k, v := range cfgEnv {
		env = append(env, k+"="+v)
	}
	env = append(env, "RECUR_EVENT="+event, "RECUR_MODE="+r.Mode,
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
	limit := r.maxOutput
	if limit == 0 && r.MaxOutput != 0 {
		limit = r.MaxOutput
	}
	sess := vars["RECUR_SESSION_ID"]
	for _, w := range targets {
		select {
		case w.jobs <- job{env: env, dir: vars["RECUR_CWD"], name: event, sess: sess, maxOutput: limit, done: done}:
		default:
			r.fail(w.hook, event, "queue full, event dropped")
			done()
		}
	}
}

type cappedBuffer struct {
	limit      int
	head, tail []byte
	size       int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	total := len(p)
	b.size += total
	if b.limit <= 0 {
		b.head = append(b.head, p...)
		return total, nil
	}
	half := b.limit / 2
	if room := half - len(b.head); room > 0 {
		n := min(len(p), room)
		b.head, p = append(b.head, p[:n]...), p[n:]
	}
	b.tail = append(b.tail, p...)
	if keep := b.limit - half; len(b.tail) > keep {
		b.tail = append(b.tail[:0], b.tail[len(b.tail)-keep:]...)
	}
	return total, nil
}

func (b *cappedBuffer) Text() (string, bool) {
	head, tail := string(b.head), string(b.tail)
	if b.limit <= 0 || b.size <= b.limit {
		return head + tail, false
	}
	msg := fmt.Sprintf("\n[output truncated: %d of %d bytes omitted (max_output=%d, first %d + last %d kept); filter with rg, grep, head, tail or pass a larger max_output]\n",
		b.size-len(b.head)-len(b.tail), b.size, b.limit, len(b.head), len(b.tail))
	return head + msg + tail, true
}

func (r *Runner) run(h Hook, j job) {
	seconds := h.Timeout
	if seconds <= 0 {
		seconds = DefaultTimeout
	}
	limit := j.maxOutput
	if !h.SendOutput && (limit <= 0 || limit > 2048) {
		limit = 2048
	}
	out := &cappedBuffer{limit: limit}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	defer cancel()
	cmd := shell.Command(ctx, j.dir, j.env, h.Command)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = time.Second

	startTime := time.Now().UTC()
	if err := cmd.Start(); err != nil {
		r.fail(h, j.name, err.Error())
		if h.SendOutput && j.sess != "" {
			r.recordOutput(Result{SessionID: j.sess, HookName: h.Name, Command: h.Command, Event: j.name, StartTime: startTime, ExitCode: 127, Output: err.Error()})
		}
		return
	}

	err := cmd.Wait()

	exitCode := 0
	if err != nil {
		exitCode = 1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			exitCode = exit.ExitCode()
		}
	}
	if ctx.Err() != nil {
		err = fmt.Errorf("timed out after %ds", seconds)
		exitCode = 124
	}
	text, truncated := out.Text()
	if err != nil {
		r.fail(h, j.name, err.Error()+": "+strings.TrimSpace(text))
	}
	if h.SendOutput && j.sess != "" {
		r.recordOutput(Result{SessionID: j.sess, HookName: h.Name, Command: h.Command, Event: j.name, StartTime: startTime, ExitCode: exitCode, Output: text, Truncated: truncated})
	}
}

func (r *Runner) recordOutput(res Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outputs == nil {
		r.outputs = make(map[string][]Result)
	}
	total := 0
	for _, q := range r.outputs {
		total += len(q)
	}
	if total >= maxPendingGlobal {
		r.fail(Hook{Name: res.HookName}, res.Event, fmt.Sprintf("global pending output queue full (%d results), result dropped for session %s", total, res.SessionID))
		return
	}
	if len(r.outputs[res.SessionID]) >= maxPending {
		r.fail(Hook{Name: res.HookName}, res.Event, fmt.Sprintf("pending output queue full for session %s, result dropped", res.SessionID))
		return
	}
	r.outputs[res.SessionID] = append(r.outputs[res.SessionID], res)
}

func (r *Runner) fail(h Hook, event, problem string) {
	shell.AppendLog(r.Log, "event=%s command=%q: %s", event, h.Command, problem)
}
