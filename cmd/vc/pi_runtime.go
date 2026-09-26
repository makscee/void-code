package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/pibin"
	"github.com/makscee/void-code/internal/piruntime"
	"github.com/spf13/cobra"
)

// installPiRuntimeCmd lets `vc update` hand the Pi runtime to the binary it
// just installed: the new binary knows the Pi it pins, the running one may not.
var installPiRuntimeCmd = &cobra.Command{
	Use:    "install-pi-runtime",
	Short:  "Put the pinned Pi runtime in place if it is missing",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return ensurePiRuntime(cmd.ErrOrStderr())
	},
}

func init() {
	rootCmd.AddCommand(installPiRuntimeCmd)
}

// piRuntimeSources is a var so the test binary can keep launch tests off the
// network (see TestMain).
var piRuntimeSources = defaultPiRuntimeSources

// defaultPiRuntimeSources puts the configured auth host first (VC_AUTH_HOST
// moves it, the way it moves `vc update`), then the GitHub release mirror.
func defaultPiRuntimeSources() []piruntime.Source {
	base := config.OSResolve().AuthHost + "/vc"
	return []piruntime.Source{
		{SumsURL: base + "/SHA256SUMS", ArchiveBase: base + "/bin"},
		piruntime.DefaultSources[1],
	}
}

// ensurePiRuntime installs the pinned Pi into ~/.void-code/runtime/pi when it
// is missing or at another version. Installs that predate v0.2.48 have no such
// folder at all, and `vc update` from those versions only swaps the binary.
//
// It covers the install.sh channel only. A desktop install carries its own Node
// and Pi (a runtime/node tree), which this leaves alone. Windows launches Pi
// through npm's pi.cmd shim, which the archive does not carry yet.
func ensurePiRuntime(w io.Writer) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if _, err := pibin.ResolveNode(); !errors.Is(err, pibin.ErrBundledNodeUnprovisioned) {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if home, err = filepath.EvalSymlinks(home); err != nil {
		return err
	}
	if piruntime.Current(home) {
		return nil
	}
	fmt.Fprintf(w, "vc: installing Pi %s into %s ...\n", piruntime.PinnedVersion, piruntime.Dir(home))
	if _, err := piruntime.Ensure(piruntime.Options{Home: home, Sources: piRuntimeSources()}); err != nil {
		fmt.Fprintf(w, "vc: could not install Pi %s; nothing was changed. Check the connection and run vc again.\n    (%v)\n", piruntime.PinnedVersion, err)
		return err
	}
	fmt.Fprintf(w, "vc: Pi %s installed.\n", piruntime.PinnedVersion)
	return nil
}

// ensurePiRuntimeWithNewBinary runs install-pi-runtime in the binary `vc update`
// just wrote.
func ensurePiRuntimeWithNewBinary(w io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "install-pi-runtime")
	cmd.Stdout = w
	cmd.Stderr = w
	return cmd.Run()
}
