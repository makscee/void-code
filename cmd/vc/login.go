package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/browser"
	"github.com/makscee/void-code/internal/config"
	"github.com/spf13/cobra"
)

var openLoginURL = func(value string) { _ = browser.OpenURL(value, os.Stderr) }

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate through Void Identity device authorization",
	Long: `Authenticate through the central Void Identity device flow.

VC opens the central identity login, displays the device approval URL and code,
and polls at the server-prescribed cadence. Credentials are atomically written
to ~/.void-code/token with mode 0600.`,
	RunE: runLogin,
}

func init() { rootCmd.AddCommand(loginCmd) }

const legacyMigrationInstruction = "Legacy VC credential detected. Contact the operator for migration, then complete the normal device login."

func printLegacyMigrationInstruction(writer io.Writer) {
	if auth.LegacyTokenExists() {
		fmt.Fprintln(writer, legacyMigrationInstruction)
	}
}

func runLogin(_ *cobra.Command, _ []string) error {
	cfg := config.OSResolve()
	return runGradualLogin(cfg, defaultMigrationDeps(cfg))
}

type migrationDeps struct {
	loadCurrent func() (string, bool, error)
	loadLegacy  func() (string, error)
	legacyMe    func(string) (auth.MeResult, error)
	start       func(string) (auth.MigrationStartResult, error)
	promptOTP   func() (string, error)
	complete    func(string, string, string) (auth.MigrationCompleteResult, error)
	relayMe     func(string) (string, error)
	save        func(string) error
	device      func() error
	out         io.Writer
}

func relayBaseURL(cfg config.Config) string { return cfg.RelayScheme + "://" + cfg.RelayHost }

func defaultMigrationDeps(cfg config.Config) migrationDeps {
	client := &http.Client{Timeout: 15 * time.Second}
	return migrationDeps{
		loadCurrent: auth.Load, loadLegacy: auth.LoadLegacyToken,
		legacyMe: func(token string) (auth.MeResult, error) { return auth.FetchMe(cfg.AuthHost, token, client) },
		start: func(token string) (auth.MigrationStartResult, error) {
			return auth.MigrationStart(cfg.AuthHost, token, client)
		},
		promptOTP: promptMigrationOTP,
		complete: func(exchange, token, code string) (auth.MigrationCompleteResult, error) {
			return auth.MigrationComplete(cfg.AuthHost, exchange, token, code, client)
		},
		relayMe: func(token string) (string, error) { return auth.FetchRelayMe(relayBaseURL(cfg), token, client) },
		save:    auth.Save, device: func() error { return runDeviceFlow(cfg) }, out: os.Stderr,
	}
}

func runGradualLogin(_ config.Config, deps migrationDeps) error {
	candidate, _, loadErr := deps.loadCurrent()
	if loadErr != nil && !errors.Is(loadErr, auth.ErrNotLoggedIn) {
		return auth.RedactedMigrationError(auth.ErrMigrationSource)
	}
	me, meErr := auth.MeResult{}, auth.ErrNotLoggedIn
	if loadErr == nil && strings.TrimSpace(candidate) != "" {
		me, meErr = deps.legacyMe(candidate)
	}
	if errors.Is(meErr, auth.ErrNotLoggedIn) {
		legacy, err := deps.loadLegacy()
		if err == nil && strings.TrimSpace(legacy) != "" {
			candidate, me, meErr = legacy, auth.MeResult{}, nil
			me, meErr = deps.legacyMe(candidate)
		}
		if errors.Is(err, auth.ErrNotLoggedIn) && errors.Is(meErr, auth.ErrNotLoggedIn) {
			return deps.device()
		}
		if err != nil && !errors.Is(err, auth.ErrNotLoggedIn) {
			return auth.RedactedMigrationError(auth.ErrMigrationSource)
		}
	}
	if meErr != nil {
		return auth.RedactedMigrationError(auth.ErrMigrationSource)
	}
	if me.UserID == "" {
		return auth.RedactedMigrationError(auth.ErrMigrationUnavailable)
	}
	legacySubject, err := deps.relayMe(candidate)
	if err != nil {
		return auth.RedactedMigrationError(err)
	}
	if legacySubject != me.UserID {
		return auth.RedactedMigrationError(auth.ErrMigrationConflict)
	}

	started, err := deps.start(candidate)
	if err != nil {
		return auth.RedactedMigrationError(err)
	}
	code, err := deps.promptOTP()
	if err != nil {
		return fmt.Errorf("login cancelled; your existing login is unchanged")
	}
	completed, err := deps.complete(started.Exchange, candidate, code)
	if err != nil {
		return auth.RedactedMigrationError(err)
	}
	if completed.SubjectID != me.UserID {
		return auth.RedactedMigrationError(auth.ErrMigrationConflict)
	}
	relaySubject, err := deps.relayMe(completed.Token)
	if err != nil {
		return auth.RedactedMigrationError(err)
	}
	if relaySubject != me.UserID || relaySubject != completed.SubjectID {
		return auth.RedactedMigrationError(auth.ErrMigrationConflict)
	}
	if err := deps.save(completed.Token); err != nil {
		return fmt.Errorf("Could not save the new login. Your existing login is unchanged.")
	}
	fmt.Fprintln(deps.out, "Logged in successfully.")
	return nil
}

