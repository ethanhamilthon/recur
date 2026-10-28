# recur

A terminal coding agent built in Go, with a persistent **Monty** Python REPL. No CPython installation or `python3` executable is required. The official OpenAI Go SDK handles Responses and Chat Completions requests and streaming.

Providers use the Responses protocol and are configured in `~/.recur/settings.toml`:

```toml
provider = "clipd"
model = "my-model"
effort = "high"

[providers.clipd]
base_url = "http://localhost:8317/v1"
api_key = "your-key"

[providers.other]
base_url = "https://example.com/v1"
api_key = "another-key"
```

`provider` selects one of the configured providers. `effort` accepts `none`, `minimal`, `low`, `medium`, `high`, or `xhigh` (support depends on the provider/model); an empty string omits the effort field (reasoning summaries are configured separately). Missing settings are created with defaults; existing files are never overwritten. There is no default provider or model: on first start `provider` and `model` are empty, the start screen tells you to run `/login` to add a provider and then `/model` to choose a model, and messages are not sent until both are set. The current version is in the `VERSION` file and is shown on the start screen. Settings no longer come from `~/.meno/provider.json`.

Set `RECUR_HOME=/path/to/.recur` to use that directory **directly** for settings, prompts, and history. The default is `~/.recur`; relative paths are resolved from the working directory. This does not change the project working directory. `/new` reloads settings and prompts. Configuration may contain API keys; keep it private.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/ethanhamilthon/recur/main/scripts/install.sh | sh
```

The script downloads the latest GitHub release for the current OS and architecture, verifies it against `checksums.txt`, and installs `recur` into `$RECUR_BINDIR` (default: `~/.local/bin`). Set `RECUR_VERSION=v0.3.0` to pin a release.

## Update

```sh
recur update          # download and replace the running binary
recur update -check   # only report the latest release
recur version
```

The start screen checks GitHub once every six hours in the background and shows `vX.Y.Z available — run recur update` when a newer release exists. Set `RECUR_NO_UPDATE_CHECK=1` to disable that check; the cache lives in `$RECUR_HOME/update.json`.

## Run

```sh
CGO_ENABLED=0 go build -o recur .
./recur
```

Build requires Go 1.26+ (the release workflow uses the `go` directive in `go.mod`). Monty is pinned through `github.com/ewhauser/gomonty v0.0.14` (upstream Monty commit `c9802b5f30d11fecf9f153feb1dfdab3abda070e`). Normal builds use bundled native libraries through purego, without Rust, CPython or cgo. Run from the project directory the agent should work in. A single non-interactive turn is also supported:

```sh
./recur -prompt 'Inspect this project'
```

## Commands

Type `/` to open command completion. Use **Up/Down** to choose, **Tab** to complete, and **Enter** to execute the selected item immediately. Slash-prefixed input without a matching visible completion (for example `/tmp/file` or `/help me with this`) is ordinary message text, not an unknown-command error. **Esc** dismisses completion so a literal command name can also be sent as text.

| Command | Action |
| --- | --- |
| `/new` | Start a fresh chat and interpreter; reload prompt files |
| `/help` | Show commands, shortcuts and prompt paths |
| `/quit` | Save the chat and exit |
| `/cancel` | Cancel the current turn |
| `/resume` | Complete/select a saved session in the current project |
| `/compact [prompt]` | Summarize everything but the last `compact_keep_turns` turns; an optional prompt guides the summary |
| `/reload` | Re-read settings, prompt files and the palette, then restart the interpreter |

`/new`, `/resume <id>`, and `/quit` also work during a turn: they cancel it first and wait for history to be saved before continuing.

**Enter** sends a message; **Shift+Enter** inserts a newline in terminals supporting the Kitty keyboard protocol, CSI-u, or xterm modified-key sequences. **Alt+Enter / Ctrl+J** also inserts a newline and works as a fallback when the terminal cannot distinguish Shift+Enter from Enter. **Esc / Ctrl+C** cancels a running turn. **Ctrl+C**, or **Ctrl+D** on empty input, exits when idle. **Dragging with the left mouse button** over the timeline highlights text and copies it to the system clipboard on release; the wheel still scrolls.

The input lives at the end of the timeline, not in a fixed bottom panel. **PgUp/PgDn**, **Ctrl+Up/Down**, and the **mouse wheel** scroll the timeline, including the input. **Ctrl+Home/End** jumps to the beginning/end. Streaming follows the end only when you have not scrolled away. Typing brings the input back into view. Sent user messages use a left rail without a background. The input, agent messages, tool calls, and tool results use the terminal background.

## Prompt files

Defaults are separate source files, embedded into the binary:

- `prompts/system.md` — agent instructions
- `prompts/prefix.py` — interpreter initialization code

On first run, editable copies are created in `~/.recur/system.md` and `~/.recur/prefix.py` (or the explicit `RECUR_HOME`). Existing copies are never overwritten. Edit those copies and use `/new` (or restart) to reload them.

The default system prompt is exactly `You are Recur, a coding agent`. Final system instructions contain **system prompt + prefix source**, never the executed prefix output. The Python environment contract, host functions, usage and limits are supplied in the `python_repl` tool description, not an additional system runtime block. Before the first request, a separate service-generated user notification contains the captured output, followed by the actual user message as a separate item. Empty output is marked explicitly. After REPL state loss, the prefix executes again and a new notification supplies its output before work continues. Ordinary turns do not rerun it. A failed/cancelled initialization does not send the user's request; the TUI returns it to the composer for retry.

## Timeline, usage and context

Assistant replies render Markdown. The timeline includes intermediate assistant messages, available reasoning text/summaries, tool calls, results, refusals and final replies. Hidden/encrypted reasoning is not exposed by the API and is not invented by the UI. Providers may omit reasoning, usage or timing data. Completed-item snapshots repair missing deltas; partial text remains visible and is saved if a request fails. See [the implementation audit](.report/verification.md) for coverage and limitations.

User messages have the same left rail as the composer, without a role heading or background. Other entries use compact markers:

- `*  ↑2.1k ↓120 · 1.5s · cache 29%` — assistant message
- `>_  ↑1.0k ↓100 · 250ms · cache 20%` — REPL call (API name: `python_repl`)
- `<  12ms` — tool result
- `~` — muted reasoning/summary block

Assistant metadata summarizes requests up to that message **within the current turn**. The final reply includes the entire turn, counting each request once even if it emitted several tool calls. REPL calls show their originating request's duration and usage; results show execution duration. Cache is cached input tokens divided by input tokens, rounded to a whole percent. Unavailable or undefined cache shows `cache —`; other missing values show `?`, not an invented zero.

Reasoning summaries are explicitly requested by default (`reasoning_summary = "auto"` in settings). Set `reasoning_summary = "off"` for providers/models that reject this option, or models without reasoning support. Transient transport failures (`408`, `409`, `429`, `5xx`, timeouts, dropped connections) are retried up to three attempts with a visible `Retrying in Ns` notice, honoring `Retry-After`. Retries happen only before any output reaches the screen, so partial text is never duplicated; other errors are reported without an automatic retry. No summary block is invented when the provider supplies no text, and encrypted reasoning is never rendered.

The single-line bar, separated from the composer by one empty row, shows `provider · model/effort · ctx ~N` on the left and the project path on the right. **`ctx ~N` is the latest request's reported input-token count**, including cached input, used as an approximation of the current context footprint. It is not session token spend, a context-window percentage, or a known remaining capacity. It becomes stale as results/new text are added and updates on the next reported request. Context counts use compact uppercase units, for example `ctx ~37K` for 36,679 tokens and `ctx ~1.2M` for 1,200,000. Missing usage shows `ctx ?`. Narrow terminals truncate the bar without wrapping.

Completed entries are cached; streaming updates only reformat the changing entry. Resizing invalidates width-dependent caches. Input sizing is recalculated only when text or dimensions change. The viewport still assembles the full timeline; no virtualization layer is needed for the measured 1,000-message case.

## Interpreter and host access

Variables and functions persist across tool calls **and user turns**. Restarting the app, `/new`, `/resume`, a worker crash, or cancelling active Python execution loses interpreter state. A separate user message in `<notify>` tells the model when previous variables are unavailable.

Monty supports a restricted Python subset, not all of CPython. Functions, lists, dicts, sets, tuples, ordinary builtins, `print()` and f-strings are supported. The only importable modules are `json`, `math`, `re`, `os`, `sys`, `pathlib`, `datetime`, `typing` and `asyncio`; everything else (for example `collections`, `dataclasses`, `enum`, `itertools`, `random`, `time`, `io`) raises `ModuleNotFoundError`, and there are no third-party packages. Those modules are partial: OS-backed calls such as `Path.exists()`, `os.environ` and `datetime.now()` raise `NotImplementedError` because Recur provides no OS handler, so use `sh`, `read` and `write` instead. `class` definitions, `del`, `@`, `input()`, `open()` and `print(..., file=)` are unsupported; importing a module never grants host access. Unsupported syntax and operations return errors. Monty's `repr` is not always CPython-identical (a one-element tuple can display as `(1)` instead of `(1,)`).

Tool results include captured print output and Monty's `repr` of the final top-level expression unless it is `None`; `_` retains the last non-None value. Ordinary runtime exceptions preserve state, including earlier assignments. Syntax errors execute nothing. The prefix executes once per new interpreter, with print output but without implicit expression display.

Each Python execution (including prefix initialization) has its own two-minute timeout, enforced by the parent worker controller; idle time and API waits do not consume it. Native limits cap interpreter heap at 128 MiB, allocations at 10,000,000 and recursion depth at 1,000. These are interpreter limits, not an OS-wide memory sandbox. Output capture is capped at 16 KiB (preserving the first 8 KiB and last 8 KiB with a middle truncation warning; slice or filter large text in Python before printing). The native VM stays in a child copy of the Go executable; cancellation kills its entire process group, so a tight native loop or a blocked shell cannot hang the TUI. The service protocol uses a separate descriptor, never stdout.

### Shell access through Python

```python
result = sh("ls -la")
print(result["stdout"])
print(result["stderr"])
result["returncode"]
```

`sh(command)` remains available for processing shell output in Python. It uses **`/bin/sh -c`**, and returns `stdout`, `stderr` and `returncode`. Nonzero status is data rather than a Python exception. Each stream is capped at 16 KiB; the containing Python execution's timeout/cancellation applies. External programs must be installed separately.

**Shell access is not an OS security sandbox.** Commands have your user's filesystem, environment and network permissions. Only run trusted tasks. Histories can contain sensitive code and command output.

### File helpers, images, and built-in documentation

The Python environment exposes `read(path, offset=1, limit=200)`,
`write(path, content)`, `patch(edits)`, and `view(path)` alongside `sh`.
`read` supports positive 1-based offsets or negative offsets from EOF, clips
overly long lines, and appends range/truncation footers on partial reads.
`write` creates missing parent directories automatically before atomic rename.
`patch` accepts a list of `{path, old, new}` replacements across multiple files
and locations; each old string must match exactly once (mismatch errors report
the match count). It validates all edits first and rolls back earlier
replacements on a write failure. Individual file writes are atomic; a multi-file
patch is not crash-atomic. Searching is done through `sh` with the system `grep`
or `rg`, narrowed by `-n`, a path and a match limit; recur does not provide its
own search helper. File helpers support UTF-8 files up to 4 MiB; paths resolve
relative to the project directory.

`view(path)` captures a PNG/JPEG image (up to 2 MiB and 20 megapixels; four per
execution). After all tool outputs in the response are committed, the next
model request receives the actual captured images in a separate notification.
Image data is preserved in history independently of the original file.
`view` is unavailable during prefix initialization.

Use `docs("index")` to discover built-in documentation topics: `runtime`,
`files`, `images`, `sessions`, `steering`, `interface`, `models`, `headless`, and `clipboard`. Unknown topics raise
an error. These documents describe implemented behavior, not planned features.

### Native runtime platforms

The pinned Go binding bundles native libraries for macOS arm64, Linux amd64/arm64 (glibc), Windows amd64, and Linux musl variants. However, **recur itself currently targets macOS/Linux** because process-group cancellation uses Unix syscalls. macOS amd64 is not bundled by this binding. Alpine/musl builds require `-tags musl`. On first use the binding extracts its embedded library into the user cache (temporary-directory fallback); this location must allow native library loading. Changing runtime or platform may require revisiting the binding. Only macOS arm64 was exercised for this change.

### Saved history and upgrading

Session files live at `$RECUR_HOME/sessions/<cwd>/<date>-<id>.jsonl` (default: `~/.recur/sessions/...`). `<cwd>` is a sanitized directory basename plus a SHA-256 path hash, derived from the cleaned absolute working directory; same-named projects stay separate. IDs use a UTC timestamp and random suffix. Settings/prompts are shared in the home; session histories remain project-scoped.

`/new` retains previous sessions and starts a fresh interpreter. Type `/resume` or `/resume ` to see this project's session completions; Up/Down selects, Tab completes, and Enter executes a completed selection. Prefixes filter full session IDs. The visible completion window is bounded and follows the selected row. There is no separate session-list command. Startup opens a fresh session and only creates its `.jsonl` file on disk after the first message (use `--resume ID` or `/resume` to open an existing session). Switching sessions saves a non-empty chat first and reloads settings/prompts; a failed save/load leaves the active chat and interpreter intact. Read-only TUI followers can still use `/new`, `/resume`, `/help`, and `/quit`.

JSONL version 2 stores a project/version header, one API item per line, one display record per line, and a final count-checked completion checkpoint. Metadata and provisional display records are never sent as API input. Each save writes a complete snapshot through a private temporary file, file sync, and atomic rename, rather than appending partial records to the active file. Interrupted temporary writes leave the last committed session intact. Malformed, wrong-project, missing-checkpoint, and unterminated session files are rejected without truncating or silently repairing them; preserve a copy and restore a known-good backup. Session data is saved at turn completion, not durably on every streamed delta. Only one writer can own a session; other clients are read-only followers. Cross-process history merging is not performed.

**Upgrading from project-local configuration:** existing project `.recur` files are not moved or overwritten. Copy any desired provider settings and custom prompts to `~/.recur` yourself, or explicitly set `RECUR_HOME` to the existing config directory. Missing files are created with defaults; existing files remain unchanged.

Existing editable prompts are **never overwritten**. Update obsolete instructions from `prompts/system.md` if needed. The Python tool description independently identifies the actual Monty tool contract and separate prefix-output notifications. Older binaries cannot load the new JSONL sessions; keep old history files/backups if downgrading.

## Source layout

`main.go` handles CLI startup and agent lifetime. `internal/tui` contains the
terminal interface, rendering, commands, terminal compatibility, headless event
printing, and their tests. `internal/cli` owns headless transports and command-line dispatch. Agent logic, session storage, and interpreter support
remain in `internal/agent`, `internal/repl`, and `internal/limitbuf`.
`go run .` and `go build .` still use the root entry point.

## Tests

```sh
go test ./...
go test -race ./...
go vet ./...
```

### Timeline visibility settings

Set these top-level values in `~/.recur/settings.toml` (or `$RECUR_HOME/settings.toml`):

```toml
max_code_lines = 999
max_result_lines = 4
max_thinking_lines = 4
```

These are also the defaults for existing configurations that omit the keys.
Each limit counts visible body rows **after wrapping** to the terminal width,
not including the heading. Only the beginning is shown; `0` hides the entire
block, including its heading and spacing. Negative values are invalid. All errors, including tool failures, remain visible as single-line notices independently of block limits. Code uses a muted light gray, while results and reasoning summaries use
a darker gray; errors retain their distinctive error styling.

Limits apply only to the TUI. Complete content remains in the saved history and
in the agent's context, and headless output is not truncated. Reload settings
with `/reload` (keeps the chat, restarts the interpreter) or `/new` (starts a new
session without deleting the old chat). Existing configuration files are never
overwritten automatically.

### Context compaction

`settings.toml` controls when older turns are replaced by a model-written summary:

```toml
max_context_tokens = 400000   # estimated size; 0 disables compaction
compact_keep_turns = 4        # turns kept verbatim
max_agent_steps = 0           # model/tool round trips per turn; 0 means no limit
```

The estimate charges 4 bytes per token and 1,200 tokens per image; inline base64 image
payloads are excluded from the byte count so one screenshot cannot trigger compaction on
its own. The status bar keeps showing the provider's real input-token count. If the kept
turns alone exceed the budget, recur reports it instead of compacting again on every
request, and an existing `<compact_summary>` is never summarized a second time. Before a request that would exceed the
budget, everything older than the last `compact_keep_turns` turns is summarized into one
`<compact_summary>` item, using the same system prompt and prefix as normal turns. Cuts
happen only at user-message boundaries, so a tool call never loses its output. If the
summary request fails during automatic compaction, the old part is dropped without a
summary and the turn continues. `/compact` does the same on demand, `/compact <prompt>`
adds an instruction to the summary request, and a failed explicit compaction leaves
history untouched. The trimmed history is saved immediately.

### Colors

Every color comes from `settings.toml`; the defaults are the built-in palette:

```toml
[colors]
accent = "#79d9ff"
foreground = "#d5ddeb"
muted = "#8794a8"
code = "#a0a0a0"
dim = "#606060"
input = "#8aadff"
assistant = "#9bdef5"
error = "#f09eac"
warn = "#e5c07b"
info = "#d7c38b"
```

Values must be `#rrggbb`; unknown names are rejected with the allowed list. Delete an entry
to fall back to its default. Markdown headings, the header, notices and the status bar all
follow this table. `/reload` applies a changed palette without restarting recur.

