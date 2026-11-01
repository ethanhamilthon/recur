package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigPrecedenceAndNoExtend(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)

	globalCfg := `
model = "global-model"
system = "global system"
[ui]
max_code_lines = 100
[env]
G_VAR = "g_val"
SHARED = "from_global"
[providers.p1]
base_url = "https://global.p1"
api_key = "k1"
[hooks.h1]
events = ["turn_end"]
command = "echo h1"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(globalCfg), 0600); err != nil {
		t.Fatal(err)
	}

	projectCfg := `
model = "project-model"
[env]
P_VAR = "p_val"
SHARED = "from_project"
[providers.p1]
base_url = "https://project.p1"
[hooks.h2]
events = ["idle"]
command = "echo h2"
`
	if err := os.WriteFile(filepath.Join(cwd, "recur.toml"), []byte(projectCfg), 0600); err != nil {
		t.Fatal(err)
	}

	// 1. Project recur.toml merges over global
	cfg, err := loadConfiguration(home, cwd, ConfigFiles{})
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	s := cfg.settings
	if s.Model != "project-model" || cfg.system != "global system" || s.MaxCodeLines != 100 {
		t.Fatalf("scalar merge error: model=%q sys=%q lines=%d", s.Model, cfg.system, s.MaxCodeLines)
	}
	if s.Environment["G_VAR"] != "g_val" || s.Environment["P_VAR"] != "p_val" || s.Environment["SHARED"] != "from_project" {
		t.Fatalf("env merge error: %+v", s.Environment)
	}
	if s.Providers["p1"].BaseURL != "https://project.p1" || s.Providers["p1"].APIKey != "k1" {
		t.Fatalf("provider deep merge error: %+v", s.Providers["p1"])
	}
	if len(cfg.hooks) != 2 {
		t.Fatalf("hooks merge error: %+v", cfg.hooks)
	}

	// 2. --no-extend skips global base
	cfgNoExt, err := loadConfiguration(home, cwd, ConfigFiles{NoExtend: true})
	if err != nil {
		t.Fatalf("no-extend load failed: %v", err)
	}
	if cfgNoExt.settings.Environment["G_VAR"] != "" || cfgNoExt.settings.Providers["p1"].APIKey != "" {
		t.Fatalf("no-extend inherited global settings: %+v", cfgNoExt.settings)
	}
	if cfgNoExt.system != "You are Recur, a coding agent" {
		t.Fatalf("no-extend must keep builtin defaults: %q", cfgNoExt.system)
	}
}

func TestConfigFalseZeroEmptyScalarsReplace(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)

	globalCfg := `
model = "global-model"
compact_keep_turns = 10
max_agent_steps = 50
system = "initial system"
[ui]
max_code_lines = 100
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(globalCfg), 0600); err != nil {
		t.Fatal(err)
	}

	projectCfg := `
compact_keep_turns = 0
max_agent_steps = 0
system = ""
[ui]
max_code_lines = 0
`
	if err := os.WriteFile(filepath.Join(cwd, "recur.toml"), []byte(projectCfg), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfiguration(home, cwd, ConfigFiles{})
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	s := cfg.settings
	if s.CompactKeepTurns != 0 || s.MaxAgentSteps != 0 || s.MaxCodeLines != 0 || cfg.system != "" {
		t.Fatalf("explicit 0/empty did not replace: turns=%d steps=%d lines=%d sys=%q",
			s.CompactKeepTurns, s.MaxAgentSteps, s.MaxCodeLines, cfg.system)
	}
}

func TestConfigArraysReplace(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)

	globalCfg := `
[ui]
status-left = ["echo left1", "echo left2"]
status-right = ["echo right1"]
[hooks.test]
events = ["turn_start", "turn_end"]
command = "echo 1"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(globalCfg), 0600); err != nil {
		t.Fatal(err)
	}

	projectCfg := `
