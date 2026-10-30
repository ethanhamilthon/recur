package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"recur/internal/agent"
	"recur/internal/repl"
)

type entry = agent.Record
type renderedEntry struct {
	source   entry
	width    int
	maxLines int
	Text     string
}
type model struct {
	a                       *agent.Agent
	input                   textarea.Model
	viewport                viewport.Model
	entries                 []entry
	events                  chan agent.Event
	busy                    bool
	cancelling              bool
	compacting              bool
	workingSince            time.Time
	steering                *agent.Steering
	queued                  []agent.Input
	images, turnImages      []repl.Image
	retry                   bool
	pasteGeneration         int
	pasting                 bool
	retryInput              string
	cancel                  context.CancelFunc
	pending                 string
	width, height           int
	cwd                     string
	selected                int
	hideCompletion          bool
	cache                   []renderedEntry
	inputValue              string
	inputWidth, inputHeight int
	contextInput            *int64
	sel                     *selection
	inputSel                *composerSelection
	inputDrag               bool
	inputTop                int
	layoutValue             string
	layoutWidth             int
	layout                  []composerRow
	latest                  string
	menuKind                string
	menu                    []command
	effort, menuGeneration  int
	menuCancel              context.CancelFunc
	setup                   []string
	sessions                []agent.SessionSummary
}

func newModel(a *agent.Agent, cwd string) model {
	input := textarea.New()
	input.Placeholder = "Ask anything, or type / for commands"
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.CharLimit = 0
	input.MaxHeight = 10000
	input.MaxWidth = 10000
	input.SetHeight(1)
	input.Focus()
	input.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")
	base := lipgloss.NewStyle().Foreground(foreground)
	style := textarea.Style{Base: base, CursorLine: base, Text: base, Placeholder: base.Foreground(muted), EndOfBuffer: base}
	input.FocusedStyle = style
	input.BlurredStyle = style
	m := model{a: a, input: input, viewport: viewport.New(80, 24), width: 80, height: 24, cwd: cwd}
	m.restoreHistory()
	m.refresh(true)
	return m
}

func (m *model) restoreHistory() {
	m.entries, m.cache, m.contextInput = m.a.Records(), nil, nil
	for _, r := range m.entries {
		if r.Meta != nil && r.Meta.RequestID != "" {
			m.contextInput = r.Meta.ContextInput
		}
	}
}

type followerTick struct{}

func (m model) idleNotifications() []string {
	if m.busy {
		return nil
	}
	return m.a.Notifications()
}

func followerPulse() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return followerTick{} })
}
func (m model) Init() tea.Cmd { return tea.Batch(textarea.Blink, followerPulse(), checkUpdate()) }
func (m model) wait() tea.Cmd { ch := m.events; return func() tea.Msg { return <-ch } }
func (m *model) resizeInput() {
	value, width, height := m.input.Value(), max(1, m.width-2), max(1, m.height/2)
	if m.inputWidth == width && m.inputHeight == height && m.inputValue == value {
		return
	}
	m.inputWidth, m.inputHeight, m.inputValue = width, height, value
	m.input.SetWidth(width)
	lines := 0
	// A textarea copy shares its viewport pointer; probing it resets live scroll.
	probe := textarea.New()
	probe.CharLimit = 0
	probe.MaxWidth = m.input.MaxWidth
	probe.Prompt = ""
	probe.ShowLineNumbers = false
	probe.SetWidth(m.input.Width())
	for _, line := range strings.Split(value, "\n") {
		probe.SetValue(line)
		lines += probe.LineInfo().Height
	}
	m.input.SetHeight(min(height, max(1, lines)))
}

// updateInput runs the textarea update with its maximum height. The textarea
// scrolls its viewport to the cursor during Update using its current height, so
// with a stale smaller height an inserted newline would scroll earlier lines out
// of view before the height is grown.
func (m *model) updateInput(msg tea.Msg) tea.Cmd {
	previous := m.input.Height()
	m.input.SetHeight(max(1, m.height/2))
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.input.SetHeight(previous)
	m.inputValue, m.inputWidth = "\x00", 0 // force resizeInput to recompute height
	return cmd
}
func (m *model) refresh(follow bool) {
	if text := m.input.Value(); !m.hideCompletion && (text == "/resume" || strings.HasPrefix(text, "/resume ")) {
		if m.sessions == nil {
			m.sessions, _ = m.a.Sessions()
		}
	} else {
		m.sessions = nil
	}
	m.resizeInput() // Constant-time unless input text or dimensions changed.
	m.viewport.Width = max(1, m.width)
	m.viewport.Height = max(1, m.height)
	m.viewport.SetContent(m.timeline())
	if follow {
		m.viewport.GotoBottom()
	}
}

