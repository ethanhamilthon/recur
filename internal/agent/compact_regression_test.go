package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAutomaticCompactionJournalSurvivesRunAndResume(t *testing.T) {
	requests := 0
	a, closeAll := summaryAgent(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if _, streaming := payload["stream"]; !streaming {
			writeSummary(w, "condensed")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"new answer"}]}]}}`+"\n\n")
	})
	defer closeAll()
	a.History = compactHistory(t, 5)
	a.REPL.Prefix = ""
	a.settings.CompactKeepTurns = 2
	if err := a.Run(context.Background(), "new question", func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("got %d requests, want summary and turn", requests)
	}
	want := a.Records()
	for _, text := range []string{"condensed", "question 4", "answer 4", "new question", "new answer"} {
		found := false
		for _, record := range want {
			found = found || strings.Contains(record.Text, text)
		}
		if !found {
			t.Errorf("compacted journal lost %q: %+v", text, want)
		}
	}
	id := strings.TrimSuffix(filepath.Base(a.HistoryPath), ".jsonl")
	if err := a.NewSession(); err != nil {
		t.Fatal(err)
	}
	if err := a.Resume(id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Records(), want) {
		t.Fatal("resume changed the compacted journal")
	}
}

func TestCompactSaveFailureRestoresHistoryAndJournal(t *testing.T) {
	a, closeAll := summaryAgent(t, func(w http.ResponseWriter, r *http.Request) {
		writeSummary(w, "condensed")
	})
	defer closeAll()
	a.History = compactHistory(t, 5)
	a.records = append(a.Records(), Record{Kind: "error", Text: "keep diagnostic"})
	history, records := a.History, a.Records()
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	path := a.HistoryPath
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A directory cannot be replaced by the atomic session-file rename.
	a.HistoryPath = t.TempDir()
	_, err = a.Compact(context.Background(), "")
	a.HistoryPath = path
	if err == nil {
		t.Fatal("expected save failure")
	}
	if !reflect.DeepEqual(a.History, history) || !reflect.DeepEqual(a.Records(), records) {
		t.Fatal("save failure changed the in-memory session")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(saved) {
		t.Fatalf("save failure changed the committed session: %v", err)
	}
}

func TestCompactReadOnlyDoesNotRequestSummary(t *testing.T) {
	a, closeAll := summaryAgent(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("read-only compaction must not request a summary")
	})
	defer closeAll()
	a.History = compactHistory(t, 5)
	a.ReadOnly = true
	if _, err := a.Compact(context.Background(), ""); err == nil {
		t.Fatal("read-only compaction succeeded")
	}
	if len(a.History) != 20 {
		t.Fatal("read-only compaction changed history")
	}
}

func TestEstimateTokensCountsEachImageNotTextMentions(t *testing.T) {
	image := map[string]string{"type": "input_image", "image_url": "data:image/png;base64,AAAA"}
	estimate := func(parts ...any) int64 {
		return estimateTokens([]json.RawMessage{raw(map[string]any{"role": "user", "content": parts})})
	}
	if delta := estimate(image, image) - estimate(image); delta < imageTokens || delta > imageTokens+100 {
		t.Fatalf("second image added %d tokens, want about %d", delta, imageTokens)
	}
	if got := estimate(map[string]string{"type": "input_text", "text": "input_image"}); got >= imageTokens {
		t.Fatalf("text mention counted as an image: %d", got)
	}
}

func TestTurnStartIgnoresServiceMessages(t *testing.T) {
	history := compactHistory(t, 3)
	history = append(history, raw(map[string]any{"role": "user", "content": "<notify>prefix restarted</notify>"}),
		raw(map[string]any{"role": "user", "content": []any{
			map[string]string{"type": "input_text", "text": "<notify>Images queued by view().</notify>"},
			map[string]string{"type": "input_image", "image_url": "data:image/png;base64,AAAA"},
		}}))
	if got := turnStart(history, 2); got != 4 {
		t.Fatalf("turnStart = %d, want second user question at 4", got)
	}
	for _, item := range []json.RawMessage{
		raw(map[string]any{"role": "user", "content": "<compact_summary>summary</compact_summary>"}),
		raw(map[string]any{"type": "function_call_output", "output": "ok"}),
	} {
		if userTurn(item) {
			t.Fatalf("service item counted as user turn: %s", item)
		}
	}
	imageInput := raw(map[string]any{"role": "user", "content": []any{
		map[string]string{"type": "input_text", "text": "[Image: screenshot.png]"},
		map[string]string{"type": "input_image", "image_url": "data:image/png;base64,AAAA"},
	}})
	if !userTurn(imageInput) {
		t.Fatal("user image attachment did not count as a turn")
	}
}
