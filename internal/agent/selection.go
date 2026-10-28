package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Selection struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
}

var Efforts = []string{"none", "low", "medium", "high", "xhigh", "max"}

func (a *Agent) selection() *Selection {
	return &Selection{a.settings.Provider, a.settings.Model, a.settings.Effort}
}
func (a *Agent) SessionInfo() map[string]any {
	var size any
	for _, r := range a.Records() {
		if r.Meta != nil && r.Meta.RequestID != "" {
			size = nil
			if r.Meta.ContextInput != nil {
				size = *r.Meta.ContextInput
			}
		}
	}
	return map[string]any{"id": strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl"), "cwd": a.REPL.Dir,
		"model": a.settings.Model, "provider": a.settings.Provider, "effort": a.settings.Effort, "ctx_size": size}
}

// Select updates this session and new-session defaults; callers must wait for an idle turn.
func (a *Agent) Select(provider, model, effort string) error {
	if err := a.Writable(); err != nil {
		return err
	}
	s, err := loadSettings(a.home)
	if err != nil {
		return err
	}
	s.Provider, s.Model, s.Effort = provider, model, effort
	data, err := toml.Marshal(s)
	if err != nil {
		return err
	}
	if err = validateSettings(s); err != nil {
		return err
	}
	previous := a.settings
	a.applySettings(s)
	if a.HasSavedSession() {
		if err = a.Save(); err != nil {
			a.applySettings(previous)
			return err
		}
	}
	if err = writePrivate(filepath.Join(a.home, "settings.toml"), data); err != nil {
		a.applySettings(previous)
		if a.HasSavedSession() {
			return fmt.Errorf("saving defaults: %w; session rollback: %v", err, a.Save())
		}
		return err
	}
	return nil
}

// ModelChoice is a model offered by one configured provider.
type ModelChoice struct{ Provider, Model string }

// AllModels lists models from every configured provider concurrently. A
// provider that fails is reported in errs and does not hide the others.
func (a *Agent) AllModels(ctx context.Context) (choices []ModelChoice, errs map[string]error) {
	providers := a.Settings().Providers
	type result struct {
		name   string
		models []string
		err    error
	}
	results := make(chan result, len(providers))
	for name, p := range providers {
		go func() {
			client := newClient(p)
			page := client.Models.ListAutoPaging(ctx)
			var models []string
			for page.Next() {
				models = append(models, page.Current().ID)
			}
			results <- result{name, models, page.Err()}
		}()
	}
	errs = map[string]error{}
	for range providers {
		r := <-results
		if r.err != nil {
			errs[r.name] = r.err
			continue
		}
		for _, m := range r.models {
			choices = append(choices, ModelChoice{r.name, m})
		}
	}
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].Provider != choices[j].Provider {
			return choices[i].Provider < choices[j].Provider
		}
		return choices[i].Model < choices[j].Model
	})
	return choices, errs
}

func (a *Agent) AddProvider(name string, provider Provider) error {
	if err := a.Writable(); err != nil {
		return err
	}
	s, err := loadSettings(a.home)
	if err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("provider name is required")
	}
	if _, exists := s.Providers[name]; exists {
		return fmt.Errorf("provider already exists: %s", name)
	}
	if s.Providers == nil {
		s.Providers = map[string]Provider{}
	}
	s.Providers[name] = provider
	if err = validateSettings(s); err != nil {
		return err
	}
	data, err := toml.Marshal(s)
	if err != nil {
		return err
	}
	if err = writePrivate(filepath.Join(a.home, "settings.toml"), data); err != nil {
		return err
	}
	current := a.Settings()
	current.Providers = s.Providers
	a.applySettings(current)
	return nil
}

var bashParameters = map[string]any{"type": "object", "properties": map[string]any{
	"command":    map[string]any{"type": "string"},
	"timeout":    map[string]any{"type": []string{"integer", "null"}, "description": "seconds, default 120, maximum 600"},
	"max_output": map[string]any{"type": []string{"integer", "null"}, "description": "output bytes to keep, default 8192 (first half + last half); 0 = no truncation"},
}, "required": []string{"command", "timeout", "max_output"}, "additionalProperties": false}

// shellEnv is exported into the shell before every call.
func (a *Agent) shellEnv() map[string]string {
	env := map[string]string{"RECUR_ENDPOINT": ""}
	if a.socketDir != "" {
		env["RECUR_ENDPOINT"] = filepath.Join(a.socketDir, "socket")
	}
	for key, value := range a.SessionInfo() {
		if key != "cwd" {
			env["RECUR_"+strings.ToUpper(key)] = fmt.Sprint(value)
			if value == nil {
				env["RECUR_"+strings.ToUpper(key)] = ""
			}
		}
	}
	env["RECUR_SESSION_ID"] = env["RECUR_ID"]
	delete(env, "RECUR_ID")
	return env
}
