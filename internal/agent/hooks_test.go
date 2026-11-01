package agent

import (
	"context"
	"encoding/json"
	"encoding/xml"
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

func TestConfigSourcesPriority(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)
	flagged := filepath.Join(t.TempDir(), "flag.toml")

	open := func(files ConfigFiles) *Agent {
		t.Helper()
		a, err := NewWith(cwd, files)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(a.Close)
		return a
	}

	// Implicit global missing: neutral built-in defaults used
	a := open(ConfigFiles{})
	if len(a.Sources) != 1 || a.Sources[0].Origin != "none" {
		t.Fatalf("defaults sources: %+v", a.Sources)
	}

	// Home config.toml exists
	write(t, filepath.Join(home, "config.toml"), "[hooks.h1]\nevents = ['idle']\ncommand = 'true'\n")
	a = open(ConfigFiles{})
	if len(a.Sources) != 1 || a.Sources[0].Origin != "home" {
		t.Fatalf("home sources: %+v", a.Sources)
	}

	// Project recur.toml exists
	write(t, filepath.Join(cwd, "recur.toml"), "[hooks.h2]\nevents = ['turn_start']\ncommand = 'true'\n")
	a = open(ConfigFiles{})
	if len(a.Sources) != 2 || a.Sources[0].Origin != "home" || a.Sources[1].Origin != "project" {
		t.Fatalf("project sources: %+v", a.Sources)
	}

	// Explicit --config
	write(t, flagged, "[hooks.h3]\nevents = ['turn_end']\ncommand = 'true'\n")
	a = open(ConfigFiles{Config: flagged})
	if len(a.Sources) != 2 || a.Sources[0].Origin != "home" || a.Sources[1].Origin != "flag" {
		t.Fatalf("flag sources: %+v", a.Sources)
	}

	// --no-extend skips global
	a = open(ConfigFiles{Config: flagged, NoExtend: true})
	if len(a.Sources) != 1 || a.Sources[0].Origin != "flag" {
		t.Fatalf("no-extend sources: %+v", a.Sources)
	}

	// Headless without explicit config has no hooks
	a = open(ConfigFiles{Headless: true})
	if len(a.hooks.DrainOutputs("any")) != 0 {
		t.Fatalf("headless without explicit config must have no hooks")
	}

	// Headless with explicit config has hooks
	a = open(ConfigFiles{Headless: true, Config: flagged})
	_ = a

	// Missing explicit --config must fail
	if _, err := NewWith(cwd, ConfigFiles{Config: filepath.Join(cwd, "missing.toml")}); err == nil {
		t.Fatal("missing --config file must fail")
	}
}

func answer(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello back\"}]}]}}\n\n")
}

func completionServer(t *testing.T, handler func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w)
	}))
	t.Cleanup(server.Close)
	return server
}

func newHookAgent(t *testing.T, server *httptest.Server) *Agent {
	t.Helper()
	t.Setenv("RECUR_HOME", t.TempDir())
	cwd := t.TempDir()
	a, err := New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	configureTestAgent(t, a, Settings{Provider: "test", Model: "test-model", Providers: map[string]Provider{"test": {BaseURL: server.URL}}})
	return a
}

func recorder(t *testing.T, a *Agent, events string, names ...string) func() []string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "events")
	line := "$RECUR_EVENT"
	for _, v := range names {
		line += "|$" + v
	}
	a.hooks.Set([]hooks.Hook{{
		Events:  strings.Split(events, ","),
		Command: fmt.Sprintf(`echo "%s" >> %s`, line, out),
	}})
	return func() []string {
		t.Helper()
		a.hooks.Wait(5 * time.Second)
		data, _ := os.ReadFile(out)
		return strings.Fields(string(data))
	}
}

func joined(get func() []string) string {
	return strings.Join(get(), " ")
}

