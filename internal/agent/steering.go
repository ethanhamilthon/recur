package agent

import (
	"path/filepath"
	"strings"
	"sync"

	"recur/internal/repl"
)

// Input keeps attachments with their instruction through acceptance and recall.
type Input struct {
	Text   string
	Images []repl.Image
}

func (input Input) Display() string {
	if len(input.Images) == 0 {
		return input.Text
	}
	text := input.Text
	for _, image := range input.Images {
		text += "\n[Image: " + filepath.Base(image.Path) + "]"
	}
	return strings.TrimSpace(text)
}
func (a *Agent) addInput(input Input, emit func(Event), steering bool) {
	if len(input.Images) == 0 {
		a.message("user", input.Text)
	} else {
		parts := []any{map[string]any{"type": "input_text", "text": input.Display()}}
		for _, img := range input.Images {
			parts = append(parts, map[string]any{"type": "input_image", "image_url": img.URL, "detail": "auto"})
		}
		a.History = append(a.History, raw(map[string]any{"role": "user", "content": parts}))
	}
	kind := "user"
	if len(input.Images) == 0 && strings.HasPrefix(input.Text, "<notify>") {
		kind = "notice"
	}
	emit(Event{Kind: kind, Text: input.Display(), Steering: steering})
}

// Steering serializes acceptance and recall; taken instructions cannot be recalled.
type Steering struct {
	mu      sync.Mutex
	pending []Input
}

func (s *Steering) Push(text string) bool { return s.PushInput(Input{Text: text}) }
func (s *Steering) PushInput(input Input) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) >= 64 {
		return false
	}
	s.pending = append(s.pending, input)
	return true
}
func (s *Steering) Take() []string {
	var texts []string
	for _, input := range s.TakeInputs() {
		texts = append(texts, input.Text)
	}
	return texts
}
func (s *Steering) TakeInputs() []Input {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.pending
	s.pending = nil
	return pending
}
func (s *Steering) Recall() (string, bool) { input, ok := s.RecallInput(); return input.Text, ok }
func (s *Steering) RecallInput() (Input, bool) {
	if s == nil {
		return Input{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return Input{}, false
	}
	i := len(s.pending) - 1
	text := s.pending[i]
	s.pending = s.pending[:i]
	return text, true
}