[ui]
status-left = []
[hooks.test]
events = ["idle"]
`
	if err := os.WriteFile(filepath.Join(cwd, "recur.toml"), []byte(projectCfg), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfiguration(home, cwd, ConfigFiles{})
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	s := cfg.settings
	if len(s.StatusLeft) != 0 {
		t.Fatalf("status-left was not replaced by empty array: %+v", s.StatusLeft)
	}
	if len(s.StatusRight) != 1 || s.StatusRight[0] != "echo right1" {
		t.Fatalf("status-right was not inherited: %+v", s.StatusRight)
	}
	if len(cfg.hooks) != 1 || len(cfg.hooks[0].Events) != 1 || cfg.hooks[0].Events[0] != "idle" {
		t.Fatalf("hook events array was not replaced: %+v", cfg.hooks[0])
	}
}

func TestDisabledHookMayOmitCommandAndEvents(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)

	globalCfg := `
[hooks.h1]
events = ["turn_end"]
command = "make test"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(globalCfg), 0600); err != nil {
		t.Fatal(err)
	}

	projectCfg := `
[hooks.h1]
enabled = false
[hooks.h2]
enabled = false
`
	if err := os.WriteFile(filepath.Join(cwd, "recur.toml"), []byte(projectCfg), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfiguration(home, cwd, ConfigFiles{})
	if err != nil {
		t.Fatalf("disabled hooks should not fail validation: %v", err)
	}
	if len(cfg.hooks) != 0 {
		t.Fatalf("expected 0 active hooks, got %d", len(cfg.hooks))
	}
}

