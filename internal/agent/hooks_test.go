package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"recur/internal/hooks"
	"recur/internal/repl"
)

const hookToml = "[[hook]]\nevent = \"idle\"\ncommand = \"true\"\n"

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func source(a *Agent, name string) Source {
	for _, s := range a.Sources {
		if s.Name == name {
			return s
		}
	}
	return Source{}
}

// hookOrigins lists the hooks.toml layers an agent read, in order.
func hookOrigins(a *Agent) string {
	var out []string
	for _, s := range a.Sources {
		if s.Name == "hooks.toml" {
			out = append(out, s.Origin)
		}
	}
	return strings.Join(out, "+")
}

func TestConfigSourcesPriority(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)
	local, flagged := filepath.Join(cwd, ".recur"), filepath.Join(t.TempDir(), "flag.toml")
	open := func(files ConfigFiles) *Agent {
		t.Helper()
		a, err := NewWith(cwd, files)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(a.Close)
		return a
	}
	a := open(ConfigFiles{})
	if len(a.Sources) != 3 || source(a, "hooks.toml").Origin != "none" || source(a, "hooks.toml").Path != "" ||
		source(a, "system.md") != (Source{"system.md", filepath.Join(home, "system.md"), "home"}) {
		t.Fatalf("defaults: %+v", a.Sources)
	}
	if _, err := os.Stat(filepath.Join(home, "hooks.toml")); !os.IsNotExist(err) {
		t.Fatal("hooks.toml must not be created automatically")
	}
	write(t, filepath.Join(home, "hooks.toml"), hookToml)
	if a = open(ConfigFiles{}); source(a, "hooks.toml") != (Source{"hooks.toml", filepath.Join(home, "hooks.toml"), "home"}) {
		t.Fatalf("home: %+v", a.Sources)
	}
	write(t, filepath.Join(local, "hooks.toml"), hookToml)
	write(t, filepath.Join(local, "prefix.sh"), "echo local")
	a = open(ConfigFiles{})
	if hookOrigins(a) != "home+project" || source(a, "prefix.sh").Origin != "project" || source(a, "system.md").Origin != "home" || a.LocalConfig != local {
		t.Fatalf("project: %+v", a.Sources)
	}
	write(t, flagged, hookToml)
	if a = open(ConfigFiles{Hooks: flagged}); hookOrigins(a) != "home+project+flag" {
		t.Fatalf("flag: %+v", a.Sources)
	}
	if a = open(ConfigFiles{Headless: true}); hookOrigins(a) != "none" {
		t.Fatalf("headless: %+v", a.Sources)
	}
	if a = open(ConfigFiles{Headless: true, Hooks: flagged}); hookOrigins(a) != "flag" {
		t.Fatalf("headless with --hooks: %+v", a.Sources)
	}
	if _, err := NewWith(cwd, ConfigFiles{Hooks: filepath.Join(cwd, "missing.toml")}); err == nil {
		t.Fatal("a missing --hooks file must fail")
	}
	a = open(ConfigFiles{})
	write(t, filepath.Join(home, "settings.toml"), "enable_local_recur = false\n")
	if err := a.Reload(); err != nil || !a.LocalIgnored || hookOrigins(a) != "home" {
		t.Fatalf("reload must refresh sources: %v %+v", err, a.Sources)
	}
	if a = open(ConfigFiles{}); !a.LocalIgnored || hookOrigins(a) != "home" || source(a, "prefix.sh").Origin != "home" {
		t.Fatalf("disabled local config: ignored=%v %+v", a.LocalIgnored, a.Sources)
	}
}

func TestHookLayersStackAndDeduplicate(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, "settings.toml"), "enable_local_recur = true\n")
	flagged := filepath.Join(t.TempDir(), "flag.toml")
	write(t, filepath.Join(home, "hooks.toml"), hookToml+"[[hook]]\nevent = \"turn_end\"\ncommand = \"home\"\n")
	write(t, filepath.Join(cwd, ".recur", "hooks.toml"), hookToml+"[[hook]]\nevents = [\"turn_end\"]\ncommand = \"project\"\n")
	write(t, flagged, "[[hook]]\nevent = \"idle\"\ncommand = \"flag\"\n")
	commands := func(files ConfigFiles) string {
		t.Helper()
		c, err := loadConfiguration(home, cwd, files)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range c.hooks {
			out = append(out, h.Command)
		}
		return strings.Join(out, ",")
	}
	if got := commands(ConfigFiles{Hooks: flagged}); got != "true,home,project,flag" {
		t.Fatalf("interactive layers: %s", got)
	}
	if got := commands(ConfigFiles{Hooks: flagged, Headless: true}); got != "flag" {
		t.Fatalf("headless layers: %s", got)
	}
	write(t, filepath.Join(cwd, ".recur", "hooks.toml"), "[[hook]]\nevent = \"nope\"\ncommand = \"x\"\n")
	if _, err := loadConfiguration(home, cwd, ConfigFiles{}); err == nil || !strings.Contains(err.Error(), filepath.Join(cwd, ".recur", "hooks.toml")) {
		t.Fatalf("invalid project layer must name its file: %v", err)
	}
}

