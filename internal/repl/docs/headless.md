# Headless JSON/stdio transport and notifications

## Running Headless

`recur --mode jsonl` starts a long-lived control service. It reads
newline-delimited JSON requests from stdin, writes results and events to stdout,
and waits. Launching does not start work: no model request, no turn, and no new
session file are created until a stdin `input` request (or an explicit
notification) arrives. `--resume ID` opens an existing project session without
running it. Without `--resume`, the service starts a new session that is saved
after its first completed message.

Flags: `--mode jsonl`, `--resume ID`, `--config PATH`, `--no-extend`. The former
`--prompt` flag and the plain-text output mode are removed; every headless
interaction uses this transport.

Framing:
- Each stdin record is one UTF-8 JSON object on one line. Blank lines are ignored.
- Each stdout line is one JSON object with a monotonically increasing `seq`.
  Nothing else is written to stdout; diagnostics go to stderr.
- A record larger than 2 MiB is discarded and reported as `record_too_large`;
  the stream stays in sync. This transport limit is separate from the per-file
  and per-image limits below.
- Invalid JSON, or a request without `id`, yields a protocol error without an id
  (`invalid_json`, `missing_id`). The session continues.

## Startup record

```json
{"type":"ready","seq":1,"protocol":1,"version":"0.6.0","cwd":"/work/project",
 "state":{"session":{"id":"…","model":"…","provider":"…","effort":"…","ctx_size":null},"busy":false,"cancelling":false,"compacting":false,"queued":0},
 "capabilities":{"requests":["input", "…"],"max_record_bytes":2097152,"max_images":8,"max_file_bytes":65536}}
```

## Requests

Every request has a unique `id` among requests still in flight, a `type`, and
optional fields. Images and files are paths; relative paths resolve against the
project directory.

| Request | Fields | Idle | Busy (a turn or compaction is active) |
| --- | --- | --- | --- |
| `input` | `text`, `images`, `files` | starts a turn (`status: started`, `turn`) | queued as steering (`status: queued`); refused while compacting (`busy`) or after a stop (`cancelling`) |
| `steer` | same as `input` | `not_busy` error | same as busy `input` |
| `recall` | none | `not_recallable` | returns the newest unaccepted steering (`status: recalled`, `input`); once accepted, `not_recallable` |
| `stop` | none | `status: idle` | cancels the active operation (`status: cancelling`); the terminal `done` follows |
| `quit` | none | saves and closes (`status: closed`) then `shutdown` | cancels, then saves and closes after `done` (`status: deferred`) |
| `new` | none | starts a new session (`status: ok`), emits `session` | cancels, then switches after `done` (`status: deferred`) |
| `resume` | `args.session_id` | opens the session (`status: ok`), emits `session` | as `new`; unknown ids are `not_found` |
| `sessions` | none | lists `{id, title, model, updated}` | allowed (read-only) |
| `history` | none | display records of the session | allowed (read-only) |
| `state` | none | session, busy flags, queued count | allowed (read-only) |
| `rename` | `args.title` (empty restores the default) | renames | `busy` |
| `reload` | none | re-reads configuration, `notice` | `busy` |
| `compact` | `text` (optional guidance) | background summary (`started`, then `done` with `operation: compact`) | `busy` |
| `help` | none | commands and request types | allowed (read-only) |
| `models` | none | lists provider models; runs in the background, result arrives later | `busy` |
| `providers` | none | lists providers: `name`, `base_url`, `api_key_env`, `api_key_configured` | `busy` |
| `select_model` | `args.provider`, `args.model`, `args.effort` (optional) | saves the selection, `notice` | `busy` |
| `select_provider` | `args.name` | keeps the model and effort, saves the provider | `busy` |
| `add_provider` | `args.name`, `args.base_url`, `args.api_key` (optional) | saves a provider, `data` has `destination` and `project_config` | `busy` |
| `command` | `name`, `args` | runs the matching request above | same as the matching request |

`command` names: `/new`, `/resume` (`args.session_id`), `/rename`
(`args.title`), `/compact` (`args.prompt`), `/reload`, `/quit`, `/help`, `/model`
(no arguments lists models; `args.provider`/`args.model` selects),
`/login` (`args.base_url` adds; `args.name` selects; no arguments lists providers).
An unknown name is `unknown_command`. Slash-prefixed text sent as `input` is an
ordinary message.

Attachments:
- `images`: up to 8 paths per request. Each is captured as a bounded preview and an
  original snapshot kept in `$RECUR_HOME/attachments/`. Invalid files are
  `invalid_image`; more than 8 pending images is `attachment_limit`.
- `files`: each path is a regular UTF-8 text file inside the project root, read
  when the request is accepted. Content is sent with the prompt as escaped,
  labelled data, up to 64 KiB per file (larger files are marked truncated).
  Outside-root, special, binary or unreadable files are `invalid_file`, and no
  turn starts.
- A queued input keeps its snapshot; a later steering or retry uses that snapshot.

## Terminal parity

Every terminal action has one request; both interfaces run the same operations.

