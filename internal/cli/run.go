package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"recur/internal/agent"
	"recur/internal/docs"
	"recur/internal/selfupdate"
	"recur/internal/shell"
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
			return docs.Docs(out, args[1:])
		case "update":
			return selfupdate.Run(ctx, tui.Version, args[1:], out)
		case "version":
			fmt.Fprintln(out, strings.TrimPrefix(tui.Version, "v"))
			return nil
		}
	}
	flags := flag.NewFlagSet("recur", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	mode := flags.String("mode", "", "headless JSON/stdio transport: jsonl")
	sessionID := flags.String("s", "", "target session for notify/view (default: $RECUR_ENDPOINT)")
	resume := flags.String("resume", "", "resume a project session ID")
	configPath := flags.String("config", "", "configuration file (overrides recur.toml and $RECUR_HOME/recur.toml)")
	flags.StringVar(configPath, "c", "", "alias for --config")
	noExtend := flags.Bool("no-extend", false, "skip inheriting from global configuration")
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
	headless := *mode != ""
	files := agent.ConfigFiles{Config: *configPath, NoExtend: *noExtend, Headless: headless}
	if files.Config != "" {
		if files.Config, err = filepath.Abs(files.Config); err != nil {
			return err
		}
	}
	a, err := agent.New(cwd, files, *resume)
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
	return serveJSON(ctx, a, cwd, in, out, diagnostics)
}

// view queues PNG/JPEG images for the session's next model request.
func view(cwd, id string, paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("usage: recur view PATH [PATH ...]")
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
		image, err := shell.CaptureImageCopy(path, filepath.Join(home, "attachments"))
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
