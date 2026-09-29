package codexruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// ─── the pin ────────────────────────────────────────────────────────────────

func TestVersionIsThePinnedRelease(t *testing.T) {
	if Version != "0.158.0" {
		t.Fatalf("Version = %q, want 0.158.0", Version)
	}
}

func TestDefaultBaseURLIsTheOpenAIReleaseHost(t *testing.T) {
	if DefaultBaseURL != "https://github.com/openai/codex/releases/download" {
		t.Fatalf("DefaultBaseURL = %q", DefaultBaseURL)
	}
}

// The table is the spec's, character for character: the names are the release
// assets of rust-v0.158.0 and the sums come from its codex-package_SHA256SUMS.
func TestAssetForEverySupportedPlatform(t *testing.T) {
	for _, tc := range []struct {
		goos, goarch, name, sha string
	}{
		{"darwin", "arm64", "codex-package-aarch64-apple-darwin.tar.gz", "09f2a9fde318fbcd384f15b4850c1b90930678f4805647b6bded196ccf32f590"},
		{"darwin", "amd64", "codex-package-x86_64-apple-darwin.tar.gz", "46a687a4d52e2e935c23e3acaf1002a21ccfe4b6be918f407898438b5fd24b17"},
		{"linux", "arm64", "codex-package-aarch64-unknown-linux-musl.tar.gz", "bc55b988c2e0c54ac6a6d437c4e63781e671b269752ac26592675bb9e9f99902"},
		{"linux", "amd64", "codex-package-x86_64-unknown-linux-musl.tar.gz", "b33cd426c9acab9b34c5a93200ba4fe83c8e614c18ce5ef52b2bf36408b8e18c"},
		{"windows", "arm64", "codex-package-aarch64-pc-windows-msvc.tar.gz", "331983fc12799c67e0ba4e6d37598a50121e043284c5a63d00ec9e5d872c61fc"},
		{"windows", "amd64", "codex-package-x86_64-pc-windows-msvc.tar.gz", "33f58ae85da6fc20db7d4a5190f3fec815dfc4473f558a522c7cf39bb3e88cf9"},
	} {
		got, err := AssetFor(tc.goos, tc.goarch)
		if err != nil {
			t.Fatalf("AssetFor(%s, %s): %v", tc.goos, tc.goarch, err)
		}
		if got.Name != tc.name || got.SHA256 != tc.sha {
			t.Errorf("AssetFor(%s, %s) = %+v, want {Name:%s SHA256:%s}", tc.goos, tc.goarch, got, tc.name, tc.sha)
		}
	}
}

func TestAssetForUnsupportedPlatformPointsAtPi(t *testing.T) {
	for _, p := range [][2]string{{"freebsd", "amd64"}, {"linux", "386"}, {"windows", "386"}, {"darwin", "ppc64"}, {"", ""}} {
		got, err := AssetFor(p[0], p[1])
		if err == nil {
			t.Fatalf("AssetFor(%s, %s) = %+v, nil; want an error", p[0], p[1], got)
		}
		if !strings.Contains(err.Error(), "vc runtime pi") {
			t.Errorf("AssetFor(%s, %s) error %q does not tell the person to run `vc runtime pi`", p[0], p[1], err)
		}
	}
}

func TestBinaryRelPath(t *testing.T) {
	for goos, want := range map[string]string{
		"darwin":  "bin/codex",
		"linux":   "bin/codex",
		"windows": "bin/codex.exe",
	} {
		if got := BinaryRelPath(goos); got != want {
			t.Errorf("BinaryRelPath(%q) = %q, want %q", goos, got, want)
		}
	}
}

func TestDirIsVersionedUnderTheVCRuntime(t *testing.T) {
	home := filepath.Join("some", "home")
	want := filepath.Join(home, ".void-code", "runtime", "codex", "0.158.0")
	if got := Dir(home); got != want {
		t.Fatalf("Dir(%q) = %q, want %q", home, got, want)
	}
}

// ─── Ensure ─────────────────────────────────────────────────────────────────

type entry struct {
	name, body, link string
	mode             int64
	dir              bool
}

// packageTree is the shape of the real archive (tar tzvf of darwin/arm64,
// 29.09): the binary under bin/, rg under codex-path/, resources beside.
func packageTree(goos string) []entry {
	return []entry{
		{name: "bin/", dir: true, mode: 0755},
		{name: BinaryRelPath(goos), body: "#!/bin/sh\necho codex\n", mode: 0755},
		{name: "codex-path/rg", body: "rg", mode: 0755},
		{name: "codex-resources/readme.txt", body: "resources", mode: 0644},
		{name: "codex-package.json", body: `{"version":"0.158.0"}`, mode: 0644},
	}
}

