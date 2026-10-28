package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGlobalHomeDefaultsAndMissingFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RECUR_HOME", "")
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.home != filepath.Join(home, ".recur") {
		t.Fatal(a.home)
	}
	for _, name := range []string{"settings.toml", "prefix.sh", "system.md"} {
		info, err := os.Stat(filepath.Join(a.home, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private default %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(a.home, "system.md"), []byte("Keep this prompt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(a.home, "prefix.sh")); err != nil {
		t.Fatal(err)
	}
	b, err := New(a.REPL.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.System != "Keep this prompt" || b.REPL.Prefix == "" {
		t.Fatal("missing file not filled or editable prompt replaced")
	}
}

func TestScopedJSONLSessionsResumeAndCheckpoint(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	cwd := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	a, err := New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.message("user", "first session")
	a.records = []Record{{Kind: "user", Text: "first session"}, {Kind: "code", ID: "partial", Text: "x = 42", Meta: &Metadata{Duration: time.Second, Usage: &Usage{Input: 100, Output: 10, Cached: 50}}}}
	if err = a.Save(); err != nil {
		t.Fatal(err)
	}
	first := a.HistoryPath
	want := a.Records()
	if err = a.NewSession(); err != nil {
		t.Fatal(err)
	}
	second := a.HistoryPath
	if second == first {
		t.Fatal("new session overwrote first")
	}
	a.message("user", "second session")
	if err = a.Save(); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSuffix(filepath.Base(first), ".jsonl")
	if err = a.Resume(id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, a.Records()) {
		t.Fatal("metadata lost on resume")
	}
	b, err := New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.History) != 0 || b.HasSavedSession() {
		t.Fatal("startup must open an unsaved new session before first message")
	}
	b.Close()
	resumed, err := New(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if resumed.HistoryPath != first || !reflect.DeepEqual(want, resumed.Records()) {
		t.Fatal("explicit resume did not restore session")
	}
	other, err := New(filepath.Join(t.TempDir(), "project"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.REPL.Close()
	if other.sessionsDir == a.sessionsDir {
		t.Fatal("same-basename projects collided")
	}
	if err = other.Resume(id); err == nil {
		t.Fatal("cross-project resume allowed")
	}
	if err = a.Resume("../../escape"); err == nil {
		t.Fatal("path traversal accepted")
	}
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatal("not JSONL")
		}
	}
	for _, path := range []string{first, second} {
		info, e := os.Stat(path)
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("permissions: %s %v", path, e)
		}
	}
	// Corrupt an inactive session, then verify a failed switch cannot mutate the live session.
	if err = os.WriteFile(second, []byte("{\"version\":2}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = a.Resume(strings.TrimSuffix(filepath.Base(second), ".jsonl")); err == nil {
		t.Fatal("incomplete session accepted")
	}
	if a.HistoryPath != first || !reflect.DeepEqual(a.Records(), want) {
		t.Fatal("failed switch changed active session")
	}
	if err = os.WriteFile(second, data[:len(data)-1], 0600); err != nil {
		t.Fatal(err)
	}
	if err = a.Resume(strings.TrimSuffix(filepath.Base(second), ".jsonl")); err == nil {
		t.Fatal("unterminated session accepted")
	}
}

func TestNewSessionResetsInterpreterState(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = a.REPL.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = a.NewSession(); err != nil {
		t.Fatal(err)
	}
	if fresh, err := a.REPL.Ensure(context.Background()); err != nil || !fresh {
		t.Fatal("session switch retained interpreter state")
	}
}