### Markdown, working status and steering

Assistant Markdown uses distinct bright colors for heading levels, input-blue
inline code, and numbered lists with a dot and space (`1. Item`). HTTP(S) URL
text is emitted as an OSC 8 terminal hyperlink. Use your terminal's hyperlink
gesture (commonly Ctrl/Cmd+click) to open it in the system's default browser.
The terminal must support OSC 8 and permit links; unsupported terminals still
show the URL as text. Links never launch a browser automatically, and non-web
schemes are not made clickable. Wrapped/clipped links are closed on each row so
neighboring UI content does not inherit them.

While a turn is active, an input-blue spinner and `working · <elapsed>` appear
above the composer. Cancellation changes the label to `cancelling` until the
turn actually finishes. The indicator is transient, remains active even with
hidden tool/thinking blocks, and does not pull a scrolled-up viewport down.

Press **Enter while working** to queue a steering instruction instead of
cancelling the turn. Pending instructions appear as `queued` previews above the
composer. The agent accepts them in submission order before its next request,
after the current response and all tool calls from that response have completed.
This does not interrupt an in-flight API request or reset the REPL. Accepted
instructions become ordinary user messages in the timeline and saved history.

An instruction submitted just as the turn finishes continues in a new turn
without being lost or clearing an unsent draft. After failure or cancellation,
unaccepted instructions return to the input for review/resubmission. New
messages cannot be queued while cancellation is in progress, and a full queue
leaves the submitted text in the input. Pending previews are not saved as chat
messages until accepted. Press **Up on an empty composer** to recall the newest unaccepted steering instruction. It is atomically removed from the delivery queue, so it cannot also reach the agent. Command/session completion navigation takes priority, and nonempty drafts retain normal cursor movement. Once the agent accepts an instruction, it cannot be recalled as unsent. A recalled instruction can be edited and resubmitted; the remaining queue stays FIFO.

