# Sessions and configuration

RECUR_HOME defaults to ~/.recur; an explicit relative override is resolved against
the project directory. This does not change the working directory of shell
commands. settings.toml, system.md, and prefix.sh are created only when missing;
existing editable configuration is never overwritten automatically. hooks.toml
is optional and never created (recur docs hooks).

Project config: with enable_local_recur = true (the default), recur reads
system.md, prefix.sh and hooks.toml from .recur in the launch directory when they
exist there; a missing file falls back to RECUR_HOME. The flags --system, --prefix
and --hooks name a file that wins over both. A .recur that is RECUR_HOME
itself is not treated as project config. settings.toml and sessions always come
from RECUR_HOME, and recur never creates files in the project .recur. The start
screen of the TUI shows, for system, prefix and hooks, the file that was read and
whether it came from a flag, the project or RECUR_HOME (hooks: none when there is
no file). When project files exist but enable_local_recur is false it says so.
/new, /resume and /reload re-read all of them.

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
request. /reload re-reads settings.toml, prefix.sh, system.md and hooks.toml and restarts the
shell while keeping the current session and its history.

One recur process owns a session at a time; opening it elsewhere fails with
"session is in use". Locks are released by the OS after crashes. Session headers retain their own provider, model, and effort.
recur docs models and recur docs headless describe selection and transport details.
