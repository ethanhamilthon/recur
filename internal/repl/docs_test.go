package repl

import (
	"context"
	"strings"
	"testing"

	monty "github.com/ewhauser/gomonty"
)

func TestDocsThroughWorker(t *testing.T) {
	r := &REPL{Dir: t.TempDir(), Prefix: "print(docs('index'))"}
	defer r.Close()
	if _, err := r.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.PrefixOutput, "Recur documentation") {
		t.Fatal(r.PrefixOutput)
	}
	for _, topic := range []string{"index", "runtime", "sessions", "steering", "interface", "files", "images", "models", "headless", "clipboard"} {
		got, err := r.Execute(context.Background(), "text = docs('"+topic+"')\nprint(text)")
		if err != nil || got.Error || len(got.Output) < 50 {
			t.Fatalf("%s: %+v %v", topic, got, err)
		}
	}
	for _, code := range []string{"docs()", "docs(12)", "docs('missing')", "docs('../system')", "docs('index', 'runtime')"} {
		got, err := r.Execute(context.Background(), code)
		if err != nil || !got.Error {
			t.Fatalf("%s: %+v %v", code, got, err)
		}
	}
	got, err := r.Execute(context.Background(), "len(text) > 50")
	if err != nil || got.Error || !strings.Contains(got.Output, "True") {
		t.Fatalf("state lost: %+v %v", got, err)
	}
}

// The tool description names the only importable modules; Monty must agree.
func TestToolDescriptionMatchesImportableModules(t *testing.T) {
	modules := []string{"json", "math", "re", "os", "sys", "pathlib", "datetime", "typing", "asyncio"}
	missing := []string{"collections", "dataclasses", "enum", "itertools", "functools", "random", "time"}
	for _, name := range append(modules, missing...) {
		if !strings.Contains(ToolDescription, name) {
			t.Fatalf("tool description omits module %q", name)
		}
	}
	r, err := monty.NewRepl(monty.ReplOptions{ScriptName: "<p>"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range modules {
		value, err := r.FeedRun(context.Background(), "import "+name, monty.FeedOptions{})
		if err != nil {
			t.Fatalf("import %s: %v", name, err)
		}
		if strings.Contains(value.String(), "ModuleNotFoundError") {
			t.Fatalf("import %s reported %s", name, value.String())
		}
	}
	for _, name := range missing {
		value, err := r.FeedRun(context.Background(), "import "+name, monty.FeedOptions{})
		raised := err != nil && strings.Contains(err.Error(), "ModuleNotFoundError")
		if !raised && !strings.Contains(value.String(), "ModuleNotFoundError") {
			t.Fatalf("import %s did not raise ModuleNotFoundError: %v %s", name, err, value.String())
		}
	}
}
