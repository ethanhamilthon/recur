package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"recur/internal/agent"
	"recur/internal/repl"
)

func TestInputBorderGrows(t *testing.T) {
	m := newModel(&agent.Agent{}, "/project")
	m.input.SetValue("one\ntwo\nthree")
	m.refresh(false)
	if m.input.Height() != 3 {
		t.Fatalf("input height %d", m.input.Height())
	}
	view := ansi.Strip(m.inputBlock())
	lines := strings.Split(view, "\n")
	if len(lines) != 3 {
		t.Fatalf("border height: %q", view)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "▌") {
			t.Errorf("missing full-height border: %q", line)
		}
		if ansi.StringWidth(line) != 80 {
			t.Errorf("input width: %d", ansi.StringWidth(line))
		}
	}
	if lines := strings.Count(ansi.Strip(m.View()), "\n") + 1; lines != 24 {
		t.Errorf("view height %d want 24", lines)
	}
	m.input.SetValue(strings.Repeat("a", 160))
	m.refresh(false)
	if m.input.Height() < 3 {
		t.Error("wrapped input should grow")
	}
}
func TestComposerIsInScrollableTimeline(t *testing.T) {
	m := newModel(&agent.Agent{}, "/project")
	view := strings.Split(ansi.Strip(m.View()), "\n")
	inputRow := -1
	for i, line := range view {
		if strings.HasPrefix(line, "▌") {
			inputRow = i
			break
		}
	}
	if inputRow < 0 || inputRow > 5 {
		t.Fatalf("input pinned at row %d", inputRow)
	}
	for i := 0; i < 30; i++ {
		m.add("user", fmt.Sprintf("message %d", i))
	}
	m.refresh(true)
	if !strings.Contains(ansi.Strip(m.View()), "▌") {
		t.Fatal("input missing at timeline end")
	}
	m.viewport.GotoTop()
	if strings.Contains(ansi.Strip(m.View()), m.input.Placeholder) {
		t.Fatal("composer must scroll with history")
	}
	before := m.viewport.YOffset
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(model)
	if m.viewport.YOffset <= before {
		t.Fatal("PgDn did not scroll")
	}
	next, _ = m.Update(agent.Event{Kind: "delta", Text: "streaming"})
	m = next.(model)
	if m.viewport.YOffset != before+m.viewport.Height {
		t.Fatal("streaming should not steal scroll position")
	}
}
func TestCommandCompletionAndHelp(t *testing.T) {
	m := newModel(&agent.Agent{}, "/project")
	m.input.SetValue("/")
	if len(m.completions()) != len(commands) {
		t.Fatal("missing commands")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(model)
	if m.input.Value() != "/help" {
		t.Fatalf("completion %q", m.input.Value())
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "/new") {
		t.Fatal("help must list commands")
	}
	if len(m.a.History) != 0 {
		t.Fatal("commands must not be sent to model")
	}
	m.input.SetValue("/nope")
	m.busy = true
	m.steering = &agent.Steering{}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(m.queued) != 1 || m.queued[0].Text != "/nope" {
		t.Fatal("slash-prefixed text must be submitted as an ordinary instruction")
	}
}
func TestNewCommand(t *testing.T) {
	a := &agent.Agent{REPL: &repl.REPL{}, HistoryPath: filepath.Join(t.TempDir(), "history.json"), History: []json.RawMessage{json.RawMessage(`{"role":"user","content":"old"}`)}}
	m := newModel(a, "/project")
	next, _ := m.runCommand("/new")
	m = next.(model)
	if len(a.History) != 0 || len(m.entries) != 0 {
		t.Fatal("new should clear timeline and history")
	}
	if a.System == "" || a.REPL.Prefix == "" {
		t.Fatal("new should load prompt files")
	}
}
func TestTimelineHasNoBackgrounds(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previous)
	m := newModel(&agent.Agent{}, "/project")
	if block := m.inputBlock(); strings.Contains(block, "48;") {
		t.Errorf("input has a background: %q", block)
	}
	for _, kind := range []string{"user", "assistant", "code", "output", "notice", "error"} {
		block := renderEntry(entry{Kind: kind, Text: "hello\nworld"}, 80)
		for _, row := range strings.Split(block, "\n") {
			if ansi.StringWidth(row) != 80 {
				t.Errorf("%s width %d", kind, ansi.StringWidth(row))
			}
			if strings.Contains(row, "48;") {
				t.Errorf("%s has a background: %q", kind, row)
			}
		}
	}
}

