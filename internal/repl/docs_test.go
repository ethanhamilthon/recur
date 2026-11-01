package repl

import (
	"strings"
	"testing"
)

func TestDocs(t *testing.T) {
	for _, topic := range []string{"index", "runtime", "sessions", "steering", "interface", "images", "models", "headless", "clipboard", "hooks"} {
		var out strings.Builder
		if err := Docs(&out, []string{topic}); err != nil || out.Len() < 50 {
			t.Fatalf("%s: %v", topic, err)
		}
	}
	var out strings.Builder
	if err := Docs(&out, nil); err != nil || !strings.Contains(out.String(), "Recur documentation") {
		t.Fatalf("default topic: %v", err)
	}
	for _, args := range [][]string{{"missing"}, {"../system"}, {"index", "runtime"}, {""}, {"files"}} {
		if err := Docs(&out, args); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

func TestToolDescriptionFor(t *testing.T) {
	// Default values match ToolDescription constant
	desc := ToolDescriptionFor(0, 0, -1)
	if desc != ToolDescription {
		t.Fatalf("default ToolDescriptionFor mismatch:\ngot:\n%s\nwant:\n%s", desc, ToolDescription)
	}

	// Dynamic values formatted correctly
	custom := ToolDescriptionFor(45, 180, 4096)
	if !strings.Contains(custom, "default 45 (null), maximum 180") {
		t.Fatalf("missing custom timeout in:\n%s", custom)
	}
	if !strings.Contains(custom, "default 4096 (null)") {
		t.Fatalf("missing custom max_output in:\n%s", custom)
	}

	// Unlimited max_output (0)
	unlimited := ToolDescriptionFor(120, 600, 0)
	if !strings.Contains(unlimited, "default 0 (unlimited) (null)") {
		t.Fatalf("missing unlimited max_output in:\n%s", unlimited)
	}
}
