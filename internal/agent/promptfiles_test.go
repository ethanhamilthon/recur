package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPromptFilesOverrideLocalAndSurviveReload(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, ".recur"), 0700); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(cwd, ".recur", "system.md")
	flagged := filepath.Join(t.TempDir(), "sys.md")
	for path, text := range map[string]string{local: "local", flagged: "flag"} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if a.System != "local" {
		t.Fatalf("local fallback: %q", a.System)
	}
	a.Close()
	a, err = NewWith(cwd, ConfigFiles{System: flagged})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.System != "flag" {
		t.Fatalf("flag: %q", a.System)
	}
	if err = a.Reload(); err != nil || a.System != "flag" {
		t.Fatalf("reload: %q %v", a.System, err)
	}
	if _, err = NewWith(cwd, ConfigFiles{Prefix: "/nonexistent"}); err == nil {
		t.Fatal("missing --prefix file must fail")
	}
}