func promptMigrationOTP() (string, error) {
	if nonInteractive() {
		return "", errNonInteractiveLogin()
	}
	m := newMigrationOTPInputModel()
	result, err := tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(os.Stderr)).Run()
	if err != nil {
		return "", fmt.Errorf("reading code")
	}
	final, ok := result.(otpInputModel)
	if !ok || final.cancelled {
		return "", fmt.Errorf("login cancelled")
	}
	return final.value, nil
}

// runLoginInteractive is called from main.go when the banner detects logged-out
// state on any-key.  It runs the picker and completes login, or returns error.
func runLoginInteractive() error {
	return runLogin(nil, nil)
}

// errNonInteractiveLogin is returned (and printed to stderr) when login needs
// interactive input but vc is running non-interactively. It tells the caller how
// to supply credentials without a prompt.
func errNonInteractiveLogin() error {
	return fmt.Errorf("direct email and access-code login are unavailable; use the device authorization flow")
}

// otpExhaustedError returns the error shown after all OTP attempts are exhausted.
// Extracted so it can be tested independently of the full TUI login flow.
func otpExhaustedError() error {
	return fmt.Errorf("wrong or expired code — run vc login again to request a new code.\nIf you didn't receive a code, this email may not be registered — contact the operator.")
}

// ─── login picker ─────────────────────────────────────────────────────────────

type pickerChoice int

const (
	pickerNone     pickerChoice = iota
	pickerEmail                 // "Email (one-time code)"
	pickerDevice                // "Pairing code (from another device)"
	pickerCodeExch              // "Operator code (paste AAAA-BBBB)"
)

var (
	pickerTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#7C3AED")).
				MarginBottom(1)

	pickerItemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9CA3AF"))

	pickerSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#A78BFA"))

	pickerHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280")).
			Italic(true).
			MarginTop(1)
)

type pickerModel struct {
	choices  []string
	cursor   int
	selected pickerChoice
	quitting bool
}

func newPickerModel() pickerModel {
	return pickerModel{
		choices: []string{
			"Email (one-time code)",
			"Pairing code (from another device)",
			"Operator code (paste AAAA-BBBB)",
		},
		cursor: 0,
	}
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "1":
			m.cursor = 0
			m.selected = pickerEmail
			return m, tea.Quit
		case "2":
			m.cursor = 1
			m.selected = pickerDevice
			return m, tea.Quit
		case "3":
			m.cursor = 2
			m.selected = pickerCodeExch
			return m, tea.Quit
		case "enter", " ":
			m.selected = pickerChoice(m.cursor + 1)
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m pickerModel) View() string {
	if m.quitting {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(pickerTitleStyle.Render("How would you like to sign in?"))
	sb.WriteString("\n")
	for i, choice := range m.choices {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		num := fmt.Sprintf("%d. ", i+1)
		line := cursor + num + choice
		if i == m.cursor {
			sb.WriteString(pickerSelectedStyle.Render(line))
		} else {
			sb.WriteString(pickerItemStyle.Render(line))
		}
		sb.WriteString("\n")
	}
	sb.WriteString(pickerHintStyle.Render("↑/↓ or 1/2/3 to select · enter to confirm · q to cancel"))
	sb.WriteString("\n")
	return sb.String()
}

func runLoginPicker() (pickerChoice, error) {
	m := newPickerModel()
	p := tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(os.Stderr))
	result, err := p.Run()
	if err != nil {
		// Non-TTY or error: fall back to device flow.
		return pickerDevice, nil
	}
	final, ok := result.(pickerModel)
	if !ok || final.selected == pickerNone {
		return pickerNone, fmt.Errorf("login cancelled")
	}
	return final.selected, nil
}

