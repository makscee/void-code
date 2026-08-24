package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

// `vc access-request` — the subcommand behind the "Запросить доступ" button.
//
// The desktop does not speak to any service itself: it spawns `vc` and reads
// what comes back on stdout (desktop/src/main/auth-session.ts does this with
// `vc status --json` today). So the button needs a subcommand, and the
// subcommand needs an output a program can branch on — the same arrangement
// runStatusJSON already has, for the same reason.
//
// Contract with the desktop, checked below. One field decides everything:
//
//	{"accessRequest":"signed_out"}                                    no credential; nothing was asked
//	{"accessRequest":"not_requested"}                                 the server says there is no ask
//	{"accessRequest":"open","requestedAt":"…"}                        asked, waiting
//	{"accessRequest":"granted","requestedAt":"…","resolvedAt":"…"}    resolved yes
//	{"accessRequest":"declined","requestedAt":"…","resolvedAt":"…"}   resolved no
//	{"accessRequest":"invalid_credential","error":"…"}                the bearer was refused
//	{"accessRequest":"unavailable","error":"…"}                       we could not get an answer
//
// ONE field, not two, and that is the whole point. A shape with a separate
// `ok`/`error` beside a state would let the desktop read the state without
// reading the failure, and "не подавал" would appear on screen every time the
// relay was down — offering a button to somebody who already pressed it.
//
// WHAT THESE TESTS DO NOT PROVE. Nothing here has ever spoken to a live relay:
// the route is in void-relay PR #7, unmerged and unrolled, and the Keys
// migration behind it is unapplied to production and forbidden to us. This is
// httptest against the wire shape read out of that PR's source. Agreement with
// the real chain is "не смог", not "прошло".

// The words, in one place. The desktop's renderer falls back to silence for any
// word it does not know, so an eighth one is a decision, not a detail.
var knownAccessRequestStates = map[string]bool{
	"signed_out":         true,
	"not_requested":      true,
	"open":               true,
	"granted":            true,
	"declined":           true,
	"invalid_credential": true,
	"unavailable":        true,
}

