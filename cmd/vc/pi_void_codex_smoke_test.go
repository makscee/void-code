// rails:pin-on-coverage the pinned Pi works today, so there is nothing to go red; strength shown by mutation instead -- a renamed provider id, an unreachable registerProvider, a bootstrap offering no allowed model, provisioning taken away, and a stray file in the pinned tree were each killed
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The qualification the pinned-Pi step has been claiming to run.
//
// check-pinned-pi-smoke.mjs has always asked go for this test by name, and until now there was no
// such test: go answered an unmatched filter with `ok ... [no tests to run]` and exit 0, so every
// push and every release carried a green step that ran nothing.
//
// What it checks is the consequence, not the mechanism. Whether Pi "loaded the extension" is not
// observable to anybody who reports a problem; what they see is that VC does not offer a provider.
// So this asks the pinned Node and the pinned Pi entry to list their models, with the real
// extension source from pi_extension.go, and looks for void-codex and its models in that list.
//
// The environment handed to Pi is built from nothing rather than inherited, and that is
// load-bearing: with the developer's HOME in place, an extension already installed in
// ~/.pi/agent/extensions would register void-codex and the test would pass without exercising the
// pinned tree at all.
//
// Honest limit: this proves that the extension registers the provider and its models under the
// pinned runtime. It does not talk to a relay and it does not run a model -- the bootstrap is a
// local stub, so everything downstream of registration is out of its reach.

// The models the extension is willing to publish for the codex provider (pi_extension.go filters
// whatever the bootstrap offers against this set).
var voidCodexSmokeModels = []string{"gpt-5.6-terra", "gpt-5.6-sol", "gpt-5.6-luna"}

const voidCodexSmokeBootstrap = `{"version":1,"relayUrl":"https://relay.invalid","authToken":"smoke",` +
	`"providers":[{"kind":"codex","relayProviderId":"smoke-provider","models":["gpt-5.6-terra","gpt-5.6-sol","gpt-5.6-luna"]}]}`

func TestPiVoidCodexExtensionSmoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bootstrap stub is a POSIX shell script; the staged manifest this reads is darwin-arm64 anyway")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	prerequisites := requireOrSkipPinnedPiSmoke(t, root)

	work := t.TempDir()
	home := filepath.Join(work, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	extension := filepath.Join(work, "void-code.ts")
	if err := os.WriteFile(extension, []byte(piVoidCodexExtensionSource), 0600); err != nil {
		t.Fatal(err)
	}
	// The extension asks a trusted executable for its bootstrap
	// (execFileSync(VC_BOOTSTRAP_EXECUTABLE, ["pi-bootstrap"])). Answer it locally: registration is
	// what is under test, and reaching a relay to observe it would make this a network test.
	bootstrap := filepath.Join(work, "bootstrap.sh")
	if err := os.WriteFile(bootstrap, []byte("#!/bin/sh\n[ \"$1\" = \"pi-bootstrap\" ] || exit 1\nprintf '%s' '"+voidCodexSmokeBootstrap+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, prerequisites.node, prerequisites.piEntry, "-e", extension, "--offline", "--list-models")
	command.Dir = work
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "TERM=dumb", "VC_BOOTSTRAP_EXECUTABLE=" + bootstrap}
	listed, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pinned Pi did not survive --list-models with the managed extension: %v\n%s", err, listed)
	}

	// A row of the table is the provider name followed by the model id. Matched as a pair rather
	// than as a substring of the output: an error message that happens to name void-codex would
	// otherwise read as success.
	rows := map[string]map[string]bool{}
	for _, line := range strings.Split(string(listed), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 {
			if rows[fields[0]] == nil {
				rows[fields[0]] = map[string]bool{}
			}
			rows[fields[0]][fields[1]] = true
		}
	}
	var missing []string
	for _, model := range voidCodexSmokeModels {
		if !rows["void-codex"][model] {
			missing = append(missing, model)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("the pinned Pi did not register void-codex: %s not listed.\n"+
			"In the app this looks like \"VC cannot see a provider\" and like nothing else: no model can be\n"+
			"chosen and no chat can start, whatever the account and the subscription say.\n"+
			"Suspect the extension in cmd/vc/pi_extension.go or the pinned Pi's extension loader.\n"+
			"What Pi listed:\n%s", strings.Join(missing, ", "), listed)
	}
}
