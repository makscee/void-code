// Package piruntime provisions VC's managed Pi runtime from a release archive.
//
// Every install made before v0.2.48 put Pi in the global npm folder, and from
// v0.2.48 vc accepts only ~/.void-code/runtime/pi. `vc update` swaps the binary
// and never created that folder, so those installs broke on their first update,
// and install.sh's npm install of Pi fails on some networks. The release now
// publishes the pinned Pi tree per platform (pi-runtime-<ver>-<os>-<arch>.tar.gz)
// next to the vc binaries, and vc puts it in place itself: download, check the
// SHA-256 from the release's SHA256SUMS, unpack into a staging folder beside
// runtime/pi, and swap it in with a rename. On any failure the existing runtime
// is left exactly as it was.
//
// npm is never called here, and Pi is never taken from PATH.
package piruntime

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// PinnedVersion is the Pi the vc extension supports. It must equal the pin in
// desktop/runtime/pi/package.json and PI_VERSION in install.sh and install.ps1
// (installer_pi_pin_test.go enforces all three).
const PinnedVersion = "0.87.1"

// maxArchiveBytes bounds a download. The unpacked tree is about 150 MB and the
// gzip about 30 MB; anything far larger is not ours.
const maxArchiveBytes = 256 << 20

// Source is one place the archive and its checksum list are served from.
type Source struct {
	// SumsURL is the release's SHA256SUMS.
	SumsURL string
	// ArchiveBase is the URL prefix the archive name is appended to.
	ArchiveBase string
}

// DefaultSources are tried in order: our host first, the GitHub release as the
// mirror, the same primary/mirror scheme install.sh uses for the vc binary.
var DefaultSources = []Source{
	{SumsURL: "https://auth.makscee.ru/vc/SHA256SUMS", ArchiveBase: "https://auth.makscee.ru/vc/bin"},
	{SumsURL: "https://github.com/makscee/void-code/releases/latest/download/SHA256SUMS", ArchiveBase: "https://github.com/makscee/void-code/releases/latest/download"},
}

// ArchiveName is the release asset holding the pinned Pi tree for a platform.
func ArchiveName(goos, goarch string) string {
	return fmt.Sprintf("pi-runtime-%s-%s-%s.tar.gz", PinnedVersion, goos, goarch)
}

// Dir is the managed runtime folder under home.
func Dir(home string) string {
	return filepath.Join(home, ".void-code", "runtime", "pi")
}

func packageDir(root string) string {
	return filepath.Join(root, "node_modules", "@earendil-works", "pi-coding-agent")
}

// InstalledVersion reads the managed Pi's version from its package.json, or ""
// when there is none.
func InstalledVersion(home string) string {
	return treeVersion(Dir(home))
}

