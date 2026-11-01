package docs

import (
	"regexp"
	"strings"
	"testing"

	"recur/internal/defaults"
)

// docRef matches `recur docs NAME`; the placeholder TOPIC stands for any topic.
var docRef = regexp.MustCompile(`recur docs ([A-Za-z0-9_]+)`)

// docFileRef matches a reference to a topic by its file name, such as config.md.
var docFileRef = regexp.MustCompile(`\b(index|cli|cookbook|config|runtime|images|clipboard|sessions|steering|interface|models|hooks|headless|troubleshooting|update)\.md\b`)

// retiredConfigName is the old global file name. It is split so that the
// literal never appears in the docs or this package.
var retiredConfigName = "config" + ".toml"

// keyTopics are the topics the tool description must introduce, one line each.
var keyTopics = []string{"config", "hooks", "cli", "cookbook", "headless", "runtime", "sessions", "troubleshooting", "update"}

func TestDocs(t *testing.T) {
	for _, topic := range docTopics(t) {
		var out strings.Builder
		if err := Docs(&out, []string{topic}); err != nil || out.Len() < 50 {
			t.Fatalf("%s: %v", topic, err)
		}
	}
	var out strings.Builder
	if err := Docs(&out, nil); err != nil || !strings.Contains(out.String(), "Recur documentation") {
		t.Fatalf("default topic: %v", err)
	}
	for _, args := range [][]string{{"missing"}, {"../system"}, {"index", "runtime"}, {""}, {"files"}, {"config.md"}} {
		if err := Docs(&out, args); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

func docTopics(t *testing.T) []string {
	t.Helper()
	files, err := documentation.ReadDir("docs")
	if err != nil {
		t.Fatal(err)
	}
	var topics []string
	for _, f := range files {
		topics = append(topics, strings.TrimSuffix(f.Name(), ".md"))
	}
	return topics
}

// normalized joins runs of whitespace so references wrapped across lines still match.
func normalized(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func TestIndexListsEveryTopicAndNoPlaceholdersRemain(t *testing.T) {
	var index strings.Builder
	if err := Docs(&index, []string{"index"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(index.String(), "Each fact is documented in one topic; other topics point to it.") {
		t.Fatal("index lost the single-source rule")
	}
	for _, topic := range docTopics(t) {
		if topic != "index" && !strings.Contains(index.String(), "- "+topic+":") {
			t.Errorf("topic %q is missing from docs index", topic)
		}
		var out strings.Builder
		if err := Docs(&out, []string{topic}); err != nil || strings.Contains(out.String(), "{{") {
			t.Errorf("topic %q: err=%v or unexpanded placeholder", topic, err)
		}
	}
}

func TestConfigDocShowsShippedDefaults(t *testing.T) {
	var out strings.Builder
	if err := Docs(&out, []string{"config"}); err != nil || !strings.Contains(out.String(), strings.TrimSpace(defaults.Config)) {
		t.Fatalf("config docs do not embed internal/defaults/defaults.toml: %v", err)
	}
}

// readDocTopic returns the rendered documentation text for one topic.
func readDocTopic(t *testing.T, topic string) string {
	t.Helper()
	var out strings.Builder
	if err := Docs(&out, []string{topic}); err != nil {
		t.Fatalf("%s: %v", topic, err)
	}
	return out.String()
}

// TestDocsReferenceTopicsNotFiles keeps cross-references in the form `recur docs TOPIC`.
func TestDocsReferenceTopicsNotFiles(t *testing.T) {
	for _, topic := range docTopics(t) {
		text := readDocTopic(t, topic)
		if m := docFileRef.FindString(text); m != "" {
			t.Errorf("topic %q refers to another topic file %q; use `recur docs TOPIC`", topic, m)
		}
		if strings.Contains(text, retiredConfigName) {
			t.Errorf("topic %q mentions the retired config file name; the file is recur.toml", topic)
		}
	}
}

// TestDocsRecurDocsReferencesExist checks that every `recur docs X` names an existing topic.
func TestDocsRecurDocsReferencesExist(t *testing.T) {
	topics := map[string]bool{"TOPIC": true}
	for _, topic := range docTopics(t) {
		topics[topic] = true
	}
	for _, topic := range docTopics(t) {
		for _, m := range docRef.FindAllStringSubmatch(normalized(readDocTopic(t, topic)), -1) {
			if !topics[m[1]] {
				t.Errorf("topic %q references `recur docs %s`, which is not a topic", topic, m[1])
			}
		}
	}
	for _, m := range docRef.FindAllStringSubmatch(normalized(ToolDescription), -1) {
		if !topics[m[1]] {
			t.Errorf("tool description references `recur docs %s`, which is not a topic", m[1])
		}
	}
}

// TestRecurTomlIsTheOnlyConfigName guards the file name and the precedence wording.
func TestRecurTomlIsTheOnlyConfigName(t *testing.T) {
	for _, topic := range docTopics(t) {
		text := readDocTopic(t, topic)
		for _, bad := range []string{retiredConfigName, "recur.toml or", "settings.toml", "hooks.toml"} {
			if strings.Contains(text, bad) {
				t.Errorf("topic %q contains %q", topic, bad)
			}
		}
	}
	if !strings.Contains(readDocTopic(t, "config"), "`$RECUR_HOME/recur.toml`") {
		t.Fatal("config topic does not name the global file $RECUR_HOME/recur.toml")
	}
}

// TestRecurVariableTableLivesInCLI keeps the RECUR_* reference in one table.
func TestRecurVariableTableLivesInCLI(t *testing.T) {
	for _, topic := range docTopics(t) {
		if topic == "cli" {
			continue
		}
		for _, line := range strings.Split(readDocTopic(t, topic), "\n") {
			if strings.HasPrefix(line, "| `RECUR_") {
				t.Errorf("topic %q has a RECUR_* table row; the table belongs to cli: %q", topic, line)
			}
		}
	}
	cli := readDocTopic(t, "cli")
	for _, name := range []string{"RECUR_HOME", "RECUR_NO_UPDATE_CHECK", "RECUR_ENDPOINT", "RECUR_SESSION_ID", "RECUR_MODEL", "RECUR_PROVIDER", "RECUR_EFFORT", "RECUR_CTX_SIZE", "RECUR_CWD", "RECUR_VERSION", "RECUR_STATE", "RECUR_MODE", "RECUR_SEQ", "RECUR_TS"} {
		if !strings.Contains(cli, "`"+name+"`") {
			t.Errorf("cli topic does not document %s", name)
		}
	}
}

func TestDocsOmitMigrationGuidance(t *testing.T) {
	topics := docTopics(t)
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
		"status-left",
		"status-right",
		"curl -fsSL",
	}
	for _, topic := range topics {
		lower := strings.ToLower(readDocTopic(t, topic))
		for _, marker := range removed {
			if strings.Contains(lower, strings.ToLower(marker)) {
				t.Fatalf("%s topic still contains removed migration marker %q", topic, marker)
			}
		}
	}
}

// installerTopic is the only topic that may name the first-install script and its variables.
const installerTopic = "update"

// installerMarkers belong to scripts/install.sh, which only the first install uses. README.md keeps
// the install section; it is not embedded, so this test does not read it.
var installerMarkers = []string{"install.sh", "RECUR_BINDIR"}

// TestInstallerIsNamedOnlyInUpdate keeps the first-install script out of every topic but update.
func TestInstallerIsNamedOnlyInUpdate(t *testing.T) {
	for _, topic := range docTopics(t) {
		if topic == installerTopic {
			continue
		}
		text := readDocTopic(t, topic)
		for _, marker := range installerMarkers {
			if strings.Contains(text, marker) {
				t.Errorf("topic %q names %q; only the %s topic may describe the installer", topic, marker, installerTopic)
			}
		}
	}
}

// TestDocsKeepCurrentContract checks that each topic still states the facts it owns.
func TestDocsKeepCurrentContract(t *testing.T) {
	required := map[string][]string{
		"config": {
			"## Discovery and Precedence",
			"## Merge Order",
			"## Key Reference",
			"`--no-extend`",
			"`$RECUR_HOME/recur.toml`",
			"`max_context_tokens`",
			"`max_agent_steps`",
			"`shell.max_timeout`",
			"`providers.NAME.api_key_env`",
			"`hooks.NAME.events`",
			"`hooks.NAME.send_output`",
			"`hooks.NAME.headless_support`",
			"## The recur.toml Format",
		},
		"hooks": {
			"events = [\"turn_end\"]",
			"events = [\"session_start\"]",
			"strictly asynchronous",
			"send_output = true",
			"NEXT TURN",
			"[hooks.session_init]",
			"command = \"cat AGENTS.md\"",
			"The first turn may start before that",
			"## Immediate Notifications and Loop Prevention",
			"[ \"$RECUR_TURN_SOURCE\" = user ] || exit 0",
		},
		"sessions": {
			"# Sessions",
			"## Storage Layout",
			"## Locks",
			"`$RECUR_HOME/sessions/",
			"session is in use by another recur process",
		},
		"cli": {
			"## Environment variables",
			"## Exit codes",
			"`-no-extend`",
		},
		"index": {
			"config: recur.toml discovery and precedence",
			"hooks: named hooks",
			"troubleshooting: startup and configuration errors",
			"update: recur update",
		},
		"troubleshooting": {
			"session is in use by another recur process",
			"unknown event",
			"hooks.log",
			"status.log",
			"missing field",
			"api_key_env",
		},
		"update": {
			"RECUR_NO_UPDATE_CHECK",
			"`$RECUR_HOME/update.json`",
			"recur update --check",
			"checksums.txt",
			"SHA-256",
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
	if strings.Contains(hooks, "start of the session's first turn") {
		t.Fatalf("hooks topic still promises session_start output on the first turn")
	}
}

// TestToolDescriptionPointsToDocs checks that the tool description introduces Recur and its topics.
func TestToolDescriptionPointsToDocs(t *testing.T) {
	for _, want := range []string{"recur.toml", "recur docs config", "recur docs index", "RECUR_SESSION_ID", "~/.recur/recur.toml"} {
		if !strings.Contains(ToolDescription, want) {
			t.Errorf("tool description does not mention %q", want)
		}
	}
	topics := map[string]bool{}
	for _, topic := range docTopics(t) {
		topics[topic] = true
	}
	for _, key := range keyTopics {
		if !topics[key] {
			t.Errorf("key topic %q does not exist", key)
		}
		if !strings.Contains(ToolDescription, "\n  "+key+":") {
			t.Errorf("tool description has no line for topic %q", key)
		}
	}
	if docFileRef.MatchString(ToolDescription) || strings.Contains(ToolDescription, ".md") {
		t.Error("tool description refers to a documentation file name")
	}
	if len(ToolDescription) > 4096 || strings.Count(ToolDescription, "\n") > 60 {
		t.Errorf("tool description is %d bytes and %d lines; keep it under 4 KB and 60 lines",
			len(ToolDescription), strings.Count(ToolDescription, "\n"))
	}
}
