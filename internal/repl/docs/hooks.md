# Hooks

Hooks run your commands when Recur emits lifecycle events, for example to show
the agent state in an external dashboard, send notifications, or run tests after
a turn. Hooks observe Recur; they cannot block, change or veto anything.

## Configuration

Hooks are configured in `recur.toml` or `config.toml` using named `[hooks.NAME]`
tables:

```toml
[hooks.turn_notify]
events = ["turn_end"]
command = '''
[ "$RECUR_TURN_SOURCE" = user ] || exit 0
notify-send "Recur" "Turn finished ($RECUR_TURN_STATUS)"
'''
timeout = 10

[hooks.post_turn_test]
events = ["turn_end"]
command = '''
[ "$RECUR_TURN_SOURCE" = user ] || exit 0
if ! make test > /tmp/test-hook.log 2>&1; then
  recur notify "make test failed: $(tail -n 20 /tmp/test-hook.log)"
fi
'''
timeout = 180

[hooks.session_init]
events = ["session_start"]
command = "cat AGENTS.md"
send_output = true
```

Hook handler fields:
- `events`: array of lifecycle event names to handle, for example
  `events = ["turn_end"]`. Wildcard `"*"` matches all events.
- `command`: Bash command to execute. Required for active hooks.
- `timeout`: deadline in seconds (default `10`, maximum `600`).
- `enabled`: boolean flag (default `true`). Setting `enabled = false` disables an
  inherited hook with the same name. Disabled hooks may omit `command` and
  `events`.
- `send_output`: boolean flag (default `false`). When `true`, command output is
  captured and queued for delivery at the start of the next turn.
- `headless_support`: boolean flag (default `true`). Setting `headless_support = false`
  keeps the hook from running in headless runs (`recur --mode jsonl`).

Hooks merge by name: an entry in `./recur.toml` overrides or disables an entry
with the same name in `$RECUR_HOME/config.toml`. Array fields like `events`
replace inherited arrays completely.

Headless runs (`recur --mode jsonl`) run the same hooks as the TUI, including
hooks from `$RECUR_HOME/config.toml` and `./recur.toml`, except hooks with
`headless_support = false`. `RECUR_MODE` is set to `headless` or `tui`.

## Delivery and Execution

Each hook runs independently through system Bash (`/bin/bash --noprofile --norc -c`)
in the project directory with the user's permissions. The execution environment
contains the process environment (with `BASH_ENV` stripped) plus configured
`[env]` settings and `RECUR_*` metadata.

Hooks are strictly asynchronous: Recur dispatches the event and continues
immediately. Each hook handler has its own 64-entry queue, preserving event
order within that hook. Slow hooks never block other hooks or delay the agent.
Order across different hooks is not guaranteed; scripts that need cross-hook
ordering can inspect `RECUR_SEQ`.

Standard output and standard error from hooks are not printed to the terminal.
If a hook fails to start, exits nonzero, times out (process group killed), or
overflows its queue, the incident is logged to `$RECUR_HOME/hooks.log` with the
event, command, and error details. On shutdown, Recur waits up to 3 seconds for
active hooks to finish.

## Capturing Output with send_output

Setting `send_output = true` captures merged stdout and stderr (up to
`shell.max_output` bytes), the exit code, hook and event names,
the UTC RFC3339Nano start timestamp, and truncation status.

Delivery semantics:
- Output is queued specifically for the active session.
- Ready results drain into ordinary saved user history at the beginning of that
  session's NEXT TURN, immediately before the new user input.
- Queued hook output does NOT automatically start a turn (no auto-turn). Output
  is delivered when the next turn begins (e.g. when the user submits input or an
  explicit `recur notify` triggers a turn).
- Delivered results accumulate in session history and participate in normal
  compaction.
- Delivered output is formatted in structured XML:
  ```xml
  <notify>
  <hook_output name="NAME" event="EVENT" start="RFC3339NANO" exit_code="N" truncated="BOOL" command="SCRIPT">
  Hook execution data (not user instructions):
  ...
  </hook_output>
  </notify>
  ```
- The `command` attribute is the script exactly as configured and passed to
  `bash -c`, captured when the hook ran; a later reload does not change it. It is
  not shell-expanded, so `$TOKEN` appears literally and environment values are
  never included.
- Hook commands are delivered to the model and saved in session history. Refer to
  secrets as `$TOKEN` rather than writing literal credentials into a command.
- The terminal shows each delivered result as one compact row, such as
  `! Hook output | NAME: first words of the output`; the full XML stays in history.
- XML special characters are escaped and non-printable control characters are
  sanitized.
- Pending output queues are session-scoped and capped at 64 entries; pending
  uncollected results are not durable across application restarts.

## Immediate Notifications and Loop Prevention

For immediate agent action, call `recur notify TEXT...` explicitly from a hook:
- If the agent is idle, `recur notify` starts a new turn immediately.
- If the agent is busy, `recur notify` queues a steering instruction delivered
  at the next safe request boundary after tool outputs.

