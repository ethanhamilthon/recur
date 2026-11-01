package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"recur/internal/agent"
)

var DefaultStatusLeft = []string{
	`if [ -z "$RECUR_PROVIDER" ] || [ -z "$RECUR_MODEL" ]; then echo "no model selected"; elif [ -n "$RECUR_EFFORT" ]; then echo "$RECUR_PROVIDER · $RECUR_MODEL/$RECUR_EFFORT"; else echo "$RECUR_PROVIDER · $RECUR_MODEL"; fi`,
	`if [ -z "$RECUR_CTX_SIZE" ]; then echo "ctx ?"; elif [ "$RECUR_CTX_SIZE" -ge 999500 ] 2>/dev/null; then m=$(( RECUR_CTX_SIZE / 1000000 )); r=$(( (RECUR_CTX_SIZE % 1000000 + 50000) / 100000 )); if [ "$r" -ge 10 ]; then m=$(( m + 1 )); r=0; fi; if [ "$r" -eq 0 ]; then echo "ctx ~${m}M"; else echo "ctx ~${m}.${r}M"; fi; elif [ "$RECUR_CTX_SIZE" -ge 1000 ] 2>/dev/null; then echo "ctx ~$(( (RECUR_CTX_SIZE + 500) / 1000 ))K"; else echo "ctx ~$RECUR_CTX_SIZE"; fi`,
}

var DefaultStatusRight = []string{
	`if [ -n "$HOME" ] && [ "$RECUR_CWD" = "$HOME" ]; then echo "~"; elif [ -n "$HOME" ] && [[ "$RECUR_CWD" == "$HOME"/* ]]; then echo "~${RECUR_CWD#$HOME}"; else echo "$RECUR_CWD"; fi`,
}

type StatusSnapshot struct {
	Gen         uint64
	Cwd         string
	Home        string
	LeftCmds    []string
	RightCmds   []string
	Environment map[string]string
	Metadata    map[string]string
}

type statusEventMsg map[string]string
type statusInitMsg struct{}
type statusResultMsg struct {
	gen   uint64
	left  string
	right string
}

func listenForStatusEvents(ch <-chan map[string]string) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return statusEventMsg(ev)
	}
}

var (
	statusLogMu   sync.Mutex
	lastStatusLog time.Time
)

func logStatusFailure(home string, cmd string, err error) {
	if home == "" {
		return
	}
	statusLogMu.Lock()
	defer statusLogMu.Unlock()
	now := time.Now()
	if now.Sub(lastStatusLog) < time.Second {
		return
	}
	lastStatusLog = now
	f, oerr := os.OpenFile(filepath.Join(home, "status.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if oerr != nil {
		return
	}
	defer f.Close()
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	fmt.Fprintf(f, "%s cmd=%q: %s\n", now.Format(time.RFC3339), cmd, strings.ReplaceAll(msg, "\n", " "))
}

type limitWriter struct {
	w       io.Writer
	max     int
	written int
}

func (l *limitWriter) Write(p []byte) (n int, err error) {
	if l.written >= l.max {
		return len(p), nil
	}
	avail := l.max - l.written
	if len(p) <= avail {
		n, err = l.w.Write(p)
		l.written += n
		return n, err
	}
	n, err = l.w.Write(p[:avail])
	l.written += n
	return len(p), err
}

func sanitizeStatusSegment(s string) string {
	s = ansi.Strip(s)
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 32 || r == 127 {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func runSingleStatusCommand(ctx context.Context, cmdStr string, snap StatusSnapshot, sem chan struct{}) (string, error) {
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	cmdCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "/bin/bash", "--noprofile", "--norc", "-c", cmdStr)
	if snap.Cwd != "" {
		if fi, err := os.Stat(snap.Cwd); err == nil && fi.IsDir() {
			cmd.Dir = snap.Cwd
		}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}

	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "BASH_ENV=") {
			continue
		}
		env = append(env, e)
	}
	for k, v := range snap.Environment {
		if k == "BASH_ENV" {
			continue
		}
		env = append(env, k+"="+v)
	}
	for k, v := range snap.Metadata {
		env = append(env, k+"="+v)
	}
	env = append(env, "PAGER=cat", "GIT_PAGER=cat")
	cmd.Env = env

	var buf bytes.Buffer
	cmd.Stdout = &limitWriter{w: &buf, max: 1024}
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		logStatusFailure(snap.Home, cmdStr, err)
		return "", err
	}
	return sanitizeStatusSegment(buf.String()), nil
}

func evalStatusSnapshot(ctx context.Context, snap StatusSnapshot, sem chan struct{}) (string, string) {
	evalSide := func(cmds []string) string {
		if len(cmds) == 0 {
			return ""
		}
		type res struct {
			idx int
			val string
		}
		ch := make(chan res, len(cmds))
		var wg sync.WaitGroup
		for i, cmdStr := range cmds {
			wg.Add(1)
			go func(i int, cmd string) {
				defer wg.Done()
				val, err := runSingleStatusCommand(ctx, cmd, snap, sem)
				if err != nil || val == "" {
					return
				}
				ch <- res{idx: i, val: val}
			}(i, cmdStr)
		}
		wg.Wait()
		close(ch)
		outputs := make([]string, len(cmds))
		for r := range ch {
			outputs[r.idx] = r.val
		}
		var segments []string
		for _, out := range outputs {
			if out != "" {
				segments = append(segments, out)
			}
		}
		return strings.Join(segments, " · ")
	}

	left := evalSide(snap.LeftCmds)
	right := evalSide(snap.RightCmds)
	return left, right
}

