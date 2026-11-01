package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hooksSetup writes provider settings and a home config.toml recording events.
func hooksSetup(t *testing.T, status int) (record string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("RECUR_HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}]}}\n\n")
	}))
	t.Cleanup(server.Close)
	config := fmt.Sprintf("provider='test'\nmodel='m'\n[providers.test]\nbase_url=%q\n", server.URL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(t.TempDir(), "events")
}

func hooksConfig(t *testing.T, record, serverURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	text := fmt.Sprintf("provider='test'\nmodel='m'\n[providers.test]\nbase_url=%q\n[hooks.all]\nevents = [\"*\"]\ncommand = 'echo \"$RECUR_EVENT|$RECUR_MODE|$RECUR_EXIT_CODE\" >> %s'\n", serverURL, record)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHeadlessRunsHooksOnlyWithExplicitFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECUR_HOME", home)
	record := filepath.Join(t.TempDir(), "events")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}]}}\n\n")
	}))
	defer server.Close()

	homeCfg := fmt.Sprintf("provider='test'\nmodel='m'\n[providers.test]\nbase_url=%q\n[hooks.implicit]\nevents = [\"*\"]\ncommand = 'echo \"$RECUR_EVENT|$RECUR_MODE|$RECUR_EXIT_CODE\" >> %s'\n", server.URL, record)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(homeCfg), 0600); err != nil {
		t.Fatal(err)
	}

	var out, diagnostics bytes.Buffer
	if err := Run(context.Background(), []string{"--prompt", "hi"}, strings.NewReader(""), &out, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(record); len(got) != 0 {
		t.Fatalf("headless run used implicit hooks: %q", got)
	}

	explicitFile := hooksConfig(t, record, server.URL)
	if err := Run(context.Background(), []string{"--prompt", "hi", "--config", explicitFile, "--no-extend"}, strings.NewReader(""), &out, &diagnostics); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(record)
	want := "recur_start|headless|\nsession_start|headless|\nturn_start|headless|\nsession_title_change|headless|\nmodel_request_start|headless|\nmodel_request_end|headless|\nturn_end|headless|\nsession_end|headless|\nrecur_exit|headless|0\n"
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	// Without --no-extend, explicit --config inherits global hooks too
	os.Remove(record)
	if err := Run(context.Background(), []string{"--prompt", "hi", "--config", explicitFile}, strings.NewReader(""), &out, &diagnostics); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(record)
	if strings.Count(string(got), "turn_start|headless|") != 2 {
		t.Fatalf("expected 2 turn_start events when global hooks inherited, got: %s", got)
	}
}

func TestRecurExitReportsFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECUR_HOME", home)
	record := filepath.Join(t.TempDir(), "events")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
	}))
	defer server.Close()

	file := hooksConfig(t, record, server.URL)
	var out, diagnostics bytes.Buffer
	err := Run(context.Background(), []string{"--prompt", "hi", "--config", file}, strings.NewReader(""), &out, &diagnostics)
	if err == nil {
		t.Fatal("expected the provider error")
	}
	got, _ := os.ReadFile(record)
	if !strings.HasSuffix(string(got), "session_end|headless|\nrecur_exit|headless|1\n") || !strings.Contains(string(got), "error|headless|\n") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestInvalidHooksFlagFailsStartup(t *testing.T) {
	hooksSetup(t, 200)
	var out, diagnostics bytes.Buffer
	for _, path := range []string{filepath.Join(t.TempDir(), "missing.toml"), func() string {
		p := filepath.Join(t.TempDir(), "bad.toml")
		_ = os.WriteFile(p, []byte("[hooks.bad]\nevents = [\"typo\"]\ncommand = \"true\"\n"), 0600)
		return p
	}()} {
		if err := Run(context.Background(), []string{"--prompt", "hi", "--config", path}, strings.NewReader(""), &out, &diagnostics); err == nil {
			t.Fatalf("--config %s must fail", path)
		}
	}
}
