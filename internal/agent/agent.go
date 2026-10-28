package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"recur/internal/repl"
)

var codeField = regexp.MustCompile(`"command"\s*:\s*"`)

// Decode only the complete prefix of a JSON string, withholding split escapes,
// UTF-8 characters and surrogate pairs until the next delta completes them.
func partialCode(arguments string) string {
	loc := codeField.FindStringIndex(arguments)
	if loc == nil {
		return ""
	}
	quoted := arguments[loc[1]-1:]
	var text string
	if json.NewDecoder(strings.NewReader(quoted)).Decode(&text) == nil {
		return text
	}
	for end := len(quoted); end > 0 && end >= len(quoted)-12; end-- {
		candidate := quoted[:end]
		if !utf8.ValidString(candidate) {
			continue
		}
		if json.Unmarshal([]byte(candidate+`"`), &text) == nil {
			if len(candidate) >= 6 {
				tail := strings.ToLower(candidate[len(candidate)-6:])
				if strings.HasPrefix(tail, `\ud`) && strings.ContainsAny(tail[3:4], "89ab") {
					slashes := 0
					for i := len(candidate) - 6; i >= 0 && candidate[i] == '\\'; i-- {
						slashes++
					}
					if slashes%2 == 1 {
						continue
					}
				}
			}
			return text
		}
	}
	return ""
}

type InitializationError struct{ Err error }

func (e *InitializationError) Error() string { return e.Err.Error() }
func (e *InitializationError) Unwrap() error { return e.Err }

type Agent struct {
	listener          net.Listener
	socketDir         string
	inbox             chan string
	views             chan repl.Image
	owner             *os.File
	ReadOnly          bool
	settings          Settings
	REPL              *repl.REPL
	History           []json.RawMessage
	HistoryPath       string
	System            string
	client            openai.Client
	records           []Record
	lastRequest       Metadata
	home, sessionsDir string
}

