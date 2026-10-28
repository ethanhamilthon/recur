package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestOwnershipChild(t *testing.T) {
	mode := os.Getenv("RECUR_OWNERSHIP_CHILD")
	if mode == "" {
		return
	}
	a, err := New(os.Getenv("RECUR_TEST_CWD"), os.Getenv("RECUR_TEST_SESSION"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if mode == "reader" {
		if !a.ReadOnly {
			t.Fatal("second process became writer")
		}
		if err = a.Run(context.Background(), "must not execute", func(Event) {}); err == nil {
			t.Fatal("read-only process executed a turn")
		}
		fmt.Println("READONLY")
	} else {
		if a.ReadOnly {
			t.Fatal("writer could not acquire released ownership")
		}
		fmt.Println("OWNER")
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
}
func TestOwnershipAcrossProcessesAndCrashRelease(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)
	a, err := New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Save(); err != nil {
		t.Fatal(err)
	}
	id := a.SessionInfo()["id"].(string)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := func(mode string) *exec.Cmd {
		cmd := exec.Command(executable, "-test.run=^TestOwnershipChild$")
		cmd.Env = append(os.Environ(), "RECUR_OWNERSHIP_CHILD="+mode, "RECUR_TEST_CWD="+cwd, "RECUR_TEST_SESSION="+id)
		return cmd
	}
	output, err := child("reader").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "READONLY") {
		t.Fatalf("second client: %s %v", output, err)
	}
	a.Close()
	cmd := child("writer")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	outputPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	scanner := bufio.NewScanner(outputPipe)
	if !scanner.Scan() || scanner.Text() != "OWNER" {
		t.Fatal("child owner did not initialize")
	}
	follower, err := New(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Close()
	if !follower.ReadOnly {
		t.Fatal("follower stole ownership")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil {
		t.Fatal("expected killed child exit")
	}
	replacement, err := New(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if replacement.ReadOnly {
		t.Fatal("crashed owner retained lock")
	}
	if !follower.ReadOnly {
		t.Fatal("follower silently became a writer")
	}
}
func TestReadOnlyRefreshAndSessionSwitch(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	cwd := t.TempDir()
	writer, err := New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err = writer.Save(); err != nil {
		t.Fatal(err)
	}
	follower, err := New(cwd, writer.SessionInfo()["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Close()
	if !follower.ReadOnly {
		t.Fatal("expected follower")
	}
	writer.message("user", "committed message")
	if err = writer.Save(); err != nil {
		t.Fatal(err)
	}
	if err = follower.Refresh(); err != nil {
		t.Fatal(err)
	}
	if len(follower.History) != 1 {
		t.Fatal("follower did not reload snapshot")
	}
	if err = follower.Save(); err == nil {
		t.Fatal("follower wrote history")
	}
	if err = follower.Select(writer.settings.Provider, "different", "low"); err == nil {
		t.Fatal("follower changed model")
	}
	if err = os.WriteFile(writer.HistoryPath, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = follower.Refresh(); err == nil || len(follower.History) != 1 {
		t.Fatal("corrupt snapshot destroyed follower state")
	}
	if err = writer.Save(); err != nil {
		t.Fatal(err)
	}
	if err = follower.NewSession(); err != nil {
		t.Fatal(err)
	}
	if follower.ReadOnly || follower.HistoryPath == writer.HistoryPath {
		t.Fatal("independent new session not writable")
	}
}
