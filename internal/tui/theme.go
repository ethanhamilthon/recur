package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"recur/internal/agent"
)

// defaultColors is the built-in palette; settings may override any entry by name.
var defaultColors = map[string]string{
	"accent":     "#79d9ff",
	"foreground": "#d5ddeb",
	"muted":      "#8794a8",
	"code":       "#a0a0a0",
	"dim":        "#606060",
	"input":      "#8aadff",
	"assistant":  "#9bdef5",
	"error":      "#f09eac",
	"warn":       "#e5c07b",
	"info":       "#d7c38b",
}

// init installs the built-in palette so rendering works before settings load.
func init() { applyColors(agent.Settings{}) }

// applyColors resolves the configured palette into the package colors used by rendering.
func applyColors(settings agent.Settings) {
	color := func(name string) lipgloss.Color {
		value := defaultColors[name]
		if custom := settings.Colors[name]; custom != "" {
			value = custom
		}
		return lipgloss.Color(value)
	}
	cyan, foreground, muted = color("accent"), color("foreground"), color("muted")
	codeGray, darkGray, inputBlue = color("code"), color("dim"), color("input")
	assistantColor, errorColor = color("assistant"), color("error")
	warnColor, infoColor = color("warn"), color("info")
	markdownStyle = renderMarkdownStyle(color)
	mdKey, mdRenderer = nil, nil
}

// renderMarkdownStyle keeps glamour styling on the same palette as the timeline.
func renderMarkdownStyle(color func(string) lipgloss.Color) string {
	accent, heading, code := color("accent"), color("assistant"), color("input")
	return fmt.Sprintf(markdownTemplate, heading, accent, color("warn"), color("muted"),
		color("info"), color("error"), code, code)
}
