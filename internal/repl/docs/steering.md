# Steering

During an active turn, submitting text queues an instruction without starting
an overlapping request. The agent accepts queued instructions in FIFO order at
safe boundaries after all tool calls/outputs from the response are committed.
Shell calls execute sequentially and only after the complete API response;
argument previews are never executable partial calls.

The interface shows pending instructions. Up Arrow with an empty composer
recalls the newest unaccepted instruction, unless command completion navigation
has priority. Recall is synchronized with acceptance: an accepted instruction
cannot be recalled or delivered twice. Unaccepted instructions and drafts are
preserved for recovery after cancellation or a late continuation.

Cancellation stops the active turn and kills the foreground command; shell state
and top-level background jobs survive. recur view queues captured images for the
same safe request boundary; recur docs images describes limits and persistence.
recur notify delivery is described in recur docs headless.