func treeVersion(root string) string {
	data, err := os.ReadFile(filepath.Join(packageDir(root), "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct{ Version string }
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	return pkg.Version
}

// Current reports whether home holds the managed Pi at PinnedVersion with its
// entrypoint in place.
func Current(home string) bool {
	return treeComplete(Dir(home)) == nil
}

func treeComplete(root string) error {
	if v := treeVersion(root); v != PinnedVersion {
		if v == "" {
			return fmt.Errorf("Pi package.json missing")
		}
		return fmt.Errorf("Pi is %s, vc pins %s", v, PinnedVersion)
	}
	info, err := os.Lstat(filepath.Join(packageDir(root), "dist", "cli.js"))
	if err != nil {
		return fmt.Errorf("Pi entrypoint: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Pi entrypoint is not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("Pi entrypoint is not executable")
	}
	return nil
}

// Options controls Ensure. Zero values mean the defaults.
type Options struct {
	Home    string
	Sources []Source
	Client  *http.Client
	GOOS    string
	GOARCH  string
}

// Ensure puts the pinned Pi runtime in place when it is missing or at another
// version. It returns true when it installed one. On error the runtime folder
// is unchanged.
func Ensure(opts Options) (bool, error) {
	if opts.Home == "" {
		return false, fmt.Errorf("no home directory")
	}
	if Current(opts.Home) {
		return false, nil
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.Sources == nil {
		opts.Sources = DefaultSources
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 10 * time.Minute}
	}
	if len(opts.Sources) == 0 {
		return false, fmt.Errorf("no source to download Pi from")
	}
	name := ArchiveName(opts.GOOS, opts.GOARCH)
	var errs []error
	for _, src := range opts.Sources {
		archive, err := download(opts.Client, src, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := install(opts.Home, archive); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, fmt.Errorf("download %s: %w", name, errors.Join(errs...))
}

// download fetches the archive from one source and checks it against that
// source's SHA256SUMS. A missing or mismatched entry is a refusal.
func download(client *http.Client, src Source, name string) ([]byte, error) {
	sums, err := fetch(client, src.SumsURL, 1<<20)
	if err != nil {
		return nil, err
	}
	want, ok := lookupSum(sums, name)
	if !ok {
		return nil, fmt.Errorf("%s lists no %s", src.SumsURL, name)
	}
	url := strings.TrimRight(src.ArchiveBase, "/") + "/" + name
	data, err := fetch(client, url, maxArchiveBytes)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("%s: SHA-256 %s does not match the release's %s", url, got, want)
	}
	return data, nil
}

func fetch(client *http.Client, url string, limit int64) ([]byte, error) {
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", url, limit)
	}
	return data, nil
}

// lookupSum finds name in sha256sum output ("<hex>  <name>" or "<hex> *<name>").
func lookupSum(sums []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// install unpacks a verified archive beside runtime/pi and swaps it in.
func install(home string, archive []byte) error {
	dest := Dir(home)
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return fmt.Errorf("create %s: %w", parent, err)
	}
	stage, err := os.MkdirTemp(parent, ".pi-stage-*")
	if err != nil {
		return fmt.Errorf("stage Pi runtime: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := extract(archive, stage); err != nil {
		return fmt.Errorf("unpack Pi runtime: %w", err)
	}
	if err := treeComplete(stage); err != nil {
		return fmt.Errorf("unpacked Pi runtime is incomplete: %w", err)
	}
	backup := ""
	if _, err := os.Lstat(dest); err == nil {
		b, err := os.MkdirTemp(parent, ".pi-backup-*")
		if err != nil {
			return fmt.Errorf("prepare Pi runtime rollback: %w", err)
		}
		if err := os.Remove(b); err != nil {
			return fmt.Errorf("prepare Pi runtime rollback: %w", err)
		}
		if err := os.Rename(dest, b); err != nil {
			return fmt.Errorf("move aside the old Pi runtime: %w", err)
		}
		backup = b
	}
	if err := os.Rename(stage, dest); err != nil {
		if backup != "" {
			if rbErr := os.Rename(backup, dest); rbErr != nil {
				return fmt.Errorf("put Pi runtime in place: %w (rollback failed: %v)", err, rbErr)
			}
		}
		return fmt.Errorf("put Pi runtime in place: %w", err)
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// extract unpacks a .tar.gz into root. Entries must stay inside root: absolute
// paths, "..", hard links and symlinks pointing outside are refused.
func extract(archive []byte, root string) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel := filepath.FromSlash(strings.TrimPrefix(hdr.Name, "./"))
		if rel == "" || rel == "." {
			continue
		}
		if !local(rel) {
			return fmt.Errorf("entry %q leaves the runtime folder", hdr.Name)
		}
		target := filepath.Join(root, rel)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			mode := os.FileMode(0644)
			if hdr.Mode&0111 != 0 {
				mode = 0755
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(hdr.Linkname) || !local(filepath.Join(filepath.Dir(rel), filepath.FromSlash(hdr.Linkname))) {
				return fmt.Errorf("symlink %q -> %q leaves the runtime folder", hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		default:
			return fmt.Errorf("entry %q has unsupported type %q", hdr.Name, string(hdr.Typeflag))
		}
	}
}

// local reports whether a relative path stays inside its root.
func local(rel string) bool {
	return filepath.IsLocal(rel)
}
