package main

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/browser"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/welcome"
)

// welcomeMenuDeps are the menu's edges: drawing, the two actions that leave the
// screen and come back, login, and how a fresh probe is started. They exist so
// the loop itself is testable — while it lived in main() the lines that wired
// the identity in could be deleted with the whole suite staying green.
type welcomeMenuDeps struct {
	screen  func(welcome.AuthState, <-chan welcome.IdentityUpdate) (welcome.RunResult, error)
	doctor  func()
	profile func()
	login   func() (welcome.AuthState, string, string, *launchPreflight)
	// preflight builds the probe the menu re-runs when the user comes back from
	// somewhere their balance may have changed. It is a dependency rather than a
	// direct call so a test can tell "asked again" from "showed the old number".
	preflight launchPreflightDeps
}

// runWelcomeMenu draws the landing screen until the user leaves it, rebuilding
// the state before each frame. It returns the result that ends the menu —
// SpawnPi or Quit — together with whatever the screen returned with it.
func runWelcomeMenu(state welcome.AuthState, token, authHost string, p *launchPreflight, deps welcomeMenuDeps) (welcome.RunResult, error) {
	for {
		if p != nil {
			if nudge, ready := p.updateIfReady(); ready && nudge != "" {
				state.UpdateNudge = nudge
			}
		}
		// One poll per frame, and the state it produces is what the next frame
		// starts from: that is how a name learned on iteration one survives
		// iteration two, when the preflight may no longer be reusable.
		state, late := welcomeScreenState(state, p, token, authHost)
		result, err := deps.screen(state, late)
		switch result {
		case welcome.RunDoctor:
			deps.doctor()
		case welcome.RunProfile:
			deps.profile()
			// The balance on screen was a snapshot of launch, and the user just
			// went where it changes. Ask again rather than keep claiming it.
			p = startLaunchPreflight(token, authHost, false, deps.preflight)
		case welcome.ShowTopUp:
			p = startLaunchPreflight(token, authHost, false, deps.preflight)
		case welcome.RunLogin:
			state, token, authHost, p = deps.login()
		default:
			return result, err
		}
	}
}

func defaultWelcomeMenuDeps() welcomeMenuDeps {
	return welcomeMenuDeps{
		screen: func(state welcome.AuthState, late <-chan welcome.IdentityUpdate) (welcome.RunResult, error) {
			return runWelcomeCommandTransition(state, welcome.Callbacks{}, late, rootCmd, os.Args[1:])
		},
		doctor: func() {
			fmt.Println()
			if derr := runDoctor(); derr != nil {
				fmt.Fprintf(os.Stderr, "vc: doctor: %v\n", derr)
			}
			waitForMenuReturn()
		},
		profile: func() {
			cfg := config.OSResolve()
			token, _, _ := auth.Load()
			openProfile(cfg.AuthHost, token, &http.Client{Timeout: 10 * time.Second}, func(u string) { _ = browser.OpenURL(u, os.Stdout) })
			waitForMenuReturn()
		},
		login: func() (welcome.AuthState, string, string, *launchPreflight) {
			if lerr := runLoginInteractive(); lerr != nil {
				fmt.Fprintf(os.Stderr, "vc: login failed: %v\n", lerr)
				os.Exit(1)
			}
			return refreshLaunchAfterLogin(defaultLaunchPreflightDeps())
		},
		preflight: defaultLaunchPreflightDeps(),
	}
}

func waitForMenuReturn() {
	fmt.Println("\n  press enter to return to the menu…")
	bufio.NewScanner(os.Stdin).Scan()
}