func sortedKnownAccessRequestStates() []string {
	out := make([]string, 0, len(knownAccessRequestStates))
	for k := range knownAccessRequestStates {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The only keys this output may carry. An access request answers *whether* and
// *when*; anything else on the stream is either something nobody confirmed
// (an identity read out of a refusal) or something an ask is forbidden to
// produce (an entitlement, a budget, an expiry).
var allowedAccessRequestFields = map[string]bool{
	"accessRequest": true,
	"requestedAt":   true,
	"resolvedAt":    true,
	"error":         true,
}

const (
	askCreatedMillis  = 1787479200000 // 2026-08-23T10:00:00Z
	askCreatedRFC3339 = "2026-08-23T10:00:00Z"
	askResolvedRFC339 = "2026-08-24T09:30:00Z"
)

// requestStand answers the two access-request routes and counts what it was
// asked, so "the read did not create anything" is an observation.
type requestStand struct {
	server   *httptest.Server
	handlers map[string]func(w http.ResponseWriter)
	posts    atomic.Int32
	gets     atomic.Int32
	paths    chan string
}

func newRequestStand(t *testing.T) *requestStand {
	t.Helper()
	stand := &requestStand{handlers: map[string]func(http.ResponseWriter){}, paths: make(chan string, 16)}
	stand.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case stand.paths <- r.Method + " " + r.URL.Path:
		default:
		}
		switch r.Method {
		case http.MethodPost:
			stand.posts.Add(1)
		case http.MethodGet:
			stand.gets.Add(1)
		}
		if h, ok := stand.handlers[r.Method+" "+r.URL.Path]; ok {
			h(w)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(stand.server.Close)
	return stand
}

func (s *requestStand) on(method, path string, status int, body string) *requestStand {
	s.handlers[method+" "+path] = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
	return s
}

func (s *requestStand) url() string { return s.server.URL }

func (s *requestStand) sawPath(t *testing.T) string {
	t.Helper()
	select {
	case p := <-s.paths:
		return p
	default:
		t.Fatal("the command never made a request")
		return ""
	}
}

func askBody(status string, resolved string) string {
	return `{"request":{"id":"acc-req-1","subject_id":"11111111-1111-4111-8111-111111111111",` +
		`"status":"` + status + `","created_at":1787479200000,"resolved_at":` + resolved + `}}`
}

// signedInWith puts a credential on a throwaway HOME and hands back the token,
// so a test can prove the token is used without proving it is printed.
func signedInWith(t *testing.T, token string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := auth.Save(token); err != nil {
		t.Fatal(err)
	}
}

// runAccessRequest runs the command and returns the one object it wrote,
// checking the stream is well-formed whatever the outcome was. The returned
// error is deliberately not asserted on here: an unreachable relay may or may
// not be worth a non-zero exit code, but it is always worth a parsable answer.
func runAccessRequest(t *testing.T, cfg config.Config, ask bool) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	_ = runAccessRequestJSON(cfg, ask, &buf)
	obj := decodeSingleJSONObject(t, buf.Bytes())
	assertNoANSI(t, buf.Bytes())

	state, ok := obj["accessRequest"].(string)
	if !ok {
		t.Fatalf("accessRequest = %v, want one of the contract words %v", obj["accessRequest"], sortedKnownAccessRequestStates())
	}
	if !knownAccessRequestStates[state] {
		t.Errorf("accessRequest = %q is not one of the contract words %v; the desktop renders an unknown word as nothing at all", state, sortedKnownAccessRequestStates())
	}
	for field := range obj {
		if !allowedAccessRequestFields[field] {
			t.Errorf("output carries %q = %v, which is not part of the contract", field, obj[field])
		}
	}
	return obj
}

// ── Хост ─────────────────────────────────────────────────────────────────────

// AccessCheckHost, not AuthHost. The two were split on 2026-08-23 because in
// production they are different services and the same bearer gets opposite
// verdicts from them: relay:443 answers /v1/vc/me and the sign-in host refuses
// it, while providers and the device routes exist only on the sign-in host.
// The access-request route is served by the relay, next to /v1/vc/me — so it
// follows the access check.
//
// The tests give the two settings DIFFERENT servers, because a single-server
// test passes on a build that still reads one setting for both.
func TestAccessRequestAsksTheAccessCheckHost(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ask      bool
		wantPath string
	}{
		{"read", false, "GET /v1/vc/access-requests/me"},
		{"ask", true, "POST /v1/vc/access-requests"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signedInWith(t, "accepted-token")
			check := newRequestStand(t).
				on(http.MethodGet, "/v1/vc/access-requests/me", http.StatusOK, `{"request":null}`).
				on(http.MethodPost, "/v1/vc/access-requests", http.StatusCreated, askBody("open", "null"))
			signIn := newHostSpy(t, http.StatusOK, `{"request":null}`)

			runAccessRequest(t, config.Config{AuthHost: signIn.url(), AccessCheckHost: check.url()}, tc.ask)

			if got := check.sawPath(t); got != tc.wantPath {
				t.Errorf("went to %q, want %q", got, tc.wantPath)
			}
			signIn.assertUntouched(t, "the sign-in")
		})
	}
}

// Someone with no access granted is exactly who this command is for. If the
// implementation runs the access gate first — the natural thing to reach for,
// since every other command does — then /v1/vc/me answers 402 and the one
// person who needs the button is the one who cannot press it.
func TestAccessRequestWorksForSomebodyWithNoAccess(t *testing.T) {
	for _, ask := range []bool{false, true} {
		signedInWith(t, "accepted-token")
		stand := newRequestStand(t).
			on(http.MethodGet, "/v1/vc/me", http.StatusPaymentRequired, `{"error":"budget_exceeded"}`).
			on(http.MethodGet, "/v1/vc/access-requests/me", http.StatusOK, `{"request":null}`).
			on(http.MethodPost, "/v1/vc/access-requests", http.StatusCreated, askBody("open", "null"))

		obj := runAccessRequest(t, config.Config{AuthHost: stand.url(), AccessCheckHost: stand.url()}, ask)

		want := "not_requested"
		if ask {
			want = "open"
		}
		if obj["accessRequest"] != want {
			t.Errorf("ask=%v: accessRequest = %v, want %q — a refused account is the intended caller here, not a blocked one", ask, obj["accessRequest"], want)
		}
	}
}

