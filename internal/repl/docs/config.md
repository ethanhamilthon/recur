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

Session data (`$RECUR_HOME/sessions/`) and `recur view` originals
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
the selected primary configuration. It applies to headless runs the same way as
to the TUI; the default is to inherit the global configuration.

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

Built-in defaults (the exact file shipped inside this binary; every key you omit
keeps this value):

```toml
{{defaults}}
```

Write only the keys you want to change. A minimal project file looks like
`recur docs cookbook`.

### Root Fields

- `provider`: name of the default provider (e.g., `"openrouter"`).
- `model`: name of the default model (e.g., `"anthropic/claude-3.5-sonnet"`).
- `effort`: default reasoning effort (`""`, `"none"`, `"minimal"`, `"low"`,
  `"medium"`, `"high"`, `"xhigh"`, `"max"`).
- `reasoning_summary`: `"auto"` requests reasoning summaries from the model; `"off"` does not.
- `max_context_tokens`: input token threshold that triggers automatic context
  compaction before an oversized request; `0` disables.
- `compact_keep_turns`: number of recent turns preserved verbatim when compaction
  runs (default `4`).
- `max_agent_steps`: maximum tool iterations allowed per turn (default `0`,
  unlimited).
- `max_file_bytes`: maximum bytes of each file included when a prompt references
  it with `@path` (default `65536`; `0` means unlimited; negative values are
  invalid). The limit counts raw source bytes, not tokens, and applies per file:
  it does not bound the combined size of several attached files or the model's
  context window. Over-limit files are sent as their leading bytes, cut before an
  incomplete UTF-8 character, and marked as truncated with the included and
  original sizes. The limit is read when a prompt is submitted, so `/reload`
  affects the next submission but not instructions already queued or running.
  See the `@` file references section in `interface.md`.
- `attachment_dir`: directory for full-resolution captured clipboard images
  (default `""` uses `$RECUR_HOME/attachments` or temporary storage). Relative
  paths resolve against launch cwd.
- `system`: base system prompt instructions provided to the model.

### [ui]

Controls terminal rendering and status bar content:
- `max_code_lines`: visible body rows for code blocks in history; `0` hides the body.
- `max_result_lines`: visible body rows for tool output blocks; `0` hides the body.
- `max_thinking_lines`: visible body rows for model reasoning blocks; `0` hides the body.
- `status_left`: array of shell command strings producing segments on the left
  side of the bottom status bar. Keys `status_left` and `status-left` are both
  accepted.
- `status_right`: array of shell command strings producing segments on the right
  side of the bottom status bar. Keys `status_right` and `status-right` are both
  accepted. Setting an empty array (`[]`) hides that side.

Status commands run as shell snippets with `RECUR_*` metadata. Their environment,
limits, refresh rules and padding are documented once in `recur docs interface`.

### [shell]

Configures execution parameters for the agent's bash tool:
- `timeout`: default execution deadline in seconds for tool calls.
- `max_timeout`: upper bound a single call may request; `timeout` must not exceed it.
- `max_output`: default bytes kept from merged stdout/stderr (first half + last
  half with truncation notice). `0` disables truncation.

Defaults are in the block above. The model sees the effective values in the bash
tool schema, so changing them here is enough.

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
- `headless_support`: boolean flag (default `true`). Setting `headless_support = false`
  keeps the hook from running in headless runs (`recur --mode jsonl`).
  The hook still runs in the TUI.

See `recur docs hooks` for full hook details and lifecycle events.

## Headless Mode Hooks

Headless runs (`recur --mode jsonl`) load the same configuration as the TUI, so
hooks from `./recur.toml` and `$RECUR_HOME/config.toml` run without `--config`.
Hooks with `headless_support = false` are skipped in headless runs only. The
`--config` and `--no-extend` flags behave the same way in both interfaces.
