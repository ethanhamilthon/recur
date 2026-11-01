package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"recur/internal/agent"
	"recur/internal/repl"
)

func TestFindFileTokenBasics(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		cursor  int
		wantOk  bool
		wantQ   string
		wantDir bool
		wantS   int
		wantE   int
	}{
		{"empty", "", 0, false, "", false, 0, 0},
		{"just at", "@", 1, true, "", false, 0, 1},
		{"at with word", "@foo", 4, true, "foo", false, 0, 4},
		{"at with dir", "@dir/", 5, true, "dir/", true, 0, 5},
		{"cursor inside token", "check @dir/file more", 12, true, "dir/f", false, 6, 15},
		{"token with parens", "(@file)", 6, true, "file", false, 1, 6},
		{"token with quotes", "@\"my file.txt\"", 13, true, "my file.txt", false, 0, 14},
		{"multiline token", "line 1\nline 2 @src/main.go", 26, true, "src/main.go", false, 14, 26},
		{"space after token", "@foo ", 5, false, "", false, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token, ok := findFileToken(tc.text, tc.cursor)
			if ok != tc.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOk)
			}
			if ok {
				if token.query != tc.wantQ {
					t.Errorf("query = %q, want %q", token.query, tc.wantQ)
				}
				if token.dir != tc.wantDir {
					t.Errorf("dir = %v, want %v", token.dir, tc.wantDir)
				}
				if token.start != tc.wantS || token.end != tc.wantE {
					t.Errorf("range = [%d, %d], want [%d, %d]", token.start, token.end, tc.wantS, tc.wantE)
				}
			}
		})
	}
}

func TestFindFileTokenRejectsEmailsAndURLs(t *testing.T) {
	rejected := []struct {
		text   string
		cursor int
	}{
		{"alice@example.com", 17},
		{"user@domain", 11},
		{"test.user+tag@gmail.com", 23},
		{"user_name@host.org", 18},
		{"foo-bar@baz.com", 15},
		{"https://example.com/@alice", 26},
		{"http://user:pass@example.com", 28},
		{"mailto:user@example.com", 23},
		{"git@github.com:repo.git", 23},
		{"ftp://host/@path", 16},
	}

	for _, tc := range rejected {
		t.Run(tc.text, func(t *testing.T) {
			if _, ok := findFileToken(tc.text, tc.cursor); ok {
				t.Fatalf("expected rejection for %q at cursor %d", tc.text, tc.cursor)
			}
		})
	}
}

