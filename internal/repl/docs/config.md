# Configuration

Recur uses a unified TOML configuration file (`recur.toml` or `config.toml`)
to manage provider credentials, model defaults, limits, UI settings, status
bar commands, shell behavior, environment variables, and named lifecycle hooks.

## Discovery and Precedence

Recur resolves its primary configuration file in order:
1. `--config PATH`: an explicit path provided via the command line. A relative
   path resolves against the launch working directory. If the specified file is
   missing, unreadable, or invalid, Recur exits immediately with an error
   (no fallback).
2. `./recur.toml`: a project-local configuration in the launch working directory
   (project root). Recur does not perform an upward directory search.
3. `$RECUR_HOME/config.toml`: the global user configuration file. `RECUR_HOME`
   defaults to `~/.recur`.

Session data (`$RECUR_HOME/sessions/`) and clipboard attachments
(`$RECUR_HOME/attachments/`) always reside under `RECUR_HOME`, independently of
where project configuration files are located.

## Inheritance and Deep Merging

Recur merges configuration layers in this sequence:
  Built-in defaults -> Global configuration (`$RECUR_HOME/config.toml`) -> Selected primary configuration

If the selected primary configuration and global configuration refer to the same
physical file (a same-file alias), the file is read only once.

The `--no-extend` flag tells Recur to skip reading the separate global base
configuration (`$RECUR_HOME/config.toml`). It does not disable built-in defaults,
nor does it prevent loading `$RECUR_HOME/config.toml` when that file is itself
the selected primary configuration.

Merging rules:
- Tables merge recursively by key (`[ui]`, `[shell]`, `[env]`, `[providers]`, `[hooks]`).
- Scalars replace inherited values, including explicit empty strings (`""`), `0`,
  and `false`.
- Arrays replace inherited arrays completely (for example, `status_left`,
  `status_right`, and hook `events`).
- `system`: setting `system = "..."` replaces the entire system prompt string.
  An explicit empty string (`system = ""`) clears the system instructions
  without falling back to built-in defaults.
- Hooks: `[hooks.NAME]` tables merge by hook name. An inherited hook can be
  customized by name or disabled by specifying `enabled = false`. Disabled hooks
  may omit `command` and `events`.
- UI settings: omitted UI settings inherit from the base layer. There is no
  special default-only CLI mode.

Generic string interpolation, profiles, and include directives are not
supported.

## The recur.toml Format

Below is a complete unified configuration showing default values:

```toml
# Provider and model defaults
provider = ""
model = ""
effort = ""
reasoning_summary = "auto"
max_context_tokens = 400000
compact_keep_turns = 4
max_agent_steps = 0
attachment_dir = ""
system = "You are Recur, a coding agent"

[ui]
max_code_lines = 999
max_result_lines = 4
max_thinking_lines = 4
status_left = [
  'if [ -n "$RECUR_PROVIDER" ] && [ -n "$RECUR_MODEL" ]; then echo "$RECUR_PROVIDER · $RECUR_MODEL${RECUR_EFFORT:+/$RECUR_EFFORT}"; else echo "no model selected"; fi',
  'if [ -n "$RECUR_CTX_SIZE" ]; then if [ "$RECUR_CTX_SIZE" -ge 1000000 ]; then echo "ctx ~$(( (RECUR_CTX_SIZE + 500000) / 1000000 ))M"; elif [ "$RECUR_CTX_SIZE" -ge 1000 ]; then echo "ctx ~$(( (RECUR_CTX_SIZE + 500) / 1000 ))K"; else echo "ctx $RECUR_CTX_SIZE"; fi; else echo "ctx ?"; fi'
]
status_right = [
  'if [ "$RECUR_CWD" = "$HOME" ]; then echo "~"; elif [[ "$RECUR_CWD" == "$HOME"/* ]]; then echo "~${RECUR_CWD#$HOME}"; else echo "$RECUR_CWD"; fi'
]

[shell]
timeout = 120
max_timeout = 600
max_output = 8192

[env]

[providers]

[hooks]
```

### Root Fields

- `provider`: name of the default provider (e.g., `"openrouter"`).
- `model`: name of the default model (e.g., `"anthropic/claude-3.5-sonnet"`).
- `effort`: default reasoning effort (`""`, `"none"`, `"minimal"`, `"low"`,
  `"medium"`, `"high"`, `"xhigh"`, `"max"`).
