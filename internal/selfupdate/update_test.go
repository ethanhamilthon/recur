package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// stubEndpoints points the package at a local release server for the test's duration.
func stubEndpoints(t *testing.T, server *httptest.Server) {
	t.Helper()
	api, down := apiURL, downURL
	apiURL, downURL = server.URL+"/latest", server.URL+"/download/"
	t.Cleanup(func() { apiURL, downURL = api, down })
}

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.2.1", "v0.2.2", true},
		{"v0.2.1", "0.2.1", false},
		{"0.9.9", "1.0.0", true},
		{"1.0.0", "0.9.9", false},
		{"dev", "v1.0.0", false},
		{"0.2.1", "garbage", false},
	}
	for _, c := range cases {
		if got := Newer(c.current, c.latest); got != c.want {
			t.Fatalf("Newer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestChecksumAndArchive(t *testing.T) {
	listing := []byte("abc  recur_darwin_arm64.tar.gz\ndef *recur_linux_amd64.tar.gz\n")
	if got := checksum(listing, "recur_linux_amd64.tar.gz"); got != "def" {
		t.Fatalf("checksum = %q", got)
	}
	if got := checksum(listing, "recur_windows_amd64.tar.gz"); got != "" {
		t.Fatalf("unexpected checksum %q", got)
	}
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tarWriter.Write([]byte("hi"))
	if err := tarWriter.WriteHeader(&tar.Header{Name: "recur", Mode: 0o755, Size: 6, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tarWriter.Write([]byte("binary"))
	tarWriter.Close()
	gzipWriter.Close()
	binary, err := binaryFromArchive(archive.Bytes())
	if err != nil || string(binary) != "binary" {
		t.Fatalf("binaryFromArchive = %q, %v", binary, err)
	}
}

func TestAvailableUsesCacheAndFetchesOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECUR_HOME", home)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v0.3.0"})
	}))
	defer server.Close()
	stubEndpoints(t, server)

	if got := Available(context.Background(), "0.2.1"); got != "v0.3.0" {
		t.Fatalf("Available = %q", got)
	}
	if got := Available(context.Background(), "0.2.1"); got != "v0.3.0" {
		t.Fatalf("cached Available = %q", got)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if got := Available(context.Background(), "0.3.0"); got != "" {
		t.Fatalf("up-to-date Available = %q", got)
	}
	if got := Available(context.Background(), "dev"); got != "" {
		t.Fatalf("dev Available = %q", got)
	}
}

func TestInstallReplacesBinary(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no release assets for this platform")
	}
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	tarWriter.WriteHeader(&tar.Header{Name: "recur", Mode: 0o755, Size: 4, Typeflag: tar.TypeReg})
	tarWriter.Write([]byte("next"))
	tarWriter.Close()
	gzipWriter.Close()
	asset, err := assetName()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive.Bytes())
	sums := hex.EncodeToString(digest[:]) + "  " + asset + "\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/latest"):
			json.NewEncoder(w).Encode(map[string]string{"tag_name": "v9.9.9"})
		case strings.HasSuffix(r.URL.Path, "checksums.txt"):
			w.Write([]byte(sums))
		default:
			w.Write(archive.Bytes())
		}
	}))
	defer server.Close()
	stubEndpoints(t, server)

	target := filepath.Join(t.TempDir(), "recur")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replace(target, []byte("new")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new" {
		t.Fatalf("replace left %q, %v", data, err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), "0.2.1", []string{"-check"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "latest v9.9.9") {
		t.Fatalf("check output = %q", out.String())
	}
}

func TestCacheFileHonorsRelativeHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECUR_HOME", "relative-home")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(resolved, "relative-home", "update.json")
	if got := cacheFile(); got != want {
		t.Fatalf("cacheFile = %q, want %q", got, want)
	}
	var cached checkCache
	if err := json.Unmarshal([]byte(`{"checked":"2000-01-01T00:00:00Z","latest":"v1.0.0"}`), &cached); err != nil {
		t.Fatal(err)
	}
	if time.Since(cached.Checked) < cacheTTL {
		t.Fatal("stale cache should expire")
	}
}