Explicit `/new`, `/resume`, or `/quit` discards pending input as part of switching/exiting; these commands wait for the active turn to end. Late steering continuation preserves both the unsent draft and the viewport position when scrolled up.

Notifications use a single muted gray row, `! · <message>`, truncated to the
terminal width. REPL reset notices display `Python session restarted. Previous
variables were cleared.` instead of protocol details. Full technical notices
remain unchanged in saved history and the model's context.

Errors use one row with a red `!` icon, a dot separator, and dark-blue message text. The row normalizes whitespace and truncates to terminal width; underlying records and headless stderr retain full diagnostics. Failed turns and save failures return errors instead of being reported as successful. If both execution and persistence fail, both causes are retained. UI-only diagnostics join the display journal when starting the next turn, switching sessions, or quitting; they are not sent to the model.

### Streaming tool calls

REPL arguments stream into the timeline as decoded Python code, not raw JSON. Each call has its own entry even if argument deltas interleave. Incomplete escape/Unicode sequences wait for enough input to decode correctly; completion snapshots reconcile the entry without duplicating it. Provisional entries carry a `preview` label. They can survive in the display journal after a failed stream but are not committed as completed API calls.

**Streaming is UI-only.** Tools still execute sequentially in response order in the shared REPL, only after the entire API response completes. No partial code is executed, and calls do not run in parallel. Steering is accepted only after the current response's calls and outputs complete. Headless mode prints final calls once; tool stdout/stderr itself is not streamed while a call runs.