// ── Что видно ────────────────────────────────────────────────────────────────

func TestAccessRequestReadsEachState(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		body         string
		wantState    string
		wantRequest  string // "" = must be absent
		wantResolved string // "" = must be absent
	}{
		{"never_asked", http.StatusOK, `{"request":null}`, "not_requested", "", ""},
		{"open", http.StatusOK, askBody("open", "null"), "open", askCreatedRFC3339, ""},
		{"granted", http.StatusOK, askBody("granted", "1787563800000"), "granted", askCreatedRFC3339, askResolvedRFC339},
		{"declined", http.StatusOK, askBody("declined", "1787563800000"), "declined", askCreatedRFC3339, askResolvedRFC339},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signedInWith(t, "accepted-token")
			stand := newRequestStand(t).on(http.MethodGet, "/v1/vc/access-requests/me", tc.status, tc.body)

			obj := runAccessRequest(t, config.Config{AccessCheckHost: stand.url()}, false)

			if obj["accessRequest"] != tc.wantState {
				t.Errorf("accessRequest = %v, want %q", obj["accessRequest"], tc.wantState)
			}
			// The waiting screen has one fact to show and this is it. A date the
			// desktop has to parse out of prose is a date it will get wrong, so
			// it travels as RFC3339 in UTC — the same shape resetAt already uses.
			assertFieldOrAbsent(t, obj, "requestedAt", tc.wantRequest)
			assertFieldOrAbsent(t, obj, "resolvedAt", tc.wantResolved)
		})
	}
}

func assertFieldOrAbsent(t *testing.T, obj map[string]any, field, want string) {
	t.Helper()
	got, present := obj[field]
	if want == "" {
		if present {
			t.Errorf("%s = %v, want absent", field, got)
		}
		return
	}
	if !present {
		t.Errorf("%s absent, want %q", field, want)
		return
	}
	if got != want {
		t.Errorf("%s = %v, want %q (RFC3339, UTC — created_at arrives as epoch MILLISECONDS)", field, got, want)
	}
}

// ── «Нет заявки» против «не смогли спросить» ─────────────────────────────────

