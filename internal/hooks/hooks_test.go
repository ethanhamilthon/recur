package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseValidatesHooks(t *testing.T) {
	list, err := Parse("[[hook]]\nevent = \"turn_start\"\ncommand = \"true\"\n\n[[hook]]\nevents = [\"turn_end\", \"idle\"]\ncommand = \"true\"\ntimeout = 5\n")
	if err != nil || len(list) != 2 || list[0].Events[0] != "turn_start" || len(list[1].Events) != 2 || list[1].Timeout != 5 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if list, err = Parse("# only comments\n"); err != nil || len(list) != 0 {
		t.Fatalf("empty file: %v %v", list, err)
	}
	for name, text := range map[string]string{
		"unknown event":   "[[hook]]\nevent = \"turn_begin\"\ncommand = \"true\"\n",
		"no command":      "[[hook]]\nevent = \"idle\"\n",
		"no event":        "[[hook]]\ncommand = \"true\"\n",
		"unknown field":   "[[hook]]\nevent = \"idle\"\ncmd = \"true\"\n",
		"timeout too big": "[[hook]]\nevent = \"idle\"\ncommand = \"true\"\ntimeout = 601\n",
		"negative":        "[[hook]]\nevent = \"idle\"\ncommand = \"true\"\ntimeout = -1\n",
		"syntax":          "[[hook]\n",
	} {
		if _, err := Parse(text); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

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
	doc := read(t, filepath.Join("..", "repl", "docs", "hooks.md"))
	for _, event := range Events {
		if !strings.Contains(doc, "`"+event+"`") {
			t.Errorf("event %s is missing from recur docs hooks", event)
		}
	}
}