- `reasoning_summary`: handling of reasoning summaries (`"auto"`).
- `max_context_tokens`: cumulative input token threshold triggering automatic
  context compaction before an oversized request (default `400000`; `0` disables).
- `compact_keep_turns`: number of recent turns preserved verbatim when compaction
  runs (default `4`).
- `max_agent_steps`: maximum tool iterations allowed per turn (default `0`,
  unlimited).
- `attachment_dir`: directory for full-resolution captured clipboard images
  (default `""` uses `$RECUR_HOME/attachments` or temporary storage). Relative
  paths resolve against launch cwd.
- `system`: base system prompt instructions provided to the model.

### [ui]

Controls terminal rendering and status bar content:
- `max_code_lines`: visible body rows for code blocks in history (default `999`;
  `0` hides code body).
- `max_result_lines`: visible body rows for tool output blocks (default `4`; `0`
  hides result body).
- `max_thinking_lines`: visible body rows for model reasoning blocks (default `4`;
  `0` hides thinking body).
- `status_left`: array of shell command strings producing segments on the left
  side of the bottom status bar. Keys `status_left` and `status-left` are both
  accepted.
- `status_right`: array of shell command strings producing segments on the right
  side of the bottom status bar. Keys `status_right` and `status-right` are both
  accepted. Setting an empty array (`[]`) hides that side.

The bottom status row has exactly 2 display-cell spaces padding on each side
(`"  "` + content + `"  "`). Available content width is `max(0, width - 4)`.
Narrow terminal widths (1 to 5 cells, 80 cells) are handled cleanly without
wrapping or negative padding. Unicode wide characters and emojis are truncated
with display-width awareness.

Each status command runs in a fresh `/bin/bash --noprofile --norc -c` process in
the project directory. Inherited `BASH_ENV` is stripped. Commands receive the
effective environment plus raw `RECUR_*` metadata: `RECUR_SESSION_ID`, `RECUR_CWD`,
`RECUR_PROVIDER`, `RECUR_MODEL`, `RECUR_EFFORT`, `RECUR_CTX_SIZE`, `RECUR_VERSION`,
`RECUR_STATE` (`idle`, `busy`, or `cancelling`), and `RECUR_ENDPOINT`. There are
no `RECUR_*_LABEL` variables or Go formatting APIs; shell commands assemble and
format labels themselves.

Nonempty stdout outputs from commands are trimmed, flattened to single lines,
stripped of ANSI/OSC control sequences, and joined with `" · "` in declared order.
Empty or failing commands are omitted silently. Commands run asynchronously with
a 500 ms timeout, 1 KiB output limit, and a concurrency limit of 4 workers.
Refreshes are event-driven: evaluation triggers on EVERY Recur lifecycle event
(all 22 events), initial render, and `/reload`. There is no polling, timer, or
token-streaming refresh. Rapid events cancel running status commands and restart
evaluation for the new snapshot. Status commands do not emit lifecycle events.
No status commands run in headless mode.

### [shell]

Configures execution parameters for the agent's bash tool:
- `timeout`: default execution deadline in seconds for tool calls (default `120`).
- `max_timeout`: upper bound for tool deadlines (default `600`).
- `max_output`: maximum bytes kept from merged stdout/stderr (default `8192`;
  first half + last half with truncation notice). `0` disables truncation.

### [env]

Key-value table defining custom environment variables:
```toml
[env]
GO111MODULE = "on"
EDITOR = "nano"
```
Variables defined in `[env]` are merged over the process environment and applied
independently to every Bash tool, hook, and status bar process. Recur never
mutates the parent process environment (`os.Environ`). The `RECUR_*` prefix is
reserved for host metadata.

### [providers.NAME]

Configures API endpoints and authentication for model providers:
```toml
[providers.openrouter]
base_url = "https://openrouter.ai/api/v1"
api_key_env = "OPENROUTER_API_KEY"

[providers.custom]
base_url = "https://api.example.com/v1"
api_key = "secret-token"
```

- `base_url`: HTTP(S) endpoint URL supporting the OpenAI Responses API.
- `api_key`: literal secret string.
- `api_key_env`: name of an environment variable containing the secret API key.
  Recur resolves the key at runtime from the process environment plus `[env]`.
  Specifying both `api_key` and `api_key_env` with nonempty values is an error.
  An explicit empty string (`api_key = ""` or `api_key_env = ""`) clears an
  inherited setting.

