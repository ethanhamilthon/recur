package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"recur/internal/agent"
)

func TestShortPathUsesTildeForHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == string(filepath.Separator) {
		t.Skip("no usable home directory")
	}
	cases := map[string]string{
		home:                     "~",
		filepath.Join(home, "p"): "~" + string(filepath.Separator) + "p",
		home + "x/y":             home + "x/y",
		"/tmp/elsewhere":         "/tmp/elsewhere",
	}
	for path, want := range cases {
		if got := shortPath(path); got != want {
			t.Errorf("shortPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestBottomBarShortensWorkingDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == string(filepath.Separator) {
		t.Skip("no usable home directory")
	}
	a, err := agent.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := model{a: a, cwd: filepath.Join(home, "work", "demo"), width: 120}
	if bar := m.bottomBar(); !strings.Contains(bar, "~/work/demo") {
		t.Fatalf("status bar does not show a shortened path: %q", bar)
	}
}
