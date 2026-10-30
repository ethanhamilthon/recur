package repl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"recur/prompts"
)

func start(t *testing.T, r *REPL) {
	t.Helper()
	t.Cleanup(r.Close)
	if fresh, err := r.Ensure(context.Background()); err != nil || !fresh {
		t.Fatalf("start: %v %v", fresh, err)
	}
}

func run(t *testing.T, r *REPL, command string, timeout time.Duration) Result {
	t.Helper()
	result, err := r.Execute(context.Background(), command, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPersistentShell(t *testing.T) {
	r := &REPL{Dir: "../..", Prefix: prompts.Prefix}
	start(t, r)
	if !strings.Contains(r.PrefixOutput, "# Agent rules") {
		t.Fatalf("prefix output: %q", r.PrefixOutput)
	}
	for _, test := range []struct {
		command, want string
		code          int
	}{
		{"x=40; echo hello", "hello\n", 0},
		{"echo $((x + 2))", "42\n", 0},
		{"add() { echo $(($1 + 1)); }", "", 0},
		{"add $x", "41\n", 0},
		{"cd internal && basename \"$PWD\"", "internal\n", 0},
		{"basename \"$(pwd)\"", "internal\n", 0},
		{"printf problem >&2; exit 7", "problem", 7},
		{"echo after-exit $x", "after-exit 40\n", 0},
		{"false", "", 1},
		{"if then", "syntax error", 2},
		{"missing-command-xyz", "not found", 127},
		{"cat | wc -c", "0", 0},
		{"cat; echo after-cat", "after-cat\n", 0},
		{"cat <<'EOF'\nheredoc $x\nEOF", "heredoc $x\n", 0},
		{"echo $PAGER $GIT_PAGER", "cat cat\n", 0},
	} {
		result := run(t, r, test.command, 10*time.Second)
		if result.ExitCode != test.code || result.Error != (test.code != 0) || !strings.Contains(result.Output, test.want) {
			t.Errorf("%s: %+v; want %q code %d", test.command, result, test.want, test.code)
		}
	}
	if fresh, err := r.Ensure(context.Background()); err != nil || fresh {
		t.Fatalf("unexpected reset: %v %v", fresh, err)
	}
}

func TestEnvIsExportedBeforeEveryCall(t *testing.T) {
	model := "m o"
	r := &REPL{Dir: t.TempDir(), Env: func() map[string]string {
		return map[string]string{"RECUR_MODEL": model, "RECUR_CTX_SIZE": "12"}
	}}
	start(t, r)
	model = "changed"
	result := run(t, r, `sh -c 'echo "$RECUR_MODEL|$RECUR_CTX_SIZE"'`, 5*time.Second)
	if result.Output != "changed|12\n" {
		t.Fatalf("%+v", result)
	}
}

func TestTimeoutKeepsStateAndKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	r := &REPL{Dir: dir}
	start(t, r)
	run(t, r, "x=kept", time.Second)
	started := time.Now()
	result := run(t, r, "sh -c 'sleep 30 & echo $! > child.pid; wait'", 300*time.Millisecond)
	if result.ExitCode != 124 || !strings.Contains(result.Output, "timed out") || time.Since(started) > 5*time.Second {
		t.Fatalf("timeout: %+v after %s", result, time.Since(started))
	}
	data, err := os.ReadFile(filepath.Join(dir, "child.pid"))
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	for _, c := range strings.TrimSpace(string(data)) {
		pid = pid*10 + int(c-'0')
	}
	// Allow init to reap the killed grandchild; a zombie still answers signal 0.
	for i := 0; syscall.Kill(pid, 0) == nil; i++ {
		if i == 40 {
			t.Fatalf("grandchild %d survived", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
	result = run(t, r, "while :; do :; done", 200*time.Millisecond)
	if result.ExitCode != 124 {
		t.Fatalf("builtin loop: %+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	result, err = r.Execute(ctx, "sleep 30", 10*time.Second)
	if err != nil || result.ExitCode != 130 {
		t.Fatalf("cancel: %+v %v", result, err)
	}
	if result = run(t, r, "echo $x", time.Second); result.Output != "kept\n" {
		t.Fatalf("state lost: %+v", result)
	}
}

func TestBackgroundJobOutlivesCallTimeout(t *testing.T) {
	dir := t.TempDir()
	r := &REPL{Dir: dir}
	start(t, r)
	result := run(t, r, "(sleep 0.5; echo \"job done $?\" > job.txt) &\necho started", time.Second)
	if result.ExitCode != 0 || result.Output != "started\n" {
		t.Fatalf("%+v", result)
	}
	if result = run(t, r, "sleep 5", 100*time.Millisecond); result.ExitCode != 124 {
		t.Fatalf("%+v", result)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(dir, "job.txt"))
		if string(data) == "job done 0\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background job was killed by another call's timeout")
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.Close()
	if _, err := r.Execute(context.Background(), "true", time.Second); err == nil {
		t.Fatal("closed shell accepted a command")
	}
}

func TestOutputLimit(t *testing.T) {
	r := &REPL{Dir: t.TempDir()}
	start(t, r)
	for _, command := range []string{"yes x | head -c 400000", "i=0; while [ $i -lt 40000 ]; do echo xxxxxxxxx; i=$((i+1)); done"} {
		result := run(t, r, command, 20*time.Second)
		if result.ExitCode != 0 || len(result.Output) > DefaultOutputLimit+300 || !strings.Contains(result.Output, "[output truncated") {
			t.Fatalf("%s: length=%d %+v", command, len(result.Output), result.ExitCode)
		}
	}
}

func TestPrefixFailures(t *testing.T) {
	r := &REPL{Dir: t.TempDir(), Prefix: "if then"}
	if _, err := r.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("syntax: %v", err)
	}
	r.Prefix = "echo init; false"
	start(t, r)
	if r.PrefixOutput != "init\n" {
		t.Fatalf("nonzero prefix status must not fail: %q", r.PrefixOutput)
	}
	r.Close()
	r.Prefix = "sleep 30"
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := r.Ensure(ctx); err == nil {
		t.Fatal("expected prefix cancellation")
	}
	if r.runner != nil {
		t.Fatal("failed prefix left a shell")
	}
}

func TestOutputLimitChoices(t *testing.T) {
	r := &REPL{Dir: t.TempDir(), Prefix: "yes p | head -c 100000"}
	start(t, r)
	if len(r.PrefixOutput) != 100000 || strings.Contains(r.PrefixOutput, "[output truncated") {
		t.Fatalf("prefix truncated: %d", len(r.PrefixOutput))
	}
	for _, tc := range []struct{ limit, max int }{{1000, 1300}, {NoOutputLimit, 50000}} {
		result, err := r.ExecuteLimit(context.Background(), "yes x | head -c 50000", 20*time.Second, tc.limit)
		if err != nil || len(result.Output) > tc.max {
			t.Fatalf("limit %d: len=%d err=%v", tc.limit, len(result.Output), err)
		}
		if truncated := strings.Contains(result.Output, "[output truncated"); truncated != (tc.limit > 0) {
			t.Fatalf("limit %d: truncated=%v", tc.limit, truncated)
		}
	}
	result := run(t, r, "echo small", time.Second)
	if result.Output != "small\n" {
		t.Fatalf("small: %q", result.Output)
	}
}
