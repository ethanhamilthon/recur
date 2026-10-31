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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"recur/internal/hooks"
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

type Agent struct {
	listener          net.Listener
	socketDir         string
	inbox             chan string
	views             chan repl.Image
	owner             *os.File
	settings          Settings
	REPL              *repl.REPL
	History           []json.RawMessage
	HistoryPath       string
	System            string
	LocalConfig       string // project .recur directory supplying config files, or ""
	LocalIgnored      bool   // project .recur files exist but enable_local_recur is off
	Sources           []Source
	client            openai.Client
	records           []Record
	title             string // set by /rename; "" means the first user message
	lastRequest       Metadata
	files             ConfigFiles
	hooks             *hooks.Runner
	lastTitle         string // last title reported by session_title_change
	home, sessionsDir string
}

func New(dir string, target ...string) (*Agent, error) {
	return NewWith(dir, ConfigFiles{}, target...)
}

// NewWith is New with explicit config files that override .recur lookup.
func NewWith(dir string, files ConfigFiles, target ...string) (*Agent, error) {
	config, err := configDir(dir)
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfiguration(config, dir, files)
	if err != nil {
		return nil, err
	}
	a := &Agent{files: files, home: config, System: cfg.system, LocalConfig: cfg.local, LocalIgnored: cfg.ignored, Sources: cfg.sources,
		hooks: &hooks.Runner{Log: filepath.Join(config, "hooks.log")}, REPL: &repl.REPL{Prefix: cfg.prefix, Dir: dir}}
	a.applySettings(cfg.settings)
	if err := a.openProject(dir, target...); err != nil {
		a.release()
		return nil, err
	}
	a.REPL.Env, a.REPL.Observe = a.shellEnv, a.Fire
	a.hooks.Set(cfg.hooks)
	return a, nil
}
func raw(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
func (a *Agent) message(role, text string) {
	a.History = append(a.History, raw(map[string]any{"role": role, "content": text}))
}
func (a *Agent) Save() error {
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
	if a.records == nil {
		a.records = a.Records()
	}
	forward := emit
	emit = func(e Event) {
		if e.Kind == "error" {
			a.Fire("error", "RECUR_ERROR", clip(e.Text))
		}
		a.records = ApplyEvent(a.records, e)
		forward(e)
	}
	turnStart := time.Now()
	source := "user"
	if len(input.Images) == 0 && strings.HasPrefix(input.Text, "<notify>") {
		source = "notify"
	}
	a.Fire("turn_start", "RECUR_TURN_SOURCE", source, "RECUR_MESSAGE_FILE", input.Display())
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
		status, problem := "ok", ""
		if err != nil {
			status, problem = "error", clip(err.Error())
			if errors.Is(err, context.Canceled) {
				status = "cancelled"
			}
		}
		a.Fire("turn_end", "RECUR_TURN_STATUS", status, "RECUR_TURN_DURATION_MS", ms(time.Since(turnStart)), "RECUR_ERROR", problem)
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
		for _, instruction := range instructions {
			a.Fire("steer_accepted", "RECUR_MESSAGE_FILE", instruction.Display())
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
	userAdded := false
	// Display the input before shell initialization so a slow prefix does not
	// look like a frozen UI. It joins API history only after the prefix runs;
	// a failed initialization withdraws it; the withdraw event carries the text
	// so the UI can return it to the composer for retry.
	shown := inputEvent(input, false)
	shown.ID = fmt.Sprintf("input-%d", time.Now().UnixNano())
	recordsBefore := append([]Record(nil), a.records...)
	emit(shown)
	initFailed := func(cause error) error {
		a.records = recordsBefore
		forward(Event{Kind: "withdraw", ID: shown.ID, Text: input.Text})
		return cause
	}

	var total Usage
	usageKnown := true
	for step := 0; a.settings.MaxAgentSteps <= 0 || step < a.settings.MaxAgentSteps; step++ {
		if ctx.Err() != nil {
			if !userAdded {
				return initFailed(ctx.Err())
			}
			return ctx.Err()
		}
		fresh, e := a.REPL.Ensure(ctx)
		if e != nil {
			if !userAdded {
				return initFailed(e)
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
			a.commitInput(input)
			userAdded = true
			a.titleChanged()
		}

		acceptSteering()
		if images := a.viewed(); len(images) > 0 {
			text := "<notify>\nImages queued by recur view." + imageOriginals(images) + "\n</notify>"
			content := []any{map[string]any{"type": "input_text", "text": text}}
			for _, img := range images {
				content = append(content, map[string]any{"type": "input_image", "image_url": img.URL, "detail": "auto"})
			}
			a.History = append(a.History, raw(map[string]any{"role": "user", "content": content}))
			emit(Event{Kind: "notice", Text: text})
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
					durableRecords = ApplyEvent(durableRecords, Event{Kind: r.Kind, Text: r.Text, ID: a.itemID(int64(i)), Final: true})
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
		a.records = baseRecords
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
				emit(Event{Kind: r.Kind, Text: r.Text, ID: a.itemID(int64(i)), Meta: &m, Final: true})
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
			result := a.runTool(ctx, call.Name, call.Arguments)
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

// runTool executes one function call; failures become an error result for the model.
func (a *Agent) runTool(ctx context.Context, name, arguments string) repl.Result {
	if ctx.Err() != nil {
		return repl.Result{Output: ctx.Err().Error(), Error: true}
	}
	if name != "bash" {
		return repl.Result{Output: "Unknown tool: " + name, Error: true}
	}
	var args struct {
		Command   string `json:"command"`
		Timeout   *int64 `json:"timeout"`
		MaxOutput *int64 `json:"max_output"`
	}
	timeout, limit := repl.DefaultTimeout, repl.DefaultOutputLimit
	err := json.Unmarshal([]byte(arguments), &args)
	if err == nil && args.Timeout != nil {
		if timeout = time.Duration(*args.Timeout) * time.Second; timeout <= 0 || timeout > repl.MaxTimeout {
			err = fmt.Errorf("timeout must be 1..600 seconds; run longer work as a top-level background job ending with recur notify")
		}
	}
	if err == nil && args.MaxOutput != nil {
		if limit = int(*args.MaxOutput); limit < 0 {
			err = fmt.Errorf("max_output must be >= 0 bytes (0 = no truncation)")
		}
	}
	if err == nil {
		var result repl.Result
		a.Fire("shell_start", "RECUR_COMMAND_FILE", args.Command, "RECUR_TIMEOUT", strconv.Itoa(int(timeout/time.Second)))
		started := time.Now()
		if result, err = a.REPL.ExecuteLimit(ctx, args.Command, timeout, limit); err == nil {
			a.Fire("shell_end", "RECUR_EXIT_CODE", strconv.Itoa(result.ExitCode), "RECUR_DURATION_MS", ms(time.Since(started)),
				"RECUR_TIMED_OUT", strconv.FormatBool(result.ExitCode == 124))
			return result
		}
	}
	return repl.Result{Output: err.Error(), ExitCode: 2, Error: true}
}

// itemID identifies a response item of the latest request in display events.
func (a *Agent) itemID(index int64) string {
	return fmt.Sprintf("%s-%d", a.lastRequest.RequestID, index)
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
	a.Fire("model_request_start")
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
				emit(Event{Kind: "code", Text: partialCode(arguments[event.OutputIndex]), Provisional: true, ID: a.itemID(event.OutputIndex), Final: true})
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
					emit(Event{Kind: "code", Text: partialCode(arguments[event.OutputIndex]), Provisional: true, ID: a.itemID(event.OutputIndex), Final: true})
					continue
				}
			}

			for _, record := range RecordsForItem(json.RawMessage(event.Item.RawJSON())) {
				emit(Event{Kind: record.Kind, Text: record.Text, ID: a.itemID(event.OutputIndex), Final: true, Provisional: event.Type == "response.output_item.added"})
			}
		case "response.output_text.delta":
			emit(Event{Kind: "delta", Text: event.Delta, ID: a.itemID(event.OutputIndex), Provisional: true})
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			emit(Event{Kind: "reasoning_delta", Text: event.Delta, ID: a.itemID(event.OutputIndex), Provisional: true})
		case "response.refusal.delta":
			emit(Event{Kind: "refusal_delta", Text: event.Delta, ID: a.itemID(event.OutputIndex), Provisional: true})
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
			usage := a.lastRequest.Usage
			if usage == nil {
				usage = &Usage{}
			}
			a.Fire("model_request_end", "RECUR_DURATION_MS", ms(a.lastRequest.Duration),
				"RECUR_INPUT_TOKENS", strconv.FormatInt(usage.Input, 10), "RECUR_OUTPUT_TOKENS", strconv.FormatInt(usage.Output, 10))
			completed = make([]json.RawMessage, len(event.Response.Output))
			for i, item := range event.Response.Output {
				completed[i] = json.RawMessage(item.RawJSON())
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
