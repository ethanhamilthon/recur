package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"recur/internal/agent"
	"strings"
	"testing"
	"time"
)

func TestHeadlessTransportsAndExplicitResume(t *testing.T) {
	for _, mode := range []string{"stdio", "jsonl"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("RECUR_HOME", home)
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}]}}\n\n")
			}))
			defer server.Close()
			config := fmt.Sprintf("provider='test'\nmodel='m'\n[providers.test]\nbase_url=%q\n", server.URL)
			if err := os.WriteFile(filepath.Join(home, "settings.toml"), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			input := "first\nsecond\n"
			if mode == "jsonl" {
				input = "{\"prompt\":\"first\"}\n{\"prompt\":\"second\"}\n"
			}
			var out, diagnostics bytes.Buffer
			if err := Run(context.Background(), []string{"--mode", mode}, strings.NewReader(input), &out, &diagnostics); err != nil {
				t.Fatal(err)
			}
			if count != 2 || !strings.Contains(out.String(), "answer") || strings.Contains(out.String(), "\x1b") {
				t.Fatalf("bad output: %s", out.String())
			}
			files, err := filepath.Glob(filepath.Join(home, "sessions", "*", "*.jsonl"))
			if err != nil || len(files) != 1 {
				t.Fatalf("sessions: %v %v", files, err)
			}
			id := strings.TrimSuffix(filepath.Base(files[0]), ".jsonl")
			if mode == "jsonl" {
				decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
				done := 0
				for decoder.More() {
					var event map[string]any
					if err = decoder.Decode(&event); err != nil {
						t.Fatal(err)
					}
					if event["type"] == "done" {
						done++
					}
				}
				if done != 2 {
					t.Fatalf("done events=%d", done)
				}
			}
			out.Reset()
			if err = Run(context.Background(), []string{"--prompt", "continued", "--resume", id}, strings.NewReader(""), &out, &diagnostics); err != nil {
				t.Fatal(err)
			}
			files, _ = filepath.Glob(filepath.Join(home, "sessions", "*", "*.jsonl"))
			if len(files) != 1 {
				t.Fatal("resume created extra session")
			}
			if err = Run(context.Background(), []string{"--prompt", "fresh"}, strings.NewReader(""), &out, &diagnostics); err != nil {
				t.Fatal(err)
			}
			files, _ = filepath.Glob(filepath.Join(home, "sessions", "*", "*.jsonl"))
			if len(files) != 2 {
				t.Fatal("headless default did not create new session")
			}
		})
	}
}
func TestHeadlessRejectsInvalidInputAndModes(t *testing.T) {
	for _, args := range [][]string{{"--mode", "bad"}, {"unexpected"}} {
		var out bytes.Buffer
		if err := Run(context.Background(), args, strings.NewReader(""), &out, &out); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	t.Setenv("RECUR_HOME", t.TempDir())
	var out, diagnostics bytes.Buffer
	if err := Run(context.Background(), []string{"--mode", "jsonl"}, strings.NewReader("bad-json\n"), &out, &diagnostics); err == nil {
		t.Fatal("invalid JSONL accepted")
	}
}

func TestIdleHeadlessNotificationStartsTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECUR_HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
	}))
	defer server.Close()
	config := fmt.Sprintf("provider='test'\nmodel='m'\n[providers.test]\nbase_url=%q\n", server.URL)
	if err := os.WriteFile(filepath.Join(home, "settings.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	in, input := io.Pipe()
	output, out := io.Pipe()
	defer in.Close()
	defer output.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { defer out.Close(); done <- Run(ctx, []string{"--mode", "jsonl"}, in, out, io.Discard) }()
	decoder := json.NewDecoder(output)
	var event struct {
		Type    string
		Session map[string]any
	}
	if err := decoder.Decode(&event); err != nil {
		t.Fatal(err)
	}
	id := event.Session["id"].(string)
	var diagnostic bytes.Buffer
	if err := Run(ctx, []string{"notify", "-s", id, "hello </notify>"}, strings.NewReader(""), io.Discard, &diagnostic); err != nil {
		t.Fatal(err)
	}
	sawNotice := false
	for {
		var next struct{ Type, Text string }
		if err := decoder.Decode(&next); err != nil {
			t.Fatal(err)
		}
		if next.Type == "notice" && strings.Contains(next.Text, "hello &lt;/notify&gt;") {
			sawNotice = true
		}
		if next.Type == "done" {
			break
		}
	}
	if !sawNotice {
		t.Fatal("idle notification not delivered as a notice")
	}
	input.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestHeadlessCancellationWhileWaitingForInput(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	in, writer := io.Pipe()
	defer in.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, []string{"--mode", "stdio"}, in, io.Discard, io.Discard) }()
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("input read blocked cancellation")
	}
}

func TestHeadlessReadOnlyFollowerPublishesCommittedSnapshots(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	writer, err := agent.New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err = writer.Save(); err != nil {
		t.Fatal(err)
	}
	id := writer.SessionInfo()["id"].(string)
	output, out := io.Pipe()
	defer output.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer out.Close()
		done <- Run(ctx, []string{"--mode", "jsonl", "--resume", id}, strings.NewReader("ignored input"), out, io.Discard)
	}()
	decoder := json.NewDecoder(output)
	var event struct {
		Type     string
		ReadOnly bool `json:"read_only"`
		Records  []agent.Record
	}
	if err = decoder.Decode(&event); err != nil || event.Type != "session" || !event.ReadOnly {
		t.Fatalf("not a read-only follower: %+v %v", event, err)
	}
	if err = decoder.Decode(&event); err != nil || event.Type != "snapshot" {
		t.Fatalf("initial snapshot: %+v %v", event, err)
	}
	writer.History = append(writer.History, json.RawMessage(`{"role":"user","content":"new committed message"}`))
	if err = writer.Save(); err != nil {
		t.Fatal(err)
	}
	if err = decoder.Decode(&event); err != nil || len(event.Records) != 1 || event.Records[0].Text != "new committed message" {
		t.Fatalf("snapshot not refreshed: %+v %v", event, err)
	}
	cancel()
	if err = <-done; err != context.Canceled {
		t.Fatal(err)
	}
	if len(writer.History) != 1 {
		t.Fatal("follower mutated session")
	}
}
