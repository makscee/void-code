package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/spf13/cobra"
)

// `vc access-request` is the subcommand behind the desktop's "Запросить доступ"
// button. The desktop does not speak to any service itself — it spawns vc and
// reads stdout, exactly as it already does with `vc status --json` — so the
// button needs a subcommand, and the subcommand needs an output a program can
// branch on.
//
// ONE field decides everything:
//
//	{"accessRequest":"signed_out"}                                  no credential; nothing was asked
//	{"accessRequest":"not_requested"}                               the server says there is no ask
//	{"accessRequest":"open","requestedAt":"…"}                      asked, waiting
//	{"accessRequest":"granted","requestedAt":"…","resolvedAt":"…"}  resolved yes
//	{"accessRequest":"declined","requestedAt":"…","resolvedAt":"…"} resolved no
//	{"accessRequest":"invalid_credential","error":"…"}              the bearer was refused
//	{"accessRequest":"unavailable","error":"…"}                     we could not get an answer
//
// One field and not two, for the same reason runStatusJSON has one: a state
// beside a separate ok/error would let the desktop read the state without
// reading the failure, and "не подавал" would appear on screen every time the
// relay was down.
const (
	accessRequestStateSignedOut  = "signed_out"
	accessRequestStateNone       = "not_requested"
	accessRequestStateInvalidCre = "invalid_credential"
	accessRequestStateUnavail    = "unavailable"
)

var accessRequestCmd = &cobra.Command{
	Use:   "access-request",
	Short: "Ask an operator for access, or read the state of the ask already filed",
	Long: `Ask an operator for access, or read the state of the ask already filed.

Without --ask the state is only read, and reading never creates a request: a
desktop that polls the refusal screen must not fill the operator's queue by
watching. With --ask the request is filed; pressing the button twice is one ask,
and the second answer carries the first submission time.

An access request is a question, never a grant: it hands out no subscription,
no budget and no expiry — those stay an operator path.`,
	Args: cobra.NoArgs,
	RunE: runAccessRequestCommand,
}

func init() {
	accessRequestCmd.Flags().Bool("ask", false, "file the request; without this the state is only read and nothing is created")
	accessRequestCmd.Flags().Bool("json", false, "emit the state as a single JSON object instead of a sentence (for callers with no terminal, e.g. the desktop app)")
	rootCmd.AddCommand(accessRequestCmd)
}

// accessRequestJSONRunner is the same indirection statusJSONRunner is, for the
// same reason: tests swap the destination without making a network call.
var accessRequestJSONRunner = func(cfg config.Config, ask bool, out io.Writer) error {
	return runAccessRequestJSON(cfg, ask, out)
}

// runAccessRequestCommand keeps reading and asking apart. They are different
// acts — opening the refusal screen must not file a request — and the flag is
// the only thing separating them.
func runAccessRequestCommand(cmd *cobra.Command, _ []string) error {
	ask, asJSON := false, false
	if cmd != nil {
		ask, _ = cmd.Flags().GetBool("ask")
		asJSON, _ = cmd.Flags().GetBool("json")
	}
	cfg := config.OSResolve()
	if asJSON {
		return accessRequestJSONRunner(cfg, ask, os.Stdout)
	}
	return runAccessRequestText(cfg, ask, os.Stdout)
}

// accessRequestOutcome is what one invocation learned, before it is rendered
// for either audience. The state is always set; everything else is present only
// when it was actually established.
type accessRequestOutcome struct {
	state       string
	requestedAt string
	resolvedAt  string
	reason      string
}

