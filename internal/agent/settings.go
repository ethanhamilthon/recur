package agent

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/pelletier/go-toml/v2"
)

type Provider struct {
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"api_key"`
}

type Settings struct {
	AttachmentDir    string              `toml:"attachment_dir"`
	MaxContextTokens int64               `toml:"max_context_tokens"`
	CompactKeepTurns int                 `toml:"compact_keep_turns"`
	MaxAgentSteps    int                 `toml:"max_agent_steps"`
	MaxCodeLines     int                 `toml:"max_code_lines"`
	MaxResultLines   int                 `toml:"max_result_lines"`
	MaxThinkingLines int                 `toml:"max_thinking_lines"`
	Provider         string              `toml:"provider"`
	Model            string              `toml:"model"`
	ReasoningSummary string              `toml:"reasoning_summary"`
	Effort           string              `toml:"effort"`
	Providers        map[string]Provider `toml:"providers"`
}

const defaultSettings = `# Empty uses the platform temporary directory/recur-images. Relative paths use project cwd.
attachment_dir = ""
# No provider or model is configured by default. In recur, use /login to add a
# provider, then /model to choose a model.
provider = ""
model = ""
# Leave empty to use the provider's default reasoning effort.
effort = ""
# Set to off for providers/models that do not support reasoning summaries.
reasoning_summary = "auto"
# Compact older turns into a summary when the history exceeds this estimated size.
# The estimate is 4 bytes per token plus 1200 per image; 0 disables compaction.
max_context_tokens = 400000
# Turns kept verbatim when compacting.
compact_keep_turns = 4
# Model/tool round trips allowed in one turn; 0 means no limit.
max_agent_steps = 0
# Visible body rows per timeline block; 0 hides the whole block.
max_code_lines = 999
max_result_lines = 4
max_thinking_lines = 4
`

func loadSettings(dir string) (Settings, error) {
	s := Settings{MaxCodeLines: 999, MaxResultLines: 4, MaxThinkingLines: 4, MaxContextTokens: 400000, CompactKeepTurns: 4}
	path := filepath.Join(dir, "settings.toml")
	text, err := loadOrCreateFile(path, defaultSettings)
	if err != nil {
		return s, err
	}
	if err = toml.Unmarshal([]byte(text), &s); err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	if err := validateSettings(s); err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

func validateSettings(s Settings) error {
	if s.MaxCodeLines < 0 || s.MaxResultLines < 0 || s.MaxThinkingLines < 0 {
		return fmt.Errorf("max_*_lines must be non-negative")
	}
	if s.MaxContextTokens < 0 || s.CompactKeepTurns < 0 || s.MaxAgentSteps < 0 {
		return fmt.Errorf("max_context_tokens, compact_keep_turns and max_agent_steps must be non-negative")
	}
	// An empty provider and model mean nothing has been selected yet; that is
	// valid and recur asks the user to /login and choose a /model.
	if s.Provider != "" {
		if _, ok := s.Providers[s.Provider]; !ok {
			return fmt.Errorf("selected provider %q is not configured", s.Provider)
		}
	}
	for name, p := range s.Providers {
		u, err := url.Parse(p.BaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("provider %q needs an HTTP(S) base_url", name)
		}
	}
	if s.ReasoningSummary != "" && s.ReasoningSummary != "auto" && s.ReasoningSummary != "off" {
		return fmt.Errorf("reasoning_summary must be auto or off")
	}
	switch s.Effort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max":
	default:
		return fmt.Errorf("unsupported effort %q", s.Effort)
	}
	return nil
}

func configDir(cwd string) (string, error) {
	dir := os.Getenv("RECUR_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".recur")
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	return filepath.Abs(dir)
}

// Settings returns the selected configuration without exposing mutable provider state.
func (a *Agent) Settings() Settings {
	s := a.settings
	s.Providers = make(map[string]Provider, len(a.settings.Providers))
	for name, provider := range a.settings.Providers {
		s.Providers[name] = provider
	}
	return s
}

func (a *Agent) applySettings(s Settings) {
	a.settings = s
	a.client = newClient(s.Providers[s.Provider])
}

func newClient(p Provider) openai.Client {
	return openai.NewClient(
		option.WithBaseURL(strings.TrimRight(p.BaseURL, "/")+"/"),
		option.WithAPIKey(p.APIKey),
		option.WithOrganization(""),
		option.WithProject(""),
		option.WithMaxRetries(0),
		option.WithRequestTimeout(10*time.Minute),
	)
}
