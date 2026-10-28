package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"recur/internal/agent"
)

type command struct{ name, icon, description, label string }

var commands = []command{
	{name: "/new", icon: "+", description: "Start a new chat and REPL; reload prompt files"},
	{name: "/help", icon: "?", description: "Show commands and keyboard shortcuts"},
	{name: "/quit", icon: "->", description: "Save the chat and exit"},
	{name: "/cancel", icon: "!", description: "Cancel the current turn"},
	{name: "/resume", icon: "<", description: "Resume a saved session in this project"},
	{name: "/compact", icon: "=", description: "Summarize older turns; add a prompt to guide it"},
	{name: "/reload", icon: "*", description: "Reload settings and prompts; restart the REPL"},
	{name: "/model", icon: "~", description: "Choose model; Left/Right changes effort"},
	{name: "/login", icon: "+", description: "Select or create a provider"},
}

// sessionTitle is the first user message, shortened for the resume picker.
func sessionTitle(s agent.SessionSummary) string {
	title := []rune(s.Title)
	if len(title) == 0 {
		return "(no message)"
	}
	if len(title) > 40 {
		return string(title[:39]) + "…"
	}
	return string(title)
}

// sessionTime shows the last update: time today, day this year, date before.
func sessionTime(t time.Time) string {
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	if t.Year() == now.Year() {
		return t.Format("Jan 2")
	}
	return t.Format("2006-01-02")
}

func (m model) completions() []command {
	text := m.input.Value()
	if m.menuKind != "" {
		var matches []command
		for _, c := range m.menu {
			haystack := c.name
			if m.menuKind == "model" {
				haystack += " " + c.description
			}
			if strings.Contains(strings.ToLower(haystack), strings.ToLower(text)) {
				matches = append(matches, c)
			}
		}
		return matches
	}
	if !m.hideCompletion && (text == "/resume" || strings.HasPrefix(text, "/resume ")) {
		sessions, err := m.a.Sessions()
		if err != nil {
			return nil
		}
		prefix := strings.TrimSpace(strings.TrimPrefix(text, "/resume"))
		lower := strings.ToLower(prefix)
		titleWidth, modelWidth := 0, 0
		for _, s := range sessions {
			titleWidth, modelWidth = max(titleWidth, min(len([]rune(sessionTitle(s))), 40)), max(modelWidth, len(s.Model))
		}
		var matches []command
		for _, s := range sessions {
			title := sessionTitle(s)
			if !strings.HasPrefix(s.ID, prefix) && !strings.Contains(strings.ToLower(title), lower) {
				continue
			}
			label := fmt.Sprintf("%-*s  %-*s  %s", titleWidth, title, modelWidth, s.Model, sessionTime(s.Mod))
			matches = append(matches, command{name: "/resume " + s.ID, icon: "<", label: label})
		}
		return matches
	}

	if m.hideCompletion || !strings.HasPrefix(text, "/") || strings.ContainsAny(text, " \n\t") {
		return nil
	}
	var result []command
	for _, cmd := range commands {
		if m.a.ReadOnly && cmd.name != "/new" && cmd.name != "/help" && cmd.name != "/quit" && cmd.name != "/resume" {
			continue
		}
		if strings.HasPrefix(cmd.name, text) {
			result = append(result, cmd)
		}
	}
	return result
}
func (m *model) complete() bool {
	matches := m.completions()
	if len(matches) == 0 {
		return false
	}
	m.input.SetValue(matches[m.selected%len(matches)].name)
	m.input.CursorEnd()
	m.selected = 0
	return true
}

func (m model) runCommand(name string) (tea.Model, tea.Cmd) {
	if name == "/new" || strings.HasPrefix(name, "/resume ") {
		count := len(m.images)
		for _, input := range m.queued {
			count += len(input.Images)
		}
		if count > 0 {
			return m.stop("notice", "Submit or remove pending images before switching sessions.")
		}
	}
	m.input.Reset()
	m.selected = 0
	m.hideCompletion = false
	verb := strings.Fields(name)
	if len(verb) == 0 {
		return m, nil
	}
	if verb[0] != "/resume" && verb[0] != "/compact" && len(verb) != 1 {
		return m.stop("error", "Unexpected command arguments.")
	}
	if verb[0] == "/resume" {
		sessions, err := m.a.Sessions()
		found := false
		if len(verb) == 2 {
			for _, session := range sessions {
				if session.ID == verb[1] {
					found = true
				}
			}
		}
		if err == nil && !found {
			err = fmt.Errorf("choose a session from /resume completions")
		}
		if err != nil {
			return m.stop("error", err.Error())
		}
	}
	switch verb[0] {
	case "/model", "/login":
		if m.busy || m.a.ReadOnly {
			return m.stop("notice", "Wait for the active turn before changing settings.")
		}
		cmd := m.openPicker(strings.TrimPrefix(verb[0], "/"))
		m.refresh(true)
		return m, cmd
	case "/reload":
		if m.busy || m.a.ReadOnly {
			return m.stop("notice", "Wait for the active turn before reloading settings.")
		}
		if err := m.a.Reload(); err != nil {
			return m.stop("error", err.Error())
		}
		applyColors(m.a.Settings())
		m.cache = nil
		m.add("notice", "Settings reloaded; Python state was reset.")
	case "/compact":
		if m.busy || m.a.ReadOnly {
			return m.stop("notice", "Wait for the active turn before compacting.")
		}
		return m.startCompact(strings.TrimSpace(strings.TrimPrefix(name, "/compact")))
	case "/help":
		var help strings.Builder
		for _, cmd := range commands {
			fmt.Fprintf(&help, "%-9s %s\n", cmd.name, cmd.description)
		}
		help.WriteString("\nEnter: execute selected completion or send / steer while working · Shift+Enter / Alt+Enter / Ctrl+J: newline\nClipboard: Ctrl+V (Cmd+V when forwarded) · Ctrl+X: remove last image\nTab: complete · Up/Down: select completion · unmatched slash text: send normally · Up on empty input: recall queued steering\nPgUp/PgDn or mouse wheel: scroll timeline\nEsc / Ctrl+C: cancel current turn\n\nConfig files: $RECUR_HOME (default: ~/.recur): settings.toml, prefix.py, system.md\nEdit them, then /reload to apply them to this chat.\nHost access: sh(command) in python_repl; not an OS security sandbox.")
		m.add("info", help.String())
	case "/new", "/resume":
		if m.busy {
			m.pending = name
			m.requestCancel()
		} else {
			m.a.RememberDisplay(m.entries)
			var err error
			if verb[0] == "/resume" {
				err = m.a.Resume(verb[1])
			} else {
				err = m.a.NewSession()
			}
			if err != nil {
				m.add("error", err.Error())
			} else {
				applyColors(m.a.Settings())
				m.pasteGeneration++
				m.pasting = false
				m.images = nil
				m.entries = nil
				m.cache = nil
				m.contextInput = nil
				m.restoreHistory()
			}
		}
	case "/quit":
		if m.busy {
			m.pending = "/quit"
			m.requestCancel()
		} else {
			m.a.RememberDisplay(m.entries)
			if !m.a.ReadOnly && m.a.HasSavedSession() {
				if err := m.a.Save(); err != nil {
					return m.stop("error", err.Error())
				}
			}
			return m, tea.Quit
		}
	case "/cancel":
		if m.busy {
			m.requestCancel()
		} else {
			return m.stop("info", "No turn is running.")
		}
	default:
		return m.stop("error", "Unknown command: "+name+". Type /help.")
	}
	m.refresh(true)
	return m, nil
}
