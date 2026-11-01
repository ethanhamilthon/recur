package agent

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"recur/internal/defaults"
	"recur/internal/hooks"
)

// ConfigFiles overrides configuration lookup. Config replaces the primary file
// (relative paths resolve against the working directory); NoExtend skips the
// global $RECUR_HOME/recur.toml layer; Headless drops hooks without headless support.
type ConfigFiles struct {
	Config   string
	NoExtend bool
	Headless bool
}

// Source names one configuration layer that contributed to the session.
type Source struct {
	Name, Path, Origin string
}

type configuration struct {
	settings Settings
	system   string
	hooks    []hooks.Hook
	local    string
	sources  []Source
	primary  string
}

// PrimaryConfig returns the primary configuration file path, the write target for /model and /login.
func (a *Agent) PrimaryConfig() string {
	if a == nil {
		return ""
	}
	if a.primaryConfig != "" {
		return a.primaryConfig
	}
	if a.home != "" {
		return filepath.Join(a.home, "recur.toml")
	}
	return ""
}

type rawConfig struct {
	Provider         *string                `toml:"provider"`
	Model            *string                `toml:"model"`
	Effort           *string                `toml:"effort"`
	ReasoningSummary *string                `toml:"reasoning_summary"`
	MaxContextTokens *int64                 `toml:"max_context_tokens"`
	CompactKeepTurns *int                   `toml:"compact_keep_turns"`
	MaxAgentSteps    *int                   `toml:"max_agent_steps"`
	MaxFileBytes     *int                   `toml:"max_file_bytes"`
	AttachmentDir    *string                `toml:"attachment_dir"`
	System           *string                `toml:"system"`
	Providers        map[string]rawProvider `toml:"providers"`
	UI               rawUI                  `toml:"ui"`
	Shell            rawShell               `toml:"shell"`
	Env              map[string]string      `toml:"env"`
	Hooks            map[string]rawHook     `toml:"hooks"`
}

type rawProvider struct {
	BaseURL   *string `toml:"base_url"`
	APIKey    *string `toml:"api_key"`
	APIKeyEnv *string `toml:"api_key_env"`
}

type rawUI struct {
	MaxCodeLines     *int      `toml:"max_code_lines"`
	MaxResultLines   *int      `toml:"max_result_lines"`
	MaxThinkingLines *int      `toml:"max_thinking_lines"`
	StatusLeft       *[]string `toml:"status_left"`
	StatusRight      *[]string `toml:"status_right"`
}

type rawShell struct {
	Timeout    *int `toml:"timeout"`
	MaxTimeout *int `toml:"max_timeout"`
	MaxOutput  *int `toml:"max_output"`
}

type rawHook struct {
	Events          *[]string `toml:"events"`
	Command         *string   `toml:"command"`
	Timeout         *int      `toml:"timeout"`
	Enabled         *bool     `toml:"enabled"`
	SendOutput      *bool     `toml:"send_output"`
	HeadlessSupport *bool     `toml:"headless_support"`
}

func parseRaw(data []byte) (rawConfig, error) {
	var cf rawConfig
	err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&cf)
	if err != nil {
		var strictErr *toml.StrictMissingError
		if errors.As(err, &strictErr) {
			return cf, errors.New(strictErr.String())
		}
		return cf, err
	}
	return cf, nil
}

func applyLayer(s *Settings, system *string, hooksMap map[string]hooks.Hook, raw rawConfig) {
	setStr := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	setInt := func(dst *int, src *int) {
		if src != nil {
			*dst = *src
		}
	}
	setStr(&s.Provider, raw.Provider)
	setStr(&s.Model, raw.Model)
	setStr(&s.Effort, raw.Effort)
	setStr(&s.ReasoningSummary, raw.ReasoningSummary)
	setStr(&s.AttachmentDir, raw.AttachmentDir)
	setStr(system, raw.System)
	if raw.MaxContextTokens != nil {
		s.MaxContextTokens = *raw.MaxContextTokens
	}
	setInt(&s.CompactKeepTurns, raw.CompactKeepTurns)
	setInt(&s.MaxAgentSteps, raw.MaxAgentSteps)
	setInt(&s.MaxFileBytes, raw.MaxFileBytes)
	setInt(&s.MaxCodeLines, raw.UI.MaxCodeLines)
	setInt(&s.MaxResultLines, raw.UI.MaxResultLines)
	setInt(&s.MaxThinkingLines, raw.UI.MaxThinkingLines)
	setInt(&s.ShellTimeout, raw.Shell.Timeout)
	setInt(&s.ShellMaxTimeout, raw.Shell.MaxTimeout)
	setInt(&s.ShellMaxOutput, raw.Shell.MaxOutput)
	if raw.UI.StatusLeft != nil {
		s.StatusLeft = slices.Clone(*raw.UI.StatusLeft)
	}
	if raw.UI.StatusRight != nil {
		s.StatusRight = slices.Clone(*raw.UI.StatusRight)
	}
	for k, v := range raw.Env {
		if s.Environment == nil {
			s.Environment = map[string]string{}
		}
		s.Environment[k] = v
	}
	for k, p := range raw.Providers {
		if s.Providers == nil {
			s.Providers = map[string]Provider{}
		}
		cur := s.Providers[k]
		setStr(&cur.BaseURL, p.BaseURL)
		setStr(&cur.APIKey, p.APIKey)
		setStr(&cur.APIKeyEnv, p.APIKeyEnv)
		s.Providers[k] = cur
	}
	for k, h := range raw.Hooks {
		cur := hooksMap[k]
		cur.Name = k
		if h.Events != nil {
			cur.Events = slices.Clone(*h.Events)
		}
		if h.Command != nil {
			cur.Command = *h.Command
		}
		if h.Timeout != nil {
			cur.Timeout = *h.Timeout
		}
		if h.Enabled != nil {
			cur.Enabled = h.Enabled
		}
		if h.SendOutput != nil {
			cur.SendOutput = *h.SendOutput
		}
		if h.HeadlessSupport != nil {
			cur.HeadlessSupport = h.HeadlessSupport
		}
		hooksMap[k] = cur
	}
}

