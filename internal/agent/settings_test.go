package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsAndHome(t *testing.T) {
	cwd := t.TempDir()
	home := filepath.Join(t.TempDir(), ".recur")
	t.Setenv("RECUR_HOME", home)
	a, err := New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !strings.HasPrefix(a.HistoryPath, filepath.Join(home, "sessions")+string(os.PathSeparator)) {
		t.Fatal(a.HistoryPath)
	}

	text := `provider = "other"
model = "custom-model"
effort = "high"
[providers.clipd]
base_url = "http://localhost:8317/v1"
api_key = "first"
[providers.other]
base_url = "https://example.com/v1/"
api_key = "second"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.NewSession(); err != nil {
		t.Fatal(err)
	}
	if a.Settings().Model != "custom-model" || a.Settings().Effort != "high" ||
		a.Settings().Providers["other"].BaseURL != "https://example.com/v1/" ||
		a.Settings().Providers["other"].APIKey != "second" {
		t.Fatal("settings not applied")
	}
	next, err := New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.Settings().Providers["other"].APIKey != "second" {
		t.Fatal("settings overwritten")
	}
}

func TestInvalidSettings(t *testing.T) {
	for _, text := range []string{
		"not valid toml",
		"provider = 'p'\nmodel = 'm'\nreasoning_summary = 'invalid'\n[providers.p]\nbase_url = 'https://example.com'",
		`provider = "missing"` + "\n" + `model = "m"`,
		"provider = 'p'\nmodel = 'm'\neffort = 'oops'\n[providers.p]\nbase_url = 'https://example.com'",
		"provider = 'p'\nmodel = 'm'\n[providers.p]\nbase_url = 'file:///tmp'",
		"provider = 'p'\nmodel = 'm'\n[providers.p]\nbase_url = 'https://example.com'\napi_key = 'k'\napi_key_env = 'E'",
		"[shell]\ntimeout = 0",
		"[shell]\ntimeout = 200\nmax_timeout = 100",
	} {
		t.Run(text, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfiguration(dir, dir, ConfigFiles{}); err == nil {
				t.Fatal("expected settings error")
			}
		})
	}
}

func TestRelativeHome(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("RECUR_HOME", "custom/.recur")
	dir, err := configDir(cwd)
	if err != nil || dir != filepath.Join(cwd, "custom", ".recur") {
		t.Fatalf("%s %v", dir, err)
	}
}

func TestSettingsSnapshotDoesNotMutateAgent(t *testing.T) {
	a := &Agent{}
	configureTestAgent(t, a, Settings{Provider: "p", Model: "m", Providers: map[string]Provider{"p": {BaseURL: "https://example.com"}}})
	snapshot := a.Settings()
	snapshot.Model = "changed"
	snapshot.Providers["p"] = Provider{BaseURL: "changed"}
	if a.Settings().Model != "m" || a.Settings().Providers["p"].BaseURL != "https://example.com" {
		t.Fatal("mutable settings exposed")
	}
}

func TestTimelineLineSettings(t *testing.T) {
	for _, tc := range []struct {
		name, extra            string
		code, result, thinking int
		invalid                bool
	}{
		{"old config", "", 999, 4, 4, false},
		{"hidden", "[ui]\nmax_code_lines = 0\nmax_result_lines = 0\nmax_thinking_lines = 0\n", 0, 0, 0, false},
		{"custom", "[ui]\nmax_code_lines = 12\nmax_result_lines = 8\nmax_thinking_lines = 2\n", 12, 8, 2, false},
		{"negative code", "[ui]\nmax_code_lines = -1\n", 0, 0, 0, true},
		{"negative result", "[ui]\nmax_result_lines = -1\n", 0, 0, 0, true},
		{"negative thinking", "[ui]\nmax_thinking_lines = -1\n", 0, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			text := "provider = 'p'\nmodel = 'm'\n" + tc.extra + "[providers.p]\nbase_url = 'https://example.com'\n"
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadConfiguration(dir, dir, ConfigFiles{})
			if tc.invalid {
				if err == nil {
					t.Fatal("negative limit accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			s := cfg.settings
			if s.MaxCodeLines != tc.code || s.MaxResultLines != tc.result || s.MaxThinkingLines != tc.thinking {
				t.Fatalf("limits: %+v", s)
			}
		})
	}
}

func TestCompactionSettingsValidation(t *testing.T) {
	base := Settings{Provider: "p", Providers: map[string]Provider{"p": {BaseURL: "https://example.com"}}, ShellTimeout: 120, ShellMaxTimeout: 600}
	base.MaxContextTokens = -1
	if err := validateSettings(base); err == nil {
		t.Fatal("negative token budget accepted")
	}
	base.MaxContextTokens, base.CompactKeepTurns = 0, -2
	if err := validateSettings(base); err == nil {
		t.Fatal("negative keep count accepted")
	}
	base.CompactKeepTurns = 0
	if err := validateSettings(base); err != nil {
		t.Fatal(err)
	}
}