## Model and provider selection

`/model` fetches models from all configured providers when opened and shows each with its provider; choosing one makes that provider the default. Providers that fail are reported and skipped. Type to filter,
Up/Down selects, Left/Right chooses `none`, `low`, `medium`, `high`, `xhigh`, or
`max`, and Enter applies the selection. Esc cancels. Changes are disabled during
an active turn. The selected model/effort update the current session and global
defaults for new sessions, not older sessions.

`/login` lists providers and ends with **+ Add**. The creation wizard asks for a
unique name, HTTP(S) base URL, and an optional API key. Keys are masked in the
composer and stored only in private configuration, never in chat history. Only the
Responses API is supported; a provider configured with the removed
`chat_completions` `api_type` fails to load with an explicit error. After choosing a
provider, use `/model` to select a compatible model. Responses supports streaming,
tool calls, image input, refusals and reported usage. Unsupported provider-specific
extensions are not inferred.

JSONL session headers pin `provider`, `model`, and `effort` in a selection object;
credentials remain in settings.toml. Missing providers produce an explicit error.
Old headers without selection use current defaults and pin them on their next
save. Runtime endpoint/credential changes still apply by provider identity.

Python exposes `session.id`, `session.model`, `session.provider`, `session.effort`,
`session.cwd`, and `session.ctx_size` as a named-tuple snapshot refreshed before
every execution, including the prefix. Fields are immutable; replacing the
Python variable does not change application settings. `ctx_size` matches the
status bar's latest request input-token estimate (including cache), or `None`
when unknown. It is not model context capacity.

