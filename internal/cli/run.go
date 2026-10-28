package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"recur/internal/agent"
	"recur/internal/selfupdate"
	"recur/internal/tui"
)

func Run(ctx context.Context, args []string, in io.Reader, out, diagnostics io.Writer) error {
	notify := len(args) > 0 && args[0] == "notify"
	if notify {
		args = args[1:]
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if len(args) > 0 && !notify {
		switch args[0] {
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
	mode := flags.String("mode", "", "headless transport: stdio or jsonl")
	sessionID := flags.String("s", "", "target session for notify")
	resume := flags.String("resume", "", "resume a project session ID")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 && !notify {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *mode != "" && *mode != "stdio" && *mode != "jsonl" {
		return fmt.Errorf("mode must be stdio or jsonl")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if notify {
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
	a, err := agent.New(cwd, *resume)
	if err != nil {
		return err
	}
	defer a.Close()
	if !headless {
		return tui.Run(a, cwd)
	}
	encoder := json.NewEncoder(out)
	emit := tui.NewEventPrinter(out, diagnostics)
	var writeErr error
	send := func(value any) {
		if writeErr == nil {
			writeErr = encoder.Encode(value)
		}
	}
	if *mode == "jsonl" {
		send(map[string]any{"type": "session", "session": a.SessionInfo(), "read_only": a.ReadOnly})
		emit = func(e agent.Event) {
			send(map[string]any{"type": e.Kind, "text": e.Text, "id": e.ID, "meta": e.Meta, "provisional": e.Provisional})
		}
	}
	if a.ReadOnly {
		fmt.Fprintln(diagnostics, "Read-only session; input is not submitted.")
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		previous := ""
		for {
			data, _ := json.Marshal(a.Records())
			snapshot := string(data)
			if snapshot != previous {
				previous = snapshot
				if *mode == "jsonl" {
					send(map[string]any{"type": "snapshot", "records": a.Records(), "session": a.SessionInfo()})
				} else {
					for _, record := range a.Records() {
						fmt.Fprintln(out, record.Text)
					}
				}
			}
			if writeErr != nil {
				return writeErr
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
			if err = a.Refresh(); err != nil {
				fmt.Fprintln(diagnostics, err)
			}
		}
	}
	run := func(text string) error {
		err := a.Run(ctx, text, emit)
		if *mode == "jsonl" {
			send(map[string]any{"type": "done", "ok": err == nil, "session": a.SessionInfo()})
		} else {
			fmt.Fprintln(out)
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
		var text string
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message := <-a.Inbox():
			if err = run(message); err != nil {
				return err
			}
			continue
		case line, ok := <-lines:
			if !ok {
				return <-finished
			}
			text = line
		}
		if *mode == "jsonl" {
			var request struct {
				Prompt string `json:"prompt"`
			}
			if err = json.Unmarshal([]byte(text), &request); err != nil {
				return fmt.Errorf("invalid JSONL input: %w", err)
			}
			text = request.Prompt
			if text == "" {
				return fmt.Errorf("JSONL input requires a nonempty prompt")
			}
		}
		if text == "" {
			continue
		}
		if err = run(text); err != nil {
			return err
		}
	}
}
