package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"recur/internal/agent"
)

func TestDefaultPaletteMatchesBuiltInColors(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previous)
	applyColors(agent.Settings{})
	if got := string(cyan); got != "#79d9ff" {
		t.Fatalf("accent = %s", got)
	}
	if got := string(foreground); got != "#d5ddeb" {
		t.Fatalf("foreground = %s", got)
	}
	header := lipgloss.NewStyle().Foreground(cyan).Render("recur")
	if !strings.Contains(header, "38;2;121;217;255") {
		t.Fatalf("accent not applied: %q", header)
	}
}

func TestConfiguredColorsOverrideDefaults(t *testing.T) {
	applyColors(agent.Settings{Colors: map[string]string{"accent": "#ff0000", "muted": "#010203"}})
	defer applyColors(agent.Settings{})
	if string(cyan) != "#ff0000" || string(muted) != "#010203" {
		t.Fatalf("overrides not applied: %s %s", cyan, muted)
	}
	if string(foreground) != "#d5ddeb" {
		t.Fatalf("untouched color changed: %s", foreground)
	}
	if !strings.Contains(markdownStyle, "#ff0000") {
		t.Fatal("markdown style must follow the palette")
	}
}