// ─── email OTP flow ──────────────────────────────────────────────────────────

func runEmailFlow(cfg config.Config) error {
	httpClient := &http.Client{Timeout: 15 * time.Second}

	email, err := promptEmail()
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nSending a 6-digit code to %s...\n", email)
	_, err = auth.EmailStart(cfg.AuthHost, email, httpClient)
	if err != nil {
		return fmt.Errorf("sending OTP: %w", err)
	}
	fmt.Fprintln(os.Stderr, "Code sent. Check your email.")

	// Retry loop: up to 3 attempts to enter the correct OTP.
	for attempt := 1; attempt <= 3; attempt++ {
		code, err := promptOTP()
		if err != nil {
			return err
		}

		res, err := auth.EmailVerify(cfg.AuthHost, email, code, httpClient)
		if err != nil {
			if errors.Is(err, auth.ErrOTPWrong) {
				remaining := 3 - attempt
				if remaining > 0 {
					fmt.Printf("Wrong code — %d attempt(s) left. Try again.\n", remaining)
					continue
				}
				return otpExhaustedError()
			}
			return fmt.Errorf("OTP verify failed: %w", err)
		}

		// Success — save token.
		if err := auth.Save(res.Token); err != nil {
			return fmt.Errorf("saving token: %w", err)
		}
		fmt.Printf("Logged in successfully.\n")
		return nil
	}
	return fmt.Errorf("too many failed attempts")
}

// promptEmail reads an email address from stdin using a bubbletea model.
func promptEmail() (string, error) {
	if nonInteractive() {
		return "", errNonInteractiveLogin()
	}
	m := newEmailInputModel()
	p := tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(os.Stderr))
	result, err := p.Run()
	if err != nil {
		// Non-TTY fallback: use fmt.Scan.
		var email string
		fmt.Fprint(os.Stderr, "Email: ")
		if _, err := fmt.Scan(&email); err != nil {
			return "", fmt.Errorf("reading email: %w", err)
		}
		return strings.TrimSpace(email), nil
	}
	final, ok := result.(emailInputModel)
	if !ok || final.cancelled {
		return "", fmt.Errorf("login cancelled")
	}
	return final.value, nil
}

// promptOTP reads a 6-digit OTP from stdin using a bubbletea model.
func promptOTP() (string, error) {
	if nonInteractive() {
		return "", errNonInteractiveLogin()
	}
	m := newOTPInputModel()
	p := tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(os.Stderr))
	result, err := p.Run()
	if err != nil {
		// Non-TTY fallback: use fmt.Scan.
		var code string
		fmt.Fprint(os.Stderr, "Code: ")
		if _, err := fmt.Scan(&code); err != nil {
			return "", fmt.Errorf("reading code: %w", err)
		}
		return strings.TrimSpace(code), nil
	}
	final, ok := result.(otpInputModel)
	if !ok || final.cancelled {
		return "", fmt.Errorf("login cancelled")
	}
	return final.value, nil
}

// ─── simple text input models ─────────────────────────────────────────────────

var (
	inputLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9CA3AF"))
	inputValueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F9FAFB")).
			Bold(true)
	inputCursorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#7C3AED")).
				Bold(true)
)

type emailInputModel struct {
	value     string
	cancelled bool
}

func newEmailInputModel() emailInputModel { return emailInputModel{} }

func (m emailInputModel) Init() tea.Cmd { return nil }

func (m emailInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			if strings.Contains(m.value, "@") {
				return m, tea.Quit
			}
		case "backspace", "ctrl+h":
			if len(m.value) > 0 {
				m.value = m.value[:len(m.value)-1]
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.value += string(msg.Runes)
			}
		}
	}
	return m, nil
}

func (m emailInputModel) View() string {
	var sb strings.Builder
	sb.WriteString(inputLabelStyle.Render("Email: "))
	sb.WriteString(inputValueStyle.Render(m.value))
	sb.WriteString(inputCursorStyle.Render("█"))
	sb.WriteString("\n")
	sb.WriteString(inputLabelStyle.Render("(press enter to confirm)"))
	sb.WriteString("\n")
	return sb.String()
}

type otpInputModel struct {
	value     string
	cancelled bool
	notice    string
}

