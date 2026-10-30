package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"path/filepath"
	"recur/internal/agent"
	"strings"
	"testing"
)

func TestEnterExecutesSelectedCommandWithoutExtraConfirmation(t *testing.T) {
	for _, text := range []string{"/", "/he", "/help"} {
		m := newModel(&agent.Agent{}, "/project")
		m.input.SetValue(text)
		if text == "/" {
			m.selected = 1
		}
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(model)
		if cmd != nil || m.busy || len(m.entries) != 1 || !strings.Contains(m.entries[0].Text, "keyboard") && !strings.Contains(m.entries[0].Text, "Enter:") {
			t.Fatalf("%q: command not executed immediately: %+v", text, m.entries)
		}
		if m.input.Value() != "" {
			t.Fatalf("%q: composer not cleared", text)
		}
	}
}

func TestSlashTextIsOrdinarySteeringWithoutCommandCompletion(t *testing.T) {
	for _, text := range []string{"/tmp/example.go", "/unknown", "/help me with this", "/new\nexplain this", "/resume missing-id"} {
		m := newModel(&agent.Agent{}, "/project")
		m.busy, m.steering = true, &agent.Steering{}
		m.input.SetValue(text)
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(model)
		accepted := m.steering.TakeInputs()
		if len(accepted) != 1 || accepted[0].Text != text || len(m.entries) != 0 {
			t.Fatalf("%q: got accepted=%v, entries=%v", text, accepted, m.entries)
		}
	}
}

func TestDismissedCompletionAllowsLiteralCommandText(t *testing.T) {
	m := newModel(&agent.Agent{}, "/project")
	m.busy, m.steering = true, &agent.Steering{}
	m.input.SetValue("/help")
	m.hideCompletion = true
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if got := m.steering.TakeInputs(); len(got) != 1 || got[0].Text != "/help" {
		t.Fatalf("dismissed completion still executed command: %v", got)
	}
}

func TestNavigationCommandsAndUnsentSessionNotSaved(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	cwd := t.TempDir()
	writer, err := agent.New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if sessions, _ := writer.Sessions(); len(sessions) != 0 {
		t.Fatalf("empty session created before first message: %v", sessions)
	}
	writer.History = append(writer.History, []byte(`{"role":"user","content":"saved"}`))
	if err = writer.Save(); err != nil {
		t.Fatal(err)
	}
	m := newModel(writer, cwd)
	for _, r := range "/help" {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(model)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(m.entries) == 0 || !strings.Contains(m.entries[len(m.entries)-1].Text, "/new") {
		t.Fatalf("help failed: %+v", m.entries)
	}
	m.input.SetValue("/new")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m.input.SetValue("/quit")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd == nil {
		t.Fatal("/quit did not exit")
	}
	if sessions, _ := m.a.Sessions(); len(sessions) != 1 {
		t.Fatalf("unsent /new session was saved on /quit: %v", sessions)
	}
}

func TestRenameSessionPersistsTitle(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	cwd := t.TempDir()
	a, err := agent.New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.History = append(a.History, []byte(`{"role":"user","content":"first message"}`))
	if err = a.Save(); err != nil {
		t.Fatal(err)
	}
	m := newModel(a, cwd)
	run := func(text string) {
		m.input.SetValue(text)
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(model)
	}
	title := func() string {
		sessions, err := m.a.Sessions()
		if err != nil || len(sessions) != 1 {
			t.Fatalf("sessions: %v %v", sessions, err)
		}
		return sessions[0].Title
	}
	run("/rename")
	if m.input.Value() != "/rename " {
		t.Fatalf("bare /rename should prompt for a name, got %q", m.input.Value())
	}
	run("/rename  My   project ")
	if got := title(); got != "My project" {
		t.Fatalf("title after rename = %q", got)
	}
	id := strings.TrimSuffix(filepath.Base(m.a.HistoryPath), ".jsonl")
	if err := m.a.NewSession(); err != nil {
		t.Fatal(err)
	}
	if err := m.a.Resume(id); err != nil {
		t.Fatal(err)
	}
	if err := m.a.Save(); err != nil {
		t.Fatal(err)
	}
	if got := title(); got != "My project" {
		t.Fatalf("title lost after resume and save = %q", got)
	}
	run("/rename ")
	if got := title(); got != "first message" {
		t.Fatalf("empty rename should restore default, got %q", got)
	}
}
