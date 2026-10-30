# Hooks

Hooks run your commands when Recur emits lifecycle events, for example to show
the agent state in a terminal multiplexer or to send a desktop notification.
They observe Recur; they cannot block, change or veto anything.

## Files

hooks.toml is read from the first of these that exists:

1. `--hooks FILE`
2. `./.recur/hooks.toml` in the launch directory (with enable_local_recur = true,
   unless that .recur is RECUR_HOME)
3. `$RECUR_HOME/hooks.toml` (default ~/.recur/hooks.toml)

The first match replaces the others; files are never merged. Recur never creates
hooks.toml. Without a file there are no hooks. The start screen lists where
system.md, prefix.sh and hooks.toml were read from. /new, /resume and /reload
re-read the file; if it is invalid, /reload fails and the running hooks stay.
Headless runs (--prompt, --mode jsonl) use hooks only when --hooks is given, so
a recur started by an agent never overwrites the status of its parent.

## Format

```toml
[[hook]]
event = "turn_start"            # one event, or "*" for all of them
command = "echo working > ~/.recur-state"
timeout = 10                    # seconds, 1..600, default 10

[[hook]]
events = ["turn_end", "idle"]   # several events
command = "~/bin/notify-done.sh"
```

Every hook needs event (or events) and command. Unknown events, unknown keys
and syntax errors are startup errors that name the file. The command runs with
`/bin/sh -c` in the project directory, with the environment of the recur process
plus the variables below.

## Delivery

Hooks are asynchronous: Recur queues the event and continues at once. Each hook
has its own queue (64 events), so one hook sees events in the order they
happened, and a slow hook never delays another one. The queue is kept across
/new and /reload while the hook is unchanged. RECUR_SEQ increases with every
event if a script needs to order events from different hooks.

stdout and stderr of hooks are captured, never printed, so they cannot corrupt
the terminal. A hook that exits nonzero, fails to start, times out (its process
group is killed) or overflows its queue is recorded in `$RECUR_HOME/hooks.log`
with the event, command and the first 2 KiB of output. Nothing else is logged.
On exit Recur waits up to 3 seconds for queued hooks, so recur_exit hooks have a
moment to finish.

## Variables

Every event sets RECUR_EVENT, RECUR_SEQ, RECUR_TS (Unix milliseconds),
RECUR_MODE (tui or headless), RECUR_SESSION_ID, RECUR_CWD, RECUR_PROVIDER,
RECUR_MODEL, RECUR_EFFORT and RECUR_ENDPOINT, the session socket, so a hook can
call `recur notify`. Unset values are empty.

Variables ending in `_FILE` hold the path of a temporary file with the text; it
exists while the hooks for that event run, then it is removed. Copy it if you
need it later. Errors are cut to 2000 bytes.

## Events

Process and session:

- `recur_start`: recur started and its session is open. RECUR_VERSION.
- `recur_exit`: the process is ending. RECUR_EXIT_CODE (0 or 1).
- `session_start`: a session became active. RECUR_SESSION_SOURCE is startup,
  resume or new; RECUR_PREV_SESSION_ID after /new and /resume.
- `session_switch`: /new or /resume replaced the session. RECUR_PREV_SESSION_ID.
- `session_end`: the session is being left. RECUR_REASON is switch or exit.
- `session_title_change`: the /resume title changed (first message, /rename,
  restored session). RECUR_SESSION_TITLE.

Turns:

- `turn_start`: a turn began. RECUR_TURN_SOURCE is user or notify;
  RECUR_MESSAGE_FILE holds the text.
- `turn_end`: the turn finished. RECUR_TURN_STATUS is ok, error or cancelled;
  RECUR_TURN_DURATION_MS; RECUR_ERROR when it failed.
- `idle`: interface only. Work ended and no follow-up turn starts: after a turn
  (queued messages go back to the composer or start a new turn first) and after
  /compact. Use it for the "done" state.
- `error`: an error was reported during a turn, including failed saves and failed
  automatic compaction. RECUR_ERROR.

Input:

- `steer_queued`: interface only. A message typed during a turn was queued.
  RECUR_MESSAGE_FILE.
- `steer_accepted`: the running turn took a queued message. RECUR_MESSAGE_FILE.
- `notify_received`: recur notify delivered text that Recur picked up.
  RECUR_NOTIFY_FILE holds the wrapped notification.
- `image_attached`: an image arrived. RECUR_IMAGE_SOURCE is paste or view.

Model:

- `model_change`: /model changed provider, model or effort. RECUR_PREV_PROVIDER,
  RECUR_PREV_MODEL, RECUR_PREV_EFFORT; the RECUR_* model variables are the new ones.
- `model_request_start`, `model_request_end`: one provider request. The end event
  has RECUR_DURATION_MS, RECUR_INPUT_TOKENS and RECUR_OUTPUT_TOKENS (0 when the
  provider reports no usage). Failed requests produce `error` and `turn_end`.
- `model_retry`: a request is retried. RECUR_ERROR, RECUR_ATTEMPT (the next one).

Shell and compaction:

- `prefix_start`, `prefix_end`: the prefix runs in a fresh shell. The end event has
  RECUR_PREFIX_OK (true or false) and RECUR_DURATION_MS.
- `shell_start`: the bash tool starts. RECUR_COMMAND_FILE, RECUR_TIMEOUT (seconds).
- `shell_end`: RECUR_EXIT_CODE, RECUR_DURATION_MS, RECUR_TIMED_OUT (true when the
  exit code is 124).
- `compact_start`: RECUR_COMPACT_REASON is manual or auto. Not sent when there is
  nothing to compact.
- `compact_end`: RECUR_COMPACT_STATUS is ok or error; RECUR_ERROR.

MCP calls and top-level background jobs are ordinary shell commands and have no
events of their own.

## Example: agent state

```toml
[[hook]]
events = ["turn_start", "compact_start"]
command = "echo working > /tmp/recur-$RECUR_SESSION_ID"

[[hook]]
event = "idle"
command = "echo done > /tmp/recur-$RECUR_SESSION_ID"

[[hook]]
event = "recur_exit"
command = "rm -f /tmp/recur-$RECUR_SESSION_ID"
```

Hooks run commands with your permissions, like the bash tool. A project
.recur/hooks.toml is code from that repository: review it before opening
untrusted projects, or set enable_local_recur = false.
