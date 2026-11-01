# Sessions

## Storage Layout

Each project keeps its own JSONL session files under `$RECUR_HOME/sessions/<project-key>/`.
The project key is the project directory's basename (sanitized, up to 40 characters), a hyphen
and the first 8 bytes of the SHA-256 of its absolute path, in hex. `RECUR_HOME` is documented in `recur docs cli`.

`$RECUR_HOME/attachments/` holds the full-resolution originals of images added with `recur view`
(see `recur docs images`), and of headless `images` when `attachment_dir` is empty. Clipboard captures
and headless `images` go to `attachment_dir` when it is set (see `recur docs config` and
`recur docs clipboard`).

## Persistence

A session file is an atomic, count-checked snapshot. It holds the complete API history, the
display records, request metadata and the delivered notifications.

Saving happens when a turn completes. A new session exists in memory and gets its file only after
its first message completes, unless `--resume` selects an existing session.

## Session Commands

- `/new`: keeps older sessions and opens a fresh conversation.
- `/resume`: selects a saved session of this project and reopens it. The switch waits for active
  work to finish and keeps the histories separate.
- `/quit`, and the JSON `quit` request, are the same during work: the turn is cancelled, then the
  session is saved and the process exits once the turn ends.
- `/compact [prompt]`: replaces turns older than `compact_keep_turns` with a model-written summary,
  then saves immediately. Compaction also runs by itself before a request whose estimated input
  exceeds `max_context_tokens`.

During work, `/new` and `/resume` cancel the turn and switch after it ends. A switch is refused
while pending images remain in the composer or queue, so they are never discarded.

Command list and key bindings: `recur docs interface`.

## Locks

Only one Recur process may own a session at a time. Opening a session that another process holds
fails with `session is in use by another recur process: ID` (see `recur docs troubleshooting`).
The OS releases the lock when the process exits or crashes.

Provider, model and effort are stored in each session header. Resuming restores them: see `recur docs models`.
