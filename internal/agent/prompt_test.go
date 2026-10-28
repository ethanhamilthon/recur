package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"recur/internal/repl"
	"recur/prompts"
	"strings"
	"testing"
)

func TestRuntimeContractOnlyInToolDescription(t *testing.T) {
	if prompts.System != "You are Recur, a coding agent" {
		t.Fatalf("unexpected default system: %q", prompts.System)
	}
	for _, system := range []string{"", "Custom instructions retained verbatim."} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Instructions string
				Tools        []struct{ Name, Description string }
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			want := system
			if want == "" {
				want = prompts.System
			}
			want += "\n\n<prefix>\nprint('context')\n</prefix>"
			if body.Instructions != want {
				t.Errorf("system prompt changed: %q; want %q", body.Instructions, want)
			}
			if len(body.Tools) != 1 || body.Tools[0].Name != "bash" || body.Tools[0].Description != repl.ToolDescription {
				t.Errorf("missing runtime contract: %+v", body.Tools)
			}
			if strings.Contains(body.Instructions, "<runtime>") || strings.Contains(body.Instructions, "<prefix_output>") {
				t.Error("runtime/output leaked into instructions")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
		}))
		a := &Agent{System: system, REPL: &repl.REPL{Prefix: "print('context')"}}
		configureTestAgent(t, a, Settings{Provider: "test", Model: "test", Providers: map[string]Provider{"test": {BaseURL: server.URL}}})
		_, err := a.request(context.Background(), func(Event) {})
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
