package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type Selection struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
}

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
	s, data, err := a.editSettings(func(s *Settings) error {
		s.Provider, s.Model, s.Effort = provider, model, effort
		return nil
	})
	if err != nil {
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
	if err = a.writeSettings(data); err != nil {
		a.applySettings(previous)
		if a.HasSavedSession() {
			return fmt.Errorf("saving defaults: %w; session rollback: %v", err, a.Save())
		}
		return err
	}
	if previous.Provider != provider || previous.Model != model || previous.Effort != effort {
		a.Fire("model_change", "RECUR_PREV_PROVIDER", previous.Provider, "RECUR_PREV_MODEL", previous.Model, "RECUR_PREV_EFFORT", previous.Effort)
	}
	return nil
}

// ModelChoice is a model offered by one configured provider.
type ModelChoice struct{ Provider, Model string }

// AllModels lists models from every configured provider concurrently. A
// provider that fails is reported in errs and does not hide the others.
func AllModels(ctx context.Context, providers map[string]Provider) (choices []ModelChoice, errs map[string]error) {
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
	s, data, err := a.editSettings(func(s *Settings) error {
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
		return nil
	})
	if err != nil {
		return err
	}
	if err = a.writeSettings(data); err != nil {
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
	info := a.SessionInfo()
	endpoint, ctx := "", ""
	if a.socketDir != "" {
		endpoint = filepath.Join(a.socketDir, "socket")
	}
	if info["ctx_size"] != nil {
		ctx = fmt.Sprint(info["ctx_size"])
	}
	return map[string]string{
		"RECUR_ENDPOINT":   endpoint,
		"RECUR_SESSION_ID": fmt.Sprint(info["id"]),
		"RECUR_MODEL":      a.settings.Model,
		"RECUR_PROVIDER":   a.settings.Provider,
		"RECUR_EFFORT":     a.settings.Effort,
		"RECUR_CTX_SIZE":   ctx,
	}
}
