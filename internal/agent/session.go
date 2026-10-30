package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"recur/internal/hooks"
	"recur/prompts"
)

func (a *Agent) acquire() error {
	file, err := os.OpenFile(a.HistoryPath+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		file.Close()
		return fmt.Errorf("session is in use by another recur process: %s", strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl"))
	}
	if err != nil {
		file.Close()
		return err
	}
	a.owner = file
	if err := a.listenNotifications(); err != nil {
		a.release()
		return err
	}
	return nil
}
func (a *Agent) release() {
	if a.listener != nil {
		a.listener.Close()
		a.listener = nil
		os.Remove(a.HistoryPath + ".endpoint")
		os.RemoveAll(a.socketDir)
	}
	if a.owner != nil {
		_ = syscall.Flock(int(a.owner.Fd()), syscall.LOCK_UN)
		_ = a.owner.Close()
		a.owner = nil
		if _, err := os.Stat(a.HistoryPath); os.IsNotExist(err) {
			_ = os.Remove(a.HistoryPath + ".lock")
		}
	}
}
func (a *Agent) HasSavedSession() bool {
	_, err := os.Stat(a.HistoryPath)
	return len(a.History) > 0 || err == nil
}
func (a *Agent) Close() { a.REPL.Close(); a.release() }

// loadOrCreateFile materializes the editable defaults without overwriting user changes.
func loadOrCreateFile(path, fallback string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return string(data), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		data, err = os.ReadFile(path)
		return string(data), err
	}
	if err != nil {
		return "", err
	}
	_, err = file.WriteString(fallback)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return fallback, err
}

type configuration struct {
	settings       Settings
	prefix, system string
	hooks          []hooks.Hook
	local          string // project .recur directory that supplied a config file
	ignored        bool   // project config exists but enable_local_recur is off
	sources        []Source
}

// ConfigFiles holds --system, --prefix and --hooks paths; they win over .recur
// files. NoHooks disables hooks (headless runs without --hooks).
type ConfigFiles struct {
	System, Prefix, Hooks string
	NoHooks               bool
}

// Source tells where one configuration file is read from. Origin is flag,
// project or home; hooks.toml may also be absent (Origin "none", no Path).
type Source struct{ Name, Path, Origin string }

// loadConfiguration reads settings from home. system.md, prefix.sh and hooks.toml
// come from the given files, else (with enable_local_recur) from cwd/.recur when
// they exist there and it is not home, else from home.
func loadConfiguration(home, cwd string, files ConfigFiles) (c configuration, err error) {
	if c.settings, err = loadSettings(home); err != nil {
		return c, err
	}
	local, err := filepath.Abs(filepath.Join(cwd, ".recur"))
	if err != nil || !projectConfig(local, home) {
		local = ""
	}
	if local != "" && !c.settings.EnableLocalRecur {
		for _, name := range []string{"system.md", "prefix.sh", "hooks.toml"} {
			c.ignored = c.ignored || isFile(filepath.Join(local, name))
		}
		local = ""
	}
	if c.system, err = c.pick(files.System, home, local, "system.md", prompts.System, true); err != nil {
		return c, err
	}
	if c.prefix, err = c.pick(files.Prefix, home, local, "prefix.sh", prompts.Prefix, true); err != nil {
		return c, err
	}
	if files.NoHooks {
		c.sources = append(c.sources, Source{Name: "hooks.toml", Origin: "none"})
		return c, nil
	}
	text, err := c.pick(files.Hooks, home, local, "hooks.toml", "", false)
	if err == nil {
		if c.hooks, err = hooks.Parse(text); err != nil {
			err = fmt.Errorf("%s: %w", c.sources[len(c.sources)-1].Path, err)
		}
	}
	return c, err
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// pick reads name from the flag file, else the project copy (never created),
// else home. A missing home file is created from fallback when create is set;
// otherwise it means the file is absent.
func (c *configuration) pick(flag, home, local, name, fallback string, create bool) (string, error) {
	src := Source{Name: name, Path: flag, Origin: "flag"}
	if flag == "" {
		src.Path, src.Origin = filepath.Join(local, name), "project"
		if local == "" || !isFile(src.Path) {
			src.Path, src.Origin = filepath.Join(home, name), "home"
		}
	}
	var text string
	var err error
	switch {
	case src.Origin == "home" && create:
		text, err = loadOrCreateFile(src.Path, fallback)
	default:
		var data []byte
		data, err = os.ReadFile(src.Path)
		text = string(data)
		if os.IsNotExist(err) && src.Origin == "home" {
			src.Path, src.Origin, err = "", "none", nil
		}
	}
	if src.Origin == "project" {
		c.local = local
	}
	c.sources = append(c.sources, src)
	return text, err
}

// projectConfig reports whether local is a directory other than RECUR_HOME;
// os.SameFile also matches symlinked and differently cased paths.
func projectConfig(local, home string) bool {
	info, err := os.Stat(local)
	if err != nil || !info.IsDir() {
		return false
	}
	homeInfo, err := os.Stat(home)
	return err != nil || !os.SameFile(info, homeInfo)
}

// Session changes are called only after the active turn has finished.
func (a *Agent) NewSession() error      { return a.changeSession("") }
func (a *Agent) Resume(id string) error { return a.changeSession(id) }

func (a *Agent) changeSession(id string) error {
	if id != "" && id == strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl") {
		return nil
	}
	c, err := loadConfiguration(a.home, a.REPL.Dir, a.files)
	if err != nil {
		return err
	}
	if a.HasSavedSession() {
		if err = a.Save(); err != nil {
			return err
		}
	}
	next := &Agent{files: a.files, home: a.home, sessionsDir: a.sessionsDir, REPL: a.REPL, hooks: a.hooks,
		System: c.system, LocalConfig: c.local, LocalIgnored: c.ignored, Sources: c.sources}
	next.applySettings(c.settings)
	if id == "" {
		err = next.createSession()
	} else {
		err = next.openSession(id)
	}
	if err != nil {
		next.release()
		return err
	}
	previous := strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl")
	a.Fire("session_end", "RECUR_REASON", "switch")
	a.Close()
	next.REPL.Prefix, next.REPL.PrefixOutput = c.prefix, ""
	*a = *next
	a.hooks.Set(c.hooks)
	source := "new"
	if id != "" {
		source = "resume"
	}
	a.Fire("session_start", "RECUR_SESSION_SOURCE", source, "RECUR_PREV_SESSION_ID", previous)
	a.Fire("session_switch", "RECUR_PREV_SESSION_ID", previous)
	a.titleChanged()
	return nil
}
