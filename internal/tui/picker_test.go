package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"recur/internal/agent"
)

func pickerModel(t *testing.T) model {
	t.Helper()
	t.Setenv("RECUR_HOME", t.TempDir())
	a, err := agent.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return newModel(a, a.REPL.Dir)
}
func enterPicker(m model) model {
	next, _, _ := m.pickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(model)
}

func TestModelPickerFetchAndEffortKeys(t *testing.T) {
	m := pickerModel(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"first"},{"id":"second"}]}`)
	}))
	defer server.Close()
	if err := m.a.AddProvider("test", agent.Provider{BaseURL: server.URL}); err != nil {
		t.Fatal(err)
	}
	if err := m.a.Select("test", "original", "low"); err != nil {
		t.Fatal(err)
	}
	cmd := m.openPicker("model")
	next, _ := m.Update(cmd())
	m = next.(model)
	if len(m.completions()) != 2 {
		t.Fatal(m.completions())
	}
	next, _, _ = m.pickerKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(model)
	if agent.Efforts[m.effort] != "medium" {
		t.Fatal("right did not change effort")
	}
	next, _, _ = m.pickerKey(tea.KeyMsg{Type: tea.KeyDown})
	m = enterPicker(next.(model))
	if m.menuKind != "" || m.a.Settings().Model != "second" || m.a.Settings().Effort != "medium" {
		t.Fatal("model selection not applied")
	}
}

func TestProviderWizardMasksKeyAndDoesNotSaveDraftToHistory(t *testing.T) {
	m := pickerModel(t)
	m.openPicker("login")
	if last := m.menu[len(m.menu)-1]; last.name != "Add" || last.icon != "+" {
		t.Fatal("missing final Add row")
	}
	m.selected = len(m.menu) - 1
	m = enterPicker(m)
	for _, value := range []string{"custom", "https://example.com/v1", "secret-key"} {
		m.input.SetValue(value)
		m.refresh(true)
		if len(m.setup) == 2 && strings.Contains(m.View(), "secret-key") {
			t.Fatal("credential visible")
		}
		m = enterPicker(m)
	}
	p, ok := m.a.Settings().Providers["custom"]
	if !ok || p.APIKey != "secret-key" {
		t.Fatalf("provider not saved: %+v", p)
	}
	if len(m.a.History) != 0 || strings.Contains(fmt.Sprint(m.entries), "secret-key") {
		t.Fatal("credentials leaked into history")
	}
}

func TestCancelledPickerIgnoresLateModelsAndPreservesSession(t *testing.T) {
	m := pickerModel(t)
	previous := m.a.Settings().Model
	m.menuKind = "model"
	m.menuGeneration = 2
	next, _, _ := m.pickerKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	next, _ = m.Update(modelChoices{generation: 2, models: []agent.ModelChoice{{Provider: "x", Model: "late"}}})
	m = next.(model)
	if m.menuKind != "" || len(m.menu) != 0 || m.a.Settings().Model != previous {
		t.Fatal("late response revived cancelled picker")
	}
	m.busy = true
	next, cmd := m.runCommand("/model")
	m = next.(model)
	if cmd != nil || m.menuKind != "" {
		t.Fatal("model changed during active turn")
	}
}

func TestModelPickerListsAllProvidersAndSelectsItsProvider(t *testing.T) {
	m := pickerModel(t)
	serve := func(models string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, models)
		}))
	}
	one, two := serve(`{"data":[{"id":"alpha"}]}`), serve(`{"data":[{"id":"beta"}]}`)
	defer one.Close()
	defer two.Close()
	for name, server := range map[string]*httptest.Server{"one": one, "two": two} {
		if err := m.a.AddProvider(name, agent.Provider{BaseURL: server.URL}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.a.Select("one", "alpha", "low"); err != nil {
		t.Fatal(err)
	}
	cmd := m.openPicker("model")
	next, _ := m.Update(cmd())
	m = next.(model)
	matches := m.completions()
	found := map[string]string{}
	for _, c := range matches {
		found[c.name] = c.description
	}
	if found["alpha"] != "one" || found["beta"] != "two" {
		t.Fatalf("models from all providers expected, got %v", found)
	}
	for i, c := range matches {
		if c.name == "beta" {
			m.selected = i
		}
	}
	m = enterPicker(m)
	if s := m.a.Settings(); s.Provider != "two" || s.Model != "beta" {
		t.Fatalf("provider not switched with model: %+v", s)
	}
}