func validateHooks(m map[string]hooks.Hook) ([]hooks.Hook, error) {
	var list []hooks.Hook
	for name, h := range m {
		enabled := h.Enabled == nil || *h.Enabled
		if !enabled {
			continue
		}
		if len(h.Events) == 0 || strings.TrimSpace(h.Command) == "" {
			return nil, fmt.Errorf("hook %q needs events and command", name)
		}
		for _, e := range h.Events {
			if e != "*" && !slices.Contains(hooks.Events, e) {
				return nil, fmt.Errorf("hook %q: unknown event %q; see recur docs hooks", name, e)
			}
		}
		if h.Timeout < 0 || h.Timeout > hooks.MaxTimeout {
			return nil, fmt.Errorf("hook %q: timeout must be 1..%d seconds", name, hooks.MaxTimeout)
		}
		if h.Timeout == 0 {
			h.Timeout = hooks.DefaultTimeout
		}
		b := enabled
		h.Enabled = &b
		headless := h.HeadlessSupport == nil || *h.HeadlessSupport
		h.HeadlessSupport = &headless
		list = append(list, h)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

func sameFile(p1, p2 string) bool {
	i1, e1 := os.Stat(p1)
	i2, e2 := os.Stat(p2)
	if e1 != nil || e2 != nil {
		return filepath.Clean(p1) == filepath.Clean(p2)
	}
	return os.SameFile(i1, i2)
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// loadConfiguration merges the built-in defaults, the global $RECUR_HOME/recur.toml
// (the base layer) and the primary file (--config, else ./recur.toml, else the
// global file itself). Files from earlier releases are never read.
func loadConfiguration(home, cwd string, files ConfigFiles) (c configuration, err error) {
	globalPath := filepath.Join(home, "recur.toml")
	globalExists := isFile(globalPath)

	primaryPath, primaryOrigin := "", ""
	if files.Config != "" {
		p := files.Config
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		info, err := os.Stat(p)
		if err != nil {
			return c, fmt.Errorf("config %s: %w", p, err)
		}
		if info.IsDir() {
			return c, fmt.Errorf("config %s: is a directory", p)
		}
		primaryPath, primaryOrigin = p, "flag"
	} else if localRecur := filepath.Join(cwd, "recur.toml"); isFile(localRecur) && !sameFile(localRecur, globalPath) {
		// ./recur.toml is the project file only when it is not the global file
		// (cwd may be $RECUR_HOME itself or an alias of it).
		primaryPath, primaryOrigin = localRecur, "project"
	} else {
		primaryPath, primaryOrigin = globalPath, "home"
	}

	var s Settings
	var system string
	hooksMap := map[string]hooks.Hook{}

	defRaw, err := parseRaw([]byte(defaults.Config))
	if err != nil {
		return c, fmt.Errorf("builtin defaults: %w", err)
	}
	applyLayer(&s, &system, hooksMap, defRaw)

	var sources []Source
	if !files.NoExtend && globalExists && !sameFile(globalPath, primaryPath) {
		data, err := os.ReadFile(globalPath)
		if err != nil {
			return c, fmt.Errorf("%s: %w", globalPath, err)
		}
		raw, err := parseRaw(data)
		if err != nil {
			return c, fmt.Errorf("%s: %w", globalPath, err)
		}
		applyLayer(&s, &system, hooksMap, raw)
		sources = append(sources, Source{Name: filepath.Base(globalPath), Path: globalPath, Origin: "home"})
	}

	if primaryOrigin == "home" && !globalExists {
		sources = append(sources, Source{Name: filepath.Base(primaryPath), Path: primaryPath, Origin: "none"})
	} else {
		data, err := os.ReadFile(primaryPath)
		if err != nil {
			return c, fmt.Errorf("%s: %w", primaryPath, err)
		}
		raw, err := parseRaw(data)
		if err != nil {
			return c, fmt.Errorf("%s: %w", primaryPath, err)
		}
		applyLayer(&s, &system, hooksMap, raw)
		sources = append(sources, Source{Name: filepath.Base(primaryPath), Path: primaryPath, Origin: primaryOrigin})
	}

	hookList, err := validateHooks(hooksMap)
	if err != nil {
		return c, fmt.Errorf("%s: %w", primaryPath, err)
	}

	if files.Headless {
		hookList = slices.DeleteFunc(hookList, func(h hooks.Hook) bool { return !*h.HeadlessSupport })
	}

	if err := validateSettings(s); err != nil {
		return c, fmt.Errorf("%s: %w", primaryPath, err)
	}

	local := ""
	if primaryOrigin == "project" {
		local = cwd
	}
	return configuration{
		settings: s,
		system:   system,
		hooks:    hookList,
		local:    local,
		sources:  sources,
		primary:  primaryPath,
	}, nil
}

func readFileIfExists(path string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	return data, err == nil
}

func rollbackFile(path string, backup []byte, existed bool) {
	if existed {
		_ = writePrivate(path, backup)
	} else {
		_ = os.Remove(path)
	}
}
