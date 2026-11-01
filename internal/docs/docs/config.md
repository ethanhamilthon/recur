# Configuration

Recur is configured only by `recur.toml` files. A file is read from up to two places and
merged over built-in defaults. This topic owns everything about those files; other topics
point here for discovery, precedence, merge rules and keys.

## Discovery and Precedence

The primary file is the first of these that applies:
1. `--config PATH` (or `-c PATH`). A relative path resolves against the launch directory.
   A missing, unreadable or invalid file is a startup error; nothing falls back.
2. `./recur.toml` in the launch directory (the project root). There is no upward search.
3. `$RECUR_HOME/recur.toml`, the global file. `RECUR_HOME` defaults to `~/.recur`, so the
   default path is `~/.recur/recur.toml` (see `recur docs cli`).

If no file exists, the built-in defaults apply.

## Merge Order

Layers apply from lowest to highest: built-in defaults, then the global file
`$RECUR_HOME/recur.toml`, then the primary file. If the primary and the global file are the
same physical file (a same-file alias), it is read once.

`--no-extend` skips the separate global layer in both the TUI and headless runs. Built-in
defaults still apply, and a primary file that is itself the global file is still read.

Merge rules:
- Tables merge by key: `[ui]`, `[shell]`, `[env]`, `[providers]` (per provider name) and
  `[hooks]` (per hook name). A layer sets only the fields it writes.
- Scalars replace inherited values, including `""`, `0` and `false`.
- Arrays replace inherited arrays completely (`status_left`, `status_right`, hook `events`).
- `system` replaces the whole system prompt; `system = ""` clears it.
- `enabled = false` disables an inherited hook with the same name.
- A provider's `api_key` and `api_key_env` are checked after merging. To drop an inherited
  one, set it to `""` in the higher layer.
- Generic string interpolation, profiles and include directives are not supported.

Validation runs on the merged result. Unknown keys and unknown tables are errors, so a typo
stops startup. Error messages are listed in `recur docs troubleshooting`.

## Writes from the TUI

`/model` and `/login` patch raw TOML keys in the primary file, keeping unrelated comments,
values and sparse inheritance. The destination is shown after saving. The primary file is
the one described above, or `$RECUR_HOME/recur.toml` when no project or `--config` file is used.
`/login` cannot overwrite an existing provider name; edit the file to change one.

## Reloading

`/reload` in the TUI re-reads the files for the active chat. Settings, `[env]`, hooks and
status commands take effect for later tool calls and events. The active provider, model and
effort stay as selected in the session. `/new` and `/resume` also re-read the files.

## The recur.toml Format

Built-in defaults (the exact file shipped inside this binary; every key you omit keeps the
value shown here):

```toml
{{defaults}}
```

Write only the keys you want to change. A minimal project file is shown in `recur docs cookbook`.

## Key Reference

Keys under a table are shown with their table prefix: `ui.max_code_lines` is `max_code_lines`
inside `[ui]`. Sizes in bytes and durations in seconds are plain integers.

| Key | Type | Default | Constraints |
| --- | --- | --- | --- |
| `provider` | string | `""` | Empty, or the name of a `[providers.NAME]` table. |
| `model` | string | `""` | Model name sent to the provider. Empty makes requests fail with "no provider or model selected". |
| `effort` | string | `""` | Empty, or one of `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`. |
| `reasoning_summary` | string | `"auto"` | `auto` requests reasoning summaries; `off` does not. |
| `max_context_tokens` | integer | `400000` | At least 0. Estimated input tokens above which compaction runs before a request; `0` disables it. |
| `compact_keep_turns` | integer | `4` | At least 0. Recent turns kept verbatim when compacting. |
| `max_agent_steps` | integer | `0` | At least 0. Tool iterations allowed per turn; `0` is unlimited. |
| `max_file_bytes` | integer | `65536` | At least 0. Per-file byte limit for `@path` attachments; `0` is unlimited. |
| `attachment_dir` | string | `""` | Where captured images are saved. Empty uses `recur-images` in the platform temporary directory for TUI clipboard pastes, and `$RECUR_HOME/attachments` for headless `images`. `recur view` always uses `$RECUR_HOME/attachments` and ignores this key. Relative paths resolve against the launch directory. |
| `system` | string | `"You are Recur, a coding agent"` | System prompt. `""` clears it. |
| `ui.max_code_lines` | integer | `999` | At least 0. Visible body rows of code blocks; `0` hides the body. |
| `ui.max_result_lines` | integer | `4` | At least 0. Visible body rows of tool output; `0` hides the body. |
| `ui.max_thinking_lines` | integer | `4` | At least 0. Visible body rows of reasoning; `0` hides the body. |
| `ui.status_left` | array of strings | two built-in snippets | Each entry is a shell snippet printing one segment. `[]` hides the side. |
| `ui.status_right` | array of strings | one built-in snippet | Same rules as `status_left`. |
| `shell.timeout` | integer seconds | `120` | Greater than 0 and not above `shell.max_timeout`. Default for bash tool calls. |
| `shell.max_timeout` | integer seconds | `600` | Greater than 0. Largest timeout a call may request. |
| `shell.max_output` | integer bytes | `8192` | At least 0. Output bytes kept per call; `0` disables truncation. |
| `env.NAME` | string | none | NAME must not start with `RECUR_`. |
| `providers.NAME.base_url` | string | none | HTTP(S) URL with a host, when set. The provider must speak the OpenAI Responses API. |
| `providers.NAME.api_key` | string | none | Literal secret. Cannot be combined with a nonempty `api_key_env`. |
| `providers.NAME.api_key_env` | string | none | Name of the variable holding the key, read from `[env]` and then the process environment. Must be set and nonblank while this provider is selected. Cannot be combined with a nonempty `api_key`. |
| `hooks.NAME.events` | array of strings | none | Required unless the hook is disabled. Event names from `recur docs hooks`, or `"*"`. |
| `hooks.NAME.command` | string | none | Required unless the hook is disabled. Non-blank Bash command. |
| `hooks.NAME.timeout` | integer seconds | `10` | 1 to 600. `0` or omitted means 10. |
| `hooks.NAME.enabled` | boolean | `true` | `false` disables the hook, including an inherited one with the same name. |
| `hooks.NAME.send_output` | boolean | `false` | `true` queues captured output for the next turn. |
| `hooks.NAME.headless_support` | boolean | `true` | `false` skips the hook in `--mode jsonl` runs. |

Notes on the tables:
- `[ui]` status commands run with the RECUR_* metadata described in `recur docs cli`. Their
  environment, limits and refresh rules are in `recur docs interface`.
- `[shell]` values are shown to the model in the bash tool schema. Shell behavior is in `recur docs runtime`.
- `[env]` values are applied separately to every shell call, hook and status command. Recur
  never changes its own process environment.
- Hook fields are listed above. Events, delivery, variables and loop prevention are in `recur docs hooks`.
  Headless runs load hooks the same way as the TUI, except hooks with `headless_support = false`.
- Secrets are never copied into the file, session history or logs. `api_key_env` names a
  variable. The key itself stays in the environment.

Example provider table:

```toml
[providers.openrouter]
base_url = "https://openrouter.ai/api/v1"
api_key_env = "OPENROUTER_API_KEY"
```
