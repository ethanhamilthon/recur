package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"recur/internal/repl"
)

func TestInitializationFailureRetryAndSaveErrors(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
	}))
	defer server.Close()
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	configureTestAgent(t, a, Settings{Provider: "p", Model: "m", Providers: map[string]Provider{"p": {BaseURL: server.URL}}})
	a.REPL.Prefix = "if then"
	var events []Event
	err = a.Run(context.Background(), "keep request", func(e Event) { events = append(events, e) })
	var init *InitializationError
	if !errors.As(err, &init) || requests != 0 || len(a.History) != 0 {
		t.Fatalf("failed init sent/committed user request: %v", err)
	}
	// The input is shown immediately, then withdrawn when the prefix fails.
	if len(events) != 3 || events[0].Kind != "user" || events[0].Text != "keep request" ||
		events[1].Kind != "withdraw" || events[1].ID != events[0].ID || events[2].Kind != "error" {
		t.Fatalf("failed init must show, withdraw, then report: %+v", events)
	}
	if shown := ApplyEvent(ApplyEvent(nil, events[0]), events[1]); len(shown) != 0 {
		t.Fatalf("withdraw left the input displayed: %+v", shown)
	}
	for _, r := range a.Records() {
		if r.Kind == "user" {
			t.Fatal("withdrawn input stayed in the journal")
		}
	}
	a.REPL.Prefix = ""
	if err = a.Run(context.Background(), "keep request", func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || len(a.History) != 2 || !strings.Contains(string(a.History[0]), "(no output)") {
		t.Fatal("retry duplicated request or omitted output status")
	}
	if err = os.Remove(a.HistoryPath); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(a.HistoryPath, 0700); err != nil {
		t.Fatal(err)
	}
	a.REPL.Close()
	a.REPL.Prefix = "if then"
	err = a.Run(context.Background(), "retry me", func(Event) {})
	if !errors.As(err, &init) || !strings.Contains(err.Error(), "prefix failed") || !strings.Contains(err.Error(), "save history") {
		t.Fatalf("lost combined errors: %v", err)
	}
}

func TestInputDisplayedBeforeSlowPrefix(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
	}))
	defer server.Close()
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	configureTestAgent(t, a, Settings{Provider: "p", Model: "m", Providers: map[string]Provider{"p": {BaseURL: server.URL}}})
	release := t.TempDir() + "/go"
	a.REPL.Prefix = "while [ ! -e " + release + " ]; do sleep 0.01; done"
	events := make(chan Event, 16)
	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background(), "hello", func(e Event) { events <- e }) }()
	if first := <-events; first.Kind != "user" || first.Text != "hello" {
		t.Fatalf("input must be displayed before the prefix finishes: %+v", first)
	}
	if err = os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if next := <-events; next.Kind != "notice" || !strings.Contains(next.Text, "Shell session initialized.") {
		t.Fatalf("initialization notice must follow the prefix: %+v", next)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	// API order is unchanged: prefix notification first, then the request.
	if len(a.History) != 2 || !strings.Contains(string(a.History[0]), "prefix_output") || !strings.Contains(string(a.History[1]), "hello") {
		t.Fatalf("history order: %s", a.History)
	}
}

func TestCancelledInitializationDoesNotCommitRequest(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = a.Run(ctx, "unsent", func(Event) {})
	var initialization *InitializationError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &initialization) || len(a.History) != 0 {
		t.Fatal("cancelled request cannot be retried safely")
	}
}

func TestSaveFailurePropagatesAfterSuccessfulTurn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
	}))
	defer server.Close()
	a := &Agent{REPL: &repl.REPL{}, HistoryPath: t.TempDir()}
	defer a.Close()
	configureTestAgent(t, a, Settings{Provider: "p", Model: "m", Providers: map[string]Provider{"p": {BaseURL: server.URL}}})
	if err := a.Run(context.Background(), "go", func(Event) {}); err == nil || !strings.Contains(err.Error(), "save history") {
		t.Fatalf("save error lost: %v", err)
	}
}

func TestMidTurnAndRestoredPrefixNotifications(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var p struct {
			Instructions string
			Input        []json.RawMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		if strings.Contains(p.Instructions, "<prefix_output>") {
			t.Error("output leaked into system")
		}
		if requests == 2 {
			var tail struct{ Role, Content string }
			_ = json.Unmarshal(p.Input[len(p.Input)-1], &tail)
			if !strings.HasPrefix(tail.Content, "<notify>") || !strings.Contains(tail.Content, "session restarted") || !strings.Contains(tail.Content, "context ready") {
				t.Error("mid-turn reset output missing")
			}
			if !strings.Contains(string(p.Input[len(p.Input)-2]), "function_call_output") {
				t.Error("restart notice must follow tool output")
			}
		}
		if requests == 3 {
			var notice, user struct{ Role, Content string }
			_ = json.Unmarshal(p.Input[len(p.Input)-2], &notice)
			_ = json.Unmarshal(p.Input[len(p.Input)-1], &user)
			if !strings.Contains(notice.Content, "session restarted") || user.Content != "continue" {
				t.Error("restored notice must precede real user message")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		output := []any{}
		if requests == 1 {
			output = append(output, map[string]string{"type": "function_call", "name": "bash", "call_id": "c", "arguments": `{"command":"echo 42","timeout":null}`})
		}
		fmt.Fprintf(w, "data: %s\n\n", raw(map[string]any{"type": "response.completed", "response": map[string]any{"output": output}}))
	}))
	defer server.Close()
	cwd := t.TempDir()
	a, err := New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.REPL.Prefix = "echo 'context ready'"
	s := Settings{Provider: "p", Model: "m", Providers: map[string]Provider{"p": {BaseURL: server.URL}}}
	configureTestAgent(t, a, s)
	if err = a.Run(context.Background(), "start", func(e Event) {
		if e.Kind == "output" {
			a.REPL.Close()
		}
	}); err != nil {
		t.Fatal(err)
	}
	id := a.SessionInfo()["id"].(string)
	a.Close()
	b, err := New(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.REPL.Prefix = a.REPL.Prefix
	configureTestAgent(t, b, s)
	if err = b.Run(context.Background(), "continue", func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatal(requests)
	}
}
