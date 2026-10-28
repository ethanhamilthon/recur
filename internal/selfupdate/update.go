// Package selfupdate checks GitHub releases and installs the recur binary.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository that publishes recur releases.
const Repo = "ethanhamilthon/recur"

const (
	cacheTTL = 6 * time.Hour
	maxAsset = 64 << 20
)

// Release endpoints are variables so tests can serve them locally.
var (
	apiURL  = "https://api.github.com/" + "repos/" + Repo + "/releases/latest"
	downURL = "https://github.com/" + Repo + "/releases/download/"
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

func parse(value string) ([3]int, bool) {
	var out [3]int
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	if i := strings.IndexAny(value, "-+"); i >= 0 {
		value = value[:i]
	}
	parts := strings.Split(value, ".")
	if len(parts) != len(out) {
		return out, false
	}
	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return out, false
		}
		out[i] = number
	}
	return out, true
}

// Newer reports whether latest is a later semantic version than current.
func Newer(current, latest string) bool {
	have, ok := parse(current)
	if !ok {
		return false
	}
	want, ok := parse(latest)
	if !ok {
		return false
	}
	for i := range have {
		if have[i] != want[i] {
			return want[i] > have[i]
		}
	}
	return false
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
	if _, ok := parse(current); !ok || os.Getenv("RECUR_NO_UPDATE_CHECK") != "" {
		return ""
	}
	path := cacheFile()
	var cached checkCache
	if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &cached) == nil &&
		cached.Latest != "" && time.Since(cached.Checked) < cacheTTL {
		return newerTag(current, cached.Latest)
	}
	latest, err := Latest(ctx)
	if err != nil {
		return ""
	}
	if data, err := json.Marshal(checkCache{Checked: time.Now(), Latest: latest}); err == nil {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, append(data, '\n'), 0o644)
	}
	return newerTag(current, latest)
}

func newerTag(current, latest string) string {
	if Newer(current, latest) {
		return latest
	}
	return ""
}

func assetName() (string, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return "", fmt.Errorf("no prebuilt binary for %s/%s; build from source", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("recur_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH), nil
}

func checksum(listing []byte, asset string) string {
	for _, line := range strings.Split(string(listing), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}

// binaryFromArchive returns the recur executable stored in a release tarball.
func binaryFromArchive(archive []byte) ([]byte, error) {
	reader, err := gzip.NewReader(strings.NewReader(string(archive)))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	entries := tar.NewReader(reader)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("release archive has no recur binary")
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != "recur" {
			continue
		}
		return io.ReadAll(io.LimitReader(entries, maxAsset))
	}
}

// Install replaces the running binary with the newest release.
func Install(ctx context.Context, current string, out io.Writer) error {
	latest, err := Latest(ctx)
	if err != nil {
		return err
	}
	if _, known := parse(current); known && !Newer(current, latest) {
		fmt.Fprintf(out, "recur %s is the latest release.\n", current)
		return nil
	}
	asset, err := assetName()
	if err != nil {
		return err
	}
	base := downURL + latest + "/"
	archive, err := fetch(ctx, base+asset, maxAsset)
	if err != nil {
		return err
	}
	sums, err := fetch(ctx, base+"checksums.txt", 1<<20)
	if err != nil {
		return err
	}
	want := checksum(sums, asset)
	if want == "" {
		return fmt.Errorf("checksums.txt does not list %s", asset)
	}
	digest := sha256.Sum256(archive)
	if got := hex.EncodeToString(digest[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset, got, want)
	}
	binary, err := binaryFromArchive(archive)
	if err != nil {
		return err
	}
	target, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	if err := replace(target, binary); err != nil {
		return err
	}
	fmt.Fprintf(out, "Updated recur %s -> %s at %s. Restart recur to use it.\n", strings.TrimPrefix(current, "v"), latest, target)
	return nil
}

// replace writes data beside target and renames it into place, so a failed update keeps the old binary.
func replace(target string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(target), ".recur-update-*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s: %w", target, err)
	}
	name := temp.Name()
	defer os.Remove(name)
	_, err = temp.Write(data)
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(name, 0o755); err != nil {
		return err
	}
	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("cannot replace %s: %w", target, err)
	}
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