## Headless transports

```sh
recur --prompt 'Inspect this project'                # new session, single turn
recur --prompt 'Continue' --resume FULL_SESSION_ID   # explicit continuation
printf 'First request\nSecond request\n' | recur --mode stdio
printf '{"prompt":"Inspect this project"}\n' | recur --mode jsonl
recur --mode jsonl --resume FULL_SESSION_ID
```

Stdio accepts one prompt per line. JSONL accepts one `{"prompt":"..."}` object
per line; escaped newlines allow multiline prompts. JSONL stdout contains a
`session` record, display-event records (`type`, `text`, `id`, `meta`,
`provisional`), and a `done` record with `ok` and session metadata per turn.
`--prompt` can also be combined with `--mode jsonl`. Diagnostics stay on stderr;
no TUI control sequences are added to stdout. A failed turn, malformed input or
cancellation returns a nonzero exit status. EOF ends the process. Without explicit
resume, headless runs create new sessions, not continuations of the last one.

### External notifications

```sh
recur notify -s FULL_SESSION_ID 'Please also check the tests'
printf 'A message from another process' | recur notify -s FULL_SESSION_ID
```

Notifications are project-scoped, XML-escaped and wrapped in `<notify>` before
steering-like delivery at the next safe request boundary. An idle TUI checks
once per second; an idle headless client with open stdin reacts immediately.
An inactive session is an error, not a deferred message. Maximum message text is
128 KiB; the live queue holds 64 messages. Acknowledgement means the owner
**enqueued** the text, not that the model accepted it or that it was saved.
Pending notifications are in-memory and can be lost when the owner exits,
crashes or switches sessions. Do not automatically retry after an ambiguous
acknowledgement failure; exactly-once delivery is not guaranteed. Once accepted,
notifications are persisted with normal history.