func TestInvalidHooksFailLoadingAndReloadKeepsOldHooks(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)
	out := filepath.Join(t.TempDir(), "out")
	write(t, filepath.Join(home, "hooks.toml"), "[[hook]]\nevent = \"idle\"\ncommand = 'echo \"$RECUR_EVENT\" >> "+out+"'\n")
	a, err := New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	write(t, filepath.Join(home, "hooks.toml"), "[[hook]]\nevent = \"nope\"\ncommand = \"true\"\n")
	if err = a.Reload(); err == nil || !strings.Contains(err.Error(), filepath.Join(home, "hooks.toml")) || !strings.Contains(err.Error(), "unknown event") {
		t.Fatalf("reload error = %v", err)
	}
	a.Fire("idle")
	a.hooks.Wait(5 * time.Second)
	if data, _ := os.ReadFile(out); string(data) != "idle\n" {
		t.Fatalf("a failed reload must keep the running hooks, got %q", data)
	}
	if _, err = New(cwd); err == nil {
		t.Fatal("startup with an invalid hooks.toml must fail")
	}
}

// recorder installs one hook that appends "EVENT|value|..." lines to a file;
// the returned function waits for the hooks and returns the lines.
func recorder(t *testing.T, a *Agent, events string, vars ...string) func() []string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "events")
	line := "$RECUR_EVENT"
	for _, v := range vars {
		line += "|$" + v
	}
	// Written to hooks.toml as well, so the hook survives /new, /resume and /reload.
	toml := "[[hook]]\nevents = [\"" + strings.ReplaceAll(events, ",", "\", \"") + "\"]\ncommand = 'echo \"" + line + "\" >> " + out + "'\n"
	write(t, filepath.Join(a.home, "hooks.toml"), toml)
	list, err := hooks.Parse(toml)
	if err != nil {
		t.Fatal(err)
	}
	a.hooks.Set(list)
	return func() []string {
		t.Helper()
		a.hooks.Wait(5 * time.Second)
		data, _ := os.ReadFile(out)
		return strings.Fields(string(data))
	}
}

func completionServer(t *testing.T, body func(w http.ResponseWriter)) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { body(w) }))
	t.Cleanup(server.Close)
	return server
}

func answer(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":7,\"output_tokens\":3},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n\n")
}

func newHookAgent(t *testing.T, server *httptest.Server) *Agent {
	t.Helper()
	t.Setenv("RECUR_HOME", t.TempDir())
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	configureTestAgent(t, a, Settings{Provider: "p", Model: "m", Providers: map[string]Provider{"p": {BaseURL: server.URL}}})
	return a
}

func TestTurnEmitsOrderedEvents(t *testing.T) {
	a := newHookAgent(t, completionServer(t, answer))
	got := recorder(t, a, "*", "RECUR_TURN_STATUS", "RECUR_SESSION_TITLE", "RECUR_INPUT_TOKENS", "RECUR_OUTPUT_TOKENS", "RECUR_TURN_SOURCE")
	if err := a.Run(context.Background(), "hello  world", func(Event) {}); err != nil {
		t.Fatal(err)
	}
	row := func(fields ...string) string { return strings.Join(fields, "|") }
	want := []string{
		row("turn_start", "", "", "", "", "user"), row("prefix_start", "", "", "", "", ""), row("prefix_end", "", "", "", "", ""),
		row("session_title_change", "", "hello world", "", "", ""), row("model_request_start", "", "", "", "", ""),
		row("model_request_end", "", "", "7", "3", ""), row("turn_end", "ok", "", "", "", ""),
	}
	// Fields splits on spaces inside the title, so compare after rejoining.
	if joined := strings.Join(got(), " "); joined != strings.Join(want, " ") {
		t.Fatalf("events:\n got %s\nwant %s", joined, strings.Join(want, " "))
	}
}