func TestQuotePath(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"main.go", "@main.go"},
		{"internal/tui/files.go", "@internal/tui/files.go"},
		{"internal/", "@internal/"},
		{"my file.txt", "@\"my file.txt\""},
		{"dir with space/", "@\"dir with space/\""},
		{"quote\"in\"path.go", "@\"quote\\\"in\\\"path.go\""},
	}

	for _, tc := range tests {
		got := quotePath(tc.in)
		if got != tc.want {
			t.Errorf("quotePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSanitizeLabel(t *testing.T) {
	input := "hello\x00world\x1b[31mred\x07!"
	sanitized := sanitizeLabel(input)
	if strings.ContainsAny(sanitized, "\x00\x1b\x07") {
		t.Fatalf("sanitized contains control chars: %q", sanitized)
	}
	if !strings.HasPrefix(sanitized, "hello?world") {
		t.Fatalf("unexpected sanitization: %q", sanitized)
	}
}

func TestFilterRanking(t *testing.T) {
	indexed := []string{
		"cmd/recur/main.go",
		"internal/agent/agent.go",
		"internal/tui/files.go",
		"main.go",
		"recur.toml",
		"scripts/build_release.sh",
	}

	// Query "main":
	// Basename prefix matches: "cmd/recur/main.go" (base "main.go"), "main.go" (base "main.go")
	// Both are Category 0. Deterministic tie-break by path: cmd/recur/main.go < main.go.
	res := filterFileCompletions(indexed, "main")
	if len(res) < 2 || res[0] != "cmd/recur/main.go" || res[1] != "main.go" {
		t.Fatalf("query 'main' results = %v", res)
	}

	// Query "rec":
	// Basename prefix matches: "cmd/recur/main.go" (base "main.go", no match on base)
	// "recur.toml": base is "recur.toml", prefix match -> Cat 0
	// "cmd/recur/main.go": path contains "rec" -> Cat 1
	res = filterFileCompletions(indexed, "rec")
	if len(res) < 2 {
		t.Fatalf("query 'rec' results = %v", res)
	}
	if res[0] != "recur.toml" {
		t.Errorf("res[0] = %q, want %q", res[0], "recur.toml")
	}
	if res[1] != "cmd/recur/main.go" {
		t.Errorf("res[1] = %q, want %q", res[1], "cmd/recur/main.go")
	}

	// Empty query returns up to 100 entries sorted
	empty := filterFileCompletions(indexed, "")
	if len(empty) != len(indexed) {
		t.Fatalf("empty query len = %d, want %d", len(empty), len(indexed))
	}
}

func TestScanWalkBounded(t *testing.T) {
	dir := t.TempDir()

	// Create sample structure
	_ = os.MkdirAll(filepath.Join(dir, "src", "pkg"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "src", "pkg", "app.go"), []byte("package pkg"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root"), 0644)

	// Create ignored directories
	_ = os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0755)
	_ = os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("git config"), 0644)
	_ = os.MkdirAll(filepath.Join(dir, ".recur"), 0755)
	_ = os.WriteFile(filepath.Join(dir, ".recur", "state"), []byte("state"), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "index.js"), []byte("js"), 0644)

	// Create symlink to directory (must not traverse)
	_ = os.Symlink(filepath.Join(dir, "src"), filepath.Join(dir, "link_to_src"))

	ctx, cancel := context.WithTimeout(context.Background(), fileScanTimeout)
	defer cancel()

	paths, err := scanProjectFiles(ctx, dir)
	if err != nil {
		t.Fatalf("scanProjectFiles error: %v", err)
	}

	for _, p := range paths {
		if strings.HasPrefix(p, ".git") || strings.HasPrefix(p, ".recur") || strings.HasPrefix(p, "node_modules") {
			t.Fatalf("ignored directory leaked into paths: %s", p)
		}
		if strings.HasPrefix(p, "link_to_src/") {
			t.Fatalf("traversed directory symlink: %s", p)
		}
	}

	// Check that real files are found
	hasRoot := false
	hasApp := false
	for _, p := range paths {
		if p == "root.txt" {
			hasRoot = true
		}
		if p == "src/pkg/app.go" {
			hasApp = true
		}
	}
	if !hasRoot || !hasApp {
		t.Fatalf("expected paths root.txt and src/pkg/app.go in %v", paths)
	}
}

func TestGitScanFallsBackGracefully(t *testing.T) {
	// A non-git temp dir must successfully fall back to scanWalk
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("content"), 0644)

	ctx, cancel := context.WithTimeout(context.Background(), fileScanTimeout)
	defer cancel()

	paths, err := scanProjectFiles(ctx, dir)
	if err != nil {
		t.Fatalf("expected fallback walk to succeed, got %v", err)
	}
	if len(paths) != 1 || paths[0] != "sample.txt" {
		t.Fatalf("fallback paths = %v, want ['sample.txt']", paths)
	}
}

func TestFileCompletionAcceptDirectoryContinues(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	m.fileIndexCache = []string{"src/", "src/main.go", "src/util.go"}
	m.fileIndexCwd = m.cwd
	m.fileIndexCacheTime = time.Now()

	m.fileCompletions = []string{"src/"}
	m.fileTokenStart = 0
	m.fileTokenEnd = 2
	m.input.SetValue("@s")
	m.input.CursorEnd()

	// Press Tab or Enter to accept directory
	m.acceptFileCompletion()

	// Text should now be "@src/"
	if got := m.input.Value(); got != "@src/" {
		t.Fatalf("composer after dir accept = %q, want %q", got, "@src/")
	}
	// Completion should still have matches inside src/
	if len(m.fileCompletions) == 0 {
		t.Fatal("expected file completions to continue after directory acceptance")
	}
}

func TestFileCompletionAcceptFileReplacesTokenOnly(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	m.input.SetValue("Please read @src/ and review")
	_ = m.setCursorOffset(17)
	token, ok := findFileToken(m.input.Value(), 17)
	if !ok {
		t.Fatal("token not found")
	}
	m.fileCompletions = []string{"src/main.go"}
	m.fileTokenStart = token.start
	m.fileTokenEnd = token.end

	m.acceptFileCompletion()

	want := "Please read @src/main.go and review"
	if got := m.input.Value(); got != want {
		t.Fatalf("composer after file accept = %q, want %q", got, want)
	}
	if m.hasFileCompletions() {
		t.Fatal("file completions should be closed after file acceptance")
	}
}

func TestFileCompletionEnterDoesNotSubmit(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	m.fileCompletions = []string{"main.go"}
	m.fileTokenStart = 0
	m.fileTokenEnd = 2
	m.input.SetValue("@m")
	m.input.CursorEnd()

	// First Enter: accepts file completion
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if got := m.input.Value(); got != "@main.go" {
		t.Fatalf("first enter value = %q, want %q", got, "@main.go")
	}
	if m.busy {
		t.Fatal("first Enter submitted turn while accepting file")
	}
	if m.hasFileCompletions() {
		t.Fatal("completions still active after file acceptance")
	}
}

