package control

import (
	"os"
	"path/filepath"
	"testing"

	"recur/internal/agent"
)

func TestProjectConfigOnlyForProjectFile(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)
	globalPath := filepath.Join(home, "recur.toml")
	projectPath := filepath.Join(cwd, "recur.toml")
	if err := os.WriteFile(globalPath, []byte("model = 'm'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte("system = 'p'\n"), 0600); err != nil {
		t.Fatal(err)
	}

	a, err := agent.New(cwd, agent.ConfigFiles{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c := New(a, nil)

	if c.ProjectConfig(globalPath) {
		t.Fatalf("global %s reported as project config", globalPath)
	}
	if !c.ProjectConfig(projectPath) {
		t.Fatalf("project %s not reported as project config", projectPath)
	}
	if c.ProjectConfig("") {
		t.Fatal("empty destination reported as project config")
	}
}

func TestAddProviderReportsGlobalDestinationAsNotProject(t *testing.T) {
	h := newHarness(t, &fakeModel{}, "")
	resp := h.submit(Request{Type: "add_provider", Args: map[string]string{"name": "global", "base_url": "https://example.test/v1", "api_key": "sk-global"}})
	saved, ok := resp.Data.(ProviderSaved)
	if !ok {
		t.Fatalf("unexpected response data %T", resp.Data)
	}
	if saved.Destination != filepath.Join(os.Getenv("RECUR_HOME"), "recur.toml") {
		t.Fatalf("destination = %q, want the global file", saved.Destination)
	}
	if saved.ProjectConfig {
		t.Fatal("global destination reported as project_config")
	}
}