func joined(got func() []string) string { return strings.Join(got(), " ") }

func TestStartFinishAndSessionVariables(t *testing.T) {
	a := newHookAgent(t, completionServer(t, answer))
	a.SetMode("headless")
	got := recorder(t, a, "*", "RECUR_VERSION", "RECUR_SESSION_SOURCE", "RECUR_REASON", "RECUR_EXIT_CODE", "RECUR_MODE", "RECUR_MODEL")
	a.Start("9.9")
	a.Finish(2)
	want := "recur_start|9.9||||headless|m session_start||startup|||headless|m session_end|||exit||headless|m recur_exit||||2|headless|m"
	if joined(got) != want {
		t.Fatalf("got  %s\nwant %s", joined(got), want)
	}
	// Variables common to every event.
	out := filepath.Join(t.TempDir(), "vars")
	a.hooks.Set([]hooks.Hook{{Events: []string{"idle"}, Command: `echo "$RECUR_SESSION_ID|$RECUR_CWD|$RECUR_ENDPOINT|$RECUR_PROVIDER|$RECUR_EFFORT" > ` + out}})
	a.Fire("idle")
	a.hooks.Wait(5 * time.Second)
	data, _ := os.ReadFile(out)
	id := strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl")
	if want := id + "|" + a.REPL.Dir + "|" + filepath.Join(a.socketDir, "socket") + "|p|"; strings.TrimSpace(string(data)) != want {
		t.Fatalf("vars = %q, want %q", data, want)
	}
}

func TestSessionSwitchRenameAndModelEvents(t *testing.T) {
	a := newHookAgent(t, completionServer(t, answer))
	first := strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl")
	got := recorder(t, a, "session_start,session_switch,session_end,session_title_change,model_change",
		"RECUR_SESSION_SOURCE", "RECUR_REASON", "RECUR_PREV_SESSION_ID", "RECUR_SESSION_TITLE", "RECUR_PREV_MODEL", "RECUR_EFFORT")
	if err := a.Rename("Plan  A"); err != nil {
		t.Fatal(err)
	}
	if err := a.NewSession(); err != nil {
		t.Fatal(err)
	}
	if err := a.Select("p", "m2", "high"); err != nil {
		t.Fatal(err)
	}
	want := "session_title_change||||Plan A|| session_end||switch||||" +
		" session_start|new||" + first + "||| session_switch|||" + first + "||| model_change|||||m|high"
	if joined(got) != want {
		t.Fatalf("got  %s\nwant %s", joined(got), want)
	}
}

func TestResumedSessionReportsItsTitle(t *testing.T) {
	a := newHookAgent(t, completionServer(t, answer))
	if err := a.Run(context.Background(), "first question", func(Event) {}); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl")
	if err := a.NewSession(); err != nil {
		t.Fatal(err)
	}
	got := recorder(t, a, "session_start,session_title_change", "RECUR_SESSION_SOURCE", "RECUR_SESSION_TITLE")
	if err := a.Resume(id); err != nil {
		t.Fatal(err)
	}
	if want := "session_start|resume| session_title_change||first question"; joined(got) != want {
		t.Fatalf("got %q", joined(got))
	}
}

func TestShellEvents(t *testing.T) {
	a := newHookAgent(t, completionServer(t, answer))
	out := filepath.Join(t.TempDir(), "out")
	a.hooks.Set([]hooks.Hook{{Events: []string{"shell_start", "shell_end"},
		Command: `echo "$RECUR_EVENT|$RECUR_EXIT_CODE|$RECUR_TIMED_OUT|$RECUR_TIMEOUT|$(cat "$RECUR_COMMAND_FILE" 2>/dev/null)" >> ` + out}})
	if _, err := a.REPL.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.runTool(context.Background(), "bash", `{"command":"echo hi; exit 4","timeout":7,"max_output":null}`)
	a.runTool(context.Background(), "other", `{}`)
	a.hooks.Wait(5 * time.Second)
	data, _ := os.ReadFile(out)
	want := "shell_start|||7|echo hi; exit 4\nshell_end|4|false||\n"
	if string(data) != want {
		t.Fatalf("got %q want %q", data, want)
	}
}

