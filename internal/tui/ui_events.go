package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"recur/internal/agent"
	"recur/internal/selfupdate"
)

// Version is shown on the start screen; main sets it from the VERSION file.
var Version = "dev"

// updateCheckMsg carries the newer release tag found in the background, if any.
type updateCheckMsg struct{ latest string }

// checkUpdate asks GitHub for a newer release without delaying the first frame.
func checkUpdate() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return updateCheckMsg{latest: selfupdate.Available(ctx, Version)}
	}
}

// Run runs the terminal interface until the user exits.
// The caller remains responsible for closing the agent and releasing session ownership.
func Run(a *agent.Agent, cwd string) error {
	keyboard := keyboardProtocol{}
	defer keyboard.close()
	final, err := tea.NewProgram(newModel(a, cwd), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithFilter(keyboard.filter)).Run()
	keyboard.close()
	if m, ok := final.(model); ok {
		if m.cancel != nil {
			m.cancel()
			for event := range m.events {
				if event.Kind == "done" {
					break
				}
			}
		}
		if m.menuCancel != nil {
			m.menuCancel()
		}
	}
	return err
}

type workingTick struct{ started time.Time }

func workingPulse(started time.Time) tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return workingTick{started} })
}
func (m *model) requestCancel() {
	m.cancelling = true
	if m.cancel != nil {
		m.cancel()
	}
	m.refresh(false)
}
func (m *model) queueSteering(text string) {
	if m.cancelling {
		m.add("notice", "Cancellation in progress; your message remains in the input.")
		return
	}
	input := agent.Input{Text: text, Images: m.images}
	if m.steering.PushInput(input) {
		m.queued = append(m.queued, input)
		m.images = nil
		m.input.Reset()
	} else {
		m.add("notice", "Steering queue is full; your message remains in the input.")
	}
}
func (m model) workingBlock() string {
	var out strings.Builder
	for _, input := range m.queued {
		text := input.Display()
		row := "  queued · " + strings.ReplaceAll(ansi.Strip(text), "\n", " ")
		out.WriteString(lipgloss.NewStyle().Foreground(muted).Render(ansi.Truncate(row, max(1, m.width), "…")) + "\n")
	}
	if m.busy {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		elapsed := max(time.Duration(0), time.Since(m.workingSince))
		label := "working"
		if m.compacting {
			label = "compacting"
		}
		if m.cancelling {
			label = "cancelling"
		}
		line := "  " + lipgloss.NewStyle().Foreground(inputBlue).Render(frames[int(elapsed/(80*time.Millisecond))%len(frames)]) +
			lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf(" %s · %s", label, formatDuration(elapsed)))
		out.WriteString(ansi.Truncate(line, max(1, m.width), "…") + "\n\n")
	}
	return out.String()
}

// compactMsg carries the result of the background summary started by /compact.
type compactMsg struct {
	text string
	err  error
}

// startCompact summarizes the old turns in the background so the interface keeps
// responding and the summary request can still be cancelled.
func (m model) startCompact(prompt string) (tea.Model, tea.Cmd) {
	m.busy, m.cancelling, m.compacting = true, false, true
	m.workingSince = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	a := m.a
	m.refresh(true)
	return m, tea.Batch(func() tea.Msg {
		defer cancel()
		text, err := a.Compact(ctx, prompt)
		return compactMsg{text: text, err: err}
	}, workingPulse(m.workingSince))
}

func (m model) startTurn(text string) (tea.Model, tea.Cmd) {
	m.a.RememberDisplay(m.entries)
	submission := agent.Input{Text: text, Images: m.images}
	m.turnImages, m.images = m.images, nil
	m.input.Reset()
	m.hideCompletion = false
	m.busy = true
	m.cancelling = false
	m.workingSince = time.Now()
	m.steering = &agent.Steering{}
	m.queued = nil
	m.retryInput = ""
	m.events = make(chan agent.Event, 64)
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	ch, a, steering := m.events, m.a, m.steering
	go func() {
		defer cancel()
		err := a.RunInput(ctx, submission, func(e agent.Event) { ch <- e }, steering)
		if errors.Is(err, context.Canceled) {
			ch <- agent.Event{Kind: "notice", Text: "Turn cancelled."}
		}
		message := ""
		if err != nil {
			message = err.Error()
		}
		ch <- agent.Event{Kind: "done", Text: message}
	}()
	m.refresh(true)
	return m, tea.Batch(m.wait(), workingPulse(m.workingSince))
}
func (m *model) applyAgentEvent(e agent.Event) {
	if e.Kind == "user" && e.Steering && len(m.queued) > 0 && m.queued[0].Display() == e.Text {
		m.queued = m.queued[1:]
	}
	if e.Meta != nil && e.Meta.RequestID != "" {
		m.contextInput = e.Meta.ContextInput
	}
	m.entries = agent.ApplyEvent(m.entries, e)
}
