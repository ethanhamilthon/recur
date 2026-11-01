package repl

import (
	"embed"
	"fmt"
	"io"
	"strings"
	"time"
)

// ToolDescription is the runtime contract sent independently of editable prompts.
const ToolDescription = `Run a command in system Bash (/bin/bash --noprofile --norc). Each call starts a
fresh process in the project directory; variables, functions, aliases and working
directory changes do not persist between calls. External programs run from PATH
with the user's permissions.
Result: {output, exit_code}. Stdout and stderr are merged. max_output: bytes kept,
default 8192 (null); longer output keeps the first and last halves with a
truncation notice in the middle; 0 disables truncation (use sparingly). Prefer
filtering with rg, grep, head, tail, sed -n or wc. Commands execute according to
standard Bash syntax and semantics.
timeout: seconds for this call, default 120 (null), maximum 600. A timeout or
cancellation terminates the process group (exit_code 124 or 130). Detached or
long-running tasks must explicitly redirect output to files and should notify when
done; background tasks are not persistent session jobs.
There is no TTY and stdin is empty: never start interactive programs (editors,
pagers, prompts). PAGER and GIT_PAGER are cat. Shell startup files such as
~/.bashrc, ~/.zshrc and BASH_ENV are not read.
Search with rg or grep narrowed by -n, a path and a limit; read files with sed -n 'A,Bp'
or cat -n; create or overwrite files with a quoted heredoc (cat > path <<'EOF').
Recur commands (ordinary programs; also usable from scripts and background jobs):
- recur notify TEXT... (or text on stdin): queue a <notify> message for this session.
  When the agent is idle it starts a new turn; during a turn it arrives after the
  current tool outputs.
- recur view PATH...: queue PNG/JPEG previews (max 2048 px per side and 1 MiB) for the
  next model request; at most four waiting. Originals (max 32 MiB / 20 megapixels) are
  saved separately; their paths are included for cropping details with ordinary shell tools.
- recur docs [TOPIC]: print built-in Recur documentation; recur docs index lists topics.
Every call exports RECUR_SESSION_ID, RECUR_MODEL, RECUR_PROVIDER, RECUR_EFFORT,
RECUR_CTX_SIZE (latest request input tokens, empty if unknown) and RECUR_ENDPOINT
(session socket).
The shell is not an OS security sandbox; do not perform destructive actions unless requested.`

// ToolDescriptionFor generates the tool description embedding configured shell limits.
func ToolDescriptionFor(timeout, maxTimeout, maxOutput int) string {
	if timeout <= 0 {
		timeout = int(DefaultTimeout / time.Second)
	}
	if maxTimeout <= 0 {
		maxTimeout = int(MaxTimeout / time.Second)
	}
	outputDesc := fmt.Sprintf("default %d", maxOutput)
	if maxOutput < 0 {
		outputDesc = fmt.Sprintf("default %d", DefaultOutputLimit)
	} else if maxOutput == 0 {
		outputDesc = "default 0 (unlimited)"
	}
	if timeout == 120 && maxTimeout == 600 && maxOutput == DefaultOutputLimit {
		return ToolDescription
	}
	return fmt.Sprintf(`Run a command in system Bash (/bin/bash --noprofile --norc). Each call starts a
fresh process in the project directory; variables, functions, aliases and working
directory changes do not persist between calls. External programs run from PATH
with the user's permissions.
Result: {output, exit_code}. Stdout and stderr are merged. max_output: bytes kept,
%s (null); longer output keeps the first and last halves with a
truncation notice in the middle; 0 disables truncation (use sparingly). Prefer
filtering with rg, grep, head, tail, sed -n or wc. Commands execute according to
standard Bash syntax and semantics.
timeout: seconds for this call, default %d (null), maximum %d. A timeout or
cancellation terminates the process group (exit_code 124 or 130). Detached or
long-running tasks must explicitly redirect output to files and should notify when
done; background tasks are not persistent session jobs.
There is no TTY and stdin is empty: never start interactive programs (editors,
pagers, prompts). PAGER and GIT_PAGER are cat. Shell startup files such as
~/.bashrc, ~/.zshrc and BASH_ENV are not read.
Search with rg or grep narrowed by -n, a path and a limit; read files with sed -n 'A,Bp'
or cat -n; create or overwrite files with a quoted heredoc (cat > path <<'EOF').
Recur commands (ordinary programs; also usable from scripts and background jobs):
- recur notify TEXT... (or text on stdin): queue a <notify> message for this session.
  When the agent is idle it starts a new turn; during a turn it arrives after the
  current tool outputs.
- recur view PATH...: queue PNG/JPEG previews (max 2048 px per side and 1 MiB) for the
  next model request; at most four waiting. Originals (max 32 MiB / 20 megapixels) are
  saved separately; their paths are included for cropping details with ordinary shell tools.
- recur docs [TOPIC]: print built-in Recur documentation; recur docs index lists topics.
Every call exports RECUR_SESSION_ID, RECUR_MODEL, RECUR_PROVIDER, RECUR_EFFORT,
RECUR_CTX_SIZE (latest request input tokens, empty if unknown) and RECUR_ENDPOINT
(session socket).
The shell is not an OS security sandbox; do not perform destructive actions unless requested.`,
		outputDesc, timeout, maxTimeout)
}

//go:embed docs/*.md
var documentation embed.FS

// Docs prints a documentation topic for recur docs.
func Docs(out io.Writer, args []string) error {
	topic := "index"
	if len(args) > 1 {
		return fmt.Errorf("usage: docs [TOPIC]")
	} else if len(args) == 1 {
		topic = args[0]
	}
	data, err := documentation.ReadFile("docs/" + topic + ".md")
	if topic == "" || strings.ContainsAny(topic, "/\\.") || err != nil {
		return fmt.Errorf("unknown topic %q; run docs index", topic)
	}
	_, err = out.Write(data)
	return err
}