When using `api_key_env`, Recur does not copy secrets into configuration files
or chat history. A missing or empty environment variable fails only if that
provider is currently selected, not for unused providers.

The `/login` and `/model` commands update credentials and active selections by
atomically patching targeted raw TOML keys in the selected primary configuration file,
preserving unrelated formatting, comments, values, and sparse inheritance. The write destination
is displayed on completion.

### [hooks.NAME]

Defines named lifecycle hook handlers:
```toml
[hooks.turn_notify]
events = ["turn_end"]
command = '''
[ "$RECUR_TURN_SOURCE" = user ] || exit 0
notify-send "Recur" "Turn finished ($RECUR_TURN_STATUS)"
'''
timeout = 10

[hooks.test_gate]
events = ["turn_end"]
command = '''
[ "$RECUR_TURN_SOURCE" = user ] || exit 0
make test > /tmp/test-hook.log 2>&1 || recur notify "make test failed"
'''
timeout = 180
```

- `events`: array of lifecycle event names to handle. Wildcard `"*"` matches all
  events. Hook declarations require the `events = [...]` array syntax.
- `command`: Bash command to execute.
- `timeout`: deadline in seconds (default `10`, maximum `600`).
- `enabled`: boolean flag (default `true`). Setting `enabled = false` disables an
  inherited hook with the same name.
- `send_output`: boolean flag (default `false`). When `true`, stdout/stderr output
  is captured and queued for delivery at the start of the next turn.

See `recur docs hooks` for full hook details and lifecycle events.

## Headless Mode Hooks

In headless runs (`recur -prompt '...'` or `recur --mode jsonl`), implicit hooks
defined in `./recur.toml` or `$RECUR_HOME/config.toml` are disabled. Hooks only run
when an explicit configuration file is provided via `--config PATH`. To prevent
inheriting global hooks in headless mode, combine with `--no-extend`.

## Manual Migration Guide

Recur 0.6.0 retires legacy configuration files (`settings.toml`, `system.md`,
`prefix.sh`, and `hooks.toml`) and legacy CLI flags (`--settings`, `--system`,
`--prefix`, `--hooks`, `enable_local_recur`). Recur never automatically modifies
or deletes user files. If legacy configuration files exist without a new
`config.toml`, Recur exits with a startup diagnostic instructing you to migrate
manually.

Follow this mapping to migrate existing configurations:

1. `settings.toml` -> `config.toml` (or `recur.toml`):
   - Move root settings (`provider`, `model`, `effort`, `max_context_tokens`,
     `compact_keep_turns`, `max_agent_steps`, `attachment_dir`) to the root of
     `config.toml` or `recur.toml`.
   - Move UI display limits (`max_code_lines`, `max_result_lines`,
     `max_thinking_lines`) under the `[ui]` table.
   - Move shell limits (`timeout`, `max_timeout`, `max_output`) under `[shell]`.
   - Convert provider tables under `[providers.NAME]`. Use `api_key_env` to avoid
     storing raw keys in project files.
2. `system.md` -> `system`:
   - Set `system = "..."` at the root of `config.toml` or `recur.toml`. Multiline
     strings use TOML triple quotes (`'''` or `"""`).
3. `prefix.sh` -> named hook with `send_output = true`:
   - The prefix mechanism is retired. To run commands when a session begins,
     configure an ordinary `session_start` hook with `send_output = true`:
     ```toml
     [hooks.init]
     events = ["session_start"]
     command = "cat AGENTS.md"
     send_output = true
     ```
     Unlike the old prefix, this hook is strictly asynchronous and does not act as
     a startup barrier or alter system instructions. Only READY hook outputs are
     delivered at the start of the next turn; because execution is asynchronous,
     a `session_start` hook may miss the first turn if not yet completed.
4. `hooks.toml` -> `[hooks.NAME]`:
   - Convert `[[hook]]` entries into named tables `[hooks.<name>]`.
   - Update event declarations to use the `events = [...]` array syntax. Single-string
     `event = "..."` is retired and rejected in strict TOML parsing. Example migration:
     ```toml
     # Legacy syntax (hooks.toml):
     # [[hook]]
     # event = "turn_end"
     # command = "make test"

     # 0.6.0 syntax (config.toml / recur.toml):
     [hooks.test_gate]
     events = ["turn_end"]
     command = "make test"
     ```
   - Remove retired events `prefix_start` and `prefix_end`.
