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
		a.ReadOnly = true
		return nil
	}
	if err != nil {
		file.Close()
		return err
	}
	a.owner, a.ReadOnly = file, false
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
func (a *Agent) Writable() error {
	if a.ReadOnly {
		return fmt.Errorf("session is read-only: another client owns it")
	}
	return nil
}

// Refresh reloads only committed snapshots; read failures leave the current view intact.
func (a *Agent) Refresh() error {
	if !a.ReadOnly {
		return nil
	}
	return a.readSession()
}

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
	closeErr := file.Close()
	if err == nil {
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
	if !a.ReadOnly && a.HasSavedSession() {
		if err = a.Save(); err != nil {
			return err
		}
	}
	next := *a
	next.owner, next.ReadOnly = nil, false
	next.listener, next.inbox, next.views = nil, nil, nil
	committed := false
	defer func() {
		if !committed {
			next.release()
		}
	}()
	next.applySettings(c.settings)
	next.lastRequest = Metadata{}
	next.History, next.records = nil, nil
	if id == "" {
		err = next.createSession()
	} else {
		err = next.openSession(id)
	}
	if err != nil {
		return err
	}
	a.REPL.Close()
	a.release()
	a.owner, a.ReadOnly = next.owner, next.ReadOnly
	a.listener, a.socketDir, a.inbox, a.views = next.listener, next.socketDir, next.inbox, next.views
	committed = true
	a.History, a.records, a.HistoryPath = next.History, next.records, next.HistoryPath
	a.REPL.Prefix, a.REPL.PrefixOutput = c.prefix, ""
	a.System = c.system
	a.applySettings(next.settings)
	return nil
}