### Ownership and read-only followers

A stable OS lock permits only one writer per session. Another TUI client opens
read-only, preserves scroll position, and reloads the committed JSONL every
second. It does not stream in-progress turns, execute Python or send requests.
Headless clients resuming an owned session also become followers: JSONL emits
`snapshot` records when display records change; stdio prints a complete text
snapshot on changes. Input/prompts are not submitted in this mode. Followers do
not auto-promote: reopen after the owner exits to acquire the released lock.
Independent sessions remain concurrent. The OS releases ownership after crashes.
Do not remove `.lock` files while clients are running: lock inodes must remain
stable. Socket endpoint sidecars are private; inactive/stale endpoints are not
queued for later delivery.

Clipboard image paste is available; see the section below. Python `view(path)`
also supplies actual captured images to the model.

## Clipboard images (text labels only)

Press **Ctrl+V** to read a local clipboard image, or **Cmd+V** when the terminal
forwards that shortcut. The composer shows only **`[Image 1: paste-....png]`**;
**no image pixels or terminal graphics are rendered**. **Ctrl+X** removes the last
attachment. Enter submits the caption and actual image bytes; image-only input
works too. Ordinary text paste remains available, including masked provider-key
entry. Wait for an in-progress clipboard read before submitting.

macOS uses its built-in `osascript`/AppKit bridge to convert native images to
PNG, with `pbpaste` for text. Linux requires `wl-paste` on Wayland or `xclip` on
X11 and supports PNG clipboard images. Operations have a three-second timeout
and bounded output. Shared image validation limits images to 2 MiB and 20
megapixels after conversion. At most eight clipboard images may be pending across
the current turn, composer and queued instructions.