func TestPrefixFailureIsReported(t *testing.T) {
	a := newHookAgent(t, completionServer(t, answer))
	a.REPL.Prefix = "if then"
	got := recorder(t, a, "prefix_end,turn_end,error", "RECUR_PREFIX_OK", "RECUR_TURN_STATUS")
	if err := a.Run(context.Background(), "hi", func(Event) {}); err == nil {
		t.Fatal("expected prefix failure")
	}
	if want := "prefix_end|false| error|| turn_end||error"; joined(got) != want {
		t.Fatalf("got %q", joined(got))
	}
}

func TestTurnStatusCancelledAndInputVariables(t *testing.T) {
	a := newHookAgent(t, completionServer(t, answer))
	out := filepath.Join(t.TempDir(), "out")
	a.hooks.Set([]hooks.Hook{{Events: []string{"turn_start", "turn_end", "steer_accepted"},
		Command: `echo "$RECUR_EVENT|$RECUR_TURN_STATUS|$RECUR_TURN_SOURCE|$(cat "$RECUR_MESSAGE_FILE" 2>/dev/null)" >> ` + out}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Run(ctx, "never sent", func(Event) {}); err == nil {
		t.Fatal("expected cancellation")
	}
	steering := &Steering{}
	steering.Push("and also")
	if err := a.Run(context.Background(), "<notify>\ntick\n</notify>", func(Event) {}, steering); err != nil {
		t.Fatal(err)
	}
	a.hooks.Wait(5 * time.Second)
	data, _ := os.ReadFile(out)
	want := "turn_start||user|never sent\nturn_end|cancelled||\n" +
		"turn_start||notify|<notify>\ntick\n</notify>\nsteer_accepted|||and also\nturn_end|ok||\n"
	if string(data) != want {
		t.Fatalf("got %q\nwant %q", data, want)
	}
}

func TestErrorAndRetryEvents(t *testing.T) {
	calls := 0
	a := newHookAgent(t, completionServer(t, func(w http.ResponseWriter) {
		if calls++; calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	got := recorder(t, a, "model_retry,error,turn_end", "RECUR_ATTEMPT", "RECUR_TURN_STATUS")
	if err := a.Run(context.Background(), "hi", func(Event) {}); err == nil {
		t.Fatal("expected failure")
	}
	if want := "model_retry|2| error|| turn_end||error"; joined(got) != want {
		t.Fatalf("got %q", joined(got))
	}
}

func TestInputQueueEvents(t *testing.T) {
	a := newHookAgent(t, completionServer(t, answer))
	out := filepath.Join(t.TempDir(), "out")
	a.hooks.Set([]hooks.Hook{{Events: []string{"notify_received", "image_attached"},
		Command: `echo "$RECUR_EVENT|$RECUR_IMAGE_SOURCE|$(cat "$RECUR_NOTIFY_FILE" 2>/dev/null)" >> ` + out}})
	a.inbox <- "<notify>\nbuild done\n</notify>"
	if got := a.Notifications(); len(got) != 1 {
		t.Fatalf("notifications = %v", got)
	}
	a.views <- repl.Image{URL: "data:image/png;base64,AA=="}
	if got := a.viewed(); len(got) != 1 {
		t.Fatalf("views = %v", got)
	}
	a.hooks.Wait(5 * time.Second)
	data, _ := os.ReadFile(out)
	if want := "notify_received||<notify>\nbuild done\n</notify>\nimage_attached|view|\n"; string(data) != want {
		t.Fatalf("got %q want %q", data, want)
	}
}

func TestCompactEvents(t *testing.T) {
	fail := false
	a, closeAll := summaryAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeSummary(w, "condensed")
	})
	defer closeAll()
	got := recorder(t, a, "compact_start,compact_end", "RECUR_COMPACT_REASON", "RECUR_COMPACT_STATUS")
	a.History = compactHistory(t, 4)
	if _, err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	fail = true
	a.History = compactHistory(t, 4)
	a.compactIfNeeded(context.Background(), func(Event) {})
	if _, err := a.Compact(context.Background(), ""); err == nil {
		t.Fatal("expected failure")
	}
	a.History = a.History[:2]
	if _, err := a.Compact(context.Background(), ""); err != nil { // nothing to compact: no events
		t.Fatal(err)
	}
	if want := "compact_start|manual| compact_end||ok compact_start|auto| compact_end||error compact_start|manual| compact_end||error"; joined(got) != want {
		t.Fatalf("got %q", joined(got))
	}
}
