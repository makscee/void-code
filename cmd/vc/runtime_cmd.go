package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/makscee/void-code/internal/runtimechoice"
	"github.com/spf13/cobra"
)

// runtimeTerminalAttached reports whether a person is at the terminal to answer
// the runtime menu: stdin and stdout both a terminal. A var so tests never
// open the menu on whatever terminal `go test` runs in.
var runtimeTerminalAttached = func() bool {
	return isFdTTY(int(os.Stdin.Fd())) && isFdTTY(int(os.Stdout.Fd()))
}

// chooseRuntime asks the person which runtime to launch. A var for tests.
var chooseRuntime = func() (runtimechoice.Runtime, error) {
	return promptRuntime(os.Stdin, os.Stdout)
}

var runtimeCmd = &cobra.Command{
	Use:   "runtime [pi|codex]",
	Short: "Choose what vc launches: Pi or Codex",
	Long: `Choose what vc launches: Pi or Codex.

"vc runtime pi" or "vc runtime codex" saves the choice at once; "vc runtime"
alone shows the menu. The choice is kept in ~/.void-code/config and used by
every following "vc".`,
	Args: cobra.MaximumNArgs(1),
	RunE: runRuntimeCmd,
}

func init() {
	rootCmd.AddCommand(runtimeCmd)
}

func runRuntimeCmd(cmd *cobra.Command, args []string) error {
	if switchFile := os.Getenv(runtimeSwitchFileEnv); switchFile != "" {
		return runRuntimeInSession(cmd, args, switchFile)
	}
	var chosen runtimechoice.Runtime
	if len(args) == 1 {
		parsed, err := runtimechoice.Parse(args[0])
		if err != nil {
			return fmt.Errorf("%w; укажите vc runtime pi или vc runtime codex", err)
		}
		chosen = parsed
	} else {
		if nonInteractiveFlag || !runtimeTerminalAttached() {
			return errors.New("нет терминала для меню; укажите выбор: vc runtime pi или vc runtime codex")
		}
		answered, err := chooseRuntime()
		if err != nil {
			return err
		}
		chosen = answered
	}
	if err := runtimechoice.Save(chosen); err != nil {
		return fmt.Errorf("не удалось сохранить выбор: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "vc: выбран %s, он запустится при следующем vc. Сменить: vc runtime\n", chosen.Label())
	return nil
}

// runRuntimeInSession is `vc runtime <x>` typed inside a runtime vc
// supervises: it saves the choice and leaves the request for that vc, which
// then starts <x> in the same terminal. The runtime owns the terminal, so there
// is no menu. Asking for the runtime already running fails: Pi's `/runtime`
// closes Pi on success, and with no request behind it vc would just exit.
func runRuntimeInSession(cmd *cobra.Command, args []string, switchFile string) error {
	if len(args) == 0 {
		return errors.New("внутри сессии меню нет; укажите: vc runtime pi или vc runtime codex")
	}
	chosen, err := runtimechoice.Parse(args[0])
	if err != nil {
		return fmt.Errorf("%w; укажите vc runtime pi или vc runtime codex", err)
	}
	if running, parseErr := runtimechoice.Parse(os.Getenv("VC_HARNESS")); parseErr == nil && running == chosen {
		return fmt.Errorf("уже %s", chosen.Label())
	}
	if !filepath.IsAbs(switchFile) {
		return fmt.Errorf("%s=%q is not an absolute path", runtimeSwitchFileEnv, switchFile)
	}
	if err := runtimechoice.Save(chosen); err != nil {
		return fmt.Errorf("не удалось сохранить выбор: %w", err)
	}
	if err := writeSwitchRequest(switchFile, chosen); err != nil {
		return fmt.Errorf("не удалось передать заявку на переключение: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "vc: переключаюсь на %s…\n", chosen.Label())
	return nil
}

// resolveLaunchRuntime decides what runSpawn launches: the saved choice; with
// none, the menu when a person is at a terminal (the answer is saved); with no
// terminal or --non-interactive, Pi, and nothing is saved.
func resolveLaunchRuntime() (runtimechoice.Runtime, error) {
	saved, ok, err := runtimechoice.Load()
	if err != nil {
		return "", fmt.Errorf("%w\nвыберите заново: vc runtime pi или vc runtime codex", err)
	}
	if ok {
		return saved, nil
	}
	if nonInteractiveFlag || !runtimeTerminalAttached() {
		return runtimechoice.Pi, nil
	}
	chosen, err := chooseRuntime()
	if err != nil {
		return "", err
	}
	if saveErr := runtimechoice.Save(chosen); saveErr != nil {
		fmt.Fprintf(os.Stderr, "vc: warning: выбор не сохранён, в следующий раз спрошу снова: %v\n", saveErr)
	}
	return chosen, nil
}

// promptRuntime is the numbered menu. End of input cancels it; anything other
// than a listed answer asks again, a few times at most.
func promptRuntime(in io.Reader, out io.Writer) (runtimechoice.Runtime, error) {
	fmt.Fprintln(out, "\nЧем запускать сессии?")
	fmt.Fprintln(out, "  1) Pi")
	fmt.Fprintln(out, "  2) Codex")
	fmt.Fprintln(out, "Сменить потом: vc runtime")
	reader := bufio.NewReader(in)
	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprint(out, "Выбор [1/2]: ")
		line, err := reader.ReadString('\n')
		if chosen, ok := menuAnswer(line); ok {
			return chosen, nil
		}
		if err != nil {
			fmt.Fprintln(out)
			return "", errors.New("выбор не сделан")
		}
	}
	return "", errors.New("выбор не сделан")
}

func menuAnswer(line string) (runtimechoice.Runtime, bool) {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "1", "pi":
		return runtimechoice.Pi, true
	case "2", "codex":
		return runtimechoice.Codex, true
	}
	return "", false
}

// runtimeStatusValue is the value of the `runtime:` line in `vc status`.
func runtimeStatusValue() string {
	saved, ok, err := runtimechoice.Load()
	switch {
	case err != nil:
		return "unreadable (" + err.Error() + ")"
	case !ok:
		return "Pi (not chosen yet; `vc runtime` to choose)"
	}
	return saved.Label()
}
