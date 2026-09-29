package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	term "github.com/charmbracelet/x/term"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/runtimechoice"
)

// runtimeSwitchFileEnv names the file a runtime writes to ask vc for the other
// runtime (spec 2026-09-29-vc-runtime-switch-design). vc sets it for the
// children it supervises and nowhere else: the desktop session never has it.
const runtimeSwitchFileEnv = "VC_RUNTIME_SWITCH_FILE"

// runtimeSwitchPollInterval is how often vc looks for a request while a child
// runs. A var for tests.
var runtimeSwitchPollInterval = 300 * time.Millisecond

// captureTerminal records the terminal's state before a child takes it and
// returns what puts it back. A var for tests.
var captureTerminal = func() func() {
	fd := os.Stdin.Fd()
	if !term.IsTerminal(fd) {
		return func() {}
	}
	state, err := term.GetState(fd)
	if err != nil {
		return func() {}
	}
	return func() { _ = term.Restore(fd, state) }
}

// terminalResetSequence undoes the modes a runtime stopped mid-session leaves
// on: the alternate screen, the hidden cursor, bracketed paste, every mouse
// report mode and the kitty keyboard mode. Codex 0.158 dies on SIGTERM without
// restoring any of them.
const terminalResetSequence = "\x1b[?1049l" + // leave the alternate screen
	"\x1b[?25h" + // show the cursor
	"\x1b[?2004l" + // bracketed paste off
	"\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l" + // mouse reports off
	"\x1b[<u" // pop the kitty keyboard mode

// superviseRuntimes runs the chosen runtime and, whenever a child asks for the
// other one through the request file, starts that one in the same terminal.
// Admission and the wallet notice happened once, before; the notice goes to
// the first child only. Without a request vc returns the child's own result.
//
// A switch whose target cannot be prepared (no ChatGPT grant, a failed
// install) does not end vc: the reason is printed, the saved choice goes back
// and the previous runtime starts again as an ordinary supervised child. When
// that fallback cannot be prepared either, vc ends with its error.
func superviseRuntimes(cfg config.Config, token, notice string, current runtimechoice.Runtime) error {
	dir, err := os.MkdirTemp("", "vc-runtime-switch-")
	if err != nil {
		return fmt.Errorf("prepare runtime switch: %w", err)
	}
	defer os.RemoveAll(dir)
	switchFile := filepath.Join(dir, "switch")
	var fallback runtimechoice.Runtime
	for {
		child, err := prepareRuntimeChild(current, cfg, token, notice)
		if err != nil {
			if fallback == "" {
				return err
			}
			fallBack(current, fallback, err)
			current, fallback = fallback, ""
			continue
		}
		notice, fallback = "", ""
		child.env = append(child.env, runtimeSwitchFileEnv+"="+switchFile)
		next, runErr := runSupervisedChild(child, current, switchFile)
		if next == "" {
			return runErr
		}
		_ = os.Remove(switchFile)
		fmt.Fprintf(os.Stderr, "vc: переключаюсь на %s…\n", next.Label())
		current, fallback = next, current
	}
}

// fallBack tells the person why failed could not start and puts the saved
// choice back to previous, so the next vc does not fail the same way.
func fallBack(failed, previous runtimechoice.Runtime, reason error) {
	fmt.Fprintf(os.Stderr, "vc: %s не запустился: %v\n", failed.Label(), reason)
	fmt.Fprintf(os.Stderr, "vc: возвращаюсь к %s…\n", previous.Label())
	if err := runtimechoice.Save(previous); err != nil {
		fmt.Fprintf(os.Stderr, "vc: warning: выбор %s не восстановлен: %v\n", previous.Label(), err)
	}
}

// runSupervisedChild runs one child and returns the runtime it asked for, or
// "" with the child's own result when it asked for nothing. Pi closes itself
// after its request; Codex never exits on its own, so vc stops it by
// cancelling its context. The terminal is put back after every child.
func runSupervisedChild(child runtimeChild, current runtimechoice.Runtime, switchFile string) (runtimechoice.Runtime, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	restore := captureTerminal()
	done := make(chan error, 1)
	go func() { done <- spawnHarness(ctx, child.exe, child.args, child.env) }()
	ticker := time.NewTicker(runtimeSwitchPollInterval)
	defer ticker.Stop()
	var requested runtimechoice.Runtime
	stopped := false
	for {
		select {
		case runErr := <-done:
			if requested == "" {
				requested = pendingSwitch(switchFile, current)
			}
			if stopped {
				resetTerminalModes()
			}
			restore()
			if requested != "" {
				return requested, nil
			}
			return "", runErr
		case <-ticker.C:
			if requested != "" {
				continue
			}
			requested = pendingSwitch(switchFile, current)
			if requested != "" && current == runtimechoice.Codex {
				stopped = true
				cancel()
			}
		}
	}
}

// pendingSwitch reads the request file: a runtime other than current, or ""
// for no request, the running runtime or anything that is not a runtime.
func pendingSwitch(switchFile string, current runtimechoice.Runtime) runtimechoice.Runtime {
	data, err := os.ReadFile(switchFile)
	if err != nil {
		return ""
	}
	requested, err := runtimechoice.Parse(string(data))
	if err != nil || requested == current {
		return ""
	}
	return requested
}

// resetTerminalModes writes terminalResetSequence when stdout is a terminal.
func resetTerminalModes() {
	if isFdTTY(int(os.Stdout.Fd())) {
		fmt.Fprint(os.Stdout, terminalResetSequence)
	}
}

// writeSwitchRequest puts "<runtime>\n" at path in one rename, so vc never
// reads half a request.
func writeSwitchRequest(path string, rt runtimechoice.Runtime) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".switch-*")
	if err != nil {
		return err
	}
	_, writeErr := tmp.WriteString(string(rt) + "\n")
	closeErr := tmp.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = os.Rename(tmp.Name(), path)
	}
	if writeErr != nil {
		_ = os.Remove(tmp.Name())
	}
	return writeErr
}
