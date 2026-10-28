package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func configureTestAgent(t *testing.T, a *Agent, s Settings) {
	t.Helper()
	a.applySettings(s)
	if a.home == "" {
		return
	}
	data, err := toml.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a.home, "settings.toml"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSessionSelectionDefaultsAndRestoration(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Save(); err != nil {
		t.Fatal(err)
	}
	first := strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl")
	original := a.Settings()
	if err = a.NewSession(); err != nil {
		t.Fatal(err)
	}
	if err = a.Select(original.Provider, "new-model", "max"); err != nil {
		t.Fatal(err)
	}
	if err = a.Save(); err != nil {
		t.Fatal(err)
	}
	selected := strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl")
	if err = a.Resume(first); err != nil {
		t.Fatal(err)
	}
	if a.Settings().Model != original.Model || a.Settings().Effort != original.Effort {
		t.Fatal("old session adopted new defaults")
	}
	if err = a.NewSession(); err != nil {
		t.Fatal(err)
	}
	if a.Settings().Model != "new-model" || a.Settings().Effort != "max" {
		t.Fatal("new session did not use new defaults")
	}
	if err = a.Resume(selected); err != nil {
		t.Fatal(err)
	}
	if a.Settings().Model != "new-model" {
		t.Fatal("session model not restored")
	}
	if err = a.Select(original.Provider, "bad", "unsupported"); err == nil {
		t.Fatal("invalid effort accepted")
	}
	if a.Settings().Model != "new-model" {
		t.Fatal("failed selection mutated settings")
	}
}

func TestSessionGlobalAndContextMatchesRecords(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.REPL.Prefix = "print(session.model)"
	if _, err = a.REPL.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.REPL.PrefixOutput, a.Settings().Model) {
		t.Fatal("session unavailable in prefix")
	}
	n := int64(36679)
	a.records = []Record{{Kind: "assistant", Meta: &Metadata{RequestID: "r-1", ContextInput: &n}}}
	result, err := a.REPL.Execute(context.Background(), "print(session.ctx_size)\nprint(session.cwd)\nprint(session.id)")
	if err != nil || result.Error || !strings.Contains(result.Output, "36679") || !strings.Contains(result.Output, a.REPL.Dir) {
		t.Fatalf("metadata: %+v %v", result, err)
	}
	result, err = a.REPL.Execute(context.Background(), "session.model = 'fake'")
	if err != nil || !result.Error {
		t.Fatalf("metadata should be immutable: %+v %v", result, err)
	}
	a.records = append(a.records, Record{Kind: "assistant", Meta: &Metadata{RequestID: "r-2"}})
	result, err = a.REPL.Execute(context.Background(), "session.ctx_size")
	if err != nil || result.Error || result.Output != "" {
		t.Fatalf("unknown context must be None: %+v %v", result, err)
	}
	a.REPL.Close()
	if _, err = a.REPL.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestModelListUsesSelectedProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer selected" {
			t.Error("wrong endpoint or credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"z"},{"id":"a"}]}`)
	}))
	defer server.Close()
	a := &Agent{}
	a.applySettings(Settings{Provider: "p", Model: "m", Providers: map[string]Provider{"p": {BaseURL: server.URL, APIKey: "selected"}}})
	models, errs := a.AllModels(context.Background())
	if len(errs) != 0 || len(models) != 2 || models[0].Model != "a" || models[1].Model != "z" {
		t.Fatalf("%v %v", models, errs)
	}
}

func TestLegacyHeaderPinsCurrentDefaultsAndCorruptSelectionPreservesState(t *testing.T) {
	t.Setenv("RECUR_HOME", t.TempDir())
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(a.HistoryPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	var header map[string]any
	if err = json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatal(err)
	}
	delete(header, "selection")
	lines[0] = string(raw(header))
	if err = os.WriteFile(a.HistoryPath, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	if err = a.readSession(); err != nil {
		t.Fatal(err)
	}
	if err = a.Save(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(a.HistoryPath)
	if err != nil || !strings.Contains(string(data), `"selection"`) {
		t.Fatal("legacy defaults not pinned")
	}
	previous := a.Settings()
	header["selection"] = Selection{Provider: "missing", Model: "bad"}
	lines[0] = string(raw(header))
	if err = os.WriteFile(a.HistoryPath, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	if err = a.readSession(); err == nil {
		t.Fatal("unavailable provider accepted")
	}
	if a.Settings().Provider != previous.Provider {
		t.Fatal("failed read mutated selection")
	}
}
