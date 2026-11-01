# Command line reference

This is the only place that lists every command, flag and environment variable.

## Commands

- `recur`: start the interactive TUI in the current directory (the project root).
- `recur --mode jsonl`: run without the TUI, JSONL over stdin/stdout. The only
  non-interactive mode (see `recur docs headless`).
- `recur notify [-s SESSION_ID] [--] TEXT...`: send text to a running session.
  Reads stdin when no text is given. Inside a recur shell the session is taken
  from `RECUR_ENDPOINT`; elsewhere `-s` is required.
- `recur view [-s SESSION_ID] PATH...`: queue PNG/JPEG images for the next model
  request (see `recur docs images`).
- `recur docs [TOPIC]`: print built-in documentation; `recur docs index` lists topics.
- `recur update [--check]`: install the latest release, or only report whether one exists.
- `recur version`: print the installed version.

Go-style flags accept one or two dashes (`-config` and `--config` are the same).

## Flags

- `-mode jsonl`: run the headless JSONL transport instead of the TUI.
- `-resume SESSION_ID`: reopen an existing project session (TUI and headless); an unknown ID is an error.
- `-config PATH`, `-c PATH`: use this file as the primary configuration
  (see `recur docs config` for precedence).
- `-no-extend`: do not inherit `$RECUR_HOME/config.toml`.
- `-s SESSION_ID`: target session for `notify` and `view`.

Positional arguments are an error except for `notify` and `view`.
The removed flag `-prompt` is rejected; `-mode` accepts only `jsonl`.

## Environment variables

- `RECUR_HOME`: data and global config directory (default `~/.recur`); a relative
  value resolves against the launch directory.
- `RECUR_NO_UPDATE_CHECK`: any non-empty value disables the startup update check.
- `RECUR_ENDPOINT`: set by recur inside shell calls and hooks; read by `notify`
  and `view`. Do not set it by hand except to target an outer session
  (see `recur docs cookbook`).
- `RECUR_*` metadata for shell, hooks and status commands: see `recur docs runtime`,
  `recur docs hooks`, `recur docs interface`.
- Provider API keys are read from whatever variable a provider's `api_key_env` names.

## Exit codes

`0` success; `1` any error (message on stderr). A failed headless turn is reported in its `done` record and does not change the exit code.
Inside the bash tool, `124` means timeout and `130` means cancellation.
