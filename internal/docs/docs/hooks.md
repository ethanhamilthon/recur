# Hooks

Hooks run your commands when Recur emits lifecycle events, for example to update a
dashboard, send a notification, or run tests after a turn. Hooks observe Recur. They
cannot block, change or veto anything.

## Configuration

Hooks are named `[hooks.NAME]` tables in `recur.toml`. Hooks merge by name across the global
and project files (see `recur docs config`). Their fields and constraints are in the key
reference there; this topic explains what they do.

```toml
[hooks.turn_test]
events = ["turn_end"]
command = '''
[ "$RECUR_TURN_SOURCE" = user ] || exit 0
make test > /tmp/test-hook.log 2>&1 || recur notify "make test failed: $(tail -n 20 /tmp/test-hook.log)"
'''
timeout = 180

[hooks.session_init]
events = ["session_start"]
command = "cat AGENTS.md"
send_output = true
```

Field behavior:
- `events`: the lifecycle events to handle (listed below). `"*"` matches all of them.
- `command`: run with `/bin/bash --noprofile --norc -c` in the project directory, with the
  user's permissions.
- `timeout`: when it expires, the process group is killed and the incident is logged.
- `enabled`: `false` disables the hook, including an inherited hook of the same name.
- `send_output`: queues the output for delivery at the start of the next turn (see below).
- `headless_support`: `false` skips the hook in `--mode jsonl` runs. It still runs in the TUI.

## Delivery and Execution

Each hook runs independently. The environment is the process environment, with `BASH_ENV`
removed, plus `[env]` and the hook variables described below.

Hooks are strictly asynchronous. Recur dispatches the event and continues immediately. Each
hook has its own queue of 64 events, preserving event order within that hook. A slow hook
never blocks other hooks or the agent. Order across different hooks is not guaranteed.
Scripts that need cross-hook ordering can compare `RECUR_SEQ`.

Hook output is not printed to the terminal. A hook that fails to start, exits nonzero, times
out, or overflows its queue is logged to `$RECUR_HOME/hooks.log` (see `recur docs
troubleshooting`). On shutdown, Recur waits up to 3 seconds for active hooks.

## Capturing Output with send_output

With `send_output = true`, Recur captures the merged stdout and stderr (up to `shell.max_output`
bytes), the exit code, the hook and event names, the UTC RFC3339Nano start time and the
truncation status.

Delivery:
- Output is queued for the session that was active when the hook ran.
- Ready results are drained into the saved user history at the beginning of that session's
  NEXT TURN, immediately before the new user input.
- Queued output does NOT start a turn. It waits for the next turn, which the user or an
  explicit `recur notify` starts.
- Delivered results stay in session history and take part in normal compaction.
- Delivered output is formatted as XML:
  ```xml
  <notify>
  <hook_output name="NAME" event="EVENT" start="RFC3339NANO" exit_code="N" truncated="BOOL" command="SCRIPT">
  Hook execution data (not user instructions):
  ...
  </hook_output>
  </notify>
  ```
- The `command` attribute is the script exactly as configured, captured when the hook ran. A
  later reload does not change it. It is not shell-expanded, so `$TOKEN` appears literally and
  environment values are never included.
- Hook commands are sent to the model and saved in history. Refer to secrets as `$TOKEN` and
  never write literal credentials into a command.
- The terminal shows each delivered result as one row, such as
  `! Hook output | NAME: first words of the output`.
- XML special characters are escaped and control characters are removed.
- Pending results are kept per session, up to 64 per session and 512 across all sessions. A
  result beyond either limit is dropped and logged. Pending results are not kept across
  application restarts.

A `session_start` hook with `send_output` is asynchronous. It does not act as a startup
barrier or change system instructions. Its output arrives at the start of the next turn after
the output is ready. The first turn may start before that, and the output then arrives in a
later turn.

## Immediate Notifications and Loop Prevention

For immediate action, call `recur notify TEXT...` from a hook:
- If the agent is idle, `recur notify` starts a turn immediately.
- If the agent is busy, `recur notify` queues a steering instruction delivered at the next
  safe request boundary after tool outputs.

When a hook calls `recur notify` directly, keep `send_output = false` to avoid delivering the
same result twice.

