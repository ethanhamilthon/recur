package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubEndpoints points the package at a local release server for the test's duration.
func stubEndpoints(t *testing.T, server *httptest.Server) {
	t.Helper()
	api, script := apiURL, scriptURL
	apiURL, scriptURL = server.URL+"/latest", server.URL+"/"
	t.Cleanup(func() { apiURL, scriptURL = api, script })
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

func TestInstallRunsPinnedScriptForBinaryDirectory(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			json.NewEncoder(w).Encode(map[string]string{"tag_name": "v9.9.9"})
		case "/v9.9.9/scripts/install.sh":
			fmt.Fprintf(w, "echo \"$RECUR_VERSION $RECUR_BINDIR\" > %q\n", marker)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	stubEndpoints(t, server)
	var out bytes.Buffer
	if err := Install(context.Background(), "0.2.1", &out); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	executable, _ := os.Executable()
	executable, _ = filepath.EvalSymlinks(executable)
	data, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(data)) != "v9.9.9 "+filepath.Dir(executable) {
		t.Fatalf("script env = %q, %v", data, err)
	}
	out.Reset()
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