func TestSelectedProviderSecretResolutionAndConflict(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)
	t.Setenv("MY_SECRET", "super-secret-key")

	// 1. Selected provider with api_key_env resolves from effective environment
	cfgText := `
provider = "p1"
model = "m1"
[env]
MY_INDIRECT = "indirect-key"
[providers.p1]
base_url = "https://example.com"
api_key_env = "MY_INDIRECT"
[providers.p2_unused]
base_url = "https://unused.com"
api_key_env = "NONEXISTENT_VAR"
`
	if err := os.WriteFile(filepath.Join(cwd, "recur.toml"), []byte(cfgText), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfiguration(home, cwd, ConfigFiles{})
	if err != nil {
		t.Fatalf("failed to resolve valid selected provider: %v", err)
	}
	if cfg.settings.Providers["p1"].APIKeyEnv != "MY_INDIRECT" {
		t.Fatalf("APIKeyEnv not preserved: %+v", cfg.settings.Providers["p1"])
	}

	// 2. Selected provider missing env var must fail
	cfgTextBad := `
provider = "p1"
model = "m1"
[providers.p1]
base_url = "https://example.com"
api_key_env = "MISSING_ENV_VAR"
`
	if err := os.WriteFile(filepath.Join(cwd, "recur.toml"), []byte(cfgTextBad), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfiguration(home, cwd, ConfigFiles{}); err == nil {
		t.Fatal("expected error for missing selected provider env var")
	}

	// 3. Provider with both api_key and api_key_env conflicts
	cfgTextConflict := `
provider = "p1"
model = "m1"
[providers.p1]
base_url = "https://example.com"
api_key = "direct-key"
api_key_env = "MY_SECRET"
`
	if err := os.WriteFile(filepath.Join(cwd, "recur.toml"), []byte(cfgTextConflict), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfiguration(home, cwd, ConfigFiles{}); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("expected conflict error: %v", err)
	}

	// 4. Reserved RECUR_* env key rejected
	cfgTextReserved := `
[env]
RECUR_CUSTOM = "bad"
`
	if err := os.WriteFile(filepath.Join(cwd, "recur.toml"), []byte(cfgTextReserved), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfiguration(home, cwd, ConfigFiles{}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved env key error: %v", err)
	}
}

func TestUnknownKeysRejected(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)

	for _, text := range []string{
		"unknown_field = 'val'\n",
		"[ui]\nunknown_ui = 1\n",
		"[shell]\nunknown_shell = true\n",
		"[hooks.h1]\nevents = ['idle']\ncommand = 'true'\nunknown_hook = 5\n",
	} {
		if err := os.WriteFile(filepath.Join(cwd, "recur.toml"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfiguration(home, cwd, ConfigFiles{}); err == nil {
			t.Fatalf("unknown field accepted: %s", text)
		}
	}
}

func TestMissingExplicitConfigFails(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)

	missing := filepath.Join(t.TempDir(), "nonexistent.toml")
	if _, err := loadConfiguration(home, cwd, ConfigFiles{Config: missing}); err == nil {
		t.Fatal("missing explicit config must fail without fallback")
	}
}

func TestSameFileAliasLoadedOnlyOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECUR_HOME", home)
	globalPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(globalPath, []byte("model = 'm1'\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfiguration(home, home, ConfigFiles{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.sources) != 1 || cfg.sources[0].Origin != "home" {
		t.Fatalf("expected 1 source for same file alias, got %+v", cfg.sources)
	}
}

func TestLosslessSparseWritesAndRollback(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("RECUR_HOME", home)

	// Primary recur.toml has comments and custom ui
	initialProject := `# Custom Project Config
system = "custom system instructions"

[ui]
# Preserve this comment
max_code_lines = 42

[providers.other]
base_url = "https://other.example"
api_key = "other-key"
`
	projectPath := filepath.Join(cwd, "recur.toml")
	if err := os.WriteFile(projectPath, []byte(initialProject), 0600); err != nil {
		t.Fatal(err)
	}

	a, err := New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// Select updates provider, model, effort sparsely
	if err = a.AddProvider("p_new", Provider{BaseURL: "https://new.example", APIKey: "k_new"}); err != nil {
		t.Fatal(err)
	}
	if err = a.Select("p_new", "my-model", "high"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Check comments and existing keys were preserved
	if !strings.Contains(content, "# Custom Project Config") ||
		!strings.Contains(content, "# Preserve this comment") ||
		!strings.Contains(content, "max_code_lines = 42") ||
		!strings.Contains(content, "custom system instructions") ||
		!strings.Contains(content, "[providers.other]") {
		t.Fatalf("unrelated values or comments lost: %s", content)
	}

	// Check new values sparsely added
	if !strings.Contains(content, "provider = \"p_new\"") ||
		!strings.Contains(content, "model = \"my-model\"") ||
		!strings.Contains(content, "effort = \"high\"") ||
		!strings.Contains(content, "[providers.p_new]") {
		t.Fatalf("sparse keys not added: %s", content)
	}

	// Rollback on write failure: invalid effort fails and preserves file
	backupContent := content
	if err = a.Select("p_new", "my-model", "unsupported-effort"); err == nil {
		t.Fatal("expected error on unsupported effort")
	}
	afterFail, _ := os.ReadFile(projectPath)
	if string(afterFail) != backupContent {
		t.Fatalf("rollback did not restore file: %s", afterFail)
	}
}

func TestStatusUnderscoreKeysAccepted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECUR_HOME", home)
	cfg := `
[ui]
status_left = ["echo left_underscore"]
status_right = ["echo right_underscore"]
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfiguration(home, t.TempDir(), ConfigFiles{})
	if err != nil {
		t.Fatalf("failed loading config with underscore status keys: %v", err)
	}
	if len(c.settings.StatusLeft) != 1 || c.settings.StatusLeft[0] != "echo left_underscore" {
		t.Fatalf("StatusLeft unexpected: %+v", c.settings.StatusLeft)
	}
	if len(c.settings.StatusRight) != 1 || c.settings.StatusRight[0] != "echo right_underscore" {
		t.Fatalf("StatusRight unexpected: %+v", c.settings.StatusRight)
	}
}
