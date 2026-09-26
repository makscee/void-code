package piruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type entry struct {
	name, body, link string
	mode             int64
}

func piTree(version string) []entry {
	pkg := "node_modules/@earendil-works/pi-coding-agent/"
	return []entry{
		{name: pkg + "package.json", body: fmt.Sprintf(`{"name":"@earendil-works/pi-coding-agent","version":%q}`, version), mode: 0644},
		{name: pkg + "dist/cli.js", body: "#!/usr/bin/env node\n", mode: 0755},
		{name: "node_modules/.bin/pi", link: "../@earendil-works/pi-coding-agent/dist/cli.js"},
	}
}

func tarGz(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.link != "" {
			hdr = &tar.Header{Name: e.name, Linkname: e.link, Typeflag: tar.TypeSymlink, Mode: 0777}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.link == "" {
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

// release serves SHA256SUMS and the archive; sumOf lets a test publish a
// checksum that does not match the bytes served.
func release(t *testing.T, archive []byte, sumOf []byte) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	name := ArchiveName("darwin", "arm64")
	sum := sha256.Sum256(sumOf)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/SHA256SUMS":
			fmt.Fprintf(w, "%s  vc-darwin-arm64\n%s  %s\n", strings.Repeat("0", 64), hex.EncodeToString(sum[:]), name)
		case "/bin/" + name:
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func source(srv *httptest.Server) Source {
	return Source{SumsURL: srv.URL + "/SHA256SUMS", ArchiveBase: srv.URL + "/bin"}
}

func opts(home string, sources ...Source) Options {
	return Options{Home: home, Sources: sources, GOOS: "darwin", GOARCH: "arm64"}
}

func TestEnsureInstallsIntoAnInstallWithNoRuntime(t *testing.T) {
	home := t.TempDir()
	// A pre-v0.2.48 install: ~/.void-code exists, runtime/ never did.
	if err := os.MkdirAll(filepath.Join(home, ".void-code"), 0700); err != nil {
		t.Fatal(err)
	}
	archive := tarGz(t, piTree(PinnedVersion))
	srv, _ := release(t, archive, archive)

	installed, err := Ensure(opts(home, source(srv)))
	if err != nil || !installed {
		t.Fatalf("Ensure = %v, %v; want true, nil", installed, err)
	}
	if !Current(home) {
		t.Fatal("runtime not current after install")
	}
	if runtime.GOOS != "windows" {
		if target, err := os.Readlink(filepath.Join(Dir(home), "node_modules", ".bin", "pi")); err != nil || !strings.HasSuffix(target, "dist/cli.js") {
			t.Fatalf(".bin/pi = %q, %v", target, err)
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(home, ".void-code", "runtime", ".pi-*"))
	if len(leftovers) != 0 {
		t.Fatalf("staging left behind: %v", leftovers)
	}
}

func TestEnsureLeavesACurrentRuntimeAlone(t *testing.T) {
	home := t.TempDir()
	archive := tarGz(t, piTree(PinnedVersion))
	srv, hits := release(t, archive, archive)
	if _, err := Ensure(opts(home, source(srv))); err != nil {
		t.Fatal(err)
	}
	*hits = 0
	installed, err := Ensure(opts(home, source(srv)))
	if err != nil || installed || *hits != 0 {
		t.Fatalf("second Ensure = %v, %v with %d requests; want no-op", installed, err, *hits)
	}
}

func TestEnsureReplacesAnotherVersion(t *testing.T) {
	home := t.TempDir()
	old := tarGz(t, piTree("0.80.0"))
	if err := extract(old, mkdir(t, Dir(home))); err != nil {
		t.Fatal(err)
	}
	archive := tarGz(t, piTree(PinnedVersion))
	srv, _ := release(t, archive, archive)
	if installed, err := Ensure(opts(home, source(srv))); err != nil || !installed {
		t.Fatalf("Ensure = %v, %v", installed, err)
	}
	if v := InstalledVersion(home); v != PinnedVersion {
		t.Fatalf("version = %q", v)
	}
}

func TestEnsureRefusesATamperedArchiveAndKeepsTheOldRuntime(t *testing.T) {
	home := t.TempDir()
	if err := extract(tarGz(t, piTree("0.80.0")), mkdir(t, Dir(home))); err != nil {
		t.Fatal(err)
	}
	good := tarGz(t, piTree(PinnedVersion))
	tampered := tarGz(t, append(piTree(PinnedVersion), entry{name: "node_modules/evil.js", body: "x", mode: 0644}))
	srv, _ := release(t, tampered, good)

	installed, err := Ensure(opts(home, source(srv)))
	if err == nil || installed || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Ensure = %v, %v; want a hash refusal", installed, err)
	}
	if v := InstalledVersion(home); v != "0.80.0" {
		t.Fatalf("old runtime changed: version %q", v)
	}
	if _, err := os.Stat(filepath.Join(Dir(home), "node_modules", "evil.js")); !os.IsNotExist(err) {
		t.Fatal("tampered content reached the runtime")
	}
}

func TestEnsureFallsBackToTheMirror(t *testing.T) {
	home := t.TempDir()
	down := httptest.NewServer(http.NotFoundHandler())
	defer down.Close()
	archive := tarGz(t, piTree(PinnedVersion))
	srv, _ := release(t, archive, archive)
	if installed, err := Ensure(opts(home, source(down), source(srv))); err != nil || !installed {
		t.Fatalf("Ensure = %v, %v", installed, err)
	}
}

func TestEnsureRefusesAnArchiveMissingFromTheSums(t *testing.T) {
	home := t.TempDir()
	srv, _ := release(t, nil, nil)
	o := opts(home, source(srv))
	o.GOARCH = "amd64"
	if _, err := Ensure(o); err == nil || !strings.Contains(err.Error(), "lists no") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractRefusesEntriesLeavingTheRoot(t *testing.T) {
	for _, bad := range [][]entry{
		{{name: "../escape", body: "x", mode: 0644}},
		{{name: "/abs", body: "x", mode: 0644}},
		{{name: "node_modules/link", link: "../../outside"}},
		{{name: "node_modules/link", link: "/etc/passwd"}},
	} {
		root := t.TempDir()
		if err := extract(tarGz(t, bad), root); err == nil {
			t.Errorf("extract(%q) succeeded; want refusal", bad[0].name)
		}
	}
}

func TestEnsureRefusesAnIncompleteArchive(t *testing.T) {
	home := t.TempDir()
	archive := tarGz(t, piTree("0.1.0"))
	srv, _ := release(t, archive, archive)
	if _, err := Ensure(opts(home, source(srv))); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(Dir(home)); !os.IsNotExist(err) {
		t.Fatal("incomplete archive reached the runtime folder")
	}
}

func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}
