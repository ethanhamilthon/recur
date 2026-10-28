package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"recur/internal/agent"
)

func TestWorkingIndicatorLifecycleAndScrolling(t *testing.T) {
	m := newModel(&agent.Agent{}, "/project")
	if m.workingBlock() != "" {
		t.Fatal("idle indicator shown")
	}
	m.busy, m.workingSince = true, time.Now()
	for i := 0; i < 40; i++ {
		m.add("user", "history")
	}
	m.refresh(true)
	if !strings.Contains(ansi.Strip(m.View()), "working") {
		t.Fatal("missing indicator")
	}
	m.viewport.GotoTop()
	offset := m.viewport.YOffset
	next, cmd := m.Update(workingTick{m.workingSince})
	m = next.(model)
	if cmd == nil || m.viewport.YOffset != offset {
		t.Fatal("tick must animate without stealing scroll")
	}
	next, cmd = m.Update(workingTick{time.Now().Add(-time.Hour)})
	m = next.(model)
	if cmd != nil {
		t.Fatal("stale timer restarted")
	}
	m.cancel = func() {}
	m.requestCancel()
	if !strings.Contains(ansi.Strip(m.workingBlock()), "cancelling") {
		t.Fatal("missing cancellation status")
	}
	next, _ = m.Update(agent.Event{Kind: "done"})
	m = next.(model)
	if m.workingBlock() != "" {
		t.Fatal("indicator persisted after completion")
	}
}

func TestSteeringQueueAndRecovery(t *testing.T) {
	m := newModel(&agent.Agent{}, "/project")
	m.busy, m.workingSince = true, time.Now()
	m.steering = &agent.Steering{}
	m.cancel = func() {}
	m.input.SetValue("follow up")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.input.Value() != "" || len(m.queued) != 1 || strings.Join(m.steering.Take(), "") != "follow up" {
		t.Fatal("message not queued")
	}
	if !strings.Contains(ansi.Strip(m.workingBlock()), "queued · follow up") {
		t.Fatal("pending message invisible")
	}
	m.applyAgentEvent(agent.Event{Kind: "user", Text: "follow up", Steering: true})
	if len(m.queued) != 0 || len(m.entries) != 1 {
		t.Fatal("accepted steering duplicated or not cleared")
	}
	m.input.SetValue("not accepted")
	m.queueSteering(m.input.Value())
	m.requestCancel()
	m.input.SetValue("draft")
	next, _ = m.Update(agent.Event{Kind: "done", Text: context.Canceled.Error()})
	m = next.(model)
	if m.input.Value() != "not accepted\n\ndraft" || len(m.queued) != 0 {
		t.Fatalf("pending text lost: %q", m.input.Value())
	}
}

func TestSteeringQueueFullKeepsInput(t *testing.T) {
	m := newModel(&agent.Agent{}, "/project")
	m.steering = &agent.Steering{}
	for i := 0; i < 64; i++ {
		m.steering.Push("first")
	}
	m.input.SetValue("second")
	m.queueSteering("second")
	if m.input.Value() != "second" || len(m.queued) != 0 {
		t.Fatal("queue-full submission lost")
	}
}

func TestLateSteeringContinuesWithoutLosingDraft(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct{ Input []json.RawMessage }
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
	}))
	defer server.Close()
	home := t.TempDir()
	t.Setenv("RECUR_HOME", home)
	config := fmt.Sprintf("provider = 'p'\nmodel = 'm'\n[providers.p]\nbase_url = '%s'\n", server.URL)
	if err := os.WriteFile(filepath.Join(home, "settings.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := agent.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.REPL.Prefix = ""
	m := newModel(a, "/project")
	m.busy, m.workingSince = true, time.Now()
	m.queued = []agent.Input{{Text: "late first"}, {Text: "late second"}}
	m.input.SetValue("unsent draft")
	for i := 0; i < 40; i++ {
		m.add("notice", "Older event")
	}
	m.refresh(false)
	m.viewport.GotoTop()
	next, _ := m.Update(agent.Event{Kind: "done"})
	m = next.(model)
	if m.input.Value() != "unsent draft" || !m.busy || m.viewport.YOffset != 0 {
		t.Fatal("late continuation lost draft or failed to start")
	}
	defer func() {
		if m.cancel != nil {
			m.cancel()
		}
	}()
	deadline := time.After(5 * time.Second)
	for m.busy {
		select {
		case e := <-m.events:
			next, _ = m.Update(e)
			m = next.(model)
		case <-deadline:
			t.Fatal("continuation did not complete")
		}
	}
	var users []string
	for _, e := range m.entries {
		if e.Kind == "user" {
			users = append(users, e.Text)
		}
	}
	if strings.Join(users, ",") != "late first,late second" || len(m.queued) != 0 || m.input.Value() != "unsent draft" {
		t.Fatalf("lost or duplicated steering: %v %q", users, m.input.Value())
	}
}
