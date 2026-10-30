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