// runAccessRequestJSON writes exactly one JSON object to out — the desktop's
// side of the contract above. Failures are part of the object, not an empty
// stream plus an exit code: the caller has a screen to draw either way.
func runAccessRequestJSON(cfg config.Config, ask bool, out io.Writer) error {
	outcome := resolveAccessRequest(cfg, ask)

	obj := map[string]any{"accessRequest": outcome.state}
	// Absent, not empty: a field that always serialises as "" would read on
	// the far side as a date the client somehow failed to render, rather than
	// as a date that does not exist.
	if outcome.requestedAt != "" {
		obj["requestedAt"] = outcome.requestedAt
	}
	if outcome.resolvedAt != "" {
		obj["resolvedAt"] = outcome.resolvedAt
	}
	if outcome.reason != "" {
		obj["error"] = outcome.reason
	}
	return json.NewEncoder(out).Encode(obj)
}

// runAccessRequestText says the same thing to a human at a terminal.
func runAccessRequestText(cfg config.Config, ask bool, out io.Writer) error {
	outcome := resolveAccessRequest(cfg, ask)
	switch outcome.state {
	case accessRequestStateSignedOut:
		fmt.Fprintln(out, "not logged in — run: vc login")
	case accessRequestStateNone:
		fmt.Fprintln(out, "no access request has been filed — run: vc access-request --ask")
	case auth.AccessRequestOpen:
		fmt.Fprintf(out, "access request filed %s — waiting for an operator\n", outcome.requestedAt)
	case auth.AccessRequestGranted:
		fmt.Fprintf(out, "access request filed %s was granted %s\n", outcome.requestedAt, outcome.resolvedAt)
	case auth.AccessRequestDeclined:
		fmt.Fprintf(out, "access request filed %s was declined %s\n", outcome.requestedAt, outcome.resolvedAt)
	default:
		fmt.Fprintln(out, outcome.reason)
	}
	return nil
}

// resolveAccessRequest performs the one call this invocation is allowed to make
// and reports what it learned.
func resolveAccessRequest(cfg config.Config, ask bool) accessRequestOutcome {
	token, _, err := auth.Load()
	token = strings.TrimSpace(token)
	// No credential means no call — not "a call that will fail". The relay
	// would answer 401 and the outcome would look the same from here, but a
	// POST from a machine nobody is signed in on is an attempt at somebody
	// else's queue.
	if err != nil || token == "" {
		return accessRequestOutcome{state: accessRequestStateSignedOut}
	}

	client := &http.Client{Timeout: authProbeTimeout}
	var (
		request auth.AccessRequest
		exists  bool
	)
	if ask {
		request, err = auth.AskForAccess(cfg.AccessCheckHost, token, client)
		exists = err == nil
	} else {
		request, exists, err = auth.FetchAccessRequest(cfg.AccessCheckHost, token, client)
	}

	switch {
	case errors.Is(err, auth.ErrNotLoggedIn):
		// The one failure here that signing in again fixes, and the one piece
		// of advice that is wrong for every other failure in this file.
		return accessRequestOutcome{state: accessRequestStateInvalidCre, reason: err.Error()}
	case err != nil:
		// Nothing was learned, so nothing may be dated.
		return accessRequestOutcome{state: accessRequestStateUnavail, reason: err.Error()}
	case !exists:
		return accessRequestOutcome{state: accessRequestStateNone}
	}

	switch request.Status {
	case auth.AccessRequestOpen, auth.AccessRequestGranted, auth.AccessRequestDeclined:
	default:
		// auth refuses these already; the second guard is here because a word
		// the desktop cannot name arrives on its screen as nothing at all.
		return accessRequestOutcome{
			state:  accessRequestStateUnavail,
			reason: auth.ErrAccessRequestUnavailable.Error(),
		}
	}

	outcome := accessRequestOutcome{
		state:       request.Status,
		requestedAt: request.CreatedAt.UTC().Format(time.RFC3339),
	}
	if request.ResolvedAt != nil {
		outcome.resolvedAt = request.ResolvedAt.UTC().Format(time.RFC3339)
	}
	// Nothing else travels. The identity in the answer is one this client did
	// not verify, and an ask creates no entitlement, budget or expiry — so
	// there is nowhere for any of it to go.
	return outcome
}
