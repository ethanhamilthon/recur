package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"recur/internal/agent"
	"recur/internal/control"
)

func pickerModel(t *testing.T) (model, *agent.Agent) {
	t.Helper()
	t.Setenv("RECUR_HOME", t.TempDir())
	a, err := agent.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return newModel(a, a.REPL.Dir), a
}
func enterPicker(m model) model {
	next, _, _ := m.pickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	return settle(next.(model))
}

func TestModelPickerFetchAndEffortKeys(t *testing.T) {
	m, a := pickerModel(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"first"},{"id":"second"}]}`)
	}))
	defer server.Close()
	if err := a.AddProvider("test", agent.Provider{BaseURL: server.URL}); err != nil {
		t.Fatal(err)
	}
	if err := a.Select("test", "original", "low"); err != nil {
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
	if m.menuKind != "" || a.Settings().Model != "second" || a.Settings().Effort != "medium" {
		t.Fatal("model selection not applied")
	}
}

func TestProviderWizardMasksKeyAndDoesNotSaveDraftToHistory(t *testing.T) {
	m, a := pickerModel(t)
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
	p, ok := a.Settings().Providers["custom"]
	if !ok || p.APIKey != "secret-key" {
		t.Fatalf("provider not saved: %+v", p)
	}
	if len(a.History) != 0 || strings.Contains(fmt.Sprint(m.entries), "secret-key") {
		t.Fatal("credentials leaked into history")
	}
}

func TestCancelledPickerIgnoresLateModelsAndPreservesSession(t *testing.T) {
	m, a := pickerModel(t)
	m, a, release := heldModel(t, "")
	m = startBusy(t, m)
	defer release()
	previous := a.Settings().Model
	m.menuKind = "model"
	m.menuGeneration = 2
	next, _, _ := m.pickerKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	next, _ = m.Update(modelChoices{generation: 2, models: []control.ModelInfo{{Provider: "x", Model: "late"}}})
	m = next.(model)
	if m.menuKind != "" || len(m.menu) != 0 || a.Settings().Model != previous {
		t.Fatal("late response revived cancelled picker")
	}
	next, cmd := m.runCommand("/model")
	m = next.(model)
	if cmd != nil || m.menuKind != "" {
		t.Fatal("model changed during active turn")
	}
}

func TestModelPickerListsAllProvidersAndSelectsItsProvider(t *testing.T) {
	m, a := pickerModel(t)
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
		if err := a.AddProvider(name, agent.Provider{BaseURL: server.URL}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Select("one", "alpha", "low"); err != nil {
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
	if s := a.Settings(); s.Provider != "two" || s.Model != "beta" {
		t.Fatalf("provider not switched with model: %+v", s)
	}
}

func TestPrimaryConfigMethod(t *testing.T) {
	var nilAgent *agent.Agent
	if nilAgent.PrimaryConfig() != "" {
		t.Fatalf("nil agent PrimaryConfig() = %q, want empty", nilAgent.PrimaryConfig())
	}

	home := t.TempDir()
	t.Setenv("RECUR_HOME", home)
	a, err := agent.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	dest := a.PrimaryConfig()
	if dest == "" {
		t.Fatal("expected non-empty PrimaryConfig()")
	}
	if !strings.HasSuffix(dest, "config.toml") {
		t.Fatalf("PrimaryConfig() = %q, want suffix config.toml", dest)
	}
}

func TestCredentialDestinationInLoginAndModelNotices(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECUR_HOME", home)
	projectDir := t.TempDir()
	projectToml := filepath.Join(projectDir, "recur.toml")
	_ = os.WriteFile(projectToml, []byte("model = \"base-model\"\n"), 0600)

	a, err := agent.New(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if a.PrimaryConfig() != projectToml {
		t.Fatalf("a.PrimaryConfig() = %q, want %q", a.PrimaryConfig(), projectToml)
	}

	m := newModel(a, projectDir)

	// 1. openPicker("login") displays credential destination
	m.openPicker("login")
	entriesStr := fmt.Sprint(m.entries)
	if !strings.Contains(entriesStr, "Credentials destination: "+projectToml) {
		t.Fatalf("openPicker login entries = %v, want Credentials destination: %s", m.entries, projectToml)
	}

	// 2. Select "Add" row
	if last := m.menu[len(m.menu)-1]; last.name != "Add" || last.icon != "+" {
		t.Fatal("missing final Add row")
	}
	m.selected = len(m.menu) - 1
	m = enterPicker(m)

	entriesStr = fmt.Sprint(m.entries)
	if !strings.Contains(entriesStr, "Credentials will be saved to "+projectToml) {
		t.Fatalf("entering setup entries = %v, want Credentials will be saved to: %s", m.entries, projectToml)
	}

	// 3. Provider name
	m.input.SetValue("proj-prov")
	m = enterPicker(m)

	// 4. Base URL
	m.input.SetValue("https://api.project.com/v1")
	m = enterPicker(m)

	// At API key step, warning about project config is shown
	entriesStr = fmt.Sprint(m.entries)
	if !strings.Contains(entriesStr, "Warning: API key will be saved to project configuration ("+projectToml+")") {
		t.Fatalf("setup API key prompt missing project config warning: %v", m.entries)
	}

	// 5. API key entered and submitted
	m.input.SetValue("secret-proj-key")
	m = enterPicker(m)

	// Success notice contains destination
	entriesStr = fmt.Sprint(m.entries)
	if !strings.Contains(entriesStr, "Provider created in "+projectToml+". Choose a model with /model.") {
		t.Fatalf("provider created notice missing destination: %v", m.entries)
	}
	// Verify secret key is not leaked
	if strings.Contains(entriesStr, "secret-proj-key") || strings.Contains(fmt.Sprint(a.History), "secret-proj-key") {
		t.Fatal("secret key leaked into entries or history")
	}

	// 6. Model selection notice shows destination
	m.openPicker("model")
	m.menu = []command{{name: "gpt-4o", description: "proj-prov"}}
	m.selected = 0
	m = enterPicker(m)

	entriesStr = fmt.Sprint(m.entries)
	if !strings.Contains(entriesStr, "(saved to "+projectToml+")") {
		t.Fatalf("model selection notice missing destination: %v", m.entries)
	}
}