func TestShiftEnterTerminalSequences(t *testing.T) {
	for _, sequence := range []string{"\x1b[13;2u", "\x1b[27;2;13~", "\x1b[13;2~"} {
		t.Run(fmt.Sprintf("%q", sequence), func(t *testing.T) {
			m := newModel(&agent.Agent{}, "/project")
			p := tea.NewProgram(m,
				tea.WithInput(strings.NewReader("one"+sequence+"two\x03")),
				tea.WithOutput(&strings.Builder{}),
				tea.WithoutRenderer(),
				tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
					if key, ok := msg.(tea.KeyMsg); ok && key.Type == tea.KeyCtrlC {
						return tea.QuitMsg{}
					}
					return msg
				}),
			)
			result, err := p.Run()
			if err != nil {
				t.Fatal(err)
			}
			got := result.(model)
			if got.input.Value() != "one\ntwo" || got.input.Height() != 2 || len(got.entries) != 0 {
				t.Fatalf("value %q, height %d, entries %v", got.input.Value(), got.input.Height(), got.entries)
			}
		})
	}
}

func TestMultilineEditing(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEnter, Alt: true}, {Type: tea.KeyCtrlJ}} {
		m := newModel(&agent.Agent{}, "/project")
		m.input.SetValue("onetwo")
		m.input.SetCursor(3)
		next, _ := m.Update(key)
		m = next.(model)
		if m.input.Value() != "one\ntwo" || m.input.Height() != 2 {
			t.Fatalf("%s: value %q, height %d", key, m.input.Value(), m.input.Height())
		}
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
		m = next.(model)
		if m.input.Line() != 0 {
			t.Fatal("up did not move to previous line")
		}
	}
}

func TestInputHeightMatchesWrappedRows(t *testing.T) {
	for _, text := range []string{
		strings.Repeat("a", 78),
		strings.Repeat("word ", 40),
		strings.Repeat("界", 80),
		"first\n\nlast\n",
	} {
		m := newModel(&agent.Agent{}, "/project")
		m.input.SetValue(text)
		m.refresh(false)
		probe := m.input
		rows := 0
		for _, line := range strings.Split(text, "\n") {
			probe.SetValue(line)
			rows += probe.LineInfo().Height
		}
		if m.input.Height() != min(rows, m.height/2) {
			t.Errorf("%q: height %d, rows %d", text, m.input.Height(), rows)
		}
		for _, line := range strings.Split(m.inputBlock(), "\n") {
			if ansi.StringWidth(line) != m.width {
				t.Errorf("width %d want %d", ansi.StringWidth(line), m.width)
			}
		}
	}
}

func TestCommandsWaitForTurnCompletion(t *testing.T) {
	for _, name := range []string{"/new", "/quit"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("RECUR_HOME", t.TempDir())
			a, err := agent.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			a.History = []json.RawMessage{json.RawMessage(`{"role":"user","content":"keep"}`)}
			m := newModel(a, "/project")
			m.busy = true
			n := int64(123)
			m.contextInput = &n
			cancelled := false
			m.cancel = func() { cancelled = true }
			next, cmd := m.runCommand(name)
			m = next.(model)
			if !cancelled || cmd != nil || !m.busy || m.pending != name || len(a.History) != 1 {
				t.Fatal("command did not wait for turn completion")
			}
			next, cmd = m.Update(agent.Event{Kind: "done"})
			m = next.(model)
			if m.busy || m.pending != "" {
				t.Fatal("pending command not consumed")
			}
			if name == "/new" {
				if len(a.History) != 0 || len(m.entries) != 0 || m.contextInput != nil {
					t.Fatal("new did not reset session")
				}
			} else {
				if cmd == nil {
					t.Fatal("quit command not issued")
				}
				if _, ok := cmd().(tea.QuitMsg); !ok {
					t.Fatal("expected quit")
				}
			}
		})
	}
}
