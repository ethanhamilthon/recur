package tui

import (
	tea "github.com/charmbracelet/bubbletea"
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
		accepted := m.steering.Take()
		if len(accepted) != 1 || accepted[0] != text || len(m.entries) != 0 {
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
	if got := m.steering.Take(); len(got) != 1 || got[0] != "/help" {
		t.Fatalf("dismissed completion still executed command: %v", got)
	}
}

func TestReadOnlyAllowsNavigationCommandsAndUnsentSessionNotSaved(t *testing.T) {
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
	id := writer.SessionInfo()["id"].(string)
	follower, err := agent.New(cwd, id)
	if err != nil || !follower.ReadOnly {
		t.Fatalf("expected read-only follower: %v %v", follower.ReadOnly, err)
	}
	defer follower.Close()
	m := newModel(follower, cwd)
	for _, r := range "/help" {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(model)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(m.entries) == 0 || !strings.Contains(m.entries[len(m.entries)-1].Text, "/new") {
		t.Fatalf("help failed in read-only mode: %+v", m.entries)
	}
	m.input.SetValue("ordinary message")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.busy || !strings.Contains(m.entries[len(m.entries)-1].Text, "read-only") {
		t.Fatalf("read-only mode submitted message: %+v", m.entries)
	}
	m.input.SetValue("/new")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.a.ReadOnly {
		t.Fatal("/new did not leave read-only mode")
	}
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
