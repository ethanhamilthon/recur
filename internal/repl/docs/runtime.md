# Shell runtime

The bash tool runs commands in system Bash (`/bin/bash --noprofile --norc`).
Each call starts a fresh process in the project directory; variables, functions,
aliases and working directory changes do not persist between calls.
Programs that run as separate processes, including scripts executed through
their shebang, use whatever interpreter they name.

Because each call is stateless, related operations should be executed within a
single command or organized into reusable scripts. External programs are found
through PATH and run with the user's permissions in their own process group.
The directory of the running recur binary comes first in PATH, so recur notify,
recur view and recur docs always work, including inside scripts. There is no
TTY and stdin is empty; PAGER and GIT_PAGER are cat. ~/.bashrc, ~/.zshrc and other
shell startup files are not read, and inherited BASH_ENV is excluded. Exported
variables from the launch environment and configured [env] settings are provided.

Each call returns {output, exit_code}. Stdout and stderr are merged. The
max_output argument sets how many bytes are kept: default 8192 (first 4 KiB and
last 4 KiB with a truncation notice in the middle); 0 disables truncation.
Prefer filtering with rg, grep, head, tail or sed -n. Commands parse and run
according to standard Bash syntax and semantics; a syntax error runs nothing and
returns exit_code 2.

timeout is given per call in seconds: default 120, maximum 600. A timeout
(exit_code 124) or cancellation (exit_code 130) terminates the command's
process group.

Detached or long-running work must explicitly redirect stdout and stderr to
files, and notify upon completion using recur notify:

  (make release > /tmp/release.log 2>&1; recur notify "release: exit $?") &

Background processes are not persistent session jobs. Background tasks that do
not redirect their output may be unblocked when output pipes close. Tracked
process groups are terminated on cancellation.

Every call exports session metadata: RECUR_SESSION_ID, RECUR_MODEL,
RECUR_PROVIDER, RECUR_EFFORT, RECUR_CTX_SIZE and RECUR_ENDPOINT (the session
socket used by recur notify and recur view).

Hooks (recur docs hooks) receive session variables and RECUR_CWD.

The shell is not an OS security sandbox.
