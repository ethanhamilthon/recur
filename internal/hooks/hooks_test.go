package hooks

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestFireRunsMatchingHooksInOrderWithVariables(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	r := &Runner{Mode: "tui"}
	r.Set([]Hook{
		{Events: []string{"turn_start", "turn_end"}, Command: `echo "$RECUR_EVENT $RECUR_MODE $X $(cat "$Y_FILE") $PWD" >> ` + out},
		{Events: []string{"idle"}, Command: "echo wrong >> " + out},
	})
	r.Fire("turn_start", map[string]string{"X": "1", "Y_FILE": "payload\nline2", "RECUR_CWD": dir})
	r.Fire("model_request_start", nil)
	r.Fire("turn_end", map[string]string{"X": "2", "Y_FILE": "p2", "RECUR_CWD": dir})
	r.Wait(5 * time.Second)
	got := strings.Split(strings.TrimSpace(read(t, out)), "\n")
	resolved, _ := filepath.EvalSymlinks(dir)
	if len(got) != 3 || !strings.HasPrefix(got[0], "turn_start tui 1 payload") || !strings.HasPrefix(got[1], "line2 ") || !strings.HasSuffix(got[1], resolved) ||
		!strings.HasPrefix(got[2], "turn_end tui 2 p2 ") || !strings.HasSuffix(got[2], resolved) {
		t.Fatalf("got %q", got)
	}
}

func TestWildcardAndSequenceVariables(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	r := &Runner{}
	r.Set([]Hook{{Events: []string{"*"}, Command: `echo "$RECUR_SEQ $RECUR_EVENT" >> ` + out + `; test -n "$RECUR_TS"`}})
	r.Fire("idle", nil)
	r.Fire("error", nil)
	r.Wait(5 * time.Second)
	lines := strings.Split(strings.TrimSpace(read(t, out)), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], " idle") || !strings.HasSuffix(lines[1], " error") || lines[0] >= lines[1] {
		t.Fatalf("lines = %q", lines)
	}
}

func TestPayloadFilesAreRemovedAfterHooksFinish(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{}
	r.Set([]Hook{{Events: []string{"idle"}, Command: `echo "$M_FILE" > ` + filepath.Join(dir, "path")}})
	r.Fire("idle", map[string]string{"M_FILE": "text"})
	r.Wait(5 * time.Second)
	path := strings.TrimSpace(read(t, filepath.Join(dir, "path")))
	if path == "" {
		t.Fatal("hook did not receive a path")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("payload file survived: %v", err)
	}
}

func TestFailuresAndTimeoutsAreLoggedAndDoNotBlock(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "hooks.log")
	r := &Runner{Log: log}
	r.Set([]Hook{
		{Events: []string{"idle"}, Command: "echo boom >&2; exit 3"},
		{Events: []string{"error"}, Command: "sleep 30", Timeout: 1},
	})
	started := time.Now()
	r.Fire("idle", nil)
	r.Fire("error", nil)
	if time.Since(started) > time.Second {
		t.Fatal("Fire blocked")
	}
	r.Wait(10 * time.Second)
	text := read(t, log)
	if !strings.Contains(text, "event=idle") || !strings.Contains(text, "exit status 3: boom") || !strings.Contains(text, "event=error") || !strings.Contains(text, "timed out after 1s") {
		t.Fatalf("log = %q", text)
	}
}

func TestSetKeepsQueuesOfIdenticalHooks(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	hook := Hook{Events: []string{"*"}, Command: `sleep 0.2; echo "$RECUR_EVENT" >> ` + out}
	r := &Runner{}
	r.Set([]Hook{hook})
	r.Fire("session_end", nil)
	r.Set([]Hook{hook}) // /new re-reads the same config
	r.Fire("session_start", nil)
	r.Wait(5 * time.Second)
	if got := read(t, out); got != "session_end\nsession_start\n" {
		t.Fatalf("order lost across Set: %q", got)
	}
}

func TestNilAndEmptyRunnersAreHarmless(t *testing.T) {
	var r *Runner
	r.Fire("idle", nil)
	r.Wait(time.Second)
	(&Runner{}).Fire("idle", nil)
}

func TestEveryEventIsDocumented(t *testing.T) {
	doc := read(t, filepath.Join("..", "docs", "docs", "hooks.md"))
	for _, event := range Events {
		if !strings.Contains(doc, "`"+event+"`") {
			t.Errorf("event %s is missing from recur docs hooks", event)
		}
	}
}

