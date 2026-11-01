# Sessions and configuration

## Storage and Configuration Discovery

`RECUR_HOME` defaults to `~/.recur`; an explicit relative override resolves
against the project launch directory. `RECUR_HOME` holds session data and image
attachments independently of project configuration files:
- `$RECUR_HOME/sessions/<project-key>/`: JSONL session files. The project key
  combines a sanitized project directory basename and directory path hash.
- `$RECUR_HOME/attachments/`: full-resolution captured clipboard images.

Configuration discovery resolves a primary configuration file in order:
1. `--config PATH`: explicit command-line path (relative paths resolve against
   launch cwd; errors fail immediately).
2. `./recur.toml`: project-root configuration in launch cwd.
3. `$RECUR_HOME/config.toml`: global user configuration.

Recur merges layers: Built-in defaults -> `$RECUR_HOME/config.toml` -> Primary
configuration. The `--no-extend` flag skips reading the global configuration file.
Tables deep-merge, while scalars and arrays replace inherited values.

## Session Files and Persistence

Each project maintains independent JSONL session files below
`$RECUR_HOME/sessions/<project-key>/`. Files are atomic, count-checked snapshots,
preserving complete API history, display records, request metadata, and
delivered notifications.

Saving occurs at turn completion. Startup opens a new session in-memory and
creates its JSONL file on disk only after the first message completes, unless
`--resume` selects an existing session.

## Session Commands

- `/new`: retains older sessions and opens a fresh conversation.
- `/resume`: provides project-scoped session completions to select and reopen a
  saved session. Switching waits for active work to finish and isolates history.
  During work, `/new` and `/resume` cancel the turn and switch after it ends. A
  switch is refused while unaccepted images are returned for editing, so they
  are never discarded.
- `/quit` and the JSON `quit` request behave the same way: during work they
  cancel, then save and exit once the turn ends.
- `/compact [prompt]`: replaces turns older than `compact_keep_turns` with a
  concise model-generated summary and saves immediately. Automatic compaction
  triggers before an oversized request if history exceeds `max_context_tokens`
  (default 400000; 0 disables).
- `/reload`: re-reads configuration (`recur.toml` or `config.toml`), applying
  updated settings, environment variables, and hook handlers to subsequent tool
  calls while preserving the current session and its history.

## Locks and Session Headers

Only one Recur process may own a session at a time; opening an active session
elsewhere fails with `"session is in use"`. OS file locks are released cleanly
even if a process crashes.

Session headers preserve their own provider, model, and reasoning effort
settings, so resuming an older session restores the model selection under which
it was recorded.
