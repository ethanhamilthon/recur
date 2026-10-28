package repl

import "strings"

// outputLimit caps captured tool output at 16 KiB, keeping head and tail.
const outputLimit = 16 * 1024

// cappedBuffer captures bounded UTF-8 tool output, keeping head and tail when truncated.
type cappedBuffer struct {
	head, tail []byte
	size       int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	total := len(p)
	b.size += total
	if room := outputLimit/2 - len(b.head); room > 0 {
		n := min(len(p), room)
		b.head, p = append(b.head, p[:n]...), p[n:]
	}
	b.tail = append(b.tail, p...)
	if len(b.tail) > outputLimit/2 {
		b.tail = append(b.tail[:0], b.tail[len(b.tail)-outputLimit/2:]...)
	}
	return total, nil
}
func (b *cappedBuffer) Reset() { b.head, b.tail, b.size = b.head[:0], b.tail[:0], 0 }
func (b *cappedBuffer) Text() string {
	head, tail := strings.ToValidUTF8(string(b.head), ""), strings.ToValidUTF8(string(b.tail), "")
	if b.size <= outputLimit {
		return head + tail
	}
	return head + "\n[output truncated at 16 KiB; filter with rg, grep, head or tail]\n" + tail
}
