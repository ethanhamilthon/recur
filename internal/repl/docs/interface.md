# Interface

Commands:
- `/new`: starts a new chat and session.
- `/resume`: selects a project session to reopen.
- `/compact [prompt]`: summarizes older turns beyond `compact_keep_turns`.
- `/reload`: re-reads configuration and updates settings for subsequent commands.
- `/model`: selects active model and reasoning effort.
- `/login`: adds or updates provider credentials.
- `/help`: displays available commands and shortcuts.
- `/quit`: saves history and exits Recur.

Up/Down navigates completions, Tab completes, and Enter immediately executes
a selected visible slash command. Without a matching visible completion,
slash-prefixed text is treated as an ordinary prompt message. Esc dismisses
active completion menus.

## File Path Completion (@)

Typing `@` in multiline composer text triggers cursor-local file path
autocompletion:
- Path search is asynchronous within the launch project root. Recur queries Git
  tracked and unignored untracked files (`git ls-files`), with a bounded
  directory-walk fallback. `.git`, `.recur`, and `node_modules` are excluded,
  and directory symlinks are never traversed.
- Email addresses (e.g. `user@example.com`) and URLs (`https://.../@name`) are
  recognized and ignored.
- The menu shows at most 8 candidates. Up/Down navigates candidates, while Tab or
  Enter inserts the selected relative path into the composer.
- Accepting a path replaces only the active `@` token, preserving existing text
  and rune cursor position. Paths containing spaces or quotes are quoted
  automatically (e.g. `@"path with spaces/file.txt"`).
- Accepting a directory appends `/` and keeps completion open to explore that
  directory.
- Accepting a file path DOES NOT submit the prompt or execute a command. Only a
  subsequent Enter submits the message.
- Works seamlessly with draft text, attached images, and during busy turns: Enter
  accepts the file path first without clearing image attachments or submitting
  steering prematurely.
- Esc closes active or scanning completion menus before cancelling an in-flight
  turn.
- Selecting a file inserts only its quoted `@path`. Content is attached when the
  prompt is submitted; see the next section.

## @ File References

A submitted prompt attaches the content of each `@path` it names:
- Each distinct path is read once, in first-mention order, relative to the launch
  project root. Repeated references attach once.
- Only regular UTF-8 text files attach. Directory references are not expanded.
  NUL-containing or non-UTF-8 files, special files, and paths or symlinks that
  resolve outside the project root report an error. Images keep their own flow.
- Reading happens in the background when you press Enter, not when the path is
  selected. The draft stays in the composer until the files are read. Esc cancels
  the read and keeps the draft. If the draft changes during the read, your edits
  are kept and nothing is sent; press Enter again.
- A missing or unreadable file reports an error and keeps the draft, so the model
  never receives a prompt without the content you asked for.
- Each file is limited to `max_file_bytes` (default 64 KiB; `0` is unlimited).
  Longer files send their leading bytes, marked as truncated with the included and
  original sizes. The limit applies per file only; several large files can still
  exceed the model's context window. See `config.md`.
- Emails, URLs, escaped `\@` and a bare `@` attach nothing.
- The composer and transcript show only the prompt. The model receives the prompt
  and the file contents in one user message. File contents are labelled as data,
  with their boundaries escaped, but they are not filtered for secrets or
  instructions.
- A queued steering instruction keeps the snapshot taken when it was submitted,
  including when it is promoted to a follow-up turn. Recalling a queued prompt
  restores editable text, and resubmitting reads the files again.
- Notifications and headless prompts are not scanned for `@` references.

## Padded Status Bar

The bottom status bar row has exactly 2 display-cell spaces padding on each side
(`"  "` + content + `"  "`). Available content width is `max(0, width - 4)`.
Narrow terminal widths (1 to 5 cells, 80 cells) are handled cleanly without
wrapping or negative padding. Wide Unicode characters and emojis are truncated
with display-width awareness.

Status content is configured in `recur.toml` or `config.toml` via `status_left`
and `status_right` command arrays under `[ui]`:
- Commands run in `/bin/bash --noprofile --norc -c` in the project directory.
- Inherited `BASH_ENV` is stripped.
- Commands receive the process environment plus `[env]` and raw `RECUR_*`
  metadata: `RECUR_SESSION_ID`, `RECUR_CWD`, `RECUR_PROVIDER`, `RECUR_MODEL`,
  `RECUR_EFFORT`, `RECUR_CTX_SIZE` (latest request input tokens, empty if
  unknown), `RECUR_VERSION`, `RECUR_STATE` (`idle`, `busy`, `cancelling`), and
  `RECUR_ENDPOINT`.
- There are no `RECUR_*_LABEL` variables or Go formatting APIs; shell commands
  assemble labels and handle missing values themselves.
- Nonempty stdout outputs from commands are trimmed, flattened, stripped of
  ANSI/OSC sequences, and joined with `" · "` in declared order. Empty or
  failing commands are omitted.
- Setting `status_left = []` or `status_right = []` hides that side.
- Execution is bounded: 500 ms deadline per command, 1 KiB output limit, and
  max 4 concurrent worker processes.
- Refreshes are event-driven: evaluation triggers on EVERY Recur lifecycle event
  (all 22 events), initial render, and successful `/reload`. There is no
  polling, timer, or streaming token refresh.
- Rapid lifecycle events cancel in-flight status commands and restart evaluation
  for the new snapshot.
- Status commands do not emit lifecycle events (no refresh recursion).
- No status commands run in headless mode.

## Editing and Navigation

- Alt+Enter or Ctrl+J inserts a newline in the composer. Shift+Enter is
  supported on compatible terminals.
- PgUp/PgDn scroll the timeline; Ctrl+Home/End jump to its start and end.
- Ctrl+C cancels active work, or exits when idle.
- Up Arrow with an empty composer recalls unaccepted queued steering.
- Dragging the left mouse button over the timeline selects text and copies it to
  the system clipboard on mouse release; the mouse wheel scrolls.
- `[ui]` in `recur.toml` controls visible body rows through `max_code_lines`,
  `max_result_lines`, and `max_thinking_lines`; setting a limit to zero hides
  that block's body while preserving headers.
- `recur docs clipboard` covers image paste shortcuts (`Ctrl+V`).
- `recur docs headless` covers the `--mode jsonl` non-interactive transport. The
  terminal interface and that transport run the same operations: input,
  steering, recall, stop, session changes, commands, and provider setup.
