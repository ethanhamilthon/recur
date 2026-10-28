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
	if _, err := os.Stat(filepath.Join(cwd, ".recur")); !os.IsNotExist(err) {
		t.Fatal("must not use cwd config when RECUR_HOME is set")
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
	if err := os.WriteFile(filepath.Join(home, "settings.toml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.NewSession(); err != nil {
		t.Fatal(err)
	}
	if a.Settings().Model != "custom-model" || a.Settings().Effort != "high" || a.Settings().Providers["other"].BaseURL != "https://example.com/v1/" || a.Settings().Providers["other"].APIKey != "second" {
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
	} {
		t.Run(text, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadSettings(dir); err == nil {
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
		{"hidden", "max_code_lines = 0\nmax_result_lines = 0\nmax_thinking_lines = 0\n", 0, 0, 0, false},
		{"custom", "max_code_lines = 12\nmax_result_lines = 8\nmax_thinking_lines = 2\n", 12, 8, 2, false},
		{"negative code", "max_code_lines = -1\n", 0, 0, 0, true},
		{"negative result", "max_result_lines = -1\n", 0, 0, 0, true},
		{"negative thinking", "max_thinking_lines = -1\n", 0, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			text := "provider = 'p'\nmodel = 'm'\n" + tc.extra + "[providers.p]\nbase_url = 'https://example.com'\n"
			if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := loadSettings(dir)
			if tc.invalid {
				if err == nil {
					t.Fatal("negative limit accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.MaxCodeLines != tc.code || s.MaxResultLines != tc.result || s.MaxThinkingLines != tc.thinking {
				t.Fatalf("limits: %+v", s)
			}
		})
	}
}

func TestCompactionSettingsValidation(t *testing.T) {
	base := Settings{Provider: "p", Providers: map[string]Provider{"p": {BaseURL: "https://example.com"}}}
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

func TestLegacyColorsTableIgnored(t *testing.T) {
	dir := t.TempDir()
	text := "provider = 'p'\nmodel = 'm'\nmax_code_lines = 12\n[providers.p]\nbase_url = 'https://example.com'\n[colors]\naccent = \"#ff0000\"\nbogus = \"red\"\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := loadSettings(dir)
	if err != nil {
		t.Fatalf("legacy [colors] table rejected: %v", err)
	}
	if s.Provider != "p" || s.Model != "m" || s.MaxCodeLines != 12 {
		t.Fatalf("settings not loaded: %+v", s)
	}
}

func TestDefaultSettingsHaveNoColorTable(t *testing.T) {
	if strings.Contains(strings.ToLower(defaultSettings), "color") {
		t.Fatal("default settings must not mention colors")
	}
	dir := t.TempDir()
	if _, err := loadSettings(dir); err != nil {
		t.Fatal(err)
	}
	created, err := os.ReadFile(filepath.Join(dir, "settings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(created)), "color") {
		t.Fatalf("created settings mention colors:\n%s", created)
	}
}
