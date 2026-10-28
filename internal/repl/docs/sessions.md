# Sessions and configuration

RECUR_HOME defaults to ~/.recur; an explicit relative override is resolved against
the project directory. This does not change the working directory of shell
commands. settings.toml, system.md, and prefix.sh are created only when missing;
existing editable configuration is never overwritten automatically.

Each project has independent JSONL session files below
$RECUR_HOME/sessions/<project-key>/. The key combines a sanitized basename and
path hash. Files are atomic, count-checked snapshots, not an append-only event
stream. They preserve API history, display records, metadata, and notifications.
Saving happens at turn completion. Startup opens a new session and creates its
JSONL file only after the first message, unless --resume selects an existing session.

/new retains older sessions and creates a new chat and shell. /resume provides
project-scoped session completions. Switching waits for active work to stop and
isolates shell state; restored chats get a new prefix notification.

/compact replaces everything before the last compact_keep_turns turns with one
model-written summary and saves immediately; max_context_tokens (default 400000,
0 disables) triggers the same compaction automatically before an oversized
request. /reload re-reads settings.toml, prefix.sh and system.md and restarts the
shell while keeping the current session and its history.

One client owns the session writer lock; other clients are read-only followers
that reload committed snapshots once per second. No live stream is broadcast.
Locks are released by the OS after crashes; reopen a follower to acquire released
ownership. Session headers retain their own provider, model, and effort.
recur docs models and recur docs headless describe selection and transport details.