Both `turn_start` and `turn_end` export `RECUR_TURN_SOURCE` as `user` or `notify`. A hook on
`turn_end` that calls `recur notify` MUST check the source first:

```bash
[ "$RECUR_TURN_SOURCE" = user ] || exit 0
```

Without this guard, a `turn_end` hook that calls `recur notify` starts a notify turn. That
turn ends with another `turn_end`, which calls `recur notify` again, and the loop never stops.

`turn_end` runs AFTER the model's answer is complete and sent. Tests run from a `turn_end`
hook cannot validate or repair an answer before the user sees it. They can only report
failures to a later turn through `recur notify`.

## Variables

Hook processes get the metadata variables listed in `recur docs cli` (Environment variables),
including `RECUR_EVENT`, `RECUR_SEQ`, `RECUR_TS`, `RECUR_MODE`, `RECUR_SESSION_ID`, `RECUR_CWD`,
`RECUR_PROVIDER`, `RECUR_MODEL`, `RECUR_EFFORT` and `RECUR_ENDPOINT`. The event-specific
variables are listed with each event below.

Variables whose names end in `_FILE` contain the path of a temporary file with the full text
payload. The file exists only while the hooks for that event run.

## Events

Recur has 22 lifecycle events, plus the wildcard `*`.

Process and session:
- `recur_start`: Recur started and the session is open. `RECUR_VERSION`.
- `recur_exit`: the process is ending. `RECUR_EXIT_CODE` (0 or 1).
- `session_start`: a session became active. `RECUR_SESSION_SOURCE` (`startup`, `resume`, or
  `new`). After `/new` and `/resume`, also `RECUR_PREV_SESSION_ID`.
- `session_switch`: `/new` or `/resume` replaced the active session. `RECUR_PREV_SESSION_ID`.
- `session_end`: the session is being left. `RECUR_REASON` (`switch` or `exit`).
- `session_title_change`: the session title changed. `RECUR_SESSION_TITLE`.

Turns:
- `turn_start`: a turn began. `RECUR_TURN_SOURCE` (`user` or `notify`) and `RECUR_MESSAGE_FILE`.
- `turn_end`: a turn finished. `RECUR_TURN_STATUS` (`ok`, `error`, or `cancelled`),
  `RECUR_TURN_DURATION_MS`, `RECUR_TURN_SOURCE`, and `RECUR_ERROR` when it failed.
- `idle`: interface only. Work finished and no follow-up turn starts.
- `error`: an error occurred during a turn. `RECUR_ERROR`.

Input:
- `steer_queued`: interface only. A steering message was queued during a turn. `RECUR_MESSAGE_FILE`.
- `steer_accepted`: the active turn accepted a queued message. `RECUR_MESSAGE_FILE`.
- `notify_received`: `recur notify` delivered a message. `RECUR_NOTIFY_FILE`.
- `image_attached`: an image was attached. `RECUR_IMAGE_SOURCE` is `paste` (TUI clipboard), `view`
  (`recur view`, when the image is handed to the model), or `input` (an image in an accepted headless
  `input` request). See `recur docs headless`.

Model:
- `model_change`: `/model` changed the selection. `RECUR_PREV_PROVIDER`, `RECUR_PREV_MODEL`,
  `RECUR_PREV_EFFORT`.
- `model_request_start`: a provider request began.
- `model_request_end`: a provider request finished. `RECUR_DURATION_MS`,
  `RECUR_INPUT_TOKENS`, `RECUR_OUTPUT_TOKENS`.
- `model_retry`: a transient request failure is being retried. `RECUR_ERROR`, `RECUR_ATTEMPT`.

Shell and compaction:
- `shell_start`: an agent bash tool call begins. `RECUR_COMMAND_FILE`, `RECUR_TIMEOUT`.
  It covers agent tool calls only, not hook or status bar processes.
- `shell_end`: an agent bash tool call ends. `RECUR_EXIT_CODE`, `RECUR_DURATION_MS`,
  `RECUR_TIMED_OUT`.
- `compact_start`: compaction began. `RECUR_COMPACT_REASON` (`manual` or `auto`).
- `compact_end`: compaction finished. `RECUR_COMPACT_STATUS` (`ok` or `error`), `RECUR_ERROR`.
