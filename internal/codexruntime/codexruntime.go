// Package codexruntime installs the pinned OpenAI Codex CLI for vc and writes
// the configuration that routes it through the Void relay.
//
// The package is the whole release archive, not the bare binary: Codex runs rg
// from codex-path/, and on Linux and Windows it carries sandbox helpers beside
// it. It lives in ~/.void-code/runtime/codex/<version>/; the person's own
// ~/.codex and any codex on PATH are never used.
//
// Installing mirrors internal/piruntime: download to a temporary file, check
// the pinned SHA-256 before anything is unpacked, unpack into a staging folder
// beside the target and swap it in with a rename. On any failure nothing is
// left behind.
package codexruntime

import (
	"fmt"
	"path/filepath"
)

// Version is the pinned Codex release (tag rust-v0.158.0).
const Version = "0.158.0"

// DefaultBaseURL is where OpenAI publishes Codex release assets; an asset is
// fetched from <base>/rust-v<Version>/<name>.
const DefaultBaseURL = "https://github.com/openai/codex/releases/download"

// Asset is one release archive and its pinned SHA-256.
type Asset struct {
	Name   string
	SHA256 string
}

// assets are the rust-v0.158.0 packages, sums from its codex-package_SHA256SUMS.
var assets = map[string]Asset{
	"darwin/arm64":  {"codex-package-aarch64-apple-darwin.tar.gz", "09f2a9fde318fbcd384f15b4850c1b90930678f4805647b6bded196ccf32f590"},
	"darwin/amd64":  {"codex-package-x86_64-apple-darwin.tar.gz", "46a687a4d52e2e935c23e3acaf1002a21ccfe4b6be918f407898438b5fd24b17"},
	"linux/arm64":   {"codex-package-aarch64-unknown-linux-musl.tar.gz", "bc55b988c2e0c54ac6a6d437c4e63781e671b269752ac26592675bb9e9f99902"},
	"linux/amd64":   {"codex-package-x86_64-unknown-linux-musl.tar.gz", "b33cd426c9acab9b34c5a93200ba4fe83c8e614c18ce5ef52b2bf36408b8e18c"},
	"windows/arm64": {"codex-package-aarch64-pc-windows-msvc.tar.gz", "331983fc12799c67e0ba4e6d37598a50121e043284c5a63d00ec9e5d872c61fc"},
	"windows/amd64": {"codex-package-x86_64-pc-windows-msvc.tar.gz", "33f58ae85da6fc20db7d4a5190f3fec815dfc4473f558a522c7cf39bb3e88cf9"},
}

// AssetFor returns the pinned archive for a platform. A platform Codex is not
// published for is an error that points the person back at Pi.
func AssetFor(goos, goarch string) (Asset, error) {
	if a, ok := assets[goos+"/"+goarch]; ok {
		return a, nil
	}
	return Asset{}, fmt.Errorf("Codex недоступен на %s/%s, выберите Pi: vc runtime pi", goos, goarch)
}

// BinaryRelPath is the Codex executable inside the package, slash-separated.
func BinaryRelPath(goos string) string {
	if goos == "windows" {
		return "bin/codex.exe"
	}
	return "bin/codex"
}

// Dir is the install folder of the pinned version under home.
func Dir(home string) string {
	return filepath.Join(home, ".void-code", "runtime", "codex", Version)
}