```toml
# Empty (default): platform temporary directory/recur-images.
# Relative paths resolve against the project directory.
attachment_dir = ""
```

The default often resolves below `/var/folders/...` on macOS. Captured files are
private (0600; newly created directories 0700). Images follow steering acceptance,
recall, late continuation and cancellation recovery. A failed prefix restores
image-only input too. Submit or remove pending images before switching sessions.
Stale captures from a prior session/picker are discarded; external notifications
do not submit an unsent draft's images.

Accepted image bytes are embedded in history, independently of retained files.
Captured files remain in the configured directory after submission/exit for
manual cleanup. Removing a draft attachment deletes that captured file; rejected
and stale captures are also cleaned up. Unsubmitted drafts are not durable across
application exit. Both retained files and saved image history can be sensitive.

**Terminal limitations:** many terminals intercept Cmd+V before Recur can see it.
Use Ctrl+V or map Cmd+V to send Ctrl+V (`0x16`) in that terminal. English V and
Russian М/м are recognized when the terminal reports modified/alternate-layout
keys through Kitty/CSI-u; plain Russian letters remain ordinary input. A legacy
terminal cannot forward modifier information it does not encode. Normal terminal
text paste continues to work. Automated tests do not read the real clipboard;
actual desktop clipboard and terminal shortcut behavior need interactive checking.
