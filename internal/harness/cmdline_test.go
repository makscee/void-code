package harness

import (
	"strings"
	"testing"
)

const (
	spacedShim = `C:\Users\Darya Shmeleva\AppData\Roaming\npm\claude.cmd`
	comspec    = `C:\Windows\System32\cmd.exe`
)

// TestWinCmdExeCmdLineSpacedShim is the regression test for the spaced-home-dir
// launch bug.  The shim must be individually quoted AND the whole post-`/c`
// payload wrapped in cmd.exe's outer-quote form, so cmd.exe peels exactly the
// outer pair and the spaced path survives as one token.
func TestWinCmdExeCmdLineSpacedShim(t *testing.T) {
	args := []string{"--settings", `C:\Users\Darya Shmeleva\cc.json`, "chat"}
	got := winCmdExeCmdLine(comspec, spacedShim, args)

	want := `"` + comspec + `" /s /c ` +
		`""` + spacedShim + `" ` +
		`"--settings" ` +
		`"C:\Users\Darya Shmeleva\cc.json" ` +
		`"chat""`
	if got != want {
		t.Fatalf("command line mismatch\n  got:  %s\n  want: %s", got, want)
	}

	// The payload after `/s /c ` must be wrapped in an outer quote pair.
	const marker = " /s /c "
	idx := strings.Index(got, marker)
	if idx < 0 {
		t.Fatalf("missing %q in command line: %s", marker, got)
	}
	payload := got[idx+len(marker):]
	if !strings.HasPrefix(payload, `"`) || !strings.HasSuffix(payload, `"`) {
		t.Fatalf("payload not wrapped in outer quotes: %q", payload)
	}

	// The shim path must appear individually quoted (inner quotes) and intact.
	if !strings.Contains(got, `"`+spacedShim+`"`) {
		t.Fatalf("shim path not individually quoted intact: %s", got)
	}
	// The spaced full path must survive unbroken.
	if !strings.Contains(got, spacedShim) {
		t.Fatalf("spaced shim path was mangled: %s", got)
	}
}

// TestWinCmdExeCmdLineArgsWithSpacesQuoted verifies each arg containing a space
// is individually quoted.
func TestWinCmdExeCmdLineArgsWithSpacesQuoted(t *testing.T) {
	got := winCmdExeCmdLine(comspec, spacedShim, []string{"arg one", "arg two"})
	if !strings.Contains(got, `"arg one"`) {
		t.Errorf("arg with space not individually quoted: %s", got)
	}
	if !strings.Contains(got, `"arg two"`) {
		t.Errorf("arg with space not individually quoted: %s", got)
	}
}

// TestCmdExeQuoteDoublesEmbeddedQuote verifies an embedded double quote is
// doubled so a malicious or unusual token cannot break out of its quoted run.
func TestCmdExeQuoteDoublesEmbeddedQuote(t *testing.T) {
	got := cmdExeQuote(`a"b`)
	want := `"a""b"`
	if got != want {
		t.Fatalf("embedded quote not escaped\n  got:  %s\n  want: %s", got, want)
	}
}

// TestIsBatchShim verifies the routing predicate: only .cmd/.bat (any case) go
// through cmd.exe; .exe and extension-less binaries take the direct-exec path.
func TestIsBatchShim(t *testing.T) {
	cases := []struct {
		bin  string
		want bool
	}{
		{`C:\Users\Darya Shmeleva\AppData\Roaming\npm\claude.cmd`, true},
		{`C:\npm\claude.CMD`, true},
		{`C:\npm\claude.bat`, true},
		{`C:\npm\claude.BAT`, true},
		{`C:\Program Files\nodejs\claude.exe`, false},
		{`C:\Program Files\nodejs\claude.EXE`, false},
		{`/usr/local/bin/claude`, false}, // extension-less PE / unix
	}
	for _, c := range cases {
		if got := isBatchShim(c.bin); got != c.want {
			t.Errorf("isBatchShim(%q) = %v, want %v", c.bin, got, c.want)
		}
	}
}