func TestSendOutputQueuesResultForSession(t *testing.T) {
	r := &Runner{MaxOutput: 1024}
	r.Set([]Hook{
		{Name: "myhook", Events: []string{"turn_end"}, Command: "echo 'hello from hook'", SendOutput: true},
	})
	r.Fire("turn_end", map[string]string{
		"RECUR_SESSION_ID": "sess-1",
		"RECUR_CWD":        t.TempDir(),
	})
	r.Wait(5 * time.Second)

	// Another session has nothing
	if out := r.DrainOutputs("sess-2"); len(out) != 0 {
		t.Fatalf("expected no outputs for sess-2, got %v", out)
	}

	// sess-1 has the output
	out := r.DrainOutputs("sess-1")
	if len(out) != 1 {
		t.Fatalf("expected 1 output for sess-1, got %d", len(out))
	}
	res := out[0]
	if res.HookName != "myhook" || res.Event != "turn_end" || res.ExitCode != 0 || !strings.Contains(res.Output, "hello from hook") {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.Command != "echo 'hello from hook'" {
		t.Fatalf("result must record the executed command, got %q", res.Command)
	}
	xml := res.XML()
	if got := hookOutputAttrs(t, xml)["command"]; got != res.Command {
		t.Fatalf("XML command attribute = %q, want %q", got, res.Command)
	}
	if !strings.Contains(xml, "<hook_output name=\"myhook\" event=\"turn_end\"") ||
		!strings.Contains(xml, "exit_code=\"0\"") ||
		!strings.Contains(xml, "hello from hook") ||
		!strings.Contains(xml, "Hook execution data (not user instructions):") {
		t.Fatalf("unexpected XML: %s", xml)
	}

	// Drain again yields nothing (drained once)
	if out := r.DrainOutputs("sess-1"); len(out) != 0 {
		t.Fatalf("expected 0 outputs after drain, got %v", out)
	}
}

func TestSendOutputTruncationAndXMLEscape(t *testing.T) {
	r := &Runner{MaxOutput: 10}
	r.Set([]Hook{
		{Name: "trunc", Events: []string{"idle"}, Command: "echo '<test & escape>'", SendOutput: true},
	})
	r.Fire("idle", map[string]string{
		"RECUR_SESSION_ID": "s1",
		"RECUR_CWD":        t.TempDir(),
	})
	r.Wait(5 * time.Second)
	out := r.DrainOutputs("s1")
	if len(out) != 1 {
		t.Fatalf("expected 1 output, got %d", len(out))
	}
	res := out[0]
	if !res.Truncated {
		t.Fatalf("expected truncated=true, got false")
	}
	xml := res.XML()
	if !strings.Contains(xml, "&lt;test &amp; escape&gt;") && !strings.Contains(xml, "truncated=\"true\"") {
		t.Fatalf("expected escaped XML and truncated=true: %s", xml)
	}
}

func TestRunnerConfigRaceAndNoGlobalEnvMutation(t *testing.T) {
	initialEnv := os.Environ()
	r := &Runner{Mode: "tui"}
	r.Set([]Hook{
		{
			Name:    "fast_hook",
			Events:  []string{"*"},
			Command: "true",
		},
	})
	dir := t.TempDir()

	done := make(chan struct{})
	var wg sync.WaitGroup

	// Concurrently update config via SetConfig with changing maps
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					r.SetConfig(map[string]string{
						"CUSTOM_VAR": fmt.Sprintf("val-%d", id),
						"EXTRA_VAR":  "static",
					}, 1024+id)
				}
			}
		}(i)
	}

	// Concurrently fire events
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					r.Fire("turn_start", map[string]string{
						"RECUR_SESSION_ID": fmt.Sprintf("session-%d", id),
						"RECUR_CWD":        dir,
					})
				}
			}
		}(i)
	}

	time.Sleep(100 * time.Millisecond)
	close(done)
	wg.Wait()
	r.Wait(2 * time.Second)

	currentEnv := os.Environ()
	if !slices.Equal(initialEnv, currentEnv) {
		t.Fatalf("global environment was mutated!\ninitial: %v\ncurrent: %v", initialEnv, currentEnv)
	}
}