func New(dir string, target ...string) (*Agent, error) {
	config, err := configDir(dir)
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfiguration(config)
	if err != nil {
		return nil, err
	}
	a := &Agent{home: config, System: cfg.system, REPL: &repl.REPL{Prefix: cfg.prefix, Dir: dir}}
	a.applySettings(cfg.settings)
	if err := a.openProject(dir, target...); err != nil {
		a.release()
		return nil, err
	}

	a.REPL.Env = a.shellEnv
	return a, nil
}
func raw(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
func (a *Agent) message(role, text string) {
	a.History = append(a.History, raw(map[string]any{"role": role, "content": text}))
}
func (a *Agent) Save() error {
	if err := a.Writable(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.HistoryPath), 0700); err != nil {
		return err
	}
	data, err := a.sessionData()
	if err != nil {
		return err
	}
	return writePrivate(a.HistoryPath, data)
}
func (a *Agent) Run(ctx context.Context, text string, emit func(Event), steering ...*Steering) error {
	return a.RunInput(ctx, Input{Text: text}, emit, steering...)
}
func (a *Agent) RunInput(ctx context.Context, input Input, emit func(Event), steering ...*Steering) (err error) {
	if err = a.Writable(); err != nil {
		emit(Event{Kind: "error", Text: err.Error()})
		return err
	}
	if a.records == nil {
		a.records = a.Records()
	}
	forward := emit
	emit = func(e Event) { a.records = ApplyEvent(a.records, e); forward(e) }
	// A crash may leave calls without outputs. Their effects are unknown, so
	// never rerun them; supply an explicit interrupted result on continuation.
	for _, output := range interruptedOutputs(a.History) {
		a.History = append(a.History, output)
		for _, r := range RecordsForItem(output) {
			emit(Event{Kind: r.Kind, Text: r.Text, Final: true})
		}
	}
	defer func() {
		if err != nil && !errors.Is(err, context.Canceled) {
			emit(Event{Kind: "error", Text: err.Error()})
		}
		if a.HasSavedSession() {
			if e := a.Save(); e != nil {
				saveError := fmt.Errorf("could not save history: %w", e)
				err = errors.Join(err, saveError)
				emit(Event{Kind: "error", Text: saveError.Error()})
			}
		}
	}()
	// Only the turn goroutine mutates history. Steering waits until all outputs
	// for the current response have been committed, preserving call/output pairs.
	acceptSteering := func() bool {
		accepted := false
		if ctx.Err() != nil {
			return false
		}
		var instructions []Input
		if len(steering) > 0 {
			instructions = steering[0].TakeInputs()
		}
		for _, text := range a.Notifications() {
			instructions = append(instructions, Input{Text: text})
		}
		for _, instruction := range instructions {
			if strings.TrimSpace(instruction.Text) != "" || len(instruction.Images) > 0 {
				a.addInput(instruction, emit, true)
				accepted = true
			}
		}
		return accepted
	}
	turnStart := time.Now()
	userAdded := false

	var total Usage
	usageKnown := true
	for step := 0; a.settings.MaxAgentSteps <= 0 || step < a.settings.MaxAgentSteps; step++ {
		if ctx.Err() != nil {
			if !userAdded {
				return &InitializationError{ctx.Err()}
			}
			return ctx.Err()
		}
		fresh, e := a.REPL.Ensure(ctx)
		if e != nil {
			if !userAdded {
				return &InitializationError{e}
			}
			return e
		}
		if fresh {
			status := "Shell session initialized. The prefix has been executed."
			if len(a.History) > 0 {
				status = "Shell session restarted. Previous variables and functions are unavailable. The prefix has been executed again."
			}
			output := a.REPL.PrefixOutput
			if output == "" {
				output = "(no output)"
			}
			notice := "<notify>\n" + status + "\n\n<prefix_output>\n" + output + "\n</prefix_output>\n</notify>"
			a.message("user", notice)
			emit(Event{Kind: "notice", Text: notice})
		}
		if !userAdded {
			a.addInput(input, emit, false)
			userAdded = true
		}

		acceptSteering()
		if images := a.viewed(); len(images) > 0 {
			text := "<notify>\nImages queued by recur view.\n</notify>"
			content := []any{map[string]any{"type": "input_text", "text": text}}
			for _, img := range images {
				content = append(content, map[string]any{"type": "input_image", "image_url": img.URL, "detail": "auto"})
			}
			a.History = append(a.History, raw(map[string]any{"role": "user", "content": content}))
			emit(Event{Kind: "notice", Text: text})
		}
		if e := a.Save(); e != nil {
			return fmt.Errorf("could not save history: %w", e)
		}
		a.compactIfNeeded(ctx, emit)
		if e := a.Save(); e != nil {
			return fmt.Errorf("could not save history: %w", e)
		}
		baseHistory := append([]json.RawMessage(nil), a.History...)
		baseRecords := append([]Record(nil), a.records...)
		finished := map[int64]json.RawMessage{}
		var durableHistory []json.RawMessage
		var durableRecords []Record
		checkpoint := func(index int64, item json.RawMessage) error {
			finished[index] = append(json.RawMessage(nil), item...)
			indexes := make([]int, 0, len(finished))
			for i := range finished {
				indexes = append(indexes, int(i))
			}
			sort.Ints(indexes)
			durableHistory = append([]json.RawMessage(nil), baseHistory...)
			durableRecords = append([]Record(nil), baseRecords...)
			for _, i := range indexes {
				part := finished[int64(i)]
				durableHistory = append(durableHistory, part)
				for _, r := range RecordsForItem(part) {
					durableRecords = ApplyEvent(durableRecords, Event{Kind: r.Kind, Text: r.Text, ID: fmt.Sprintf("%s-%d", a.lastRequest.RequestID, i), Final: true})
				}
			}
			snapshot := *a
			snapshot.History, snapshot.records = durableHistory, durableRecords
			if err := snapshot.Save(); err != nil {
				return fmt.Errorf("could not save history: %w", err)
			}
			return nil
		}
		items, e := a.requestWithRetries(ctx, emit, checkpoint)
		if e != nil {
			if durableHistory != nil {
				a.History, a.records = durableHistory, durableRecords
			} else {
				a.records = baseRecords
			}
			return e
		}
		// Discard streaming-only entries from the persistent journal. The UI
		// continues showing them until the completed records reconcile them.
		if durableRecords != nil {
			a.records = durableRecords
		} else {
			a.records = baseRecords
		}
		meta := a.lastRequest
		if meta.Usage == nil {
			usageKnown = false
		} else {
			total.Input += meta.Usage.Input
			total.Output += meta.Usage.Output
			if total.Cached < 0 || meta.Usage.Cached < 0 {
				total.Cached = -1
			} else {
				total.Cached += meta.Usage.Cached
			}
		}
		calls := 0
		// Commit and display all response items before executing their tool calls.
		for i, item := range items {
			a.History = append(a.History, item)
			for _, r := range RecordsForItem(item) {
				m := meta
				if r.Kind == "assistant" {
					m.Duration = time.Since(turnStart)
					if usageKnown {
						u := total
						m.Usage = &u
					} else {
						m.Usage = nil
					}
				}
				emit(Event{Kind: r.Kind, Text: r.Text, ID: fmt.Sprintf("%s-%d", meta.RequestID, i), Meta: &m, Final: true})
			}
		}
		if e := a.Save(); e != nil {
			return fmt.Errorf("could not save history: %w", e)
		}
		for _, item := range items {
			var call struct {
				Type, Name, Arguments string
				CallID                string `json:"call_id"`
			}
			if e = json.Unmarshal(item, &call); e != nil {
				return e
			}
			if call.Type != "function_call" {
				continue
			}
			calls++
			started := time.Now()
			var result any
			if ctx.Err() != nil {
				result = repl.Result{Output: ctx.Err().Error(), Error: true}
			} else {
				switch call.Name {
				case "bash":
					var args struct {
						Command   string `json:"command"`
						Timeout   *int64 `json:"timeout"`
						MaxOutput *int64 `json:"max_output"`
					}
					var value repl.Result
					timeout := repl.DefaultTimeout
					if e = json.Unmarshal([]byte(call.Arguments), &args); e == nil && args.Timeout != nil {
						timeout = time.Duration(*args.Timeout) * time.Second
						if timeout <= 0 || timeout > repl.MaxTimeout {
							e = fmt.Errorf("timeout must be 1..600 seconds; run longer work as a top-level background job ending with recur notify")
						}
					}
					limit := repl.DefaultOutputLimit
					if e == nil && args.MaxOutput != nil {
						if limit = int(*args.MaxOutput); limit < 0 {
							e = fmt.Errorf("max_output must be >= 0 bytes (0 = no truncation)")
						}
					}
					if e == nil {
						value, e = a.REPL.ExecuteLimit(ctx, args.Command, timeout, limit)
					}
					if e != nil {
						value = repl.Result{Output: e.Error(), ExitCode: 2, Error: true}
					}
					result = value
				default:
					result = repl.Result{Output: "Unknown tool: " + call.Name, Error: true}
				}
			}
			output := raw(map[string]any{"type": "function_call_output", "call_id": call.CallID, "output": string(raw(result))})
			a.History = append(a.History, output)
			for _, r := range RecordsForItem(output) {
				emit(Event{Kind: r.Kind, Text: r.Text, Meta: &Metadata{Duration: time.Since(started)}, Final: true})
			}
			if e := a.Save(); e != nil {
				return fmt.Errorf("could not save history: %w", e)
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if steered := acceptSteering(); calls == 0 && !steered {
			return nil
		} else if steered {
			if e := a.Save(); e != nil {
				return fmt.Errorf("could not save history: %w", e)
			}
		}
	}
	return fmt.Errorf("stopped after %d agent steps; raise max_agent_steps in settings.toml", a.settings.MaxAgentSteps)
}
func (a *Agent) request(ctx context.Context, emit func(Event), checkpoints ...func(int64, json.RawMessage) error) ([]json.RawMessage, error) {
	if strings.TrimSpace(a.settings.Model) == "" || strings.TrimSpace(a.settings.Provider) == "" {
		return nil, fmt.Errorf("no provider or model selected: use /login to add a provider, then choose a model with /model")
	}
	prompt := a.instructions()
	// Keep the on-disk protocol items verbatim while using SDK request types.
	input := make(responses.ResponseInputParam, len(a.History))
	for i, item := range a.History {
		if !json.Valid(item) {
			return nil, fmt.Errorf("invalid history item %d", i)
		}
		input[i] = param.Override[responses.ResponseInputItemUnionParam](item)
	}
	payload := responses.ResponseNewParams{
		Model:        a.settings.Model,
		Instructions: openai.String(prompt),
		Input:        responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Store:        openai.Bool(false),
		Tools: []responses.ToolUnionParam{{OfFunction: &responses.FunctionToolParam{
			Name:        "bash",
			Description: openai.String(repl.ToolDescription),
			Parameters:  bashParameters,
			Strict:      openai.Bool(true),
		}}},
	}
	if a.settings.Effort != "" {
		payload.Reasoning.Effort = shared.ReasoningEffort(a.settings.Effort)
	}
	if a.settings.ReasoningSummary != "off" {
		payload.Reasoning.Summary = shared.ReasoningSummaryAuto
	}
	started := time.Now()
	a.lastRequest = Metadata{RequestID: fmt.Sprintf("r-%d", len(a.History))}
	stream := a.client.Responses.NewStreaming(ctx, payload)
	defer stream.Close()
	var completed []json.RawMessage
	arguments := map[int64]string{}
	names := map[int64]string{}
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.function_call_arguments.delta", "response.function_call_arguments.done":
			if event.Type == "response.function_call_arguments.delta" {
				arguments[event.OutputIndex] += event.Delta
			} else {
				arguments[event.OutputIndex] = event.Arguments
			}
			if names[event.OutputIndex] == "bash" {
				emit(Event{Kind: "code", Text: partialCode(arguments[event.OutputIndex]), Provisional: true, ID: fmt.Sprintf("%s-%d", a.lastRequest.RequestID, event.OutputIndex), Final: true})
			}
		case "response.output_item.added", "response.output_item.done":
			if event.Type == "response.output_item.done" && len(checkpoints) > 0 && checkpoints[0] != nil {
				if err := checkpoints[0](event.OutputIndex, json.RawMessage(event.Item.RawJSON())); err != nil {
					return nil, err
				}
			}

			// Some providers deliver complete message.content without text deltas.
			// Show it as soon as available, not only at response.completed.
			if event.Item.Type == "function_call" {
				names[event.OutputIndex] = event.Item.Name
				if event.Item.Arguments.OfString != "" {
					arguments[event.OutputIndex] = event.Item.Arguments.OfString
				}
				if event.Item.Name == "bash" {
					emit(Event{Kind: "code", Text: partialCode(arguments[event.OutputIndex]), Provisional: true, ID: fmt.Sprintf("%s-%d", a.lastRequest.RequestID, event.OutputIndex), Final: true})
					continue
				}
			}

			for _, record := range RecordsForItem(json.RawMessage(event.Item.RawJSON())) {
				emit(Event{Kind: record.Kind, Text: record.Text, ID: fmt.Sprintf("%s-%d", a.lastRequest.RequestID, event.OutputIndex), Final: true, Provisional: event.Type == "response.output_item.added"})
			}
		case "response.output_text.delta":
			emit(Event{Kind: "delta", Text: event.Delta, ID: fmt.Sprintf("%s-%d", a.lastRequest.RequestID, event.OutputIndex), Provisional: true})
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			emit(Event{Kind: "reasoning_delta", Text: event.Delta, ID: fmt.Sprintf("%s-%d", a.lastRequest.RequestID, event.OutputIndex), Provisional: true})
		case "response.refusal.delta":
			emit(Event{Kind: "refusal_delta", Text: event.Delta, ID: fmt.Sprintf("%s-%d", a.lastRequest.RequestID, event.OutputIndex), Provisional: true})
		case "response.completed":
			a.lastRequest.Duration = time.Since(started)
			if event.Response.JSON.Usage.Valid() {
				n := event.Response.Usage.InputTokens
				a.lastRequest.ContextInput = &n
				cached := int64(-1)
				if event.Response.Usage.InputTokensDetails.JSON.CachedTokens.Valid() {
					cached = event.Response.Usage.InputTokensDetails.CachedTokens
				}
				a.lastRequest.Usage = &Usage{Input: event.Response.Usage.InputTokens, Output: event.Response.Usage.OutputTokens, Cached: cached}
			}
			completed = make([]json.RawMessage, len(event.Response.Output))
			for i, item := range event.Response.Output {
				completed[i] = json.RawMessage(item.RawJSON())
				if len(checkpoints) > 0 && checkpoints[0] != nil {
					if err := checkpoints[0](int64(i), completed[i]); err != nil {
						return nil, err
					}
				}
			}
		case "response.failed", "response.incomplete", "error":
			return nil, fmt.Errorf("provider stream: %s", event.RawJSON())
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	if completed == nil {
		return nil, fmt.Errorf("provider stream ended without a completed response")
	}
	return completed, nil
}
