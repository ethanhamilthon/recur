# Clipboard image attachments

The terminal never displays image pixels, Sixel, Kitty graphics, or base64 data.
The composer displays only an [Image N: filename] label. Ctrl+X removes the last
draft attachment and its captured file. Enter submits text and attached images;
an image-only message also works. Actual image bytes reach the model, not just
a path. Both Responses and Chat Completions receive their appropriate format.

Ctrl+V reads the local clipboard asynchronously with a three-second timeout.
On macOS, osascript's AppKit bridge converts native clipboard images to PNG;
ordinary text falls back to pbpaste. Linux requires wl-paste under Wayland or
xclip under X11; PNG clipboard images and ordinary text are supported. There is
no download/install step. Missing helpers and clipboard failures produce a
notice/error rather than clearing drafts. Ordinary terminal/bracketed text paste
continues to work. Text pasted into provider-key entry stays masked.

Cmd+V works when the terminal forwards it to Recur. Many terminals consume it
before applications see a key; in that case use Ctrl+V, or map Cmd+V to send
Ctrl+V (0x16). Supporting Cyrillic М/м on the physical V key requires a terminal
that reports modifier/key codes, such as Kitty CSI-u. Both English and Russian
reported key codes and alternate base-layout codes are recognized. Plain М/м
remains text, not a paste shortcut. Key release/repeat events do not duplicate
attachments. A legacy terminal cannot communicate modifiers it does not send.

attachment_dir in settings.toml selects the captured-image directory. Empty
uses the platform temporary directory/recur-images (often /var/folders/... on
macOS). Relative configured paths resolve against the project cwd. New
directories use 0700 and files 0600. Images use the shared view validator: PNG or
JPEG, at most 2 MiB and 20 megapixels after clipboard conversion. At most eight
clipboard images can be pending across the active input, queued instructions,
and current turn. The terminal never renders the files as graphics.

Attachments follow their instruction through steering, recall, late continuation
and cancellation recovery. Failed prefix initialization restores even image-only
input. Accepted messages are not resubmitted on cancellation. Pending images
must be submitted or removed before switching sessions. Late clipboard results
from a prior session or closed picker are discarded. Idle external notifications
do not submit images from an unsent draft.

Captured PNG files are retained after submission/exit for manual cleanup in the
configured directory. Removing a draft image removes its file. Rejected/stale
captures are cleaned up. Session history embeds the actual image bytes, so
removing retained files later does not break restored conversations. Unsubmitted
drafts are not durable across application exit. Clipboard content may be
sensitive; both files and accepted image history contain the image data.

Automated tests use synthetic clipboard data and do not inspect the user's
clipboard. Real OS clipboard access and terminal shortcut forwarding require
interactive verification on the target desktop.