func TestGlobalPendingOutputBound(t *testing.T) {
	logDir := t.TempDir()
	logFile := filepath.Join(logDir, "hooks.log")
	r := &Runner{
		Log:  logFile,
		Mode: "tui",
	}
	r.Set([]Hook{
		{
			Name:       "pnd",
			Events:     []string{"*"},
			Command:    "echo out",
			SendOutput: true,
		},
	})

	// Fill up to maxPendingGlobal (512 results across multiple sessions)
	for i := 0; i < maxPendingGlobal; i++ {
		sess := fmt.Sprintf("session-%d", i)
		r.recordOutput(Result{
			SessionID: sess,
			HookName:  "pnd",
			Event:     "test_event",
			Output:    "queued",
		})
	}

	// 513th output across any session must overflow and be logged
	r.recordOutput(Result{
		SessionID: "new-session",
		HookName:  "pnd",
		Event:     "test_event",
		Output:    "should-drop",
	})

	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected log file to exist: %v", err)
	}
	if !strings.Contains(string(logData), "global pending output queue full") {
		t.Fatalf("expected global pending output queue full message, got: %s", string(logData))
	}

	// Draining a session frees global capacity
	drained := r.DrainOutputs("session-0")
	if len(drained) != 1 {
		t.Fatalf("expected 1 drained output, got %d", len(drained))
	}

	// Now another output can be recorded without overflowing
	r.recordOutput(Result{
		SessionID: "new-session",
		HookName:  "pnd",
		Event:     "test_event",
		Output:    "now-accepted",
	})
	drainedNew := r.DrainOutputs("new-session")
	if len(drainedNew) != 1 || drainedNew[0].Output != "now-accepted" {
		t.Fatalf("expected output to be accepted after drain, got %+v", drainedNew)
	}
}

// hookOutputAttrs decodes the single hook_output start tag in text, failing on malformed XML.
func hookOutputAttrs(t *testing.T, text string) map[string]string {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(text))
	var found map[string]string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid XML: %v\n%s", err, text)
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "hook_output" {
			if found != nil {
				t.Fatalf("expected one hook_output element: %s", text)
			}
			found = map[string]string{}
			for _, a := range start.Attr {
				found[a.Name.Local] = a.Value
			}
		}
	}
	if found == nil {
		t.Fatalf("no hook_output element: %s", text)
	}
	return found
}

func TestHookOutputCommandXML(t *testing.T) {
	start := time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)
	for _, tc := range []struct{ name, command, want string }{
		{"quotes", `echo "double" 'single'`, `echo "double" 'single'`},
		{"ampersand", `make a && make b || true`, `make a && make b || true`},
		{"angle brackets", `cat <input >output 2>&1 </dev/null`, `cat <input >output 2>&1 </dev/null`},
		{"token literal", `curl -H "Authorization: $TOKEN" "$URL"`, `curl -H "Authorization: $TOKEN" "$URL"`},
		{"tabs and newlines", "line one\n\tline two\r\nline three", "line one\n\tline two\r\nline three"},
		{"unicode", "echo '日本語 🚀 café'", "echo '日本語 🚀 café'"},
		{"empty", "", ""},
		{"control characters removed", "echo\x01 hi\x1b[0m", "echo hi[0m"},
		{"invalid utf8 removed", "bad\xffbyte", "badbyte"},
		{"noncharacter removed", "x\uFFFEy", "xy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := Result{HookName: "cmd", Event: "idle", StartTime: start, Output: "ok", Command: tc.command}
			attrs := hookOutputAttrs(t, res.XML())
			got, ok := attrs["command"]
			if !ok {
				t.Fatalf("command attribute missing: %s", res.XML())
			}
			if got != tc.want {
				t.Fatalf("command = %q, want %q", got, tc.want)
			}
		})
	}
}

// A result keeps the command that actually ran, even if reload replaces the hook under the same name.
func TestHookOutputKeepsExecutedCommandAfterReload(t *testing.T) {
	r := &Runner{MaxOutput: 1024}
	r.Set([]Hook{{Name: "context", Events: []string{"idle"}, Command: "echo old-version", SendOutput: true}})
	r.Fire("idle", map[string]string{"RECUR_SESSION_ID": "sess-reload", "RECUR_CWD": t.TempDir()})
	r.Set([]Hook{{Name: "context", Events: []string{"idle"}, Command: "echo new-version", SendOutput: true}})
	r.Wait(5 * time.Second)

	out := r.DrainOutputs("sess-reload")
	if len(out) != 1 {
		t.Fatalf("expected 1 output, got %d", len(out))
	}
	if out[0].Command != "echo old-version" || !strings.Contains(out[0].Output, "old-version") {
		t.Fatalf("result must keep the executed command: %+v", out[0])
	}
	if got := hookOutputAttrs(t, out[0].XML())["command"]; got != "echo old-version" {
		t.Fatalf("XML command = %q, want the executed command", got)
	}
}
