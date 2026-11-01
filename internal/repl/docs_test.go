package repl

import (
	"strings"
	"testing"
)

func TestDocs(t *testing.T) {
	for _, topic := range []string{"index", "runtime", "sessions", "steering", "interface", "images", "models", "headless", "clipboard", "hooks"} {
		var out strings.Builder
		if err := Docs(&out, []string{topic}); err != nil || out.Len() < 50 {
			t.Fatalf("%s: %v", topic, err)
		}
	}
	var out strings.Builder
	if err := Docs(&out, nil); err != nil || !strings.Contains(out.String(), "Recur documentation") {
		t.Fatalf("default topic: %v", err)
	}
	for _, args := range [][]string{{"missing"}, {"../system"}, {"index", "runtime"}, {""}, {"files"}} {
		if err := Docs(&out, args); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

func TestToolDescriptionFor(t *testing.T) {
	// Default values match ToolDescription constant
	desc := ToolDescriptionFor(0, 0, -1)
	if desc != ToolDescription {
		t.Fatalf("default ToolDescriptionFor mismatch:\ngot:\n%s\nwant:\n%s", desc, ToolDescription)
	}

	// Dynamic values formatted correctly
	custom := ToolDescriptionFor(45, 180, 4096)
	if !strings.Contains(custom, "default 45 (null), maximum 180") {
		t.Fatalf("missing custom timeout in:\n%s", custom)
	}
	if !strings.Contains(custom, "default 4096 (null)") {
		t.Fatalf("missing custom max_output in:\n%s", custom)
	}

	// Unlimited max_output (0)
	unlimited := ToolDescriptionFor(120, 600, 0)
	if !strings.Contains(unlimited, "default 0 (unlimited) (null)") {
		t.Fatalf("missing unlimited max_output in:\n%s", unlimited)
	}
}

// readDocTopic returns the embedded documentation text for one topic.
func readDocTopic(t *testing.T, topic string) string {
	t.Helper()
	var out strings.Builder
	if err := Docs(&out, []string{topic}); err != nil {
		t.Fatalf("%s: %v", topic, err)
	}
	return out.String()
}

// TestDocsOmitMigrationGuidance guards the public topics against upgrade
// instructions and old/new comparisons while keeping the current contract.
func TestDocsOmitMigrationGuidance(t *testing.T) {
	topics := []string{"config", "hooks", "sessions", "index", "runtime"}
	removed := []string{
		"Manual Migration Guide",
		"Migrating legacy hooks",
		"Migrating prefix.sh",
		"migrat",
		"0.5.",
		"0.6.0",
		"Legacy syntax",
		"Legacy (invalid",
		"legacy configuration",
		"legacy hooks",
		"legacy prefix",
		"legacy files",
		"legacy cli",
		"Retired events",
		"prefix_start",
		"prefix_end",
		"root prefix",
		"vm_start",
		"settings.toml",
		"hooks.toml",
		"prefix.sh",
		"enable_local_recur",
		"--settings",
		"--prefix",
		"--system",
		"single-string",
	}
	for _, topic := range topics {
		text := readDocTopic(t, topic)
		lower := strings.ToLower(text)
		for _, marker := range removed {
			if strings.Contains(lower, strings.ToLower(marker)) {
				t.Fatalf("%s topic still contains removed migration marker %q", topic, marker)
			}
		}
	}

	// Current contract markers that must survive the cleanup.
	required := map[string][]string{
		"config": {
			"## Discovery and Precedence",
			"## Inheritance and Deep Merging",
			"`--no-extend`",
			"max_context_tokens = 400000",
			"compact_keep_turns = 4",
			"max_agent_steps = 0",
			"`[shell]`",
			"max_timeout = 600",
			"### [providers.NAME]",
			"`api_key_env`",
			"`[hooks.NAME]`",
			"`events = [...]`",
			"`send_output`: boolean flag (default `false`)",
			"## Headless Mode Hooks",
		},
		"hooks": {
			"events = [\"turn_end\"]",
			"events = [\"session_start\"]",
			"strictly asynchronous",
			"send_output = true",
			"NEXT TURN",
			"[hooks.init]",
			"command = \"cat AGENTS.md\"",
			"may start before the output is ready",
			"## Turn Loop Prevention",
		},
		"sessions": {
			"`$RECUR_HOME/sessions/<project-key>/`",
			"`./recur.toml`",
			"`$RECUR_HOME/config.toml`",
			"`--no-extend`",
			"session is in use",
			"`/reload`",
		},
		"index": {
			"config: unified recur.toml, precedence, options, status bar",
			"hooks: named hooks, send_output next-turn delivery",
		},
	}
	for topic, markers := range required {
		text := readDocTopic(t, topic)
		for _, marker := range markers {
			if !strings.Contains(text, marker) {
				t.Fatalf("%s topic lost current contract marker %q", topic, marker)
			}
		}
	}

	// The session-init example must not promise delivery on the first turn.
	hooks := readDocTopic(t, "hooks")
	if strings.Contains(hooks, "first turn") && !strings.Contains(hooks, "may start before the output is ready") {
		t.Fatalf("hooks topic promises first-turn delivery for async session_start output")
	}
	if strings.Contains(hooks, "start of the session's first turn") {
		t.Fatalf("hooks topic still promises session_start output on the first turn")
	}
}
