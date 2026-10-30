package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

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
}

func loadConfiguration(dir string) (configuration, error) {
	var c configuration
	var err error
	c.settings, err = loadSettings(dir)
	if err != nil {
		return c, err
	}
	c.prefix, err = loadOrCreateFile(filepath.Join(dir, "prefix.sh"), prompts.Prefix)
	if err != nil {
		return c, err
	}
	c.system, err = loadOrCreateFile(filepath.Join(dir, "system.md"), prompts.System)
	return c, err
}

// Session changes are called only after the active turn has finished.
func (a *Agent) NewSession() error      { return a.changeSession("") }
func (a *Agent) Resume(id string) error { return a.changeSession(id) }

func (a *Agent) changeSession(id string) error {
	if id != "" && id == strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl") {
		return nil
	}
	c, err := loadConfiguration(a.home)
	if err != nil {
		return err
	}
	if a.HasSavedSession() {
		if err = a.Save(); err != nil {
			return err
		}
	}
	next := &Agent{home: a.home, sessionsDir: a.sessionsDir, REPL: a.REPL, System: c.system}
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
	a.Close()
	next.REPL.Prefix, next.REPL.PrefixOutput = c.prefix, ""
	*a = *next
	return nil
}
