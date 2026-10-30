// Package selfupdate checks GitHub releases and installs the recur binary.
package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Repo is the GitHub repository that publishes recur releases.
const Repo = "ethanhamilthon/recur"

const cacheTTL = 6 * time.Hour

// Release endpoints are variables so tests can serve them locally.
var (
	apiURL    = "https://api.github.com/" + "repos/" + Repo + "/releases/latest"
	scriptURL = "https://raw.githubusercontent.com/" + Repo + "/"
)

var client = &http.Client{Timeout: 60 * time.Second}

type checkCache struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest"`
}

func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "recur-selfupdate")
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, response.Status)
	}
	return io.ReadAll(io.LimitReader(response.Body, limit))
}

// Latest returns the newest released version tag, such as v0.2.2.
func Latest(ctx context.Context) (string, error) {
	data, err := fetch(ctx, apiURL, 1<<20)
	if err != nil {
		return "", err
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(data, &release); err != nil {
		return "", err
	}
	if release.Tag == "" {
		return "", errors.New("release has no tag")
	}
	return release.Tag, nil
}

// canonical returns value as a "vX.Y.Z" semantic version, or "" when invalid.
func canonical(value string) string {
	if value = strings.TrimSpace(value); !strings.HasPrefix(value, "v") {
		value = "v" + value
	}
	if !semver.IsValid(value) {
		return ""
	}
	return value
}

// Newer reports whether latest is a later semantic version than current.
func Newer(current, latest string) bool {
	have, want := canonical(current), canonical(latest)
	return have != "" && want != "" && semver.Compare(want, have) > 0
}

func cacheFile() string {
	home := os.Getenv("RECUR_HOME")
	if home == "" {
		dir, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(dir, ".recur")
	}
	path, err := filepath.Abs(filepath.Join(home, "update.json"))
	if err != nil {
		return ""
	}
	return path
}

// Available returns the newer version tag, using a cached answer to keep startup cheap.
// It returns an empty string when the installation is current, unknown, or the check is off.
func Available(ctx context.Context, current string) string {
	if canonical(current) == "" || os.Getenv("RECUR_NO_UPDATE_CHECK") != "" {
		return ""
	}
	path := cacheFile()
	var cached checkCache
	if data, err := os.ReadFile(path); err != nil || json.Unmarshal(data, &cached) != nil ||
		cached.Latest == "" || time.Since(cached.Checked) >= cacheTTL {
		latest, err := Latest(ctx)
		if err != nil {
			return ""
		}
		cached = checkCache{Checked: time.Now(), Latest: latest}
		if data, err := json.Marshal(cached); err == nil {
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			_ = os.WriteFile(path, append(data, '\n'), 0o644)
		}
	}
	if Newer(current, cached.Latest) {
		return cached.Latest
	}
	return ""
}

// Install runs the release's install.sh for the directory of the running binary,
// so checksum verification and extraction live in one place.
func Install(ctx context.Context, current string, out io.Writer) error {
	latest, err := Latest(ctx)
	if err != nil {
		return err
	}
	if canonical(current) != "" && !Newer(current, latest) {
		fmt.Fprintf(out, "recur %s is the latest release.\n", current)
		return nil
	}
	target, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	// The script is pinned to the release tag, not to the moving main branch.
	cmd := exec.CommandContext(ctx, "sh", "-c", `curl -fsSL "$0" | sh`, scriptURL+latest+"/scripts/install.sh")
	cmd.Env = append(os.Environ(), "RECUR_BINDIR="+filepath.Dir(target), "RECUR_VERSION="+latest)
	cmd.Stdout, cmd.Stderr = out, out
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("install.sh failed: %w", err)
	}
	fmt.Fprintf(out, "Updated recur %s -> %s. Restart recur to use it.\n", strings.TrimPrefix(current, "v"), latest)
	return nil
}

// Run implements the recur update command. With check only, it reports without installing.
func Run(ctx context.Context, current string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(out)
	check := flags.Bool("check", false, "only report whether a newer release exists")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if !*check {
		return Install(ctx, current, out)
	}
	latest, err := Latest(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "installed v%s, latest %s\n", strings.TrimPrefix(current, "v"), latest)
	if Newer(current, latest) {
		fmt.Fprintln(out, "run recur update to install it")
	}
	return nil
}