func TestFileCompletionEnterWithImageAcceptsFileFirst(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	m.images = []repl.Image{{URL: "data:image/png;base64,AA=="}}
	m.fileCompletions = []string{"config.json"}
	m.fileTokenStart = 0
	m.fileTokenEnd = 3
	m.input.SetValue("@co")
	m.input.CursorEnd()

	// Enter accepts file completion first
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if got := m.input.Value(); got != "@config.json" {
		t.Fatalf("enter with image value = %q, want %q", got, "@config.json")
	}
	if len(m.images) != 1 {
		t.Fatal("image was discarded on file completion acceptance")
	}
	if m.busy {
		t.Fatal("enter submitted turn immediately with image")
	}
}

func TestFileCompletionEnterWhenBusyAcceptsFileFirst(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	m.busy = true
	m.steering = &agent.Steering{}
	m.fileCompletions = []string{"README.md"}
	m.fileTokenStart = 0
	m.fileTokenEnd = 5
	m.input.SetValue("@READ")
	m.input.CursorEnd()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if got := m.input.Value(); got != "@README.md" {
		t.Fatalf("busy enter value = %q, want %q", got, "@README.md")
	}
	if len(m.queued) != 0 {
		t.Fatal("enter queued steering immediately instead of accepting file")
	}
}

func TestEscClosesActiveCompletionBeforeTurnCancellation(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	m.busy = true
	m.fileCompletions = []string{"file1.go", "file2.go"}

	// First Esc closes completions
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)

	if m.hasFileCompletions() {
		t.Fatal("first Esc did not close completions")
	}
	if m.cancelling {
		t.Fatal("first Esc cancelled turn instead of closing completions")
	}

	// Second Esc cancels the busy turn
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if !m.cancelling {
		t.Fatal("second Esc did not request cancellation")
	}
}

func TestEscClosesLoadingCompletionBeforeTurnCancellation(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	m.busy = true
	m.fileLoading = true

	// First Esc closes loading menu
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)

	if m.fileLoading {
		t.Fatal("first Esc did not dismiss loading state")
	}
	if m.cancelling {
		t.Fatal("first Esc cancelled turn instead of closing loading completion")
	}
}

func TestFileDebounceAndCache(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "alpha.go"), []byte("package a"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "beta.go"), []byte("package b"), 0644)

	m := newModel(&agent.Agent{}, dir)
	m.input.SetValue("@al")
	m.input.CursorEnd()

	cmd := m.checkFileCompletion()
	if cmd == nil {
		t.Fatal("checkFileCompletion returned nil cmd")
	}

	// Trigger debounce message with current generations
	next, scanCmd := m.Update(fileDebounceMsg{debounceGen: m.fileDebounceGen, fileGen: m.fileGen})
	m = next.(model)
	if scanCmd == nil {
		t.Fatal("expected scanCmd from debounce")
	}

	// Execute scanCmd
	scanMsg := scanCmd()
	next, _ = m.Update(scanMsg)
	m = next.(model)

	if len(m.fileCompletions) != 1 || m.fileCompletions[0] != "alpha.go" {
		t.Fatalf("completions after scan = %v, want ['alpha.go']", m.fileCompletions)
	}

	// Cache should now be populated
	if len(m.fileIndexCache) != 2 {
		t.Fatalf("cache size = %d, want 2", len(m.fileIndexCache))
	}

	// Second query should use cache without starting scanCmd
	m.input.SetValue("@be")
	m.input.CursorEnd()
	_ = m.checkFileCompletion()
	next, followCmd := m.Update(fileDebounceMsg{debounceGen: m.fileDebounceGen, fileGen: m.fileGen})
	m = next.(model)
	if followCmd != nil {
		t.Fatalf("expected nil cmd from warm cache debounce, got %v", followCmd)
	}
	if len(m.fileCompletions) != 1 || m.fileCompletions[0] != "beta.go" {
		t.Fatalf("warm cache completions = %v, want ['beta.go']", m.fileCompletions)
	}
}

func TestUpDownNavigatesFileCompletions(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	m.fileCompletions = []string{"a.go", "b.go", "c.go"}
	m.fileSelected = 0

	// Down moves to 1
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	if m.fileSelected != 1 {
		t.Fatalf("selected = %d, want 1", m.fileSelected)
	}

	// Down moves to 2
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	if m.fileSelected != 2 {
		t.Fatalf("selected = %d, want 2", m.fileSelected)
	}

	// Down wraps to 0
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	if m.fileSelected != 0 {
		t.Fatalf("selected = %d, want 0", m.fileSelected)
	}

	// Up wraps to 2
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	if m.fileSelected != 2 {
		t.Fatalf("selected = %d, want 2", m.fileSelected)
	}
}

