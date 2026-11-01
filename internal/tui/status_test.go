package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"recur/internal/agent"
)

func TestBottomBarWidthsAndPadding(t *testing.T) {
	left := "openai · gpt-4o · ctx ~1K"
	right := "~/projects/recur"

	for _, width := range []int{0, 1, 2, 3, 4, 5, 6, 10, 20, 40, 80, 120} {
		bar := renderBottomBar(width, left, right)
		if strings.ContainsAny(bar, "\r\n") {
			t.Fatalf("width %d: bar contains newline: %q", width, bar)
		}
		sw := ansi.StringWidth(bar)
		if sw > width {
			t.Fatalf("width %d: bar exceeds width (sw=%d): %q", width, sw, bar)
		}
		if width >= 5 {
			if !strings.HasPrefix(bar, "  ") {
				t.Fatalf("width %d: missing 2 leading spaces: %q", width, bar)
			}
			if !strings.HasSuffix(bar, "  ") {
				t.Fatalf("width %d: missing 2 trailing spaces: %q", width, bar)
			}
			avail := width - 4
			content := bar[2 : len(bar)-2]
			if ansi.StringWidth(content) > avail {
				t.Fatalf("width %d: content exceeds available width: %q", width, content)
			}
		}
	}
}

func TestBottomBarUnicodeTruncation(t *testing.T) {
	// Unicode wide characters and emojis
	left := "测试项目 · 模型/high · 很高兴"
	right := "🚀/测试/路径/很长很长的路径名称"
	for _, width := range []int{1, 3, 4, 5, 10, 25, 40, 80} {
		bar := renderBottomBar(width, left, right)
		if strings.ContainsAny(bar, "\r\n") {
			t.Fatalf("width %d contains newline", width)
		}
		sw := ansi.StringWidth(bar)
		if sw > width {
			t.Fatalf("width %d: Unicode bar width %d exceeds max: %q", width, sw, bar)
		}
		if width >= 5 {
			if !strings.HasPrefix(bar, "  ") || !strings.HasSuffix(bar, "  ") {
				t.Fatalf("width %d: missing 2 spaces on edges: %q", width, bar)
			}
		}
	}
}

func TestStatusCommandsExecutionAndJoining(t *testing.T) {
	ctx := context.Background()
	sem := make(chan struct{}, 4)
	snap := StatusSnapshot{
		Cwd: t.TempDir(),
		LeftCmds: []string{
			`echo "first"`,
			`echo ""`, // empty: omitted
			`exit 1`,  // failed: omitted
			`echo "second part"`,
		},
		RightCmds: []string{
			`echo "right1"`,
			`echo "right2"`,
		},
	}

	left, right := evalStatusSnapshot(ctx, snap, sem)
	if left != "first · second part" {
		t.Fatalf("unexpected left segments: %q", left)
	}
	if right != "right1 · right2" {
		t.Fatalf("unexpected right segments: %q", right)
	}
}

func TestStatusEnvironmentAndMetadata(t *testing.T) {
	ctx := context.Background()
	sem := make(chan struct{}, 4)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	snap := StatusSnapshot{
		Cwd: dir,
		Environment: map[string]string{
			"CUSTOM_CONFIG": "custom_val",
			"BASH_ENV":      "/should/not/be/set",
		},
		Metadata: map[string]string{
			"RECUR_PROVIDER":   "anthropic",
			"RECUR_MODEL":      "claude-3-7-sonnet",
			"RECUR_EFFORT":     "medium",
			"RECUR_CTX_SIZE":   "42000",
			"RECUR_CWD":        dir,
			"RECUR_SESSION_ID": "sess-test-456",
			"RECUR_VERSION":    "0.6.0",
			"RECUR_STATE":      "busy",
			"RECUR_EVENT":      "model_request_start",
		},
		LeftCmds: []string{
			`echo "$RECUR_PROVIDER:$RECUR_MODEL/$RECUR_EFFORT:$RECUR_STATE:$RECUR_EVENT"`,
			`echo "$CUSTOM_CONFIG"`,
			`test -z "$BASH_ENV" && echo "bash_env_clean"`,
			`[ "$PWD" = "$RECUR_CWD" ] && echo "cwd_clean"`,
		},
		RightCmds: []string{
			`echo "ctx:$RECUR_CTX_SIZE:v$RECUR_VERSION:id=$RECUR_SESSION_ID"`,
		},
	}

	left, right := evalStatusSnapshot(ctx, snap, sem)
	expectedLeft := "anthropic:claude-3-7-sonnet/medium:busy:model_request_start · custom_val · bash_env_clean · cwd_clean"
	if left != expectedLeft {
		t.Fatalf("left = %q, want %q", left, expectedLeft)
	}
	expectedRight := "ctx:42000:v0.6.0:id=sess-test-456"
	if right != expectedRight {
		t.Fatalf("right = %q, want %q", right, expectedRight)
	}
}

