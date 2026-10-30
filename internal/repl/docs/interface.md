# Interface

Commands: /new starts a new chat; /resume selects a project session; Esc or
Ctrl+C cancels work; /quit saves/exits; /help shows commands and shortcuts; /compact
[prompt] summarizes older turns; /reload re-reads settings and prompts and
restarts the shell without changing the session.
Up/Down navigates completions, Tab completes, and Enter immediately executes
the selected visible completion. Without a matching visible completion,
slash-prefixed text is an ordinary message. Esc dismisses completions.
Otherwise Enter submits. Alt+Enter or Ctrl+J
inserts a newline; Shift+Enter is supported through compatible terminal
sequences. PgUp/PgDn scroll the timeline; Ctrl+Home/End jumps to its ends.
Ctrl+C cancels active work or exits when idle. Up with an empty composer can
recall pending steering. The composer scrolls with the timeline. Dragging the
left mouse button over the timeline selects text and copies it to the system
clipboard on release; the wheel still scrolls.

settings.toml controls visible body rows through max_code_lines,
max_result_lines, and max_thinking_lines; zero hides that block. Complete data
remains in history. Code argument streaming previews code generation, not tool
stdout while it runs. The status bar context estimate is the latest request's
input-token count, including cached input, not accumulated usage or capacity.

recur -prompt 'your request' runs one turn without the TUI. Diagnostics go to
stderr; answer text goes to stdout. /model selects a model and effort; /login selects or creates providers.
recur docs models explains both. recur docs headless covers JSONL, resume,
external notifications and session ownership. recur docs clipboard describes image attachments and terminal shortcut limitations;
the composer shows labels, never image pixels.