// The headline confusion, at the level the human actually sees. In every case
// below the command ends up holding no request, and in exactly one of them that
// is because a server said so. Rendering the rest as "not_requested" hands the
// button back to somebody who already pressed it — and hides the fact that the
// system is down behind a screen that looks like it is working.
func TestAccessRequestNeverPassesSilenceOffAsNeverAsked(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"relay_could_not_reach_keys", http.StatusServiceUnavailable, `{"error":"unavailable"}`},
		{"relay_broken", http.StatusInternalServerError, ``},
		// The route is not deployed anywhere yet, and prod's relay:443 answers
		// this to every path it does not serve (probed 2026-08-23). Until PR #7
		// ships, this is what a real user's client receives.
		{"connect_proxy", http.StatusBadRequest, `This is a CONNECT proxy`},
		{"route_not_deployed", http.StatusNotFound, ``},
		{"gateway", http.StatusBadGateway, ``},
		{"not_json", http.StatusOK, `<html>sign in</html>`},
		{"no_request_key", http.StatusOK, `{"ok":true}`},
		{"empty_body", http.StatusOK, ``},
		{"unknown_status_word", http.StatusOK, askBody("pending", "null")},
		{"payment_required", http.StatusPaymentRequired, `{"error":"budget_exceeded"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signedInWith(t, "accepted-token")
			stand := newRequestStand(t).on(http.MethodGet, "/v1/vc/access-requests/me", tc.status, tc.body)

			obj := runAccessRequest(t, config.Config{AccessCheckHost: stand.url()}, false)

			if obj["accessRequest"] != "unavailable" {
				t.Errorf("HTTP %d %q → accessRequest = %v, want unavailable — nothing answered, so nothing is known about any request", tc.status, tc.body, obj["accessRequest"])
			}
			reason, _ := obj["error"].(string)
			if strings.TrimSpace(reason) == "" {
				t.Error("unavailable carries no reason — the screen has nothing to say beyond a blank failure")
			}
			// Nothing was learned, so nothing may be dated.
			for _, field := range []string{"requestedAt", "resolvedAt"} {
				if _, present := obj[field]; present {
					t.Errorf("unavailable output carries %q = %v, want absent", field, obj[field])
				}
			}
		})
	}

	t.Run("nobody_listening", func(t *testing.T) {
		signedInWith(t, "accepted-token")
		stand := newRequestStand(t)
		url := stand.url()
		stand.server.Close()

		obj := runAccessRequest(t, config.Config{AccessCheckHost: url}, false)
		if obj["accessRequest"] != "unavailable" {
			t.Errorf("an unreachable host → accessRequest = %v, want unavailable", obj["accessRequest"])
		}
	})
}

// A refused bearer is neither of the two. It is fixed by signing in, which is
// the one piece of advice that is wrong for every other failure in this file
// and right for this one.
func TestAccessRequestReportsARefusedBearerSeparately(t *testing.T) {
	signedInWith(t, "stale-token")
	stand := newRequestStand(t).on(http.MethodGet, "/v1/vc/access-requests/me", http.StatusUnauthorized, ``)

	obj := runAccessRequest(t, config.Config{AccessCheckHost: stand.url()}, false)
	if obj["accessRequest"] != "invalid_credential" {
		t.Errorf("accessRequest = %v, want invalid_credential", obj["accessRequest"])
	}
}

// No credential means no call. Not "a call that will fail" — the relay would
// answer 401 and the outcome would look identical, but a POST from a machine
// nobody is signed in on is an attempt at somebody's queue.
func TestAccessRequestNeverDialsWithoutACredential(t *testing.T) {
	for _, ask := range []bool{false, true} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		stand := newRequestStand(t)
		signIn := newHostSpy(t, http.StatusOK, `{}`)

		obj := runAccessRequest(t, config.Config{AuthHost: signIn.url(), AccessCheckHost: stand.url()}, ask)

		if obj["accessRequest"] != "signed_out" {
			t.Errorf("ask=%v: accessRequest = %v, want signed_out", ask, obj["accessRequest"])
		}
		if n := stand.posts.Load() + stand.gets.Load(); n != 0 {
			t.Errorf("ask=%v: %d request(s) went out with no credential to send", ask, n)
		}
		signIn.assertUntouched(t, "the sign-in")
	}
}

// ── Идемпотентность ──────────────────────────────────────────────────────────

// Pressing the button twice is one ask. Keys answers 201 the first time and 200
// the second with the row it already has, and the client has to treat them as
// the same answer — including the date, which stays the FIRST submission. A
// client that showed "asked just now" on every press would erase the only thing
// the waiting screen knows.
func TestAskingTwiceKeepsTheFirstRequest(t *testing.T) {
	signedInWith(t, "accepted-token")
	stand := newRequestStand(t)
	first := true
	stand.handlers["POST /v1/vc/access-requests"] = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		if first {
			first = false
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		_, _ = w.Write([]byte(askBody("open", "null")))
	}
	cfg := config.Config{AccessCheckHost: stand.url()}

	one := runAccessRequest(t, cfg, true)
	two := runAccessRequest(t, cfg, true)

	for i, obj := range []map[string]any{one, two} {
		if obj["accessRequest"] != "open" {
			t.Errorf("ask %d: accessRequest = %v, want open — 201 and 200 are the same news to the person waiting", i+1, obj["accessRequest"])
		}
		if obj["requestedAt"] != askCreatedRFC3339 {
			t.Errorf("ask %d: requestedAt = %v, want %q", i+1, obj["requestedAt"], askCreatedRFC3339)
		}
	}
	if n := stand.posts.Load(); n != 2 {
		t.Errorf("two invocations made %d POSTs, want 2 — one press is one ask, and a retry inside the client is a second row somebody has to look at", n)
	}
}

// Looking is not asking. The read path must not create anything, so a desktop
// that polls the refusal screen does not fill the operator's queue by watching.
func TestReadingTheStateNeverCreatesARequest(t *testing.T) {
	signedInWith(t, "accepted-token")
	stand := newRequestStand(t).on(http.MethodGet, "/v1/vc/access-requests/me", http.StatusOK, `{"request":null}`)
	cfg := config.Config{AccessCheckHost: stand.url()}

	for i := 0; i < 3; i++ {
		runAccessRequest(t, cfg, false)
	}
	if n := stand.posts.Load(); n != 0 {
		t.Errorf("three reads made %d POST(s) — the read must not fall back to asking when there is nothing there", n)
	}
}

// ── Заявка ничего не выдаёт ──────────────────────────────────────────────────

// Even if a server hands back an entitlement, an expiry or a balance, none of it
// reaches the stream. The field allowlist in runAccessRequest is what enforces
// this; the test exists to send the payload that would exercise it. The subject
// id is on the list of things that must not travel for a second reason: an
// identity the client did not verify is not one it may display.
func TestAccessRequestOutputCarriesNoGrant(t *testing.T) {
	signedInWith(t, "accepted-token")
	stand := newRequestStand(t).on(http.MethodGet, "/v1/vc/access-requests/me", http.StatusOK,
		`{"request":{"id":"acc-req-1","subject_id":"11111111-1111-4111-8111-111111111111",`+
			`"status":"granted","created_at":1787479200000,"resolved_at":1787563800000,`+
			`"expires_at":1790000000000,"subscription":{"active":true},"pct":0,"balanceUsd":50}}`)

	var buf bytes.Buffer
	_ = runAccessRequestJSON(config.Config{AccessCheckHost: stand.url()}, false, &buf)

	obj := runAccessRequestOutput(t, buf.Bytes())
	if obj["accessRequest"] != "granted" {
		t.Errorf("accessRequest = %v, want granted", obj["accessRequest"])
	}
	for _, forbidden := range []string{"expires_at", "expiresAt", "subscription", "pct", "balanceUsd", "subject_id", "subjectId", "identity", "id"} {
		if _, present := obj[forbidden]; present {
			t.Errorf("output carries %q — an ask creates no entitlement, and names nobody the client did not verify", forbidden)
		}
	}
	for _, forbidden := range []string{"1790000000000", "balanceUsd", "11111111-1111-4111-8111-111111111111"} {
		if strings.Contains(buf.String(), forbidden) {
			t.Errorf("output text contains %q: %s", forbidden, buf.String())
		}
	}
}

func runAccessRequestOutput(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	assertNoANSI(t, raw)
	return decodeSingleJSONObject(t, raw)
}

// Whatever failed, the person reads a sentence about their situation — not the
// inside of the system, and never the credential.
func TestAccessRequestFailureOutputSaysNothingInternal(t *testing.T) {
	const secret = "tok_live_do_not_print_me"
	signedInWith(t, secret)
	stand := newRequestStand(t).on(http.MethodGet, "/v1/vc/access-requests/me", http.StatusServiceUnavailable,
		`{"error":"unavailable","upstream":"http://keys.internal:8788/v1/access-requests/me","trace":"at Object.openOrGet (/srv/keys/dist/services/access-requests.js:88:11)"}`)

	var buf bytes.Buffer
	_ = runAccessRequestJSON(config.Config{AccessCheckHost: stand.url()}, false, &buf)
	text := buf.String()

	if strings.Contains(text, secret) {
		t.Errorf("output leaked the credential: %s", text)
	}
	for _, internal := range []string{"keys.internal", "dist/services", "at Object.", "8788", stand.url()} {
		if strings.Contains(text, internal) {
			t.Errorf("output repeats the inside of the system (%q): %s", internal, text)
		}
	}
	obj := runAccessRequestOutput(t, buf.Bytes())
	reason, _ := obj["error"].(string)
	lower := strings.ToLower(reason)
	for _, advice := range []string{"vc login", "log in", "sign in", "re-authenticate"} {
		if strings.Contains(lower, advice) {
			t.Errorf("error = %q sends the human back to sign-in; the sign-in worked", reason)
			break
		}
	}
}

// A malformed answer must not take the process with it. The desktop spawns this
// command and reads stdout; a panic gives it a stack trace where the state was
// supposed to be, and the refusal screen renders nothing at all.
func TestAccessRequestSurvivesAMalformedAnswer(t *testing.T) {
	for _, body := range []string{
		``,
		`null`,
		`[]`,
		`{"request":[]}`,
		`{"request":{}}`,
		`{"request":{"id":null,"status":null,"created_at":null,"resolved_at":null}}`,
		`{"request":{"id":"acc-req-1","status":"open","created_at":"2026-08-23","resolved_at":null}}`,
		strings.Repeat("{", 4096),
	} {
		signedInWith(t, "accepted-token")
		stand := newRequestStand(t).on(http.MethodGet, "/v1/vc/access-requests/me", http.StatusOK, body)

		obj := runAccessRequest(t, config.Config{AccessCheckHost: stand.url()}, false)
		if obj["accessRequest"] != "unavailable" {
			t.Errorf("body %.40q → accessRequest = %v, want unavailable", body, obj["accessRequest"])
		}
	}
}

// ── Досягаемость ─────────────────────────────────────────────────────────────

// A subcommand nobody registered is one the desktop cannot spawn, and the whole
// point of building it in Go rather than in the renderer was that the desktop
// spawns it.
func TestAccessRequestCommandIsReachable(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"access-request"})
	if err != nil || cmd == nil || cmd.Name() != "access-request" {
		t.Fatalf("rootCmd.Find(access-request) = %v, err %v — the desktop spawns `vc access-request`", cmd, err)
	}
	for _, flag := range []string{"ask", "json"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("--%s is not a flag of `vc access-request`", flag)
		}
	}
}

// Reading and asking are different acts, and the flag is what separates them.
// Wiring them the wrong way round turns opening the refusal screen into filing
// a request — which is the one thing the human is supposed to choose.
func TestAccessRequestFlagChoosesReadOrAsk(t *testing.T) {
	var gotAsk []bool
	saved := accessRequestJSONRunner
	accessRequestJSONRunner = func(_ config.Config, ask bool, _ io.Writer) error {
		gotAsk = append(gotAsk, ask)
		return nil
	}
	t.Cleanup(func() { accessRequestJSONRunner = saved })

	cmd, _, err := rootCmd.Find([]string{"access-request"})
	if err != nil {
		t.Fatalf("rootCmd.Find: %v", err)
	}

	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"--json"}, false},
		{[]string{"--json", "--ask"}, true},
	} {
		cmd.Flags().Set("ask", "false")
		cmd.Flags().Set("json", "false")
		if err := cmd.ParseFlags(tc.args); err != nil {
			t.Fatalf("ParseFlags(%v): %v", tc.args, err)
		}
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("RunE(%v): %v", tc.args, err)
		}
	}

	if len(gotAsk) != 2 || gotAsk[0] != false || gotAsk[1] != true {
		t.Errorf("ask flags seen = %v, want [false true]", gotAsk)
	}
}
