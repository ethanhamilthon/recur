package agent

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Fire emits a hook event. kv holds variable name, value pairs added to the
// session variables; names ending in _FILE carry text (see hooks.Runner.Fire).
// It is safe to call from any goroutine and never blocks.
func (a *Agent) Fire(event string, kv ...string) {
	if a.hooks == nil {
		return
	}
	vars := map[string]string{
		"RECUR_SESSION_ID": strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl"),
		"RECUR_CWD":        a.REPL.Dir,
		"RECUR_PROVIDER":   a.settings.Provider,
		"RECUR_MODEL":      a.settings.Model,
		"RECUR_EFFORT":     a.settings.Effort,
	}
	if a.socketDir != "" {
		vars["RECUR_ENDPOINT"] = filepath.Join(a.socketDir, "socket")
	}
	for i := 0; i+1 < len(kv); i += 2 {
		vars[kv[i]] = kv[i+1]
	}
	a.hooks.Fire(event, vars)
}

// SetMode records the run mode ("tui" or "headless") exported as RECUR_MODE.
func (a *Agent) SetMode(mode string) { a.hooks.Mode = mode }

// Start reports process and session start once the interface is ready.
func (a *Agent) Start(version string) {
	source := "startup"
	if a.HasSavedSession() {
		source = "resume"
	}
	a.Fire("recur_start", "RECUR_VERSION", version)
	a.Fire("session_start", "RECUR_SESSION_SOURCE", source)
	a.titleChanged()
}

// Finish reports session end and exit, then gives queued hooks a moment to run.
func (a *Agent) Finish(code int) {
	a.Fire("session_end", "RECUR_REASON", "exit")
	a.Fire("recur_exit", "RECUR_EXIT_CODE", strconv.Itoa(code))
	a.hooks.Wait(3 * time.Second)
}

// titleChanged fires session_title_change when the /resume title differs from
// the last one reported: the /rename name or else the first user message.
func (a *Agent) titleChanged() {
	title := a.title
	for i := 0; title == "" && i < len(a.History); i++ {
		for _, r := range RecordsForItem(a.History[i]) {
			if r.Kind == "user" {
				title = strings.Join(strings.Fields(r.Text), " ")
				break
			}
		}
	}
	if title != "" && title != a.lastTitle {
		a.lastTitle = title
		a.Fire("session_title_change", "RECUR_SESSION_TITLE", title)
	}
}

func ms(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }

// clip bounds error text exported through the environment.
func clip(text string) string {
	if len(text) > 2000 {
		return strings.ToValidUTF8(text[:2000], "") + "…"
	}
	return text
}
