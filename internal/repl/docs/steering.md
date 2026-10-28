# Steering

During an active turn, submitting text queues an instruction without starting
an overlapping request. The agent accepts queued instructions in FIFO order at
safe boundaries after all tool calls/outputs from the response are committed.
Python calls execute sequentially and only after the complete API response;
argument previews are never executable partial calls.

The interface shows pending instructions. Up Arrow with an empty composer
recalls the newest unaccepted instruction, unless command completion navigation
has priority. Recall is synchronized with acceptance: an accepted instruction
cannot be recalled or delivered twice. Unaccepted instructions and drafts are
preserved for recovery after cancellation or a late continuation.

Cancellation stops the active turn and can destroy interpreter state. A later
request initializes a new interpreter and reports the restart explicitly.
view(path) queues captured images at the same safe request boundary;
docs("images") describes limits and persistence. External recur notify delivery is available; docs("headless") describes
acknowledgements, limits, and delivery semantics.