// draft is the composer state that must survive a background turn start.
type draft struct {
	text   string
	images []repl.Image
	offset int
}

func (m *model) keepDraft() draft {
	return draft{m.input.Value(), m.images, m.viewport.YOffset}
}
func (m *model) restoreDraft(d draft, follow bool) {
	m.input.SetValue(d.text)
	m.images = d.images
	m.refresh(follow)
	if !follow {
		m.viewport.SetYOffset(d.offset)
	}
}
func (m *model) add(kind, text string) { m.entries = append(m.entries, entry{Kind: kind, Text: text}) }

// stop adds a diagnostic, refreshes the view and ends the update cycle.
func (m *model) stop(kind, text string) (tea.Model, tea.Cmd) {
	m.add(kind, text)
	m.refresh(true)
	return *m, nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	msg = normalizeTerminalKey(msg)
	follow := m.viewport.AtBottom()
	switch v := msg.(type) {
	case clipboardResult:
		cmd := m.acceptClipboard(v)
		m.refresh(true)
		return m, cmd
	case followerTick:
		// While busy, the turn itself accepts notifications as steering.
		if messages := m.idleNotifications(); len(messages) > 0 {
			draft := m.keepDraft()
			next, cmd := m.startTurn(messages[0])
			m = next.(model)
			for _, text := range messages[1:] {
				m.steering.Push(text)
			}
			m.restoreDraft(draft, follow)
			return m, tea.Batch(cmd, followerPulse())
		}
		return m, followerPulse()
	case modelChoices:
		if m.menuKind != "model" || v.generation != m.menuGeneration {
			return m, nil
		}
		var failed []string
		for name, err := range v.errs {
			failed = append(failed, name+": "+err.Error())
		}
		sort.Strings(failed)
		if len(v.models) == 0 {
			m.closePicker()
			if len(failed) > 0 {
				m.add("error", "Could not list models.\n"+strings.Join(failed, "\n"))
			} else {
				m.add("notice", "No models returned by providers.")
			}
		} else {
			current := m.a.Settings()
			for i, c := range v.models {
				m.menu = append(m.menu, command{name: c.Model, description: c.Provider})
				if c.Provider == current.Provider && c.Model == current.Model {
					m.selected = i
				}
			}
			if len(failed) > 0 {
				m.add("notice", "Some providers failed to list models:\n"+strings.Join(failed, "\n"))
			}
		}
		m.refresh(true)
		return m, nil
	case updateCheckMsg:
		if v.latest != "" && v.latest != m.latest {
			m.latest = v.latest
			m.refresh(follow)
		}
		return m, nil
	case workingTick:
		if !m.busy || !v.started.Equal(m.workingSince) {
			return m, nil
		}
		m.refresh(follow)
		return m, workingPulse(m.workingSince)

	case tea.WindowSizeMsg:
		m.width, m.height = max(1, v.Width), max(1, v.Height)
		m.refresh(follow)
		return m, nil
	case tea.MouseMsg:
		if cmd, handled := m.mouseSelection(v); handled {
			m.refresh(false)
			return m, cmd
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	case composerShortcut:
		key := tea.KeyMsg{Type: tea.KeyCtrlC}
		if v.cut {
			key = tea.KeyMsg{Type: tea.KeyCtrlX}
		}
		if cmd, handled := m.composerKey(key); handled {
			return m, cmd
		}
		return m, nil
	case clipboardCopied:
		if v.err != nil {
			return m.stop("error", "Copy failed: "+v.err.Error())
		}
		return m.stop("notice", fmt.Sprintf("Copied %d lines to the clipboard.", v.lines))
	case compactMsg:
		m.busy, m.compacting, m.cancel = false, false, nil
		if errors.Is(v.err, context.Canceled) {
			return m.stop("notice", "Compaction cancelled.")
		}
		if v.err != nil {
			return m.stop("error", v.err.Error())
		}
		m.restoreHistory()
		return m.stop("notice", v.text)
	case agent.Event:
		if v.Kind == "done" {
			m.busy = false
			m.cancel = nil
			if len(m.queued) > 0 && v.Text == "" && !m.cancelling && m.pending == "" {
				draft := m.keepDraft()
				queued := append([]agent.Input(nil), m.queued...)
				m.images = queued[0].Images
				next, cmd := m.startTurn(queued[0].Text)
				m = next.(model)
				for _, text := range queued[1:] {
					m.images = text.Images
					m.queueSteering(text.Text)
				}
				m.restoreDraft(draft, follow)
				return m, cmd
			}
			if len(m.queued) > 0 || m.retry {
				drafts := m.queued
				if m.retry {
					drafts = append([]agent.Input{{Text: m.retryInput, Images: m.turnImages}}, drafts...)
				}
				drafts = append(drafts, agent.Input{Text: m.input.Value(), Images: m.images})
				var texts []string
				m.images = nil
				for _, draft := range drafts {
					if draft.Text != "" {
						texts = append(texts, draft.Text)
					}
					m.images = append(m.images, draft.Images...)
				}
				m.input.SetValue(strings.Join(texts, "\n\n"))
				m.queued = nil
			}
			m.turnImages = nil
			m.retry = false
			m.steering = nil
			m.retryInput = ""
			m.cancelling = false

			if m.pending != "" {
				name := m.pending
				m.pending = ""
				return m.runCommand(name)
			}
			m.refresh(follow)
			return m, nil
		}
		if v.Kind == "withdraw" {
			m.retryInput, m.retry = v.Text, true
		}
		m.applyAgentEvent(v)
		m.refresh(follow)
		return m, m.wait()
	case tea.KeyMsg:
		if next, cmd, handled := m.pickerKey(v); handled {
			return next, cmd
		}
		if cmd, handled := m.composerKey(v); handled {
			return m, cmd
		}
		switch v.String() {
		case "ctrl+v":
			return m.pasteClipboard()
		case "ctrl+x":
			if len(m.images) > 0 {
				image := m.images[len(m.images)-1]
				if image.Path != "" {
					_ = os.Remove(image.Path)
				}
				m.images = m.images[:len(m.images)-1]
				m.refresh(true)
				return m, nil
			}
		case "ctrl+c":
			if m.busy {
				m.requestCancel()
				return m, nil
			}
			return m.runCommand("/quit")
		case "ctrl+d":
			if !m.busy && m.input.Value() == "" {
				return m.runCommand("/quit")
			}
		case "esc":
			if len(m.completions()) == 0 && m.busy {
				m.requestCancel()
			}
			m.hideCompletion = true
			m.refresh(true)
			return m, nil
		case "pgup", "pgdown", "ctrl+up", "ctrl+down", "ctrl+home", "ctrl+end":
			switch v.String() {
			case "ctrl+home":
				m.viewport.GotoTop()
			case "ctrl+end":
				m.viewport.GotoBottom()
			case "ctrl+up":
				m.viewport.ScrollUp(1)
			case "ctrl+down":
				m.viewport.ScrollDown(1)
			default:
				m.viewport, _ = m.viewport.Update(msg)
			}
			return m, nil
		case "tab":
			if m.complete() {
				m.refresh(true)
				return m, nil
			}
		case "up", "down":
			if matches := m.completions(); len(matches) > 0 {
				delta := 1
				if v.String() == "up" {
					delta = -1
				}
				m.selected = (m.selected + delta + len(matches)) % len(matches)
				m.refresh(true)
				return m, nil
			}
			if v.String() == "up" && m.input.Value() == "" && len(m.images) == 0 && len(m.queued) > 0 {
				if input, ok := m.steering.RecallInput(); ok {
					for i := len(m.queued) - 1; i >= 0; i-- {
						if m.queued[i].Display() == input.Display() {
							m.queued = append(m.queued[:i], m.queued[i+1:]...)
							break
						}
					}
					m.input.SetValue(input.Text)
					m.images = input.Images
					m.input.CursorEnd()
				} else {
					return m.stop("notice", "That instruction was already accepted and cannot be recalled.")
				}
				m.refresh(true)
				return m, nil
			}
		case "enter":
			if m.pasting {
				return m.stop("notice", "Clipboard read in progress; wait before submitting.")
			}

			text := strings.TrimSpace(m.input.Value())
			if text == "" && len(m.images) == 0 {
				return m, nil
			}
			if matches := m.completions(); len(matches) > 0 && len(m.images) == 0 {
				return m.runCommand(matches[m.selected%len(matches)].name)
			}
			if m.compacting {
				return m.stop("notice", "Compaction is running; wait before sending.")
			}
			if m.busy {
				m.queueSteering(text)
				m.refresh(true)
				return m, nil
			}
			if s := m.a.Settings(); s.Provider == "" || s.Model == "" {
				return m.stop("notice", m.setupHint())
			}
			return m.startTurn(text)

		}
		before := m.input.Value()
		cmd := m.updateInput(msg)
		changed := m.input.Value() != before
		if changed {
			m.selected = 0
			m.hideCompletion = false
		}
		m.refresh(follow || changed)
		return m, cmd
	}
	cmd := m.updateInput(msg)
	m.refresh(follow)
	return m, cmd
}
func (m model) View() string {
	lines := strings.Split(m.viewport.View(), "\n")
	if m.sel != nil {
		lines = m.sel.highlight(lines, m.viewport.Width)
	}
	return strings.Join(m.composerHighlight(lines), "\n")
}
