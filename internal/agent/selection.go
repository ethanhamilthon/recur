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

func (a *Agent) SessionInfo() map[string]any { return a.SessionInfoFor(a.Records()) }

// SessionInfoFor describes the session using a display snapshot, so callers that
// must not read a.records while a turn runs can pass their own copy.
func (a *Agent) SessionInfoFor(records []Record) map[string]any {
	var size any
	for _, r := range records {
		if r.Meta != nil && r.Meta.RequestID != "" {
			size = nil
			if r.Meta.ContextInput != nil {
				size = *r.Meta.ContextInput
			}
		}
	}
	return map[string]any{
		"id":       strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl"),
		"cwd":      a.REPL.Dir,
		"model":    a.settings.Model,
		"provider": a.settings.Provider,
		"effort":   a.settings.Effort,
		"ctx_size": size,
	}
}

func (a *Agent) patchPrimary(patch func(string) error) error {
	target := a.primaryConfig
	if target == "" {
		target = filepath.Join(a.home, "config.toml")
	}
	data, existed := readFileIfExists(target)
	if err := patch(target); err != nil {
		return err
	}
	cfg, err := loadConfiguration(a.home, a.REPL.Dir, a.files)
	if err != nil {
		rollbackFile(target, data, existed)
		return err
	}
	a.applySettings(cfg.settings)
	return nil
}

// Select updates this session and primary config defaults; callers must wait for an idle turn.
func (a *Agent) Select(provider, model, effort string) error {
	prev := a.settings
	target := a.primaryConfig
	if target == "" {
		target = filepath.Join(a.home, "config.toml")
	}
	data, existed := readFileIfExists(target)
	err := a.patchPrimary(func(path string) error {
		return patchRootKeys(path, map[string]string{"provider": provider, "model": model, "effort": effort})
	})
	if err != nil {
		return err
	}
	if a.HasSavedSession() {
		if err = a.Save(); err != nil {
			rollbackFile(target, data, existed)
			a.applySettings(prev)
			return fmt.Errorf("saving defaults: %w; session rollback: %v", err, a.Save())
		}
	}
	if prev.Provider != provider || prev.Model != model || prev.Effort != effort {
		a.Fire("model_change", "RECUR_PREV_PROVIDER", prev.Provider, "RECUR_PREV_MODEL", prev.Model, "RECUR_PREV_EFFORT", prev.Effort)
	}
	return nil
}

// ModelChoice is a model offered by one configured provider.
type ModelChoice struct{ Provider, Model string }

// AllModels lists models from every configured provider concurrently.
func AllModels(ctx context.Context, providers map[string]Provider, environment ...map[string]string) (choices []ModelChoice, errs map[string]error) {
	type result struct {
		name   string
		models []string
		err    error
	}
	var env map[string]string
	if len(environment) > 0 {
		env = environment[0]
	}
	results := make(chan result, len(providers))
	for name, p := range providers {
		go func(name string, p Provider) {
			if p.APIKey == "" && p.APIKeyEnv != "" {
				p.APIKey = resolveEnv(p.APIKeyEnv, env)
			}
			client := newClient(p)
			page := client.Models.ListAutoPaging(ctx)
			var models []string
			for page.Next() {
				models = append(models, page.Current().ID)
			}
			results <- result{name, models, page.Err()}
		}(name, p)
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

// AddProvider patches the provider into the primary configuration.
func (a *Agent) AddProvider(name string, provider Provider) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("provider name is required")
	}
	if _, exists := a.settings.Providers[name]; exists {
		return fmt.Errorf("provider %q already exists", name)
	}
	return a.patchPrimary(func(path string) error {
		return patchProvider(path, name, provider)
	})
}

func (a *Agent) bashParameters() map[string]any {
	timeout, maxTimeout, maxOutput := a.settings.ShellTimeout, a.settings.ShellMaxTimeout, a.settings.ShellMaxOutput
	if timeout <= 0 {
		timeout = 120
	}
	if maxTimeout <= 0 {
		maxTimeout = 600
	}
	if maxOutput < 0 {
		maxOutput = 8192
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
			"timeout": map[string]any{
				"type":        []string{"integer", "null"},
				"description": fmt.Sprintf("seconds, default %d, maximum %d", timeout, maxTimeout),
			},
			"max_output": map[string]any{
				"type":        []string{"integer", "null"},
				"description": fmt.Sprintf("output bytes to keep, default %d (first half + last half); 0 = no truncation", maxOutput),
			},
		},
		"required":             []string{"command", "timeout", "max_output"},
		"additionalProperties": false,
	}
}

// shellEnv is exported into the shell before every call.
func (a *Agent) shellEnv() map[string]string {
	env := make(map[string]string, len(a.settings.Environment)+6)
	for k, v := range a.settings.Environment {
		env[k] = v
	}
	info := a.SessionInfo()
	endpoint, ctx := "", ""
	if a.socketDir != "" {
		endpoint = filepath.Join(a.socketDir, "socket")
	}
	if info["ctx_size"] != nil {
		ctx = fmt.Sprint(info["ctx_size"])
	}
	env["RECUR_ENDPOINT"] = endpoint
	env["RECUR_SESSION_ID"] = fmt.Sprint(info["id"])
	env["RECUR_MODEL"] = a.settings.Model
	env["RECUR_PROVIDER"] = a.settings.Provider
	env["RECUR_EFFORT"] = a.settings.Effort
	env["RECUR_CTX_SIZE"] = ctx
	return env
}