func newOTPInputModel() otpInputModel { return otpInputModel{} }

func newMigrationOTPInputModel() otpInputModel {
	return otpInputModel{notice: "A one-time code was sent to your prepared email."}
}

func (m otpInputModel) Init() tea.Cmd { return nil }

func (m otpInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			if len(m.value) == 6 {
				return m, tea.Quit
			}
		case "backspace", "ctrl+h":
			if len(m.value) > 0 {
				m.value = m.value[:len(m.value)-1]
			}
		default:
			if msg.Type == tea.KeyRunes {
				for _, r := range msg.Runes {
					if r >= '0' && r <= '9' && len(m.value) < 6 {
						m.value += string(r)
					}
				}
			}
		}
	}
	return m, nil
}

func (m otpInputModel) View() string {
	var sb strings.Builder
	if m.notice != "" {
		sb.WriteString(m.notice)
		sb.WriteString("\n")
	}
	sb.WriteString(inputLabelStyle.Render("6-digit code: "))
	sb.WriteString(inputValueStyle.Render(strings.Repeat("•", len(m.value))))
	if len(m.value) < 6 {
		sb.WriteString(inputCursorStyle.Render("█"))
	}
	sb.WriteString("\n")
	sb.WriteString(inputLabelStyle.Render("(enter 6 digits and press enter)"))
	sb.WriteString("\n")
	return sb.String()
}

// ─── device flow (pairing code) ──────────────────────────────────────────────

// runCodeExchange performs Flow 1a: exchange an access code for a session token.
func runCodeExchange(cfg config.Config, code string) error {
	res, err := auth.Exchange(cfg.AuthHost, code, nil)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidCode):
			return fmt.Errorf("invalid access-code format (expected AAAA-BBBB): %w", err)
		case errors.Is(err, auth.ErrCodeInvalid):
			return fmt.Errorf("access code not found or already used — check your code")
		case errors.Is(err, auth.ErrCodeExpired):
			return fmt.Errorf("access code has expired — request a new one")
		default:
			return fmt.Errorf("login failed: %w", err)
		}
	}
	if err := auth.Save(res.Token); err != nil {
		return fmt.Errorf("saving token: %w", err)
	}
	fmt.Printf("Logged in as %s\n", res.UserID)
	return nil
}

func deviceBrowserURL(authHost, verificationPath string) string {
	return strings.TrimRight(authHost, "/") + verificationPath
}

func voidCodeDeviceLabel(goos string) string {
	platform := map[string]string{"darwin": "macOS", "windows": "Windows", "linux": "Linux"}[goos]
	if platform == "" {
		platform = "this platform"
	}
	return "Void Code on " + platform
}

const priorSessionCleanupWarning = "Warning: the new login was saved, but the previous Void Code session could not be revoked. Revoke the older Void Code entry manually in Identity Devices."

func replaceDeviceCredential(token string, warnings io.Writer, load func() (string, bool, error), save func(string) error, revoke func(string) error) error {
	previous, _, err := load()
	if err != nil && !errors.Is(err, auth.ErrNotLoggedIn) {
		return err
	}
	if err := save(token); err != nil {
		return err
	}
	if previous == "" || previous == token {
		return nil
	}
	if err := revoke(previous); err != nil {
		fmt.Fprintln(warnings, priorSessionCleanupWarning)
	}
	return nil
}

func installDeviceCredential(authHost, token string, httpClient *http.Client, warnings io.Writer) error {
	return replaceDeviceCredential(token, warnings, auth.Load, auth.Save, func(previous string) error {
		return auth.RevokeSession(authHost, previous, httpClient)
	})
}