func tarGz(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		var hdr *tar.Header
		switch {
		case e.dir:
			hdr = &tar.Header{Name: e.name, Mode: e.mode, Typeflag: tar.TypeDir}
		case e.link != "":
			hdr = &tar.Header{Name: e.name, Linkname: e.link, Mode: 0777, Typeflag: tar.TypeSymlink}
		default:
			hdr = &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if !e.dir && e.link == "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sumOf(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// releaseHost serves one archive under any path and records every path asked.
type releaseHost struct {
	srv   *httptest.Server
	mu    sync.Mutex
	paths []string
}

func serveArchive(t *testing.T, archive []byte) *releaseHost {
	t.Helper()
	h := &releaseHost{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.paths = append(h.paths, r.URL.Path)
		h.mu.Unlock()
		_, _ = w.Write(archive)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

// refusingHost fails every request: an installed Codex must not reach it.
func refusingHost(t *testing.T) *releaseHost {
	t.Helper()
	h := &releaseHost{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.paths = append(h.paths, r.URL.Path)
		h.mu.Unlock()
		http.Error(w, "no network", http.StatusInternalServerError)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *releaseHost) requested() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.paths...)
}

const testGOOS, testGOARCH = "linux", "amd64"

func testOptions(home string, h *releaseHost, asset *Asset, progress *bytes.Buffer) Options {
	return Options{
		Home:     home,
		BaseURL:  h.srv.URL,
		GOOS:     testGOOS,
		GOARCH:   testGOARCH,
		Client:   h.srv.Client(),
		Progress: progress,
		Asset:    asset,
	}
}

func TestEnsureInstallsTheVerifiedPackageAndReturnsTheBinary(t *testing.T) {
	home := t.TempDir()
	archive := tarGz(t, packageTree(testGOOS))
	asset := &Asset{Name: "codex-package-test.tar.gz", SHA256: sumOf(archive)}
	host := serveArchive(t, archive)
	var progress bytes.Buffer

	got, err := Ensure(testOptions(home, host, asset, &progress))
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	want := filepath.Join(Dir(home), "bin", "codex")
	if !filepath.IsAbs(got) {
		t.Fatalf("Ensure returned a relative path %q", got)
	}
	if got != want {
		t.Fatalf("Ensure = %q, want %q", got, want)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("returned binary does not exist: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		t.Fatalf("installed codex is not executable: %v", info.Mode())
	}
	// The whole package, not the bare binary: Codex runs rg from codex-path.
	for _, rel := range []string{"codex-path/rg", "codex-resources/readme.txt", "codex-package.json"} {
		if _, err := os.Stat(filepath.Join(Dir(home), filepath.FromSlash(rel))); err != nil {
			t.Errorf("package file %s was not installed: %v", rel, err)
		}
	}
	if paths := host.requested(); len(paths) != 1 || paths[0] != "/rust-v0.158.0/codex-package-test.tar.gz" {
		t.Fatalf("requested %q, want exactly [/rust-v0.158.0/codex-package-test.tar.gz]", paths)
	}
	// 130–160 MB is not a silent wait: the person is told what is downloading.
	if !strings.Contains(strings.ToLower(progress.String()), "codex") {
		t.Errorf("no progress line about Codex was written; got %q", progress.String())
	}
}

func TestEnsureUsesTheWindowsBinaryName(t *testing.T) {
	home := t.TempDir()
	archive := tarGz(t, packageTree("windows"))
	asset := &Asset{Name: "codex-package-win.tar.gz", SHA256: sumOf(archive)}
	host := serveArchive(t, archive)
	opts := testOptions(home, host, asset, &bytes.Buffer{})
	opts.GOOS, opts.GOARCH = "windows", "amd64"

	got, err := Ensure(opts)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if want := filepath.Join(Dir(home), "bin", "codex.exe"); got != want {
		t.Fatalf("Ensure = %q, want %q", got, want)
	}
}

// Without an override Ensure takes the archive from the pinned table and asks
// for it at <base>/rust-v<ver>/<name>. The bytes served are not the real
// package, so the pinned sha256 cannot match and nothing may be installed.
func TestEnsureAsksForThePinnedAssetAndRefusesAMismatchedSum(t *testing.T) {
	home := t.TempDir()
	archive := tarGz(t, packageTree("darwin"))
	host := serveArchive(t, archive)
	opts := testOptions(home, host, nil, &bytes.Buffer{})
	opts.GOOS, opts.GOARCH = "darwin", "arm64"

	got, err := Ensure(opts)
	if err == nil {
		t.Fatalf("Ensure installed %q from bytes that do not match the pinned sha256", got)
	}
	if paths := host.requested(); len(paths) != 1 || paths[0] != "/rust-v0.158.0/codex-package-aarch64-apple-darwin.tar.gz" {
		t.Fatalf("requested %q, want exactly [/rust-v0.158.0/codex-package-aarch64-apple-darwin.tar.gz]", paths)
	}
	assertNothingInstalled(t, home)
}

func TestEnsureRefusesAMismatchedSumBeforeUnpacking(t *testing.T) {
	home := t.TempDir()
	archive := tarGz(t, packageTree(testGOOS))
	asset := &Asset{Name: "codex-package-test.tar.gz", SHA256: sumOf([]byte("some other archive"))}
	host := serveArchive(t, archive)

	got, err := Ensure(testOptions(home, host, asset, &bytes.Buffer{}))
	if err == nil {
		t.Fatalf("Ensure = %q, nil; want a sha256 mismatch error", got)
	}
	assertNothingInstalled(t, home)
}

func TestEnsureRefusesAnUnsupportedPlatformWithoutDownloading(t *testing.T) {
	home := t.TempDir()
	host := refusingHost(t)
	opts := testOptions(home, host, nil, &bytes.Buffer{})
	opts.GOOS, opts.GOARCH = "freebsd", "amd64"

	if got, err := Ensure(opts); err == nil {
		t.Fatalf("Ensure on freebsd/amd64 = %q, nil; want an error", got)
	} else if !strings.Contains(err.Error(), "vc runtime pi") {
		t.Errorf("error %q does not point at `vc runtime pi`", err)
	}
	if paths := host.requested(); len(paths) != 0 {
		t.Fatalf("unsupported platform still downloaded: %q", paths)
	}
	assertNothingInstalled(t, home)
}

func TestEnsureRefusesAPackageWithoutTheBinary(t *testing.T) {
	home := t.TempDir()
	archive := tarGz(t, []entry{{name: "codex-path/rg", body: "rg", mode: 0755}})
	asset := &Asset{Name: "codex-package-test.tar.gz", SHA256: sumOf(archive)}
	host := serveArchive(t, archive)

	if got, err := Ensure(testOptions(home, host, asset, &bytes.Buffer{})); err == nil {
		t.Fatalf("Ensure = %q, nil for a package with no bin/codex", got)
	}
	assertNothingInstalled(t, home)
}

func TestEnsureDoesNotTouchTheNetworkOnceInstalled(t *testing.T) {
	home := t.TempDir()
	archive := tarGz(t, packageTree(testGOOS))
	asset := &Asset{Name: "codex-package-test.tar.gz", SHA256: sumOf(archive)}
	first, err := Ensure(testOptions(home, serveArchive(t, archive), asset, &bytes.Buffer{}))
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}

	offline := refusingHost(t)
	for _, a := range []*Asset{asset, nil} {
		opts := testOptions(home, offline, a, &bytes.Buffer{})
		second, err := Ensure(opts)
		if err != nil {
			t.Fatalf("Ensure with Codex installed failed offline (asset override %v): %v", a != nil, err)
		}
		if second != first {
			t.Fatalf("second Ensure = %q, want %q", second, first)
		}
	}
	if paths := offline.requested(); len(paths) != 0 {
		t.Fatalf("installed Codex still went to the network: %q", paths)
	}
}

func TestEnsureRefusesEntriesLeavingTheInstallFolder(t *testing.T) {
	outside := t.TempDir()
	absolute := filepath.ToSlash(filepath.Join(outside, "abs-evil"))
	for name, bad := range map[string]entry{
		"dotdot":        {name: "../evil", body: "x", mode: 0644},
		"nested dotdot": {name: "bin/../../evil", body: "x", mode: 0644},
		"absolute":      {name: absolute, body: "x", mode: 0644},
		"escaping link": {name: "codex-path/escape", link: "../../../../../../evil-link"},
		"absolute link": {name: "codex-path/abs-link", link: filepath.ToSlash(outside)},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			tree := append(packageTree(testGOOS), bad)
			archive := tarGz(t, tree)
			asset := &Asset{Name: "codex-package-test.tar.gz", SHA256: sumOf(archive)}
			host := serveArchive(t, archive)

			if got, err := Ensure(testOptions(home, host, asset, &bytes.Buffer{})); err == nil {
				t.Fatalf("Ensure = %q, nil for an archive with entry %q", got, bad.name)
			}
			assertNothingInstalled(t, home)
			for _, p := range []string{
				filepath.Join(filepath.Dir(Dir(home)), "evil"),
				filepath.Join(Dir(home), "..", "evil"),
				filepath.Join(home, ".void-code", "runtime", "evil"),
				filepath.Join(outside, "abs-evil"),
			} {
				if _, err := os.Lstat(p); err == nil {
					t.Fatalf("archive entry %q wrote %s outside the install folder", bad.name, p)
				}
			}
		})
	}
}

// assertNothingInstalled: a refused install leaves no Codex, no half-unpacked
// staging tree and no downloaded archive under home.
func assertNothingInstalled(t *testing.T, home string) {
	t.Helper()
	if entries, err := os.ReadDir(Dir(home)); err == nil && len(entries) > 0 {
		t.Fatalf("install dir %s is not empty after a refused install: %v", Dir(home), entries)
	}
	_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			t.Errorf("refused install left %s behind", path)
		}
		return nil
	})
}

