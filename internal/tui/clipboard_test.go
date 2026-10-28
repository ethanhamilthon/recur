package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"recur/internal/agent"
	"recur/internal/repl"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
func TestClipboardReaderImageTextAndFailures(t *testing.T) {
	for _, kind := range []string{"image", "text", "invalid", "failure"} {
		t.Run(kind, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "images")
			data := pngBytes(t)
			run := func(_ context.Context, _ int, name string, args ...string) ([]byte, error) {
				if kind == "failure" {
					return nil, errors.New("clipboard unavailable")
				}
				if name == "/usr/bin/pbpaste" {
					return []byte("ordinary text"), nil
				}
				if kind == "text" {
					return []byte("text\n"), nil
				}
				if kind == "invalid" {
					data = []byte("not an image")
				}
				if err := os.WriteFile(args[len(args)-1], data, 0600); err != nil {
					t.Fatal(err)
				}
				return []byte("image\n"), nil
			}
			result, text, err := readClipboardWith(context.Background(), directory, "darwin", run)
			files, e := os.ReadDir(directory)
			if e != nil {
				t.Fatal(e)
			}
			if kind == "image" {
				if err != nil || text != "" || !strings.HasPrefix(result.URL, "data:image/png;base64,") || len(files) != 1 {
					t.Fatalf("%+v %q %v", result, text, err)
				}
				info, e := os.Stat(result.Path)
				if e != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("image permissions: %v %v", info, e)
				}
			} else {
				if len(files) != 0 {
					t.Fatal("abandoned clipboard file left behind")
				}
				if kind == "text" && (err != nil || text != "ordinary text") {
					t.Fatalf("text fallback: %q %v", text, err)
				}
				if kind != "text" && err == nil {
					t.Fatal("failed capture accepted")
				}
			}
		})
	}
}
func TestLinuxClipboardImageBackends(t *testing.T) {
	for _, wayland := range []bool{false, true} {
		t.Run(fmt.Sprint(wayland), func(t *testing.T) {
			tools := t.TempDir()
			t.Setenv("PATH", tools)
			t.Setenv("WAYLAND_DISPLAY", "")
			if wayland {
				t.Setenv("WAYLAND_DISPLAY", "test")
				if err := os.WriteFile(filepath.Join(tools, "wl-paste"), []byte("#!/bin/sh\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			data := pngBytes(t)
			calls := 0
			run := func(_ context.Context, _ int, name string, args ...string) ([]byte, error) {
				calls++
				expected := "xclip"
				if wayland {
					expected = "wl-paste"
				}
				if name != expected {
					t.Fatalf("wrong clipboard backend: %s", name)
				}
				if calls == 1 {
					return []byte("image/png\ntext/plain\n"), nil
				}
				return data, nil
			}
			result, _, err := readClipboardWith(context.Background(), t.TempDir(), "linux", run)
			if err != nil || result.URL == "" || calls != 2 {
				t.Fatalf("%+v %v calls=%d", result, err, calls)
			}
		})
	}
}
func TestClipboardCommandsAreBoundedAndCancellable(t *testing.T) {
	if _, err := clipboardCommand(context.Background(), 2, "/bin/sh", "-c", "printf toolong"); err == nil {
		t.Fatal("output cap ignored")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := clipboardCommand(ctx, 128, "/bin/sh", "-c", "exec sleep 5"); err == nil || time.Since(started) > time.Second {
		t.Fatalf("command not cancelled promptly: %v", err)
	}
}
func TestImageAttachmentPreviewRemovalAndStaleResult(t *testing.T) {
	m := newModel(&agent.Agent{}, "/project")
	file := filepath.Join(t.TempDir(), "paste.png")
	if err := os.WriteFile(file, pngBytes(t), 0600); err != nil {
		t.Fatal(err)
	}
	img, err := repl.CaptureImage(file)
	if err != nil {
		t.Fatal(err)
	}
	img.Path = file
	m.acceptClipboard(clipboardResult{image: img})
	m.refresh(true)
	view := m.View()
	if len(m.images) != 1 || !strings.Contains(view, "Image 1: paste.png") || strings.Contains(view, "base64") || strings.Contains(view, img.URL) {
		t.Fatal("image must be a text label, never rendered pixels/data")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = next.(model)
	if len(m.images) != 0 {
		t.Fatal("remove shortcut failed")
	}
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("removed draft image file was retained")
	}
	file = filepath.Join(t.TempDir(), "stale.png")
	if err = os.WriteFile(file, pngBytes(t), 0600); err != nil {
		t.Fatal(err)
	}
	m.pasteGeneration = 2
	m.acceptClipboard(clipboardResult{generation: 1, image: repl.Image{URL: "data", Path: file}})
	if len(m.images) != 0 {
		t.Fatal("stale image crossed session boundary")
	}
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("stale capture not cleaned up")
	}
}
func TestAttachmentQueueRecallAndCancellationRecovery(t *testing.T) {
	m := newModel(&agent.Agent{}, "/project")
	m.busy, m.steering = true, &agent.Steering{}
	m.images = []repl.Image{{URL: "data:image/png;base64,one", Path: "one.png"}}
	m.queueSteering("look")
	if len(m.images) != 0 || len(m.queued) != 1 {
		t.Fatal("image not queued with text")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	if len(m.images) != 1 || m.input.Value() != "look" || len(m.steering.TakeInputs()) != 0 {
		t.Fatal("recall lost/duplicated image")
	}
	m.queueSteering("look")
	m.cancelling = true
	m.input.SetValue("draft")
	next, _ = m.Update(agent.Event{Kind: "done", Text: "context canceled"})
	m = next.(model)
	if len(m.images) != 1 || m.input.Value() != "look\n\ndraft" {
		t.Fatalf("cancellation recovery: %q %v", m.input.Value(), m.images)
	}
	m.images = nil
	m.turnImages = []repl.Image{{URL: "data", Path: "retry.png"}}
	m.retry = true // Image-only prefix failure has an empty retry text.
	m.retryInput = ""
	next, _ = m.Update(agent.Event{Kind: "done", Text: "prefix failed"})
	m = next.(model)
	if len(m.images) != 1 || m.images[0].Path != "retry.png" {
		t.Fatal("image-only initialization failure lost attachment")
	}
}
func TestClipboardTextPasteAndProviderKeyMasking(t *testing.T) {
	m := newModel(&agent.Agent{}, "/project")
	m.input.SetValue("before ")
	m.acceptClipboard(clipboardResult{text: "after"})
	if m.input.Value() != "before after" {
		t.Fatal(m.input.Value())
	}
	m.menuKind = "setup"
	m.setup = []string{"name", "https://example.com"}
	m.input.Reset()
	m.acceptClipboard(clipboardResult{text: "secret-key"})
	m.refresh(true)
	if strings.Contains(m.View(), "secret-key") || !strings.Contains(m.View(), "**********") {
		t.Fatal("clipboard API key was not masked")
	}
	m.pasteGeneration++
	m.closePicker()
	m.acceptClipboard(clipboardResult{generation: m.pasteGeneration - 1, text: "late-secret"})
	if strings.Contains(m.input.Value(), "secret") {
		t.Fatal("late key paste leaked into message draft")
	}
}

type csiTestMessage string

func (m csiTestMessage) String() string { return string(m) }
func TestPasteKeysAcrossLayoutsAndModifiers(t *testing.T) {
	for _, payload := range []string{"118;5u", "118;9u", "1084;5u", "1084;9u", "1052;10u", "1084::118;5u", "118;33u"} {
		msg := csiTestMessage(fmt.Sprintf("?CSI%+v?", []byte(payload)))
		key, ok := normalizeTerminalKey(msg).(tea.KeyMsg)
		if !ok || key.Type != tea.KeyCtrlV {
			t.Fatalf("%s: not a paste key: %v", payload, normalizeTerminalKey(msg))
		}
	}
	for _, payload := range []string{"118;5:3u", "1084;9:2u"} {
		if got := normalizeTerminalKey(csiTestMessage(fmt.Sprintf("?CSI%+v?", []byte(payload)))); got != nil {
			t.Fatalf("release/repeat triggered paste: %v", got)
		}
	}
	for _, payload := range []string{"1084;1u", "118;1u", "13;5u", "oops"} {
		msg := csiTestMessage(fmt.Sprintf("?CSI%+v?", []byte(payload)))
		if key, ok := normalizeTerminalKey(msg).(tea.KeyMsg); ok && key.Type == tea.KeyCtrlV {
			t.Fatal("ordinary typing triggered clipboard read")
		}
	}
}

func TestClipboardShortcutDecodesFromTerminalBytes(t *testing.T) {
	for _, sequence := range []string{"\x16", "\x1b[118;9u", "\x1b[1084;5u", "\x1b[1084::118;9u"} {
		m := newModel(&agent.Agent{}, "/project")
		seen := false
		program := tea.NewProgram(m, tea.WithInput(strings.NewReader(sequence)), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
			if key, ok := normalizeTerminalKey(msg).(tea.KeyMsg); ok && key.Type == tea.KeyCtrlV {
				seen = true
				return tea.QuitMsg{}
			}
			return msg
		}))
		if _, err := program.Run(); err != nil {
			t.Fatal(err)
		}
		if !seen {
			t.Fatalf("terminal sequence not recognized: %q", sequence)
		}
	}
}

func TestPendingImagesPreventDestructiveSessionSwitch(t *testing.T) {
	m := newModel(&agent.Agent{}, "/project")
	m.input.SetValue("caption")
	m.images = []repl.Image{{URL: "data", Path: "draft.png"}}
	next, cmd := m.runCommand("/new")
	m = next.(model)
	if cmd != nil || m.input.Value() != "caption" || len(m.images) != 1 {
		t.Fatal("session switch discarded unsent attachment")
	}
	m.a.ReadOnly = true
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	if cmd != nil || next.(model).pasting {
		t.Fatal("read-only client read clipboard")
	}
}
func TestClipboardPendingLimitAndPickerRejection(t *testing.T) {
	m := newModel(&agent.Agent{}, "/project")
	for i := 0; i < 8; i++ {
		m.images = append(m.images, repl.Image{URL: "data", Path: fmt.Sprint(i)})
	}
	file := filepath.Join(t.TempDir(), "extra.png")
	if err := os.WriteFile(file, pngBytes(t), 0600); err != nil {
		t.Fatal(err)
	}
	m.acceptClipboard(clipboardResult{image: repl.Image{URL: "data", Path: file}})
	if len(m.images) != 8 {
		t.Fatal("pending image limit ignored")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("rejected image file retained")
	}
	m.images = nil
	m.menuKind = "setup"
	file = filepath.Join(t.TempDir(), "picker.png")
	if err := os.WriteFile(file, pngBytes(t), 0600); err != nil {
		t.Fatal(err)
	}
	m.acceptClipboard(clipboardResult{image: repl.Image{URL: "data", Path: file}})
	if len(m.images) != 0 {
		t.Fatal("image inserted into provider wizard")
	}
}

func TestMacClipboardBridgeCompilesWithoutReadingClipboard(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS bridge compiler")
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "clipboard.js")
	if err := os.WriteFile(source, []byte(clipboardPNG), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/bin/osacompile", "-l", "JavaScript", "-o", filepath.Join(directory, "clipboard.scpt"), source).CombinedOutput()
	if err != nil {
		t.Fatalf("bridge syntax: %s %v", output, err)
	}
}

func TestCopyCutShortcutsAcrossLayouts(t *testing.T) {
	for _, payload := range []string{"99;9u", "1089;9u", "99;33u"} {
		if got := normalizeTerminalKey(csiTestMessage(fmt.Sprintf("?CSI%+v?", []byte(payload)))); got != (composerShortcut{cut: false}) {
			t.Fatalf("%s: copy shortcut = %#v", payload, got)
		}
	}
	for _, payload := range []string{"120;9u", "1095;9u"} {
		if got := normalizeTerminalKey(csiTestMessage(fmt.Sprintf("?CSI%+v?", []byte(payload)))); got != (composerShortcut{cut: true}) {
			t.Fatalf("%s: cut shortcut = %#v", payload, got)
		}
	}
	for payload, want := range map[string]tea.KeyType{"99;5u": tea.KeyCtrlC, "120;5u": tea.KeyCtrlX} {
		key, ok := normalizeTerminalKey(csiTestMessage(fmt.Sprintf("?CSI%+v?", []byte(payload)))).(tea.KeyMsg)
		if !ok || key.Type != want {
			t.Fatalf("%s: control key = %v, want %v", payload, key, want)
		}
	}
	for _, payload := range []string{"99;9:3u", "120;9:3u"} {
		if got := normalizeTerminalKey(csiTestMessage(fmt.Sprintf("?CSI%+v?", []byte(payload)))); got != nil {
			t.Fatalf("%s: release produced %v", payload, got)
		}
	}
	for _, payload := range []string{"99;1u", "120;1u"} {
		switch got := normalizeTerminalKey(csiTestMessage(fmt.Sprintf("?CSI%+v?", []byte(payload)))).(type) {
		case composerShortcut, tea.KeyMsg:
			t.Fatalf("%s: plain key decoded as shortcut: %#v", payload, got)
		}
	}
}