func TestTurnStatusCancelledAndInputVariables(t *testing.T) {
	a := newHookAgent(t, completionServer(t, answer))
	out := filepath.Join(t.TempDir(), "out")
	a.hooks.Set([]hooks.Hook{{
		Events:  []string{"turn_start", "turn_end", "steer_accepted"},
		Command: `echo "$RECUR_EVENT|$RECUR_TURN_STATUS|$RECUR_TURN_SOURCE|$(cat "$RECUR_MESSAGE_FILE" 2>/dev/null)" >> ` + out,
	}})
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
	want := "turn_start||user|never sent\nturn_end|cancelled|user|\n" +
		"turn_start||notify|<notify>\ntick\n</notify>\nsteer_accepted|||and also\nturn_end|ok|notify|\n"
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
	a.hooks.Set([]hooks.Hook{{
		Events:  []string{"notify_received", "image_attached"},
		Command: `echo "$RECUR_EVENT|$RECUR_IMAGE_SOURCE|$(cat "$RECUR_NOTIFY_FILE" 2>/dev/null)" >> ` + out,
	}})
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

func TestHookSendOutputDrainedOnNextTurn(t *testing.T) {
	a := newHookAgent(t, completionServer(t, answer))
	a.hooks.Set([]hooks.Hook{{
		Name:       "post-turn",
		Events:     []string{"turn_end"},
		Command:    `[ "$RECUR_TURN_SOURCE" = "user" ] && echo "build success" || true`,
		SendOutput: true,
	}})

	// First turn runs and triggers turn_end hook
	if err := a.Run(context.Background(), "first", func(Event) {}); err != nil {
		t.Fatal(err)
	}
	a.hooks.Wait(3 * time.Second)

	// Second turn drains the hook output before the user's second message
	var notices []string
	if err := a.Run(context.Background(), "second", func(e Event) {
		if e.Kind == "notice" {
			notices = append(notices, e.Text)
		}
	}); err != nil {
		t.Fatal(err)
	}

	const command = `[ "$RECUR_TURN_SOURCE" = "user" ] && echo "build success" || true`
	found := false
	for _, n := range notices {
		if strings.Contains(n, "build success") && strings.Contains(n, "<hook_output") {
			found = true
			if got := hookCommandAttr(t, n); got != command {
				t.Fatalf("emitted notice command = %q, want %q", got, command)
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected hook output delivered as notice on second turn, got %v", notices)
	}

	saved := false
	for _, item := range a.History {
		var msg struct{ Role, Content string }
		if json.Unmarshal(item, &msg) != nil || msg.Role != "user" || !strings.Contains(msg.Content, "<hook_output") {
			continue
		}
		saved = true
		if got := hookCommandAttr(t, msg.Content); got != command {
			t.Fatalf("saved history command = %q, want %q", got, command)
		}
	}
	if !saved {
		t.Fatal("expected hook output saved as user history")
	}
}

// hookCommandAttr decodes the hook_output command attribute from an XML notice.
func hookCommandAttr(t *testing.T, text string) string {
	t.Helper()
	var doc struct {
		HookOutput struct {
			Command string `xml:"command,attr"`
		} `xml:"hook_output"`
	}
	if err := xml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("invalid hook output XML: %v\n%s", err, text)
	}
	return doc.HookOutput.Command
}

func TestHeadlessHookInheritanceAndValidation(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)

	globalCfg := `provider = "p"
model = "m"
[providers.p]
base_url = "https://example.com"

[hooks.global_hook]
events = ["turn_end"]
command = "echo global"

[hooks.partial_override]
events = ["turn_start"]
command = "echo from-global"
timeout = 10
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(globalCfg), 0600); err != nil {
		t.Fatal(err)
	}

	// 1. Implicit headless: no hooks active
	aImplicit, err := NewWith(cwd, ConfigFiles{Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	defer aImplicit.Close()
	if aImplicit.hooks != nil && len(aImplicit.hooks.List()) > 0 {
		t.Fatalf("expected 0 hooks in implicit headless, got %d", len(aImplicit.hooks.List()))
	}

	// 2. Explicit headless without --no-extend: inherits global hooks, and partial override inherits command/events
	explicitCfg := `provider = "p"
model = "m"
[providers.p]
base_url = "https://example.com"

[hooks.partial_override]
timeout = 5

[hooks.explicit_hook]
events = ["turn_end"]
command = "echo explicit"
`
	explicitPath := filepath.Join(cwd, "explicit.toml")
	if err := os.WriteFile(explicitPath, []byte(explicitCfg), 0600); err != nil {
		t.Fatal(err)
	}

	aExplicit, err := NewWith(cwd, ConfigFiles{Headless: true, Config: explicitPath})
	if err != nil {
		t.Fatalf("explicit headless with partial override should succeed, got: %v", err)
	}
	defer aExplicit.Close()
	hooksList := aExplicit.hooks.List()
	hookNames := map[string]bool{}
	for _, h := range hooksList {
		hookNames[h.Name] = true
	}
	if !hookNames["global_hook"] || !hookNames["partial_override"] || !hookNames["explicit_hook"] {
		t.Fatalf("expected global_hook, partial_override, and explicit_hook; got: %+v", hookNames)
	}

	// 3. Explicit headless with --no-extend: global hooks are skipped
	noExtendCfg := `provider = "p"
model = "m"
[providers.p]
base_url = "https://example.com"

[hooks.explicit_hook]
events = ["turn_end"]
command = "echo explicit"
`
	noExtendPath := filepath.Join(cwd, "no_extend.toml")
	if err := os.WriteFile(noExtendPath, []byte(noExtendCfg), 0600); err != nil {
		t.Fatal(err)
	}
	aNoExt, err := NewWith(cwd, ConfigFiles{Headless: true, Config: noExtendPath, NoExtend: true})
	if err != nil {
		t.Fatal(err)
	}
	defer aNoExt.Close()
	noExtHooks := aNoExt.hooks.List()
	if len(noExtHooks) != 1 || noExtHooks[0].Name != "explicit_hook" {
		t.Fatalf("expected only explicit_hook with --no-extend, got: %+v", noExtHooks)
	}

	// 4. Missing required fields without global definition fails validateHooks (not discarded)
	missingReqCfg := `provider = "p"
model = "m"
[providers.p]
base_url = "https://example.com"

[hooks.incomplete]
timeout = 5
`
	missingReqPath := filepath.Join(cwd, "incomplete.toml")
	if err := os.WriteFile(missingReqPath, []byte(missingReqCfg), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = NewWith(cwd, ConfigFiles{Headless: true, Config: missingReqPath})
	if err == nil {
		t.Fatal("expected validateHooks error for incomplete hook, but got nil")
	}
}