func TestReloadClearsFileCache(t *testing.T) {
	dir := t.TempDir()
	a, err := agent.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	m := newModel(a, dir)
	m.fileIndexCache = []string{"file.txt"}
	m.fileCompletions = []string{"file.txt"}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // not slash yet
	m = next.(model)

	// Run /reload
	m.fileCompletions = nil
	m.input.SetValue("/reload")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if m.fileIndexCache != nil {
		t.Fatal("expected /reload to clear fileIndexCache")
	}
	if len(m.fileCompletions) != 0 {
		t.Fatal("expected /reload to dismiss file completions")
	}
}

func TestSlashCommandsUnchanged(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	m.input.SetValue("/")
	matches := m.completions()
	if len(matches) == 0 {
		t.Fatal("slash commands not returned")
	}
	if m.hasFileCompletions() {
		t.Fatal("file completion should not be active for /")
	}
}

func TestGitTrackedAndUntracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runCmd := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v failed: %v", args, err)
		}
	}
	runCmd("init")
	runCmd("config", "user.name", "test")
	runCmd("config", "user.email", "test@test.com")

	_ = os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("tracked"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("untracked"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("ignored"), 0644)
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.txt\n"), 0644)

	runCmd("add", "tracked.txt", ".gitignore")
	runCmd("commit", "-m", "init")

	ctx, cancel := context.WithTimeout(context.Background(), fileScanTimeout)
	defer cancel()

	paths, err := scanProjectFiles(ctx, dir)
	if err != nil {
		t.Fatalf("scanProjectFiles failed: %v", err)
	}

	foundTracked, foundUntracked, foundIgnored := false, false, false
	for _, p := range paths {
		if p == "tracked.txt" {
			foundTracked = true
		}
		if p == "untracked.txt" {
			foundUntracked = true
		}
		if p == "ignored.txt" {
			foundIgnored = true
		}
	}

	if !foundTracked {
		t.Error("tracked.txt not found in git scan")
	}
	if !foundUntracked {
		t.Error("untracked.txt not found in git scan")
	}
	if foundIgnored {
		t.Error("ignored.txt leaked into git scan")
	}
}

func TestMultilineUnicodeTokenReplacement(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	// Multiline input with Unicode emojis before and after the @ token
	input := "First line: 🚀 launching\nSecond line: check @fi here 🎉\nThird line: done"
	m.input.SetValue(input)

	// Position cursor right after @fi (rune index 42)
	runes := []rune(input)
	cursorPos := -1
	for i := 0; i < len(runes)-2; i++ {
		if runes[i] == '@' && runes[i+1] == 'f' && runes[i+2] == 'i' {
			cursorPos = i + 3
			break
		}
	}
	if cursorPos < 0 {
		t.Fatal("could not find @fi in input")
	}

	token, ok := findFileToken(input, cursorPos)
	if !ok {
		t.Fatalf("token not found at cursor %d", cursorPos)
	}

	m.fileCompletions = []string{"files.go"}
	m.fileTokenStart = token.start
	m.fileTokenEnd = token.end
	_ = m.setCursorOffset(cursorPos)

	m.acceptFileCompletion()

	want := "First line: 🚀 launching\nSecond line: check @files.go here 🎉\nThird line: done"
	if got := m.input.Value(); got != want {
		t.Fatalf("unicode multiline replacement =\n%q\nwant:\n%q", got, want)
	}
}

func TestQuotedPathWithSpacesAcceptance(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	input := "look at @\"my fold"
	m.input.SetValue(input)
	m.input.CursorEnd()

	token, ok := findFileToken(input, len([]rune(input)))
	if !ok {
		t.Fatal("token not found")
	}

	m.fileCompletions = []string{"my folder/document.pdf"}
	m.fileTokenStart = token.start
	m.fileTokenEnd = token.end

	m.acceptFileCompletion()

	want := "look at @\"my folder/document.pdf\""
	if got := m.input.Value(); got != want {
		t.Fatalf("quoted path replacement = %q, want %q", got, want)
	}
}

func TestRenderingFileCompletions(t *testing.T) {
	m := newModel(&agent.Agent{}, t.TempDir())
	m.width = 80
	m.height = 24
	m.fileCompletions = []string{"alpha.go", "beta.go"}
	m.fileSelected = 0

	view := m.timeline()
	if !strings.Contains(view, "› alpha.go") {
		t.Fatalf("expected selected marker for alpha.go in timeline:\n%s", view)
	}
	if !strings.Contains(view, "  beta.go") {
		t.Fatalf("expected unselected marker for beta.go in timeline:\n%s", view)
	}
}
