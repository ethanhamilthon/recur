package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"recur/internal/agent"
	"recur/internal/repl"
	"recur/internal/selfupdate"
	"recur/internal/tui"
)

func Run(ctx context.Context, args []string, in io.Reader, out, diagnostics io.Writer) (err error) {
	command := ""
	if len(args) > 0 && (args[0] == "notify" || args[0] == "view") {
		command, args = args[0], args[1:]
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if len(args) > 0 && command == "" {
		switch args[0] {
		case "docs":
			return repl.Docs(out, args[1:])
		case "update":
			return selfupdate.Run(ctx, tui.Version, args[1:], out)
		case "version":
			fmt.Fprintln(out, strings.TrimPrefix(tui.Version, "v"))
			return nil
		}
	}
	flags := flag.NewFlagSet("recur", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	prompt := flags.String("prompt", "", "run one turn without TUI")
	mode := flags.String("mode", "", "headless transport: jsonl")
	sessionID := flags.String("s", "", "target session for notify/view (default: $RECUR_ENDPOINT)")
	resume := flags.String("resume", "", "resume a project session ID")
	system := flags.String("system", "", "system prompt file (overrides .recur/system.md)")
	prefix := flags.String("prefix", "", "prefix script file (overrides .recur/prefix.sh)")
	hooksFile := flags.String("hooks", "", "extra hooks file, added to home and .recur hooks.toml (headless runs use only this file)")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 && command == "" {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *mode != "" && *mode != "jsonl" {
		return fmt.Errorf("mode must be jsonl")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if command == "view" {
		return view(cwd, *sessionID, flags.Args())
	}
	if command == "notify" {
		text := strings.Join(flags.Args(), " ")
		if text == "" {
			data, e := io.ReadAll(io.LimitReader(in, 128*1024+1))
			if e != nil {
				return e
			}
			text = string(data)
		}
		return agent.Notify(cwd, *sessionID, text)
	}
	headless := *mode != "" || *prompt != ""
	files := agent.ConfigFiles{System: *system, Prefix: *prefix, Hooks: *hooksFile, Headless: headless}
	for _, f := range []*string{&files.System, &files.Prefix, &files.Hooks} {
		if *f == "" {
			continue
		}
		if *f, err = filepath.Abs(*f); err != nil {
			return err
		}
	}
	a, err := agent.NewWith(cwd, files, *resume)
	if err != nil {
		return err
	}
	defer a.Close()
	a.SetMode(map[bool]string{false: "tui", true: "headless"}[headless])
	a.Start(tui.Version)
	defer func() {
		code := 0
		if err != nil {
			code = 1
		}
		a.Finish(code)
	}()
	if !headless {
		return tui.Run(a, cwd)
	}
	jsonl := *mode == "jsonl"
	encoder := json.NewEncoder(out)
	var writeErr error
	send := func(value any) {
		if writeErr == nil {
			writeErr = encoder.Encode(value)
		}
	}
	// Without jsonl, stdout carries only the committed assistant answers.
	emit := func(e agent.Event) {
		switch {
		case jsonl:
			send(map[string]any{"type": e.Kind, "text": e.Text, "id": e.ID, "meta": e.Meta, "provisional": e.Provisional})
		case e.Kind == "assistant" && e.Meta != nil && e.Meta.RequestID != "":
			fmt.Fprintln(out, e.Text)
		case e.Kind == "error":
			fmt.Fprintln(diagnostics, e.Text)
		}
	}
	if jsonl {
		send(map[string]any{"type": "session", "session": a.SessionInfo()})
	}
	run := func(text string) error {
		err := a.Run(ctx, text, emit)
		if jsonl {
			send(map[string]any{"type": "done", "ok": err == nil, "session": a.SessionInfo()})
		}
		if writeErr != nil {
			return writeErr
		}
		return err
	}
	if writeErr != nil {
		return writeErr
	}
	if *prompt != "" {
		return run(*prompt)
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	lines := make(chan string)
	finished := make(chan error, 1)
	go func() {
		defer close(lines)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				finished <- ctx.Err()
				return
			}
		}
		finished <- scanner.Err()
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message := <-a.Inbox():
			a.Fire("notify_received", "RECUR_NOTIFY_FILE", message)
			if err = run(message); err != nil {
				return err
			}
		case line, ok := <-lines:
			if !ok {
				return <-finished
			}
			var request struct {
				Prompt string `json:"prompt"`
			}
			if err = json.Unmarshal([]byte(line), &request); err != nil {
				return fmt.Errorf("invalid JSONL input: %w", err)
			}
			if request.Prompt == "" {
				return fmt.Errorf("JSONL input requires a nonempty prompt")
			}
			if err = run(request.Prompt); err != nil {
				return err
			}
		}
	}
}

// view queues PNG/JPEG images for the session's next model request.
func view(cwd, id string, paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("usage: recur view PATH...")
	}
	endpoint, err := agent.Endpoint(cwd, id)
	if err != nil {
		return err
	}
	home := os.Getenv("RECUR_HOME")
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return err
		}
		home = filepath.Join(home, ".recur")
	}
	for _, path := range paths {
		image, err := repl.CaptureImageCopy(path, filepath.Join(home, "attachments"))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err = agent.Send(endpoint, agent.Message{Image: &image}); err != nil {
			_ = os.Remove(image.Path)
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}