// ─── config.toml ────────────────────────────────────────────────────────────

// parseTOML is just enough TOML to read what WriteConfig produces: sections,
// `key = value` pairs, comments. Keys before the first [section] land in "".
func parseTOML(t *testing.T, text string) map[string]map[string]string {
	t.Helper()
	out := map[string]map[string]string{"": {}}
	section := ""
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.Trim(line, "[]"))
			if _, dup := out[section]; dup {
				t.Fatalf("line %d: section [%s] declared twice", i+1, section)
			}
			out[section] = map[string]string{}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("line %d is not key = value: %q", i+1, raw)
		}
		key = strings.TrimSpace(key)
		if _, dup := out[section][key]; dup {
			t.Fatalf("line %d: key %s repeated in [%s]", i+1, key, section)
		}
		out[section][key] = strings.TrimSpace(value)
	}
	return out
}

func noSpace(s string) string { return strings.Join(strings.Fields(s), "") }

func TestWriteConfigRoutesCodexThroughTheRelay(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	// The token lives in the environment the child gets, never in this file;
	// a writer that reads it from env must still not put it here.
	t.Setenv("VC_AUTH_TOKEN", "tok-must-not-be-written-7f3a")

	if err := WriteConfig(codexHome, "https://relay.makscee.ru:443"); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatalf("config.toml not written: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "tok-must-not-be-written-7f3a") {
		t.Fatal("config.toml carries the VC token")
	}
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	if !strings.HasPrefix(first, "#") {
		t.Errorf("config.toml does not open with a managed-file comment; first line %q", first)
	}

	doc := parseTOML(t, text)
	expect := func(section, key, want string) {
		t.Helper()
		got, ok := doc[section][key]
		if !ok {
			t.Errorf("[%s] %s missing\n%s", section, key, text)
			return
		}
		if noSpace(got) != noSpace(want) {
			t.Errorf("[%s] %s = %s, want %s", section, key, got, want)
		}
	}
	expect("", "model", `"gpt-6-sol"`)
	expect("", "model_provider", `"void"`)
	expect("", "check_for_update_on_startup", "false")
	expect("analytics", "enabled", "false")
	expect("feedback", "enabled", "false")
	expect("model_providers.void", "name", `"Void relay"`)
	expect("model_providers.void", "base_url", `"https://relay.makscee.ru:443/codex"`)
	expect("model_providers.void", "env_key", `"VC_AUTH_TOKEN"`)
	expect("model_providers.void", "wire_api", `"responses"`)
	expect("model_providers.void", "requires_openai_auth", "false")
	expect("model_providers.void", "env_http_headers", `{ "x-void-provider" = "VC_CODEX_PROVIDER" }`)
}

func TestWriteConfigTakesTheRelayFromItsArgument(t *testing.T) {
	codexHome := t.TempDir()
	if err := WriteConfig(codexHome, "http://127.0.0.1:9443"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	doc := parseTOML(t, string(data))
	if got := doc["model_providers.void"]["base_url"]; got != `"http://127.0.0.1:9443/codex"` {
		t.Fatalf("base_url = %s, want \"http://127.0.0.1:9443/codex\"", got)
	}
}

// The file is managed: every launch rewrites it whole, so a hand edit or an
// older layout never survives next to the managed keys.
func TestWriteConfigReplacesTheWholeFile(t *testing.T) {
	codexHome := t.TempDir()
	path := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(path, []byte("model = \"foreign-model\"\n[model_providers.openai]\nbase_url = \"https://api.openai.com\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteConfig(codexHome, "https://relay.test:443"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{"foreign-model", "api.openai.com", "model_providers.openai"} {
		if strings.Contains(string(data), stale) {
			t.Fatalf("stale %q survived the rewrite:\n%s", stale, data)
		}
	}
	if doc := parseTOML(t, string(data)); doc[""]["model"] != `"gpt-6-sol"` {
		t.Fatalf("model = %s after rewrite", doc[""]["model"])
	}
}