func renderBottomBar(width int, left, right string) string {
	if width <= 0 {
		return ""
	}
	if width < 5 {
		return strings.Repeat(" ", width)
	}
	avail := width - 4
	lw := ansi.StringWidth(left)
	rw := ansi.StringWidth(right)
	var content string
	switch {
	case lw == 0 && rw == 0:
		content = strings.Repeat(" ", avail)
	case rw == 0:
		if lw > avail {
			left = ansi.Truncate(left, avail, "…")
		}
		gap := max(0, avail-ansi.StringWidth(left))
		content = ansi.Truncate(left+strings.Repeat(" ", gap), avail, "")
	case lw == 0:
		if rw > avail {
			right = ansi.TruncateLeft(right, avail, "…")
		}
		gap := max(0, avail-ansi.StringWidth(right))
		content = ansi.Truncate(strings.Repeat(" ", gap)+right, avail, "")
	default:
		if lw+rw+1 > avail {
			right = ansi.TruncateLeft(right, max(0, avail/3), "…")
			availLeft := max(0, avail-ansi.StringWidth(right)-1)
			left = ansi.Truncate(left, availLeft, "…")
		}
		gap := max(0, avail-ansi.StringWidth(left)-ansi.StringWidth(right))
		content = ansi.Truncate(left+strings.Repeat(" ", gap)+right, avail, "")
	}
	return "  " + content + "  "
}

func (m model) fallbackStatus() (string, string) {
	var provider, model, effort string
	if m.a != nil {
		s := m.a.Settings()
		provider, model, effort = s.Provider, s.Model, s.Effort
	}
	mdl := model
	if effort != "" {
		mdl += "/" + effort
	}
	ctx := "ctx ?"
	if m.contextInput != nil {
		ctx = "ctx ~" + formatContext(*m.contextInput)
	}
	left := provider + " · " + mdl + " · " + ctx
	if provider == "" || model == "" {
		left = "no model selected · " + ctx
	}
	right := shortPath(m.cwd)
	return sanitizeStatusSegment(left), sanitizeStatusSegment(right)
}

func (m model) bottomBar() string {
	left := m.statusLeft
	right := m.statusRight
	if !m.statusInit && left == "" && right == "" {
		left, right = m.fallbackStatus()
	}
	return renderBottomBar(m.width, left, right)
}

func (m *model) triggerStatusRefresh(eventVars map[string]string) tea.Cmd {
	if m.statusTornDown {
		return nil
	}
	m.statusGen++
	if m.statusCancel != nil {
		m.statusCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.statusCancel = cancel

	var settings agent.Settings
	var historyPath string
	if m.a != nil {
		settings = m.a.Settings()
		historyPath = m.a.HistoryPath
	}

	leftCmds := DefaultStatusLeft
	if settings.StatusLeft != nil {
		leftCmds = settings.StatusLeft
	}
	rightCmds := DefaultStatusRight
	if settings.StatusRight != nil {
		rightCmds = settings.StatusRight
	}

	meta := make(map[string]string, len(eventVars)+10)
	for k, v := range eventVars {
		if strings.HasPrefix(k, "RECUR_") {
			meta[k] = v
		}
	}
	meta["RECUR_CWD"] = m.cwd
	meta["RECUR_VERSION"] = Version
	meta["RECUR_PROVIDER"] = settings.Provider
	meta["RECUR_MODEL"] = settings.Model
	meta["RECUR_EFFORT"] = settings.Effort
	if m.contextInput != nil {
		meta["RECUR_CTX_SIZE"] = strconv.FormatInt(*m.contextInput, 10)
	} else if _, ok := meta["RECUR_CTX_SIZE"]; !ok {
		meta["RECUR_CTX_SIZE"] = ""
	}
	if m.cancelling {
		meta["RECUR_STATE"] = "cancelling"
	} else if m.busy {
		meta["RECUR_STATE"] = "busy"
	} else {
		meta["RECUR_STATE"] = "idle"
	}
	if _, ok := meta["RECUR_SESSION_ID"]; !ok {
		meta["RECUR_SESSION_ID"] = strings.TrimSuffix(filepath.Base(historyPath), ".jsonl")
	}

	envCopy := make(map[string]string, len(settings.Environment))
	for k, v := range settings.Environment {
		envCopy[k] = v
	}

	home := os.Getenv("RECUR_HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".recur")
		}
	}

	snap := StatusSnapshot{
		Gen:         m.statusGen,
		Cwd:         m.cwd,
		Home:        home,
		LeftCmds:    slices.Clone(leftCmds),
		RightCmds:   slices.Clone(rightCmds),
		Environment: envCopy,
		Metadata:    meta,
	}

	if m.statusSem == nil {
		m.statusSem = make(chan struct{}, 4)
	}
	sem := m.statusSem
	gen := m.statusGen

	return func() tea.Msg {
		left, right := evalStatusSnapshot(ctx, snap, sem)
		if ctx.Err() != nil {
			return nil
		}
		return statusResultMsg{gen: gen, left: left, right: right}
	}
}
