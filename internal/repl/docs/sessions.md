# Sessions and configuration

## Storage and Configuration Discovery

`RECUR_HOME` defaults to `~/.recur`; an explicit relative override resolves
against the project launch directory. `RECUR_HOME` holds session data and image
attachments independently of project configuration files:
- `$RECUR_HOME/sessions/<project-key>/`: JSONL session files. The project key
  combines a sanitized project directory basename and directory path hash.
- `$RECUR_HOME/attachments/`: originals saved by `recur view`.

Configuration discovery and layering are documented once in
`recur docs config`.

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
- `/rename [name]`: sets the session title shown in `/resume`; empty restores the
  first user message.
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
  (see `recur docs config`).
- `/reload`: re-reads configuration, applying updated settings, environment
  variables, and hook handlers while preserving the session, its history and
  its model selection (`recur docs interface`).

## Locks and Session Headers

Only one Recur process may own a session at a time; opening an active session
elsewhere fails with `"session is in use"`. OS file locks are released cleanly
even if a process crashes.

Session headers preserve their own provider, model, and reasoning effort
settings, so resuming an older session restores the model selection under which
it was recorded.
