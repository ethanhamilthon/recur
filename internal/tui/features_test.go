package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"recur/internal/agent"
)

func TestErrorNoticeStyleAndVisibility(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previous)
	e := entry{Kind: "error", Text: "First line\nSecond line\tDetails", Meta: &agent.Metadata{}}
	for _, width := range []int{1, 2, 10, 80} {
		block := renderEntry(e, width, 0)
		plain := ansi.Strip(block)
		if strings.ContainsAny(plain, "\r\n\t") || ansi.StringWidth(plain) > width || plain == "" {
			t.Fatalf("error not visible in one row at %d: %q", width, plain)
		}
		if width == 80 {
			if !strings.Contains(block, "38;2;240;158;172") || !strings.Contains(block, "38;2;96;96;96") {
				t.Fatalf("error icon/text colors missing: %q", block)
			}
			if strings.TrimSpace(plain) != "! · First line Second line Details" {
				t.Fatal(plain)
			}
		}
	}
	if !strings.Contains(e.Text, "\n") {
		t.Fatal("full diagnostics modified")
	}
}

func TestUpRecallsOnlyPendingSteeringOnEmptyInput(t *testing.T) {
	m := newModel(&agent.Agent{}, "/project")
	m.steering = &agent.Steering{}
	m.queueSteering("first")
	m.queueSteering("second")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	if m.input.Value() != "second" || len(m.queued) != 1 {
		t.Fatal("up did not recall last pending instruction")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	if m.input.Value() != "second" || len(m.queued) != 1 {
		t.Fatal("nonempty draft overwritten")
	}
	m.input.Reset()
	_ = m.steering.Take() // Agent wins the acceptance race before the UI handles Up.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	if m.input.Value() != "" || !strings.Contains(m.entries[len(m.entries)-1].Text, "already accepted") {
		t.Fatal("accepted instruction resurrected")
	}
	m.applyAgentEvent(agent.Event{Kind: "user", Text: "first", Steering: true})
	if len(m.queued) != 0 {
		t.Fatal("accepted preview not cleared")
	}
}

func TestResumeAutocompleteIsScopedAndExecutesSelectedSession(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	cwd := t.TempDir()
	a, err := agent.New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.History = append(a.History, []byte(`{"role":"user","content":"older chat"}`))
	if err = a.Save(); err != nil {
		t.Fatal(err)
	}
	oldID := strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl")
	if err = a.NewSession(); err != nil {
		t.Fatal(err)
	}
	b, err := agent.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	foreign := strings.TrimSuffix(filepath.Base(b.HistoryPath), ".jsonl")
	m := newModel(a, cwd)
	m.input.SetValue("/resume")
	matches := m.completions()
	if len(matches) != 1 {
		t.Fatalf("session choices: %v", matches)
	}
	for i, c := range matches {
		if strings.Contains(c.name, foreign) {
			t.Fatal("another cwd leaked into completion")
		}
		if c.name == "/resume "+oldID {
			m.selected = i
		}
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(model)
	if m.input.Value() != "/resume "+oldID {
		t.Fatal("Tab did not complete selected session")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(m.entries) != 1 || m.entries[0].Text != "older chat" || m.input.Value() != "" {
		t.Fatalf("resume failed: %+v", m.entries)
	}
	m.input.SetValue("/resume " + oldID[:12])
	if len(m.completions()) == 0 {
		t.Fatal("session prefix filtering failed")
	}
	if _, err = os.Stat(a.HistoryPath); err != nil {
		t.Fatal(err)
	}
}

func TestResumeWaitsAndUIErrorsStayOutOfAPIHistory(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	a, err := agent.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Save(); err != nil {
		t.Fatal(err)
	}
	oldID := strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl")
	m := newModel(a, a.REPL.Dir)
	next, _ := m.runCommand("/unknown")
	m = next.(model)
	next, _ = m.runCommand("/new")
	m = next.(model)
	if len(a.History) != 0 {
		t.Fatal("UI diagnostic leaked into API history")
	}
	newPath := a.HistoryPath
	m.busy = true
	cancelled := false
	m.cancel = func() { cancelled = true }
	next, _ = m.runCommand("/resume " + oldID)
	m = next.(model)
	if !cancelled || a.HistoryPath != newPath || m.pending != "/resume "+oldID {
		t.Fatal("resume did not wait for active turn")
	}
	next, _ = m.Update(agent.Event{Kind: "done", Text: "cancelled"})
	m = next.(model)
	if a.HistoryPath == newPath || len(m.entries) != 1 || m.entries[0].Kind != "error" {
		t.Fatal("resume lost saved UI diagnostic")
	}
	if len(a.History) != 0 {
		t.Fatal("saved UI diagnostic sent to model")
	}
}

func TestResumeCompletionWindowKeepsSelectionVisible(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	a, err := agent.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for i := 0; i < 10; i++ {
		if err = a.Save(); err != nil {
			t.Fatal(err)
		}
		if err = a.NewSession(); err != nil {
			t.Fatal(err)
		}
	}
	m := newModel(a, a.REPL.Dir)
	m.input.SetValue("/resume")
	matches := m.completions()
	m.selected = len(matches) - 1
	for _, height := range []int{7, 24} {
		m.height = height
		plain := ansi.Strip(m.timeline())
		if !strings.Contains(plain, matches[m.selected].label) {
			t.Fatal("selected session is outside visible completion window")
		}
		count := 0
		for _, row := range strings.Split(plain, "\n") {
			row = strings.TrimPrefix(strings.TrimSpace(row), "› ")
			if strings.HasPrefix(row, "< ") {
				count++
			}
		}
		if count > 8 || count < 1 {
			t.Fatalf("unbounded session menu: %d", count)
		}
	}
}

func TestPrefixFailureRecoversOriginalThenSteeringAndDraft(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	a, err := agent.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.REPL.Prefix = "1/0"
	m := newModel(a, a.REPL.Dir)
	next, _ := m.startTurn("original request")
	m = next.(model)
	m.queueSteering("queued instruction")
	m.input.SetValue("unfinished draft")
	deadline := time.After(5 * time.Second)
	for m.busy {
		select {
		case e := <-m.events:
			next, _ = m.Update(e)
			m = next.(model)
		case <-deadline:
			if m.cancel != nil {
				m.cancel()
			}
			t.Fatal("initialization failure did not finish")
		}
	}
	if m.input.Value() != "original request\n\nqueued instruction\n\nunfinished draft" {
		t.Fatalf("retry text lost or reordered: %q", m.input.Value())
	}
	if len(a.History) != 0 {
		t.Fatal("failed initialization committed user content")
	}
}
