package codexruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// maxArchiveBytes bounds a download. The packages are 130–160 MB; anything far
// larger is not the release we pinned.
const maxArchiveBytes = 512 << 20

// Options controls Ensure. Zero values mean the defaults: the running
// platform, DefaultBaseURL, the pinned asset and no progress output.
type Options struct {
	Home     string
	BaseURL  string
	GOOS     string
	GOARCH   string
	Client   *http.Client
	Progress io.Writer
	// Asset overrides the pinned archive for the platform (tests).
	Asset *Asset
}

func (o Options) withDefaults() Options {
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 30 * time.Minute}
	}
	if o.Progress == nil {
		o.Progress = io.Discard
	}
	return o
}

// Ensure returns the absolute path of the pinned Codex binary, installing the
// package first when it is not there. An installed version is returned without
// any network access. On error nothing is installed.
func Ensure(opts Options) (string, error) {
	if opts.Home == "" {
		return "", fmt.Errorf("no home directory")
	}
	opts = opts.withDefaults()
	home, err := filepath.Abs(opts.Home)
	if err != nil {
		return "", err
	}
	dest := Dir(home)
	bin := filepath.Join(dest, filepath.FromSlash(BinaryRelPath(opts.GOOS)))
	if binaryUsable(bin, opts.GOOS) == nil {
		return bin, nil
	}
	asset, err := pickAsset(opts)
	if err != nil {
		return "", err
	}
	url := strings.TrimRight(opts.BaseURL, "/") + "/rust-v" + Version + "/" + asset.Name
	fmt.Fprintf(opts.Progress, "vc: скачиваю Codex %s (%s, ~150 МБ) …\n", Version, asset.Name)
	if err := install(opts, url, asset, dest); err != nil {
		return "", err
	}
	fmt.Fprintf(opts.Progress, "vc: Codex %s установлен.\n", Version)
	return bin, nil
}

func pickAsset(opts Options) (Asset, error) {
	if opts.Asset != nil {
		return *opts.Asset, nil
	}
	return AssetFor(opts.GOOS, opts.GOARCH)
}

// binaryUsable checks that the Codex executable is a regular file and, where
// the mode bits mean something, executable.
func binaryUsable(bin, goos string) error {
	info, err := os.Lstat(bin)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", bin)
	}
	if goos != "windows" && runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("%s is not executable", bin)
	}
	return nil
}

// install downloads and verifies the archive, unpacks it beside dest and swaps
// it in.
func install(opts Options, url string, asset Asset, dest string) error {
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return fmt.Errorf("create %s: %w", parent, err)
	}
	clearLeftovers(parent)
	archive, err := downloadVerified(opts.Client, url, asset.SHA256, parent, opts.Progress)
	if err != nil {
		return err
	}
	defer os.Remove(archive)
	stage, err := os.MkdirTemp(parent, ".codex-stage-*")
	if err != nil {
		return fmt.Errorf("stage Codex: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := extractFile(archive, stage); err != nil {
		return fmt.Errorf("unpack Codex: %w", err)
	}
	if err := binaryUsable(filepath.Join(stage, filepath.FromSlash(BinaryRelPath(opts.GOOS))), opts.GOOS); err != nil {
		return fmt.Errorf("the Codex package has no usable %s: %w", BinaryRelPath(opts.GOOS), err)
	}
	return swapIn(stage, dest)
}

// downloadVerified streams url into a temporary file in dir and returns its
// path only when the SHA-256 matches want. On any failure the file is removed.
func downloadVerified(client *http.Client, url, want, dir string, progress io.Writer) (string, error) {
	f, err := os.CreateTemp(dir, ".codex-download-*")
	if err != nil {
		return "", fmt.Errorf("download Codex: %w", err)
	}
	path := f.Name()
	got, err := fetchInto(client, url, f, progress)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil && !strings.EqualFold(got, want) {
		err = fmt.Errorf("%s: SHA-256 %s does not match the pinned %s", url, got, want)
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// fetchInto copies the body of url into w and returns its hex SHA-256. When
// the size is known, the share downloaded so far goes to progress.
func fetchInto(client *http.Client, url string, w io.Writer, progress io.Writer) (string, error) {
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("download Codex: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download Codex: HTTP %d from %s", resp.StatusCode, url)
	}
	hash := sha256.New()
	dst := io.MultiWriter(w, hash)
	if resp.ContentLength > 0 {
		dst = io.MultiWriter(dst, &percentWriter{out: progress, total: resp.ContentLength, next: 10})
	}
	n, err := io.Copy(dst, io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return "", fmt.Errorf("download Codex: %w", err)
	}
	if n > maxArchiveBytes {
		return "", fmt.Errorf("download Codex: %s is larger than %d bytes", url, maxArchiveBytes)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// swapIn renames stage to dest, moving aside (and on success removing) a
// broken install already at dest.
func swapIn(stage, dest string) error {
	backup := ""
	if _, err := os.Lstat(dest); err == nil {
		b, err := os.MkdirTemp(filepath.Dir(dest), ".codex-backup-*")
		if err != nil {
			return fmt.Errorf("prepare Codex rollback: %w", err)
		}
		if err := os.Remove(b); err != nil {
			return fmt.Errorf("prepare Codex rollback: %w", err)
		}
		if err := rename(dest, b); err != nil {
			return fmt.Errorf("move aside the old Codex: %w", err)
		}
		backup = b
	}
	if err := rename(stage, dest); err != nil {
		if backup != "" {
			if rbErr := rename(backup, dest); rbErr != nil {
				return fmt.Errorf("put Codex in place: %w (rollback failed: %v)", err, rbErr)
			}
		}
		return fmt.Errorf("put Codex in place: %w", err)
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// rename is os.Rename, retried briefly on Windows, where an antivirus scanner
// often holds files it has just seen written (as in internal/piruntime).
func rename(from, to string) error {
	err := os.Rename(from, to)
	for i := 0; err != nil && runtime.GOOS == "windows" && i < 20; i++ {
		time.Sleep(150 * time.Millisecond)
		err = os.Rename(from, to)
	}
	return err
}
