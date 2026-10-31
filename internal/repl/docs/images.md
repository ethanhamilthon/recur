# Image viewing

recur view PATH... reads PNG or JPEG images, captures their bytes and sends them
to the running session through RECUR_ENDPOINT (or -s SESSION_ID outside a recur
shell). Relative paths resolve against the current directory of the command.
Inputs may be up to 32 MiB and 20 megapixels. Every image is prepared as a preview
with at most 2048 pixels on its longest side and at most 1 MiB of encoded data.
Aspect ratio is preserved (rounded to whole pixels); small images are not enlarged.
Transparent PNGs retain transparency. Opaque PNGs may become JPEGs to fit the byte
budget; previews may be reduced further if necessary. At most four images may
wait for one model request; further images fail until the queue is delivered. Invalid
files fail with a nonzero exit status. Because it is an ordinary command, it can
be used from scripts and background jobs.

Queued images are attached as actual image content in a separate user
notification right before the next model request, after all tool outputs of the
current response. They do not start a new turn on their own; combine with
recur notify when a background job should wake the agent.

Original files are never modified. An exact snapshot is saved under
$RECUR_HOME/attachments (default ~/.recur/attachments) with private permissions.
Its absolute path is included in model context so the agent can crop a detail
and call recur view again. Originals are retained for manual cleanup.
Only prepared previews are embedded in API history; restoration does not
require the original file. Existing history is not retroactively resized.
These defaults reduce provider incompatibilities but do not override provider
limits on image count or total request size. Images can make session files larger and may contain
sensitive data. Provider image support is required. recur docs clipboard
describes paste attachments; images are shown as labels only, never rendered as
terminal graphics.