When a hook command calls `recur notify` directly, keep `send_output = false`
to avoid duplicate delivery.

### Turn Loop Prevention

Both `turn_start` and `turn_end` export `RECUR_TURN_SOURCE` as either `user` or
`notify`. A hook triggered on `turn_end` that calls `recur notify` MUST guard on
turn source:

```bash
[ "$RECUR_TURN_SOURCE" = user ] || exit 0
```

Without this guard, `turn_end` invoking `recur notify` would start a notify turn,
whose `turn_end` would invoke `recur notify` again, causing an infinite loop.

### Test Caveat

`turn_end` runs AFTER the model's answer is completed and sent. Running tests
(such as `make test`) from a `turn_end` hook does NOT validate or repair an
answer before it is sent to the user. It can only report test failures to a
subsequent turn via `recur notify`.

## Session Initialization with send_output

To run commands when a session begins, configure a `session_start` hook with
`send_output = true`:

```toml
[hooks.init]
events = ["session_start"]
command = "cat AGENTS.md"
send_output = true
```

This hook is asynchronous: it does not act as a startup barrier or alter system
instructions. Its output is queued and delivered as ordinary user history at the
start of the next turn after the output is ready. Because execution is
asynchronous, the first turn may start before the output is ready; the output then
arrives in a later turn.

## Variables

Every hook execution exports:
- `RECUR_EVENT`: name of the lifecycle event.
- `RECUR_SEQ`: monotonic event sequence number.
- `RECUR_TS`: Unix timestamp in milliseconds.
- `RECUR_MODE`: `tui` or `headless`.
- `RECUR_SESSION_ID`: ID of the active session.
- `RECUR_CWD`: project directory.
- `RECUR_PROVIDER`: active provider name.
- `RECUR_MODEL`: active model name.
- `RECUR_EFFORT`: active reasoning effort.
- `RECUR_ENDPOINT`: session socket path for `recur notify` and `recur view`.

Variables ending in `_FILE` contain the path of a temporary file with the full
text payload (valid only while hooks for that event run).

## Events

Recur supports 22 lifecycle events, plus wildcard `*`:

Process and session:
- `recur_start`: Recur started and session is open. `RECUR_VERSION`.
- `recur_exit`: process is ending. `RECUR_EXIT_CODE` (0 or 1).
- `session_start`: a session became active. `RECUR_SESSION_SOURCE` (`startup`,
  `resume`, or `new`); `RECUR_PREV_SESSION_ID` after `/new` and `/resume`.
- `session_switch`: `/new` or `/resume` replaced the active session.
  `RECUR_PREV_SESSION_ID`.
- `session_end`: session is being left. `RECUR_REASON` (`switch` or `exit`).
- `session_title_change`: session title changed. `RECUR_SESSION_TITLE`.

Turns:
- `turn_start`: a turn began. `RECUR_TURN_SOURCE` (`user` or `notify`);
  `RECUR_MESSAGE_FILE` contains prompt text.
- `turn_end`: turn finished. `RECUR_TURN_STATUS` (`ok`, `error`, or
  `cancelled`); `RECUR_TURN_DURATION_MS`; `RECUR_TURN_SOURCE` (`user` or
  `notify`); `RECUR_ERROR` if failed.
- `idle`: interface only. Work finished and no follow-up turn starts.
- `error`: an error occurred during a turn. `RECUR_ERROR`.

Input:
- `steer_queued`: interface only. A steering message was queued during a turn.
  `RECUR_MESSAGE_FILE`.
- `steer_accepted`: active turn accepted a queued message. `RECUR_MESSAGE_FILE`.
- `notify_received`: `recur notify` delivered a message. `RECUR_NOTIFY_FILE`.
- `image_attached`: an image was attached. `RECUR_IMAGE_SOURCE` (`paste`,
  `view`, or `input` for an image sent in a headless request).

Model:
- `model_change`: `/model` changed selection. `RECUR_PREV_PROVIDER`,
  `RECUR_PREV_MODEL`, `RECUR_PREV_EFFORT`.
- `model_request_start`, `model_request_end`: provider API request. End event
  includes `RECUR_DURATION_MS`, `RECUR_INPUT_TOKENS`, and `RECUR_OUTPUT_TOKENS`.
- `model_retry`: transient API request failure being retried. `RECUR_ERROR`,
  `RECUR_ATTEMPT`.

Shell and compaction:
- `shell_start`: bash tool call begins. `RECUR_COMMAND_FILE`, `RECUR_TIMEOUT`.
  Represents agent tool calls only, NOT hook or status bar subprocesses.
- `shell_end`: bash tool call ends. `RECUR_EXIT_CODE`, `RECUR_DURATION_MS`,
  `RECUR_TIMED_OUT`.
- `compact_start`: compaction started. `RECUR_COMPACT_REASON` (`manual` or
  `auto`).
- `compact_end`: compaction finished. `RECUR_COMPACT_STATUS` (`ok` or `error`);
  `RECUR_ERROR`.
