// Package childenv holds the environment `vc` builds for the child processes it
// launches, rather than inherits from whoever launched `vc`. It is the CLI half
// of desktop/src/main/desktop-child-env.ts, and the rules are that file's.
package childenv

import (
	"strings"
	"testing"
)

// The platform is an argument, and never runtime.GOOS read inside PiPath, for
// the same reason its neighbour in the desktop takes one: the Go suite that
// gates a push runs on ubuntu (.github/workflows/test.yml), so a Windows branch
// selected from inside the function is a branch this check could not reach.
// Windows separators, the Windows layout of the bundled runtime, and the lookup
// of %SystemRoot% are exactly the parts nobody eyeballs correctly, and exactly
// the parts a GOOS-selected implementation hides until a person on Windows
// finds them.
//
// What is being pinned: Pi is launched with a PATH that VC composed — the
// directory of the bundled node first, then the smallest system set a child
// needs — and nothing the parent process had on its own PATH. The bug this
// answers is a real one, measured 06.09: the CLI handed Pi os.Environ() whole,
// Pi starts via `#!/usr/bin/env node`, and the nvm node that won the lookup had
// no zlib.createZstdDecompress, so the first answer died inside undici.
//
// parent is taken whole rather than as a systemRoot string because Windows
// spells its variable names in whatever case the parent used; desktopChildEnv
// looks them up case-insensitively and so must this, or it will find nothing on
// the machines that write SYSTEMROOT and be right everywhere it is ever run.
func TestPiPathIsBuiltAroundTheBundledNodeOnEveryPlatform(t *testing.T) {
	const unixNode = "/Users/real/.void-code/runtime/node/bin/node"
	const windowsNode = `C:\Users\real\.void-code\runtime\node\node.exe`
	for _, testCase := range []struct {
		name        string
		platform    string
		parent      []string
		privateNode string
		want        string
	}{
		{
			name:        "darwin puts the bundled node ahead of the system minimum",
			platform:    "darwin",
			parent:      []string{"PATH=/Users/real/.nvm/versions/node/v22.12.0/bin:/opt/homebrew/bin:/usr/bin:/bin", "HOME=/Users/real"},
			privateNode: unixNode,
			want:        "/Users/real/.void-code/runtime/node/bin:/usr/bin:/bin",
		},
		{
			name:        "linux is the same rule, not a third answer",
			platform:    "linux",
			parent:      []string{"PATH=/home/real/.nvm/versions/node/v22.12.0/bin:/usr/local/bin:/usr/bin:/bin"},
			privateNode: "/home/real/.void-code/runtime/node/bin/node",
			want:        "/home/real/.void-code/runtime/node/bin:/usr/bin:/bin",
		},
		{
			name:        "windows separates with a semicolon and ends at System32",
			platform:    "windows",
			parent:      []string{`PATH=C:\Users\real\AppData\Roaming\nvm\v22.12.0;C:\Windows\System32`, `SystemRoot=D:\Windows`},
			privateNode: windowsNode,
			want:        `C:\Users\real\.void-code\runtime\node;D:\Windows\System32`,
		},
		{
			name:        "windows finds SystemRoot however Windows spelled it",
			platform:    "windows",
			parent:      []string{`Path=C:\Users\real\AppData\Roaming\nvm\v22.12.0`, `SYSTEMROOT=D:\Windows`},
			privateNode: windowsNode,
			want:        `C:\Users\real\.void-code\runtime\node;D:\Windows\System32`,
		},
		{
			name:        "windows without SystemRoot still names a real System32",
			platform:    "windows",
			parent:      []string{`PATH=C:\Users\real\AppData\Roaming\nvm\v22.12.0`},
			privateNode: windowsNode,
			want:        `C:\Users\real\.void-code\runtime\node;C:\Windows\System32`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := PiPath(testCase.platform, testCase.parent, testCase.privateNode)
			if got != testCase.want {
				t.Fatalf("PiPath(%q, ...) = %q, want %q", testCase.platform, got, testCase.want)
			}
		})
	}
}

// The parent's PATH is not filtered, reordered or appended to — it is not read.
// Stated separately from the equality cases above because it is the property the
// incident was about, and a later change that starts folding "harmless" parent
// entries back in would still satisfy an expectation written as one string per
// platform only until somebody edited that string.
func TestPiPathTakesNothingFromTheParentsOwnPath(t *testing.T) {
	for _, testCase := range []struct {
		platform    string
		parent      []string
		privateNode string
		foreign     string
	}{
		{"darwin", []string{"PATH=/Users/real/.nvm/versions/node/v22.12.0/bin:/opt/homebrew/bin"}, "/Users/real/.void-code/runtime/node/bin/node", "/Users/real/.nvm/versions/node/v22.12.0/bin"},
		{"windows", []string{`Path=C:\Users\real\AppData\Roaming\nvm\v22.12.0`, `SystemRoot=C:\Windows`}, `C:\Users\real\.void-code\runtime\node\node.exe`, `C:\Users\real\AppData\Roaming\nvm\v22.12.0`},
	} {
		if got := PiPath(testCase.platform, testCase.parent, testCase.privateNode); strings.Contains(got, testCase.foreign) {
			t.Fatalf("PiPath(%q, ...) = %q, which carries the parent's %q", testCase.platform, got, testCase.foreign)
		}
	}
}

// A caller that has no bundled node to name must not turn that into a relative
// entry on the PATH of a process VC hands a credential to: "" and "." both mean
// the working directory, so an empty privateNode composed naively is an
// invitation to drop a `node` next to a project and have Pi run it. Pi failing
// to start is the correct outcome; a lookup that succeeds from the working
// directory is not.
func TestPiPathNeverPutsTheWorkingDirectoryOnPath(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		got := PiPath(platform, []string{"PATH=/evil", `SystemRoot=C:\Windows`}, "")
		separator := ":"
		if platform == "windows" {
			separator = ";"
		}
		for _, entry := range strings.Split(got, separator) {
			if entry == "" || entry == "." {
				t.Fatalf("PiPath(%q, ..., \"\") = %q, which puts the working directory on PATH", platform, got)
			}
		}
	}
}
