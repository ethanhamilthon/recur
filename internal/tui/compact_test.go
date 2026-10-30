package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"recur/internal/agent"
)

// TestCompactRunsInTheBackground guards the interface against a blocking
// summary request: /compact must return immediately with a running command.
func TestCompactRunsInTheBackground(t *testing.T) {
	a, err := agent.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	m := newModel(a, "/project")
	next, cmd := m.runCommand("/compact")
	m = next.(model)
	if cmd == nil || !m.busy || !m.compacting {
		t.Fatalf("compaction did not start in the background: busy=%v compacting=%v cmd=%v", m.busy, m.compacting, cmd != nil)
	}
	if m.steering != nil {
		t.Fatal("compaction must not claim steering state")
	}
	next, _ = m.Update(agent.Event{Kind: "done"})
	m = next.(model)
	if !m.compacting {
		t.Fatal("an unrelated finish event must not end compaction")
	}
	next, _ = m.Update(compactMsg{text: "Compacted 4 items into a summary; kept the last 4 turns."})
	m = next.(model)
	if m.busy || m.compacting || m.cancel != nil {
		t.Fatalf("compaction state was not cleared: busy=%v compacting=%v", m.busy, m.compacting)
	}
	last := m.entries[len(m.entries)-1]
	if last.Kind != "notice" || last.Text == "" {
		t.Fatalf("compaction result was not reported: %+v", last)
	}
}

// TestSendDuringCompactionIsRefused keeps a queued message from being stranded.
func TestSendDuringCompactionIsRefused(t *testing.T) {
	a, err := agent.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	m := newModel(a, "/project")
	m.busy, m.compacting, m.workingSince = true, true, time.Now()
	m.input.SetValue("while compacting")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(m.queued) != 0 || m.steering != nil {
		t.Fatal("a message was queued while the compaction request held the history")
	}
	if got := m.entries[len(m.entries)-1]; got.Kind != "notice" {
		t.Fatalf("expected a notice, got %+v", got)
	}
}

// TestCompactWithGuidanceRunsFromEnter: text after /compact is the guidance,
// not an ordinary chat message.
func TestCompactWithGuidanceRunsFromEnter(t *testing.T) {
	a, err := agent.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	m := newModel(a, "/project")
	m.input.SetValue("/compact keep the API decisions")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd == nil || !m.compacting || m.steering != nil {
		t.Fatalf("/compact with guidance did not start compaction: compacting=%v steering=%v", m.compacting, m.steering != nil)
	}
	if m.input.Value() != "" {
		t.Fatalf("composer not cleared: %q", m.input.Value())
	}
}
