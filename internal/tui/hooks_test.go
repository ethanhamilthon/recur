package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"recur/internal/agent"
	"recur/internal/repl"
)

// TestInterfaceEvents covers the events only the interface can know about:
// idle (no follow-up turn starts), queued steering and pasted images.
func TestInterfaceEvents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECUR_HOME", home)
	out := filepath.Join(t.TempDir(), "events")
	hooks := "[[hook]]\nevents = [\"idle\", \"steer_queued\", \"image_attached\"]\ncommand = 'echo \"$RECUR_EVENT|$RECUR_IMAGE_SOURCE|$(cat \"$RECUR_MESSAGE_FILE\" 2>/dev/null)\" >> " + out + "'\n"
	if err := os.WriteFile(filepath.Join(home, "hooks.toml"), []byte(hooks), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := agent.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	m := newModel(a, "/project")
	m.busy, m.steering, m.workingSince = true, &agent.Steering{}, time.Now()
	m.queueSteering("keep going")
	m.queued = nil // the turn accepted it; otherwise "done" would start a follow-up turn, which is not idle
	next, _ := m.Update(agent.Event{Kind: "done"})
	m = next.(model)
	m.acceptClipboard(clipboardResult{generation: m.pasteGeneration, image: repl.Image{URL: "data:image/png;base64,AA=="}})
	m.busy, m.compacting = true, true
	next, _ = m.Update(compactMsg{text: "done"})
	m = next.(model)
	a.Finish(0)
	data, _ := os.ReadFile(out)
	want := "steer_queued||keep going\nidle||\nimage_attached|paste|\nidle||\n"
	if string(data) != want || strings.Count(string(data), "idle") != 2 {
		t.Fatalf("got %q want %q", data, want)
	}
}
