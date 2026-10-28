package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"recur/internal/repl"
)

func TestPromptFilesAndNewSession(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, name := range []string{"prefix.py", "system.md", "settings.toml"} {
		if _, err := os.Stat(filepath.Join(a.home, name)); err != nil {
			t.Fatal(err)
		}
	}
	prefix := "print('custom context')\nx = 42\n999\n"
	system := "Custom system instructions."
	if err := os.WriteFile(filepath.Join(a.home, "prefix.py"), []byte(prefix), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.home, "system.md"), []byte(system), 0600); err != nil {
		t.Fatal(err)
	}
	a.message("user", "old chat")
	if _, err = a.REPL.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = a.NewSession(); err != nil {
		t.Fatal(err)
	}
	if len(a.History) != 0 || a.System != system || a.REPL.Prefix != prefix {
		t.Fatal("new session must clear history and reload both files")
	}
	if fresh, err := a.REPL.Ensure(context.Background()); err != nil || !fresh {
		t.Fatalf("new interpreter %v %v", fresh, err)
	}
	if a.REPL.PrefixOutput != "custom context\n" {
		t.Fatalf("prefix output should only include prints, got %q", a.REPL.PrefixOutput)
	}
	result, err := a.REPL.Execute(context.Background(), "x")
	if err != nil || result.Output != "42\n" {
		t.Fatalf("prefix variable: %+v %v", result, err)
	}
	next, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if len(next.History) != 0 || next.System != system || next.REPL.Prefix != prefix {
		t.Fatal("existing prompt files must not be overwritten")
	}
}

func TestPromptFailureDoesNotLeaveInterpreterAlive(t *testing.T) {
	r := &repl.REPL{Prefix: "print('before failure')\n1/0"}
	defer r.Close()
	_, err := r.Ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "prefix failed") {
		t.Fatalf("prefix failure: %v", err)
	}
	r.Prefix = "print('recovered')"
	if fresh, err := r.Ensure(context.Background()); err != nil || !fresh {
		t.Fatalf("retry %v %v", fresh, err)
	}
}

func TestReloadFailurePreservesSession(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.REPL.Prefix = "x = 42"
	if _, err = a.REPL.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.message("user", "keep history")
	a.records = []Record{{Kind: "user", Text: "keep history"}}
	previousSystem := a.System
	dir := a.home
	if err = os.WriteFile(filepath.Join(dir, "settings.toml"), []byte("invalid settings"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = a.NewSession(); err == nil {
		t.Fatal("expected reload error")
	}
	if len(a.History) != 1 || len(a.Records()) != 1 || a.System != previousSystem {
		t.Fatal("reload mutated session")
	}
	result, err := a.REPL.Execute(context.Background(), "x")
	if err != nil || result.Output != "42\n" {
		t.Fatalf("worker changed: %+v %v", result, err)
	}
}
func TestSaveFailurePreservesSession(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.REPL.Prefix = "x=42"
	if _, err = a.REPL.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.message("user", "keep")
	a.records = []Record{{Kind: "user", Text: "keep"}}
	if err = os.Mkdir(a.HistoryPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err = a.NewSession(); err == nil {
		t.Fatal("expected save error")
	}
	if len(a.History) != 1 || len(a.Records()) != 1 {
		t.Fatal("save failure discarded history")
	}
	result, err := a.REPL.Execute(context.Background(), "x")
	if err != nil || result.Output != "42\n" {
		t.Fatalf("worker changed: %+v %v", result, err)
	}
}
func TestDefaultFilesPrivateAndNeverOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "prompt")
	if _, err := loadOrCreateFile(path, "first"); err != nil {
		t.Fatal(err)
	}
	if text, err := loadOrCreateFile(path, "second"); err != nil || text != "first" {
		t.Fatalf("%q %v", text, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file permissions: %v %v", info, err)
	}
	info, err = os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("directory permissions: %v %v", info, err)
	}
}