func TestStatusStdoutCap1KiB(t *testing.T) {
	ctx := context.Background()
	sem := make(chan struct{}, 4)
	snap := StatusSnapshot{
		Cwd: t.TempDir(),
		LeftCmds: []string{
			// Generate 5000 characters
			`head -c 5000 /dev/zero | tr '\0' 'A'`,
		},
	}

	left, _ := evalStatusSnapshot(ctx, snap, sem)
	if len(left) > 1024 {
		t.Fatalf("expected stdout cap <= 1024 bytes, got %d", len(left))
	}
	if len(left) != 1024 {
		t.Fatalf("expected exactly 1024 bytes capped, got %d", len(left))
	}
}

func TestStatusDeadline500ms(t *testing.T) {
	ctx := context.Background()
	sem := make(chan struct{}, 4)
	start := time.Now()
	snap := StatusSnapshot{
		Cwd: t.TempDir(),
		LeftCmds: []string{
			`sleep 2; echo "should timeout"`,
		},
		RightCmds: []string{
			`echo "fast"`,
		},
	}

	left, right := evalStatusSnapshot(ctx, snap, sem)
	elapsed := time.Since(start)
	if left != "" {
		t.Fatalf("timed out command should be omitted, got %q", left)
	}
	if right != "fast" {
		t.Fatalf("fast command should succeed, got %q", right)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("execution took too long (%v), timeout should kill process group around 500ms", elapsed)
	}
}

func TestStatusSanitization(t *testing.T) {
	dirty := "\x1b[31;1mRed Bold\x1b[0m\r\n\tSecond Line\x00\x07\nThird"
	cleaned := sanitizeStatusSegment(dirty)
	if strings.ContainsAny(cleaned, "\r\n\t\x00\x07") {
		t.Fatalf("dirty control characters not sanitized: %q", cleaned)
	}
	if strings.Contains(cleaned, "\x1b") {
		t.Fatalf("ANSI escapes not stripped: %q", cleaned)
	}
	if cleaned != "Red Bold  Second Line Third" {
		t.Fatalf("unexpected cleaned output: %q", cleaned)
	}
}

func TestStatusGenerationAndStaleRejection(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	m.statusGen = 5
	m.statusLeft = "gen5-left"
	m.statusRight = "gen5-right"

	// Old generation result arrives
	next, _ := m.Update(statusResultMsg{gen: 4, left: "stale-left", right: "stale-right"})
	nm := next.(model)
	if nm.statusLeft != "gen5-left" || nm.statusRight != "gen5-right" {
		t.Fatalf("stale generation 4 overwrote current generation: %q, %q", nm.statusLeft, nm.statusRight)
	}

	// Current generation result arrives
	next, _ = m.Update(statusResultMsg{gen: 5, left: "fresh-left", right: "fresh-right"})
	nm = next.(model)
	if nm.statusLeft != "fresh-left" || nm.statusRight != "fresh-right" {
		t.Fatalf("fresh generation 5 failed to update: %q, %q", nm.statusLeft, nm.statusRight)
	}
}

func TestStatusEmptyHidesSide(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	m.statusInit = true
	m.statusLeft = ""
	m.statusRight = ""

	bar := m.bottomBar()
	// Both sides empty: when width=80, should be 80 spaces
	if ansi.StringWidth(bar) != 80 {
		t.Fatalf("expected width 80 for empty bar, got %d", ansi.StringWidth(bar))
	}
	if strings.TrimSpace(bar) != "" {
		t.Fatalf("expected all spaces when status is empty, got %q", bar)
	}
}

func TestStatusConcurrencyBounded(t *testing.T) {
	ctx := context.Background()
	sem := make(chan struct{}, 4)
	snap := StatusSnapshot{
		Cwd: t.TempDir(),
		LeftCmds: []string{
			`sleep 0.05; echo "1"`,
			`sleep 0.05; echo "2"`,
			`sleep 0.05; echo "3"`,
			`sleep 0.05; echo "4"`,
			`sleep 0.05; echo "5"`,
		},
	}

	left, _ := evalStatusSnapshot(ctx, snap, sem)
	if left != "1 · 2 · 3 · 4 · 5" {
		t.Fatalf("expected all 5 ordered segments, got %q", left)
	}
}

func TestDefaultBashFormatting(t *testing.T) {
	ctx := context.Background()
	sem := make(chan struct{}, 4)
	home, _ := os.UserHomeDir()
	demoCwd := filepath.Join(home, "work", "demo")

	snap := StatusSnapshot{
		Cwd: demoCwd,
		Metadata: map[string]string{
			"RECUR_PROVIDER": "anthropic",
			"RECUR_MODEL":    "claude",
			"RECUR_EFFORT":   "",
			"RECUR_CTX_SIZE": "36679",
			"RECUR_CWD":      demoCwd,
		},
		LeftCmds:  DefaultStatusLeft,
		RightCmds: DefaultStatusRight,
	}

	left, right := evalStatusSnapshot(ctx, snap, sem)
	if left != "anthropic · claude · ctx ~37K" {
		t.Fatalf("default left = %q, want 'anthropic · claude · ctx ~37K'", left)
	}
	if right != "~/work/demo" {
		t.Fatalf("default right = %q, want '~/work/demo'", right)
	}
}
