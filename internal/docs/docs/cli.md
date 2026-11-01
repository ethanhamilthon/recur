# Command line reference

This topic owns every command, flag, environment variable and exit code.

## Commands

- `recur`: start the interactive TUI in the current directory (the project root).
- `recur --mode jsonl`: run without the TUI, JSONL over stdin and stdout. This is the only
  non-interactive mode (see `recur docs headless`).
- `recur notify [-s SESSION_ID] [--] TEXT...`: send text to a running session. Reads stdin when
  no text is given. Inside a recur shell the session comes from `RECUR_ENDPOINT`. Elsewhere `-s` is required.
- `recur view [-s SESSION_ID] PATH...`: queue PNG or JPEG images for the next model request
  (see `recur docs images`).
- `recur docs [TOPIC]`: print built-in documentation. `recur docs index` lists the topics.
- `recur update [--check]`: install the latest release, or only report whether one exists
  (see `recur docs update`).
- `recur version`: print the installed version.

Go-style flags accept one or two dashes: `-config` and `--config` are the same.

## Flags

- `-mode jsonl`: run the headless JSONL transport instead of the TUI.
- `-resume SESSION_ID`: reopen an existing project session, in the TUI or headless. An unknown ID is an error.
- `-config PATH`, `-c PATH`: use this file as the primary configuration (precedence in `recur docs config`).
- `-no-extend`: do not inherit the global `$RECUR_HOME/recur.toml`.
- `-s SESSION_ID`: target session for `notify` and `view`.

Positional arguments are an error except for `notify` and `view`. The removed `-prompt` flag
is rejected. `-mode` accepts only `jsonl`.

## Environment variables

One table covers every `RECUR_*` variable. Columns say where a variable is available:
- Shell: the agent's bash tool calls (the process inherits the rest of the launch environment).
- Hooks: hook processes (`recur docs hooks`).
- Status: `status_left` and `status_right` commands (`recur docs interface`).

Variables you set (inherited means the value comes from the launch environment, so it reaches all three):

| Variable | Meaning | Shell | Hooks | Status |
| --- | --- | --- | --- | --- |
| `RECUR_HOME` | Data and global configuration directory. Default `~/.recur`; a relative value resolves against the launch directory. Read at startup. | inherited | inherited | inherited |
| `RECUR_NO_UPDATE_CHECK` | Any nonempty value disables the startup update check (see `recur docs update`). | inherited | inherited | inherited |

Variables recur sets for every call or event:

| Variable | Meaning | Shell | Hooks | Status |
| --- | --- | --- | --- | --- |
| `RECUR_ENDPOINT` | Session socket used by `recur notify` and `recur view`. Do not set it by hand except to target an outer session (see `recur docs cookbook`). | yes | yes | lifecycle events only |
| `RECUR_SESSION_ID` | ID of the active session. | yes | yes | yes |
| `RECUR_PROVIDER` | Active provider name. | yes | yes | yes |
| `RECUR_MODEL` | Active model name. | yes | yes | yes |
| `RECUR_EFFORT` | Active reasoning effort. Empty when unset. | yes | yes | yes |
| `RECUR_CTX_SIZE` | Latest request input tokens. Empty if unknown. | yes | no | yes |
| `RECUR_CWD` | Project directory. The shell starts there already, so the variable is not exported to it. | no | yes | yes |
| `RECUR_VERSION` | Recur version. Hooks get it on `recur_start`; the status bar gets it on every event. | no | recur_start only | yes |
| `RECUR_STATE` | `idle`, `busy` or `cancelling`. | no | no | yes |
| `RECUR_MODE` | `tui` or `headless`. | no | yes | no |
| `RECUR_EVENT` | Name of the lifecycle event. The status bar's first render gets `init` and a `/reload` gets `reload`. | no | yes | yes |
| `RECUR_SEQ` | Monotonic event sequence number. | no | yes | no |
| `RECUR_TS` | Unix timestamp in milliseconds. | no | yes | no |

Event variables (`RECUR_SESSION_SOURCE`, `RECUR_PREV_SESSION_ID`, `RECUR_SESSION_TITLE`,
`RECUR_REASON`, `RECUR_EXIT_CODE`, `RECUR_TURN_SOURCE`, `RECUR_TURN_STATUS`,
`RECUR_TURN_DURATION_MS`, `RECUR_ERROR`, `RECUR_PREV_PROVIDER`, `RECUR_PREV_MODEL`,
`RECUR_PREV_EFFORT`, `RECUR_DURATION_MS`, `RECUR_INPUT_TOKENS`, `RECUR_OUTPUT_TOKENS`,
`RECUR_ATTEMPT`, `RECUR_TIMEOUT`, `RECUR_TIMED_OUT`, `RECUR_COMPACT_REASON`,
`RECUR_COMPACT_STATUS`, `RECUR_IMAGE_SOURCE`, and the payload paths `RECUR_MESSAGE_FILE`,
`RECUR_NOTIFY_FILE`, `RECUR_COMMAND_FILE`) are set only on the events that carry them.
Hooks receive all of them. The status bar receives the ones that are not `*_FILE` and not
`RECUR_TIMED_OUT`. The shell receives none. Meanings are listed per event in `recur docs hooks`.

Configured `[env]` values are merged into the shell, hook and status environments. The `RECUR_`
prefix is reserved for recur (see `recur docs config`).

## Exit codes

- `recur` and its subcommands: `0` on success; `1` on any error, with the message on stderr.
  A failed headless turn is reported in its `done` record and does not change the exit code.
- Bash tool results: `124` is a timeout, `130` is a cancellation, `127` means the shell could
  not start, and `2` is a syntax error or invalid tool arguments. Otherwise the command's own status is returned.
