package repl

import (
	"context"
	"strings"
	"syscall"
	"testing"
	"time"

	"recur/prompts"
)

func TestPersistentInterpreter(t *testing.T) {
	r := &REPL{Dir: "../..", Prefix: prompts.Prefix}
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fresh, err := r.Ensure(ctx)
	if err != nil || !fresh {
		t.Fatalf("start: %v %v", fresh, err)
	}
	if !strings.Contains(r.PrefixOutput, "# Project Rules") {
		t.Fatalf("prefix output: %q", r.PrefixOutput)
	}
	for _, test := range []struct{ code, want string }{
		{"x = 40\nprint('hello')\nx + 2", "hello\n42\n"},
		{"x += 1\nx", "41\n"},
		{"x = 41; print('hello'); x + 1; # end", "hello\n42\n"},
		{"(\n x +\n 1\n)", "42\n"},
		{"def add(n):\n    return n + 1\n\nadd(x)", "42\n"},
		{"print('before')\n1/0", "ZeroDivisionError"},
		{"x", "41\n"},
		{"print('a', 'b', sep='-', end='!')", "a-b!"},
		{"print(sh('printf shell-output')[\"stdout\"])", "shell-output\n"},
		{"sh('exit 7')['returncode']", "7\n"},
		{"print(sh('printf problem >&2')['stderr'])", "problem\n"},
		{"import os\nos.getcwd()", "AttributeError"},
		{"open('/etc/passwd')", "LookupError"},
		{"input()", "LookupError"},
	} {
		result, err := r.Execute(ctx, test.code)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.Output, test.want) {
			t.Errorf("%s: got %q want %q", test.code, result.Output, test.want)
		}
	}
	fresh, err = r.Ensure(ctx)
	if err != nil || fresh {
		t.Fatalf("unexpected reset: %v %v", fresh, err)
	}
}
func TestLossAndCancellation(t *testing.T) {
	r := &REPL{Prefix: "print('init')"}
	defer r.Close()
	if _, err := r.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(r.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(context.Background(), "1"); err == nil {
		t.Fatal("expected state loss")
	}
	if fresh, err := r.Ensure(context.Background()); err != nil || !fresh {
		t.Fatalf("restart: %v %v", fresh, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := r.Execute(ctx, "while True: pass"); err == nil {
		t.Fatal("expected timeout")
	}
	if fresh, err := r.Ensure(context.Background()); err != nil || !fresh {
		t.Fatalf("restart after timeout: %v %v", fresh, err)
	}
}
func TestNoSystemPython(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	r := &REPL{Prefix: "print('Monty')"}
	defer r.Close()
	if _, err := r.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := r.Execute(context.Background(), "sum(range(10))")
	if err != nil || result.Output != "45\n" {
		t.Fatalf("%q %v", result.Output, err)
	}
}

func TestMontyMemoryAndOutputLimits(t *testing.T) {
	r := &REPL{}
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := r.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := r.Execute(ctx, "a = [0] * 200000000")
	if err != nil || !result.Error || !strings.Contains(result.Output, "MemoryError") {
		t.Fatalf("memory limit: %+v %v", result, err)
	}
	for _, code := range []string{"print('x' * 400000)", "print(sh('yes x | head -c 400000')['stdout'])"} {
		result, err = r.Execute(ctx, code)
		if err != nil || result.Error || len(result.Output) > outputLimit+100 || !strings.Contains(result.Output, "[output truncated") {
			t.Fatalf("output limit: code=%s length=%d err=%v error=%v", code, len(result.Output), err, result.Error)
		}
	}
	result, err = r.Execute(ctx, "40+2")
	if err != nil || result.Output != "42\n" {
		t.Fatalf("state after limits: %+v %v", result, err)
	}
}
func TestMontyShellCancellationAndProtocolIsolation(t *testing.T) {
	r := &REPL{}
	defer r.Close()
	if _, err := r.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := r.Execute(context.Background(), `sh("printf 'data: fake\\n'; printf stderr >&2")`)
	if err != nil || result.Error || !strings.Contains(result.Output, "data: fake") {
		t.Fatalf("protocol: %+v %v", result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err = r.Execute(ctx, `sh("sleep 30 & wait")`); err == nil {
		t.Fatal("expected shell cancellation")
	}
	if fresh, err := r.Ensure(context.Background()); err != nil || !fresh {
		t.Fatalf("restart: %v %v", fresh, err)
	}
}

func TestFreshDeadlineAfterIdlePreservesState(t *testing.T) {
	r := &REPL{Prefix: "x = 40"}
	defer r.Close()
	if _, err := r.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	execute := func(code, want string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result, err := r.Execute(ctx, code)
		if err != nil || result.Error || result.Output != want {
			t.Fatalf("%s: %+v %v", code, result, err)
		}
	}
	execute("x += 1\nx", "41\n")
	time.Sleep(25 * time.Millisecond)
	if fresh, err := r.Ensure(context.Background()); err != nil || fresh {
		t.Fatalf("idle reset state: %v %v", fresh, err)
	}
	execute(`print(sh("echo ok")["stdout"], end=""); x+1`, "ok\n42\n")
	execute("x", "41\n")
}

func TestPrefixCancellationStopsWorker(t *testing.T) {
	r := &REPL{Prefix: "while True: pass"}
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := r.Ensure(ctx); err == nil {
		t.Fatal("expected prefix timeout")
	}
	if r.cmd != nil {
		t.Fatal("prefix timeout left worker running")
	}
	r.Prefix = "x=42"
	if fresh, err := r.Ensure(context.Background()); err != nil || !fresh {
		t.Fatalf("restart: %v %v", fresh, err)
	}
}