// runDeviceFlow performs Flow 1b: pairing-code (device-authorization) flow.
func runDeviceFlow(cfg config.Config) error {
	httpClient := &http.Client{Timeout: 15 * time.Second}

	start, err := auth.DeviceStart(cfg.AuthHost, voidCodeDeviceLabel(runtime.GOOS), httpClient)
	if err != nil {
		return fmt.Errorf("starting pairing-code flow: %w", err)
	}

	verificationURL := deviceBrowserURL(cfg.AuthHost, start.VerificationPath)
	fmt.Printf("\nApprove this device in your browser:\n\n  %s\n\nDevice code: %s\n\nWaiting for authorization", verificationURL, start.UserCode)
	openLoginURL(verificationURL)

	interval := start.Interval
	if interval <= 0 {
		interval = 5
	}

	deadline := time.Now().Add(time.Duration(start.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)
		fmt.Print(".")

		res, err := auth.DevicePoll(cfg.AuthHost, start.DeviceCode, httpClient)
		if err != nil {
			if errors.Is(err, auth.ErrAuthPending) {
				continue
			}
			if errors.Is(err, auth.ErrDeviceSlowDown) {
				interval += 5
				continue
			}
			if errors.Is(err, auth.ErrDeviceExpired) {
				fmt.Println()
				return fmt.Errorf("pairing code expired — run vc login again")
			}
			if errors.Is(err, auth.ErrDeviceDenied) {
				fmt.Println()
				return fmt.Errorf("authorization denied")
			}
			if errors.Is(err, auth.ErrDeviceConsumed) {
				fmt.Println()
				return fmt.Errorf("authorization was already consumed — run vc login again")
			}
			if errors.Is(err, auth.ErrDeviceInvalid) {
				fmt.Println()
				return fmt.Errorf("authorization is invalid — run vc login again")
			}
			fmt.Println()
			return fmt.Errorf("device authorization failed: %w", err)
		}

		// Approved.
		fmt.Println()
		if err := installDeviceCredential(cfg.AuthHost, res.Token, httpClient, os.Stderr); err != nil {
			return fmt.Errorf("saving token: %w", err)
		}
		fmt.Printf("Logged in successfully.\n")
		return nil
	}

	fmt.Println()
	return fmt.Errorf("pairing flow timed out — run vc login again")
}

// runCodeExchangePrompt is the interactive picker path for the operator-code
// option: it prompts the user to paste a code, then delegates to runCodeExchange.
func runCodeExchangePrompt(cfg config.Config) error {
	code, err := promptCode()
	if err != nil {
		return err
	}
	return runCodeExchange(cfg, code)
}

// ─── operator-code input model ───────────────────────────────────────────────

type codeInputModel struct {
	value     string
	cancelled bool
}

func newCodeInputModel() codeInputModel { return codeInputModel{} }

func (m codeInputModel) Init() tea.Cmd { return nil }

func (m codeInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			if len(m.value) == 9 { // AAAA-BBBB
				return m, tea.Quit
			}
		case "backspace", "ctrl+h":
			if len(m.value) > 0 {
				m.value = m.value[:len(m.value)-1]
			}
		default:
			if msg.Type == tea.KeyRunes {
				for _, r := range msg.Runes {
					ch := string(r)
					// Accept A-Z, 2-9, and hyphen (auto-inserted position 4).
					isAlphaNum := (r >= 'A' && r <= 'Z') || (r >= '2' && r <= '9') ||
						(r >= 'a' && r <= 'z')
					if isAlphaNum && len(m.value) < 9 {
						upper := strings.ToUpper(ch)
						// Auto-insert hyphen after 4th char.
						if len(m.value) == 4 && upper != "-" {
							m.value += "-"
						}
						m.value += upper
					} else if ch == "-" && len(m.value) == 4 {
						m.value += "-"
					}
				}
			}
		}
	}
	return m, nil
}

func (m codeInputModel) View() string {
	var sb strings.Builder
	sb.WriteString(inputLabelStyle.Render("Access code (AAAA-BBBB): "))
	sb.WriteString(inputValueStyle.Render(m.value))
	if len(m.value) < 9 {
		sb.WriteString(inputCursorStyle.Render("█"))
	}
	sb.WriteString("\n")
	sb.WriteString(inputLabelStyle.Render("(type the 8-character code and press enter)"))
	sb.WriteString("\n")
	return sb.String()
}

// promptCode reads an operator access code from stdin using a bubbletea model.
func promptCode() (string, error) {
	if nonInteractive() {
		return "", errNonInteractiveLogin()
	}
	m := newCodeInputModel()
	p := tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(os.Stderr))
	result, err := p.Run()
	if err != nil {
		// Non-TTY fallback: use fmt.Scan.
		var code string
		fmt.Fprint(os.Stderr, "Access code (AAAA-BBBB): ")
		if _, err := fmt.Scan(&code); err != nil {
			return "", fmt.Errorf("reading code: %w", err)
		}
		return strings.TrimSpace(code), nil
	}
	final, ok := result.(codeInputModel)
	if !ok || final.cancelled {
		return "", fmt.Errorf("login cancelled")
	}
	return final.value, nil
}
