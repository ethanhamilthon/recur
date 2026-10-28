package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestViewRejectsInvalidAndOversizedImages(t *testing.T) {
	dir := t.TempDir()
	for _, contents := range []string{"not an image", strings.Repeat("x", 2*1024*1024+1)} {
		path := filepath.Join(dir, "bad.png")
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := CaptureImage(path); err == nil {
			t.Fatal("accepted invalid/oversized image")
		}
	}
	if _, err := CaptureImage(dir); err == nil {
		t.Fatal("accepted directory")
	}
}
