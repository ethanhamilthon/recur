# Headless transports and notifications

recur -prompt 'request' runs a single turn. Without --resume ID, headless mode
creates a new project session. --resume accepts a full existing session ID from
this project; an unknown ID is an error, not a new session.

Hooks (recur docs hooks) are off in headless runs unless --hooks FILE is given;
then RECUR_MODE is headless and the interface-only events (idle, steer_queued,
image_attached with source paste) never occur.

Without --mode, --prompt prints only the completed assistant answers to stdout;
errors go to stderr. recur --mode jsonl reads one
{"prompt":"text"} object per line. JSON strings can contain escaped newlines.
JSONL stdout contains session, display events, and done records. Events have
type, text, id, meta, and provisional fields. done includes ok and session
metadata. Use --prompt with --mode jsonl for one JSONL-output turn. Input EOF
ends the process; keep stdin open for an idle long-lived client. A failed turn,
invalid input or cancellation ends processing with nonzero exit status and full
diagnostics on stderr. Stdout has no terminal escape sequences added by the TUI.

recur notify 'message' sends to the running session. Inside a recur shell (and
any program it starts) the session comes from RECUR_ENDPOINT; elsewhere pass
-s ID for an active session of the current project. Without message arguments
it reads stdin. Use -- before message arguments starting with
a dash. The text is XML-escaped and wrapped in a notify block, then queued for
the next safe model request boundary. No extra user-authored message or tool
output is invented. An idle TUI starts a continuation within its one-second poll;
an idle headless client with open stdin starts one immediately. Active turns
accept notifications after all response tool outputs. Inactive sessions return
an error rather than storing a notification for later startup.

Notify accepts at most 128 KiB of message text and has a bounded 64-entry queue.
Its acknowledgement means the running owner enqueued the message, not that the
model accepted it or that it was durably saved. Pending notifications are
in-memory and can be lost on exit/crash or session switching; do not assume
exactly-once delivery after an ambiguous connection failure. There is no
automatic retry. Accepted notifications are persisted with normal history.

Only one client owns a session's writer lock. Opening a session that another
recur process owns fails with "session is in use"; reopen it after the owner
exits. OS locks are released even when a process crashes. Separate sessions can
be used concurrently.
