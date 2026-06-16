//go:build windows

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// applyCmdLine routes batch-shim launches through the command processor.
//
// npm installs claude as a .cmd shim under the user's home (which may contain a
// space, e.g. C:\Users\Darya Shmeleva\...). A .cmd/.bat is a script, not a PE
// image, so CreateProcess cannot run it via lpApplicationName — that field must
// name a real executable. We therefore set cmd.Path to %ComSpec% (cmd.exe, a
// real PE) and hand the shim plus its args to cmd.exe as a /s /c command line,
// quoted in cmd.exe's own dialect (winCmdExeCmdLine) so the spaced path
// survives as one token.
//
// A real .exe (or extension-less PE) is left on the direct-exec path: Go's
// default argv builder (syscall.EscapeArg) already quotes a spaced path
// correctly for CreateProcess, and routing it through cmd.exe would only add
// fragility. No-op on Unix (cmdline_other.go).
func applyCmdLine(cmd *exec.Cmd, bin string, args []string) {
	if !isBatchShim(bin) {
		return // real PE: keep direct exec.Command(bin, args...) — Go quotes it
	}

	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		// Fallback: cmd.exe under the system root (or bare cmd.exe if unset).
		if sysRoot := os.Getenv("SystemRoot"); sysRoot != "" {
			comspec = filepath.Join(sysRoot, "System32", "cmd.exe")
		} else {
			comspec = "cmd.exe"
		}
	}

	cmd.Path = comspec
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: winCmdExeCmdLine(comspec, bin, args),
	}
}
