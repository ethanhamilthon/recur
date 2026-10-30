package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	for _, name := range []string{"prefix.sh", "system.md", "settings.toml"} {
		if _, err := os.Stat(filepath.Join(a.home, name)); err != nil {
			t.Fatal(err)
		}
	}
	prefix := "echo 'custom context'\nx=42\n"
	system := "Custom system instructions."
	if err := os.WriteFile(filepath.Join(a.home, "prefix.sh"), []byte(prefix), 0600); err != nil {
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
	result, err := a.REPL.Execute(context.Background(), "echo $x", time.Second)
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
	r := &repl.REPL{Prefix: "echo before failure\nif then"}
	defer r.Close()
	_, err := r.Ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "prefix failed") {
		t.Fatalf("prefix failure: %v", err)
	}
	r.Prefix = "echo recovered"
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
	a.REPL.Prefix = "x=42"
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
	result, err := a.REPL.Execute(context.Background(), "echo $x", time.Second)
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
	result, err := a.REPL.Execute(context.Background(), "echo $x", time.Second)
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

func TestProjectLocalPrompts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECUR_HOME", home)
	cwd := t.TempDir()
	local := filepath.Join(cwd, ".recur")
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, "system.md"), "global system")
	write(filepath.Join(home, "prefix.sh"), "echo global")
	write(filepath.Join(local, "system.md"), "local system")
	write(filepath.Join(local, "settings.toml"), "model = 'local-model'\n")
	write(filepath.Join(home, "settings.toml"), "max_result_lines = 4\n") // no key: default on
	a, err := New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// A missing local file falls back to the global copy; local settings are ignored.
	if a.System != "local system" || a.REPL.Prefix != "echo global" || a.LocalConfig != local {
		t.Fatalf("system=%q prefix=%q local=%q", a.System, a.REPL.Prefix, a.LocalConfig)
	}
	if a.Settings().Model == "local-model" || !a.Settings().EnableLocalRecur || !strings.HasPrefix(a.sessionsDir, home) {
		t.Fatal("settings and sessions must stay global")
	}
	write(filepath.Join(local, "prefix.sh"), "echo local")
	if err = a.Reload(); err != nil || a.REPL.Prefix != "echo local" {
		t.Fatalf("reload ignored local prefix: %q %v", a.REPL.Prefix, err)
	}
	write(filepath.Join(local, "system.md"), "changed")
	if err = a.NewSession(); err != nil || a.System != "changed" || a.LocalConfig != local {
		t.Fatalf("new session ignored local system: %q %v", a.System, err)
	}
	entries, _ := os.ReadDir(local)
	if len(entries) != 3 {
		t.Fatalf("recur must not create files in the project .recur: %v", entries)
	}
	write(filepath.Join(home, "settings.toml"), "enable_local_recur = false\n")
	if err = a.Reload(); err != nil || a.System != "global system" || a.LocalConfig != "" {
		t.Fatalf("disabled local config still used: %q %v", a.System, err)
	}
}

func TestLocalConfigIgnoredWhenItIsHome(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("RECUR_HOME", ".recur") // relative to cwd: the project .recur is the home
	if err := os.MkdirAll(filepath.Join(cwd, ".recur"), 0700); err != nil {
		t.Fatal(err)
	}
	a, err := New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.LocalConfig != "" {
		t.Fatalf("home treated as project config: %q", a.LocalConfig)
	}
}
