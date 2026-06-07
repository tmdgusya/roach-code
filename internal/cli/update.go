package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// defaultRepo is the GitHub "owner/repo" the updater pulls releases from. Keep
// it in sync with install.sh / install.ps1 and the release workflow.
const defaultRepo = "tmdgusya/roach-code"

func repoSlug() string {
	if r := strings.TrimSpace(os.Getenv("ROACH_REPO")); r != "" {
		return r
	}
	return defaultRepo
}

// updateCommand implements `roach-code update [--check]`: download the latest
// GitHub release for this OS/arch, verify its SHA-256, and replace the running
// binary in place. No external tools required.
func updateCommand(args []string, version string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	checkOnly := fs.Bool("check", false, "only report whether a newer release exists")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	repo := repoSlug()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	latest, err := latestReleaseTag(ctx, repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		return 1
	}

	fmt.Printf("current: %s\nlatest:  %s\n", version, latest)
	if version == latest {
		fmt.Println(green("already up to date"))
		return 0
	}
	if *checkOnly {
		fmt.Println(dim("run `roach update` to upgrade"))
		return 0
	}

	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	asset := fmt.Sprintf("roach-code-%s-%s.%s", runtime.GOOS, runtime.GOARCH, ext)
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s", repo, latest)

	fmt.Printf("downloading %s ...\n", asset)
	archive, err := httpGetBytes(ctx, base+"/"+asset)
	if err != nil {
		fmt.Fprintf(os.Stderr, "update: download %s: %v\n", asset, err)
		return 1
	}

	if sums, err := httpGetBytes(ctx, base+"/SHA256SUMS"); err == nil {
		if want := sumFor(string(sums), asset); want != "" {
			if got := sha256hex(archive); !strings.EqualFold(got, want) {
				fmt.Fprintf(os.Stderr, "update: checksum mismatch for %s (want %s, got %s)\n", asset, want, got)
				return 1
			}
			fmt.Println(dim("checksum ok"))
		}
	}

	binName := "roach-code"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	newBin, err := extractArchivedBinary(archive, binName, ext)
	if err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		return 1
	}
	if err := replaceSelf(newBin); err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		return 1
	}
	fmt.Printf("%s updated to %s — restart roach to use it\n", green("✓"), latest)
	return 0
}

func latestReleaseTag(ctx context.Context, repo string) (string, error) {
	body, err := httpGetBytes(ctx, "https://api.github.com/repos/"+repo+"/releases/latest")
	if err != nil {
		return "", fmt.Errorf("resolve latest release for %s: %w", repo, err)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("parse release metadata: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("no published release for %s yet", repo)
	}
	return rel.TagName, nil
}

func httpGetBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "roach-code-update")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return body, nil
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// sumFor returns the hash for asset from a `<hash>  <name>` SHA256SUMS body.
func sumFor(sums, asset string) string {
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == asset {
			return f[0]
		}
	}
	return ""
}

func extractArchivedBinary(archive []byte, binName, ext string) ([]byte, error) {
	if ext == "zip" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("open zip: %w", err)
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == binName {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("%s not found in archive", binName)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(hdr.Name) == binName {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", binName)
}

// replaceSelf overwrites the running executable with newBin. It writes a temp
// file beside the current binary (so the final rename is atomic on the same
// volume) and swaps it in. On Windows a running image can't be overwritten in
// place but can be renamed, so the current binary is moved aside first.
func replaceSelf(newBin []byte) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved // update the real file, not a `roach` symlink to it
	}
	dir := filepath.Dir(self)

	tmp, err := os.CreateTemp(dir, ".roach-code-update-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(newBin); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write update: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		os.Remove(tmpName)
		return err
	}

	if runtime.GOOS == "windows" {
		old := self + ".old"
		os.Remove(old) // clear any leftover from a previous update
		if err := os.Rename(self, old); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("move current binary aside: %w", err)
		}
		if err := os.Rename(tmpName, self); err != nil {
			os.Rename(old, self) // roll back
			return fmt.Errorf("install new binary: %w", err)
		}
		os.Remove(old) // best effort; the old image may stay locked until exit
		return nil
	}

	if err := os.Rename(tmpName, self); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("install new binary: %w", err)
	}
	return nil
}

const updateCacheTTL = 3 * time.Hour

type updateCacheEntry struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

func updateCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "roach-code", "update-check.json")
}

func readUpdateCache() (string, bool) {
	data, err := os.ReadFile(updateCachePath())
	if err != nil {
		return "", false
	}
	var e updateCacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return "", false
	}
	if time.Since(e.CheckedAt) > updateCacheTTL || e.Latest == "" {
		return "", false
	}
	return e.Latest, true
}

func writeUpdateCache(latest string) {
	path := updateCachePath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	e := updateCacheEntry{Latest: latest, CheckedAt: time.Now()}
	data, _ := json.Marshal(e)
	_ = os.WriteFile(path, data, 0o644)
}