| Terminal action | JSON request |
| --- | --- |
| Enter on a message | `input` (queued as steering while a turn runs) |
| Shift+Enter, Ctrl+J, typing | composes the `text` field |
| Ctrl+V paste, Ctrl+X remove | `images` (paths) |
| `@` file completion | `files` (explicit paths; no completion menu) |
| Esc or Ctrl+C while working | `stop` |
| Up on an empty composer | `recall` |
| `/new`, `/resume <id>` | `new`, `resume` |
| `/rename <title>` | `rename` |
| `/compact [prompt]` | `compact` |
| `/reload` | `reload` |
| `/model` and its picker | `models`, `select_model`, `select_provider` |
| `/login` and its picker | `providers`, `add_provider`, `select_provider` |
| `/help` | `help` |
| `/quit`, Ctrl+D, Ctrl+C when idle | `quit` |

Interface-only behavior (scrolling, rendering, completion menus, keyboard
handling, and the status bar) has no JSON equivalent.

## Results

Exactly one result is emitted for every request with an id:

```json
{"type":"result","seq":4,"id":"r1","ok":true,"status":"started","turn":"turn-2"}
{"type":"result","seq":5,"id":"r2","ok":false,"error":{"code":"busy","message":"Wait for the active turn before reloading settings."}}
```

Error codes: `invalid`, `busy`, `cancelling`, `not_busy`, `queue_full`,
`not_recallable`, `attachment_limit`, `pending_images`, `setup_required`,
`not_found`, `unknown_command`, `duplicate_id`, `invalid_image`, `invalid_file`,
`closed`, `failed`. A rejected request never changes the session.

## Events

Events arrive in emission order with their `seq`:

- `started`: `turn`, `operation` (`turn` or `compact`), `source` (`user`, `notify`,
  `queued` for a promoted follow-up, or `compact`).
- `event`: `turn` and `event`, the agent's display event with its fields kept:
  `kind` (for example `user`, `delta`, `assistant`, `code`, `output`, `error`,
  `notice`, `withdraw`), `text`, `id`, `meta`, `final`, `steering`, `provisional`.
- `notice`: `text`, for example `Settings reloaded.` or `Turn cancelled.`.
- `error`: `error.code` and `error.message` for a failure that is not a turn result.
- `session`: `session` describes the session that is now active, after `new`,
  `resume`, or a compaction.
- `done`: exactly once per `started`, with `status` (`ok`, `error`, or
  `cancelled`), `error` text, `message` (compaction summary or cancellation),
  and `unsent`: inputs the model did not accept, as `{text, images, files}`.
  Those paths can be resubmitted. After a clean finish, queued input starts the
  next turn at once (`started`, `source: queued`); the finished turn's `done` is
  emitted first.
- `shutdown`: the last record, with `reason` (`quit`, `eof`, `signal`,
  `broken_output`, or `output_overflow`).

A stop acknowledgment (`result`, `status: cancelling`) is distinct from the
terminal `done` with `status: cancelled`.

## Lifecycle

- stdin end (EOF): no new inputs are accepted; accepted work drains (its `done`
  is emitted), the session is saved, then `shutdown` with `reason: eof`.
- `quit`: cancels active work, reports unaccepted inputs in `done.unsent`, saves,
  and exits with `reason: quit`. A save failure is reported and the service
  keeps running.
- Interrupt (SIGINT/SIGTERM): active work is stopped, the session is saved, and
  the process exits with `reason: signal`.
- Broken stdout, or a backlog beyond the 4096-line output limit: active work is
  stopped and the process exits with `broken_output` or `output_overflow`.
- A failed turn is reported in `done` and does not end the service. The process
  exits 0 unless the transport itself fails.
- Duplicate in-flight ids are rejected with `duplicate_id`; at most 8 model listings
  run at once.

Stop, steering, and recall act at the same safe boundaries as the terminal
interface (see `recur docs steering`). Turns are never cancelled by a later
request other than `stop`, `quit`, `new`, `resume`, or an interrupt.

## Hook Isolation

Hooks (see `recur docs hooks`) are disabled by default in headless runs.
Implicit project or global configuration files do not activate hooks. They run
only with an explicit `--config PATH`; add `--no-extend` to avoid inheriting
global hooks.

In headless mode:
- `RECUR_MODE` is `headless`.
- Interface-only events never fire: `idle` and `steer_queued` are omitted, and
  `image_attached` fires only for JSON images, with `RECUR_IMAGE_SOURCE=input`.
- Background status bar subprocesses and `@` file path indexing never run.

## Credentials

API keys are never returned or echoed. `providers` reports whether a key is
configured and the name of its environment variable, never the value. Base URLs
are returned without user information or query strings. Error text from
`add_provider`, `select_model`, and `models` is scrubbed of configured secrets.

## External Notifications (recur notify)

`recur notify 'message'` sends text to the active session. Within a Recur shell
or hook process, the session endpoint is detected automatically from
`RECUR_ENDPOINT`; outside a session, pass `-s SESSION_ID`. Without message
arguments, it reads from stdin. Use `--` before message arguments that start with
a dash.

Delivery behavior:
- If the agent is idle, `recur notify` starts a new turn immediately
  (`started`, `source: notify`).
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

## Example

```sh
printf '%s\n' \
  '{"id":"r1","type":"input","text":"Review main.go","files":["main.go"]}' \
  '{"id":"r2","type":"steer","text":"Focus on error handling"}' \
  '{"id":"r3","type":"rename","args":{"title":"Review"}}' | recur --mode jsonl
```

Read stdout line by line: `ready`, then the `result` for `r1` and its
`started`, `event`, and `done` records, then `r2` queued, and so on. Send `stop`
to end a turn early; its `done` has `status: cancelled` and any unaccepted steering in `unsent`.
