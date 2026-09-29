package installercontract

// The installers install the desktop app from the same release tag as vc
// (team-void-m/void-works#65).
//
// release.yml writes a "desktop" block into both copies of version.json:
// "desktop-<os>-<arch>" and "desktop-sums", each "desktop/<tag>/<file>". The
// desktop workflows name the files, the release names the keys, and the two
// installers read them. Nothing ties those four places together at runtime, a
// wrong name is just a 404 and a silent "desktop not installed", so this file
// does.

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The desktop files each release attaches, by the names the desktop workflows
// give them (desktop-mac-app.yml's zip step, desktop/package.json's nsis
// artifactName) and the list desktop-attach writes.
var desktopFilesByKey = map[string]string{
	"desktop-darwin-arm64":  "void-code-mac-arm64.zip",
	"desktop-darwin-amd64":  "void-code-mac-x64.zip",
	"desktop-windows-amd64": "Void-Code-windows-x64.exe",
	"desktop-sums":          "SHA256SUMS-desktop",
}

func TestReleaseListsTheTagsDesktopFilesInBothVersionJSONs(t *testing.T) {
	release := readInstaller(t, ".github/workflows/release.yml")
	blocks := regexp.MustCompile(`(?s)"desktop": \{\n(.*?)\n\s*\},\n`).FindAllStringSubmatch(release, -1)
	if len(blocks) != 2 {
		t.Fatalf("release.yml has %d version.json desktop blocks, want 2 (dist/ and void-auth/)", len(blocks))
	}
	entry := regexp.MustCompile(`"(desktop-[a-z0-9-]+)": "([^"]*)"`)
	for i, b := range blocks {
		got := map[string]string{}
		for _, m := range entry.FindAllStringSubmatch(b[1], -1) {
			got[m[1]] = m[2]
		}
		if len(got) != len(desktopFilesByKey) {
			t.Errorf("block %d has keys %v, want %v", i, keys(got), keys(desktopFilesByKey))
		}
		for key, file := range desktopFilesByKey {
			if want := "desktop/${VERSION}/" + file; got[key] != want {
				t.Errorf("block %d: %s = %q, want %q", i, key, got[key], want)
			}
		}
	}
}

func TestDesktopWorkflowsProduceTheListedFiles(t *testing.T) {
	mac := readInstaller(t, ".github/workflows/desktop-mac-app.yml")
	if !strings.Contains(mac, "release/void-code-mac-${{ matrix.arch }}.zip") {
		t.Error("desktop-mac-app.yml no longer zips to void-code-mac-<arch>.zip")
	}
	for _, arch := range []string{"arm64", "x64"} {
		if !strings.Contains(mac, "- arch: "+arch+"\n") {
			t.Errorf("desktop-mac-app.yml builds no %s", arch)
		}
	}
	pkg := readInstaller(t, "desktop/package.json")
	if !strings.Contains(pkg, `"artifactName": "Void-Code-windows-${arch}.${ext}"`) {
		t.Error("desktop/package.json nsis artifactName changed")
	}
	if !strings.Contains(readInstaller(t, ".github/workflows/release.yml"), "sha256sum * > SHA256SUMS-desktop") {
		t.Error("release.yml no longer writes SHA256SUMS-desktop")
	}
}

func TestInstallersReadTheDesktopKeysReleaseWrites(t *testing.T) {
	sh := readInstaller(t, "install.sh")
	for _, want := range []string{
		`vj_desktop_field "darwin-$ARCH"`, // detect_arch gives amd64 | arm64
		`vj_desktop_field sums`,
		`"\"desktop-$1\"`,
		`ditto -x -k`,
		`VC_SKIP_DESKTOP`,
	} {
		if !strings.Contains(sh, want) {
			t.Errorf("install.sh lacks %s", want)
		}
	}
	ps := readInstaller(t, "install.ps1")
	for _, want := range []string{
		`$versionJson.desktop.'desktop-windows-amd64'`,
		`$versionJson.desktop.'desktop-sums'`,
		`'/S', '/currentuser'`,
		`VC_SKIP_DESKTOP`,
	} {
		if !strings.Contains(ps, want) {
			t.Errorf("install.ps1 lacks %s", want)
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
