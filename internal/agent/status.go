package agent

import (
	"strings"
	"sync"
)

// maxStatusMetadataValueLen caps the length of individual metadata values
// sent to the status channel, preventing unbounded payloads.
const maxStatusMetadataValueLen = 4096

// IsAllowedStatusMetadataKey reports whether a variable key is an allowed small scalar
// metadata variable for status environments. It rejects all *_FILE payload keys and any
// keys not on the explicit metadata allowlist.
func IsAllowedStatusMetadataKey(key string) bool {
	if strings.HasSuffix(key, "_FILE") {
		return false
	}
	switch key {
	case "RECUR_EVENT",
		"RECUR_SESSION_ID",
		"RECUR_SESSION_SOURCE",
		"RECUR_SESSION_TITLE",
		"RECUR_PREV_SESSION_ID",
		"RECUR_PROVIDER",
		"RECUR_MODEL",
		"RECUR_EFFORT",
		"RECUR_PREV_PROVIDER",
		"RECUR_PREV_MODEL",
		"RECUR_PREV_EFFORT",
		"RECUR_CWD",
		"RECUR_ENDPOINT",
		"RECUR_VERSION",
		"RECUR_STATE",
		"RECUR_CTX_SIZE",
		"RECUR_TURN_SOURCE",
		"RECUR_TURN_STATUS",
		"RECUR_TURN_DURATION_MS",
		"RECUR_DURATION_MS",
		"RECUR_TIMEOUT",
		"RECUR_EXIT_CODE",
		"RECUR_ERROR",
		"RECUR_COMPACT_REASON",
		"RECUR_COMPACT_STATUS",
		"RECUR_IMAGE_SOURCE",
		"RECUR_INPUT_TOKENS",
		"RECUR_OUTPUT_TOKENS",
		"RECUR_ATTEMPT",
		"RECUR_REASON",
		"RECUR_MODE":
		return true
	default:
		return false
	}
}

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
		if strings.HasSuffix(k, "_FILE") || !IsAllowedStatusMetadataKey(k) {
			continue
		}
		if len(v) > maxStatusMetadataValueLen {
			v = v[:maxStatusMetadataValueLen]
		}
		snapshot[k] = v
	}
	snapshot["RECUR_EVENT"] = event
	if _, ok := snapshot["RECUR_VERSION"]; !ok && a.version != "" {
		snapshot["RECUR_VERSION"] = a.version
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
