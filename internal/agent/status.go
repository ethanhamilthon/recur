package agent

import (
	"strconv"
	"sync"
)

type statusBridge struct {
	mu sync.Mutex
	ch chan map[string]string
}

var globalStatusMu sync.Mutex

func (a *Agent) getStatus() *statusBridge {
	if a == nil {
		return nil
	}
	globalStatusMu.Lock()
	defer globalStatusMu.Unlock()
	if a.status == nil {
		a.status = &statusBridge{ch: make(chan map[string]string, 64)}
	}
	return a.status
}

// StatusEvents returns a receive-only channel of immutable lifecycle event metadata snapshots.
func (a *Agent) StatusEvents() <-chan map[string]string {
	sb := a.getStatus()
	if sb == nil {
		return nil
	}
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.ch
}

func (a *Agent) emitStatus(event string, vars map[string]string) {
	sb := a.getStatus()
	if sb == nil {
		return
	}
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.ch == nil {
		return
	}
	snapshot := make(map[string]string, len(vars)+4)
	for k, v := range vars {
		snapshot[k] = v
	}
	snapshot["RECUR_EVENT"] = event
	if _, ok := snapshot["RECUR_VERSION"]; !ok && a.version != "" {
		snapshot["RECUR_VERSION"] = a.version
	}
	if _, ok := snapshot["RECUR_CTX_SIZE"]; !ok && a.lastRequest.ContextInput != nil {
		snapshot["RECUR_CTX_SIZE"] = strconv.FormatInt(*a.lastRequest.ContextInput, 10)
	}
	select {
	case sb.ch <- snapshot:
	default:
		select {
		case <-sb.ch:
		default:
		}
		select {
		case sb.ch <- snapshot:
		default:
		}
	}
}
