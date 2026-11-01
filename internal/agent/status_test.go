package agent

import (
	"path/filepath"
	"strconv"
	"testing"
)

func TestStatusEventsDeliveredWithoutHooks(t *testing.T) {
	a := &Agent{
		HistoryPath: filepath.Join(t.TempDir(), "sess-123.jsonl"),
		settings: Settings{
			Provider: "testprov",
			Model:    "testmodel",
			Effort:   "high",
		},
	}
	// a.hooks is nil
	ch := a.StatusEvents()
	if ch == nil {
		t.Fatal("StatusEvents returned nil channel")
	}

	a.Fire("turn_start", "RECUR_TURN_SOURCE", "user", "CUSTOM_VAR", "val")

	select {
	case ev := <-ch:
		if ev["RECUR_EVENT"] != "turn_start" {
			t.Fatalf("expected turn_start, got %q", ev["RECUR_EVENT"])
		}
		if ev["RECUR_SESSION_ID"] != "sess-123" {
			t.Fatalf("expected sess-123, got %q", ev["RECUR_SESSION_ID"])
		}
		if ev["RECUR_PROVIDER"] != "testprov" || ev["RECUR_MODEL"] != "testmodel" || ev["RECUR_EFFORT"] != "high" {
			t.Fatalf("unexpected provider/model/effort in event: %v", ev)
		}
		if ev["RECUR_TURN_SOURCE"] != "user" {
			t.Fatalf("expected RECUR_TURN_SOURCE user, got %q", ev["RECUR_TURN_SOURCE"])
		}
		if ev["CUSTOM_VAR"] != "val" {
			t.Fatalf("expected CUSTOM_VAR val, got %q", ev["CUSTOM_VAR"])
		}
	default:
		t.Fatal("expected status event to be delivered")
	}
}

func TestStatusEventsChannelNonblocking(t *testing.T) {
	a := &Agent{
		HistoryPath: filepath.Join(t.TempDir(), "s.jsonl"),
	}
	ch := a.StatusEvents()
	// Channel capacity is 64. Send 100 events to ensure nonblocking drop of oldest.
	for i := 0; i < 100; i++ {
		a.Fire("test_event", "INDEX", strconv.Itoa(i))
	}
	// Should have 64 elements, not blocked
	if len(ch) != 64 {
		t.Fatalf("expected channel buffer full (64), got %d", len(ch))
	}
	// The newest event should have arrived (INDEX=99)
	var last map[string]string
	for len(ch) > 0 {
		last = <-ch
	}
	if last["INDEX"] != "99" {
		t.Fatalf("expected newest event INDEX 99, got %q", last["INDEX"])
	}
}
