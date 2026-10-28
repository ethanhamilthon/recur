# Image viewing

recur view PATH... reads PNG or JPEG images, captures their bytes and sends them
to the running session through RECUR_ENDPOINT (or -s SESSION_ID outside a recur
shell). Relative paths resolve against the current directory of the command.
Limits: 2 MiB and 20 megapixels per image, and at most four images waiting for
one model request; further images fail until the queue is delivered. Invalid
files fail with a nonzero exit status. Because it is an ordinary command, it can
be used from scripts and background jobs.

Queued images are attached as actual image content in a separate user
notification right before the next model request, after all tool outputs of the
current response. They do not start a new turn on their own; combine with
recur notify when a background job should wake the agent.

Captured image data is stored directly in API history; restoration does not
require the original file. Images can make session files larger and may contain
sensitive data. Provider image support is required. recur docs clipboard
describes paste attachments; images are shown as labels only, never rendered as
terminal graphics.
