# Headless transports and notifications

## Running Headless

`recur -prompt 'request'` runs a single turn without starting the interactive
TUI. Without `--resume ID`, headless mode creates a new project session. Passing
`--resume ID` reopens an existing project session; an unknown ID produces an
error.

Output modes:
- Default: prints only completed assistant answer text to stdout; errors and
  diagnostics go to stderr.
- `--mode jsonl`: reads `{"prompt":"text"}` objects from stdin (one per line)
  and writes streaming JSONL events to stdout. Emitted objects include `session`,
  display events (`type`, `text`, `id`, `meta`, `provisional`), and a terminal
  `done` record containing session metadata. Closing stdin (EOF) exits cleanly.
  Combine `--prompt` with `--mode jsonl` for a single JSONL-formatted turn.

In headless mode, stdout contains raw program output without terminal escape
sequences.

## Hook Isolation

Hooks (see `recur docs hooks`) are disabled by default in headless runs. Implicit
project or global configuration files do not activate hooks.

Hooks run in headless mode ONLY when an explicit configuration file is supplied
via `--config PATH`. Combine `--config PATH` with `--no-extend` to ensure no
global hooks are inherited.

In headless mode:
- `RECUR_MODE` is set to `headless`.
- Interface-only lifecycle events (`idle`, `steer_queued`, and `image_attached`
  with source `paste`) never fire.
- Background status bar subprocesses and `@` file path indexing never run.

## External Notifications (recur notify)

`recur notify 'message'` sends text to the active session. Within a Recur shell
or hook process, the session endpoint is detected automatically from
`RECUR_ENDPOINT`; outside a session, pass `-s SESSION_ID`. Without message
arguments, it reads from stdin. Use `--` before message arguments that start with
a dash.

Delivery behavior:
- If the agent is idle, `recur notify` starts a new turn immediately.
- If the agent is currently working, `recur notify` queues the message as
  steering, delivered at the next safe boundary after all response tool outputs
  are committed.
- Unlike hook `send_output = true` (which queues output passively for the next
  turn without starting one), `recur notify` actively triggers a continuation
  or steering.

The message is XML-escaped and wrapped in a `<notify>` block. Notifications
accept up to 128 KiB of text with a bounded queue of 64 entries. Inactive
sessions reject notifications with an error rather than queuing them on disk.
Delivered notifications are saved into normal session history.

## Session Ownership and Locks

Each session is guarded by a process lock. Opening an active session owned by
another process fails with `"session is in use"`. File locks are released
automatically by the operating system upon process exit or crash.
