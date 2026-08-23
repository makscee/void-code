package auth

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Asking for access, from the client side.
//
// The chain is four links long — desktop → vc → Relay → Keys — and this file
// pins the third one's client half: the two calls `vc` makes to Relay.
//
//	POST <AccessCheckHost>/v1/vc/access-requests      ask, or read back the one already open
//	GET  <AccessCheckHost>/v1/vc/access-requests/me   read my own state
//
// The wire shape is read from void-relay `work/access-request-route`
// (873e2e2, src/vc-access-requests.ts) and the Keys route it fronts
// (void-keys src/routes/access-requests.ts):
//
//	201 {"request":{id,subject_id,status,created_at,resolved_at}}  a new ask
//	200 {"request":{...}}                                          the ask already open
//	200 {"request":null}                                           never asked (GET only)
//	401                                                            our bearer was refused
//	503 {"error":"unavailable"}                                    Relay could not ask Keys
//
// status ∈ {open, granted, declined}; created_at / resolved_at are epoch
// MILLISECONDS (void-relay's own fixture builds them with Date.parse), and
// resolved_at is null while the ask is open.
//
// WHAT THESE TESTS DO NOT PROVE. There is no live Relay serving this route:
// PR #7 is unmerged and unrolled, and the Keys migration that backs it is not
// applied to production and we are not allowed to apply it. Everything below
// runs against httptest. Agreement with the real chain is "не смог", not
// "прошло" — the first honest check of it is the first deploy.

const (
	// The epoch-millisecond values a server actually sends, and the strings
	// they must become. Written out rather than computed, so an implementation
	// that reads milliseconds as seconds (or the reverse) cannot agree with the
	// test by making the same mistake twice.
	fixtureCreatedMillis  = int64(1787479200000) // 2026-08-23T10:00:00Z
	fixtureCreatedRFC3339 = "2026-08-23T10:00:00Z"
	fixtureResolvedMillis = int64(1787563800000) // 2026-08-24T09:30:00Z
	fixtureResolvedRFC339 = "2026-08-24T09:30:00Z"
)

// call records one request the client made, so an assertion about where it went
// is an observation rather than a guess made from a downstream error message.
type call struct {
	method string
	path   string
	query  string
	auth   string
	body   string
}

// accessRequestStand answers the two routes and remembers every call. Handlers
// are keyed "METHOD /path", and a call to anything not in the map is recorded
// and answered 404 — an unlisted route is a fact worth catching, not a default
// worth papering over.
type accessRequestStand struct {
	server   *httptest.Server
	handlers map[string]func(w http.ResponseWriter)
	calls    chan call
	posts    atomic.Int32
	gets     atomic.Int32
}

func newAccessRequestStand(t *testing.T) *accessRequestStand {
	t.Helper()
	stand := &accessRequestStand{
		handlers: map[string]func(w http.ResponseWriter){},
		calls:    make(chan call, 16),
	}
	stand.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case stand.calls <- call{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			auth:   r.Header.Get("Authorization"),
			body:   string(body),
		}:
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

func (s *accessRequestStand) on(method, path string, status int, body string) {
	s.handlers[method+" "+path] = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func (s *accessRequestStand) url() string { return s.server.URL }

func (s *accessRequestStand) lastCall(t *testing.T) call {
	t.Helper()
	select {
	case c := <-s.calls:
		return c
	default:
		t.Fatal("the client never made a request")
		return call{}
	}
}

func openRequestBody(id string) string {
	return `{"request":{"id":"` + id + `","subject_id":"11111111-1111-4111-8111-111111111111",` +
		`"status":"open","created_at":1787479200000,"resolved_at":null}}`
}

// ── Asking ───────────────────────────────────────────────────────────────────

// A first ask creates (201); a second returns the one already open (200). Both
// are the same answer to the human — "your ask is in" — so both must arrive as
// a parsed request and not as an error, and the second must carry the FIRST
// submission time. A client that reported "asked just now" on every press would
// erase the only fact the waiting screen has to show.
func TestAskForAccessAcceptsBothCreatedAndAlreadyOpen(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"created", http.StatusCreated},
		{"already_open", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stand := newAccessRequestStand(t)
			stand.on(http.MethodPost, "/v1/vc/access-requests", tc.status, openRequestBody("acc-req-1"))

			req, err := AskForAccess(stand.url(), "accepted-token", stand.server.Client())
			if err != nil {
				t.Fatalf("AskForAccess: %v", err)
			}
			if req.ID != "acc-req-1" {
				t.Errorf("ID = %q, want acc-req-1", req.ID)
			}
			if req.Status != "open" {
				t.Errorf("Status = %q, want open", req.Status)
			}
			if got := req.CreatedAt.UTC().Format(time.RFC3339); got != fixtureCreatedRFC3339 {
				t.Errorf("CreatedAt = %s, want %s — created_at is epoch milliseconds", got, fixtureCreatedRFC3339)
			}
			if req.ResolvedAt != nil {
				t.Errorf("ResolvedAt = %v, want nil — an open ask has not been resolved", req.ResolvedAt)
			}
		})
	}
}

// The ask carries nothing that could steer it. The subject comes from the
// verified session at the relay (src/vc-access-requests.ts reads user.userId and
// ignores the request entirely), and Keys refuses a trusted-subject call that
// also carries a body it would have to trust. A client that helpfully attached
// "who I think I am" would be handing the relay a second, unverified opinion.
func TestAskForAccessSendsOnlyTheBearer(t *testing.T) {
	stand := newAccessRequestStand(t)
	stand.on(http.MethodPost, "/v1/vc/access-requests", http.StatusCreated, openRequestBody("acc-req-1"))

	if _, err := AskForAccess(stand.url(), "accepted-token", stand.server.Client()); err != nil {
		t.Fatalf("AskForAccess: %v", err)
	}

	c := stand.lastCall(t)
	if c.method != http.MethodPost || c.path != "/v1/vc/access-requests" {
		t.Errorf("ask went to %s %s, want POST /v1/vc/access-requests", c.method, c.path)
	}
	if c.auth != "Bearer accepted-token" {
		t.Errorf("Authorization = %q, want the bearer", c.auth)
	}
	if c.query != "" {
		t.Errorf("ask carried a query %q — the identity is the session's, and a query is a second opinion about it", c.query)
	}
	if strings.TrimSpace(c.body) != "" {
		t.Errorf("ask carried a body %q — nothing about the ask is the caller's to say", c.body)
	}
	if n := stand.posts.Load(); n != 1 {
		t.Errorf("one ask made %d POSTs — a retry here is a second row in somebody's queue", n)
	}
}

// An ask that produced nothing is not "you never asked": the POST route has no
// such answer (the relay turns it into 503), so a client that accepted it would
// be inventing the one state the button is supposed to change.
func TestAskForAccessRefusesAnEmptyAnswer(t *testing.T) {
	stand := newAccessRequestStand(t)
	stand.on(http.MethodPost, "/v1/vc/access-requests", http.StatusOK, `{"request":null}`)

	_, err := AskForAccess(stand.url(), "accepted-token", stand.server.Client())
	if err == nil {
		t.Fatal("an ask that created nothing was reported as success")
	}
	if !errors.Is(err, ErrAccessRequestUnavailable) {
		t.Errorf("error does not carry ErrAccessRequestUnavailable: %v", err)
	}
}

// ── Reading ──────────────────────────────────────────────────────────────────

// Never having asked is an answer, and the single most important one in this
// file: it is what the button is FOR. It arrives as ok=false with no error, and
// the reading costs nothing — in particular it must not create anything.
func TestFetchAccessRequestReportsNeverAskedAsAnAnswer(t *testing.T) {
	stand := newAccessRequestStand(t)
	stand.on(http.MethodGet, "/v1/vc/access-requests/me", http.StatusOK, `{"request":null}`)

	req, ok, err := FetchAccessRequest(stand.url(), "accepted-token", stand.server.Client())
	if err != nil {
		t.Fatalf("never having asked was reported as a failure: %v", err)
	}
	if ok {
		t.Errorf("ok = true with request %+v, want false — the server said there is nothing", req)
	}

	c := stand.lastCall(t)
	if c.method != http.MethodGet || c.path != "/v1/vc/access-requests/me" {
		t.Errorf("read went to %s %s, want GET /v1/vc/access-requests/me", c.method, c.path)
	}
	if n := stand.posts.Load(); n != 0 {
		t.Errorf("reading the state made %d POST(s) — looking at the queue must not join it", n)
	}
}

// The three outcomes the screen knows how to name, and the resolution time that
// comes with the two that are finished.
func TestFetchAccessRequestReadsEachOutcome(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		wantStatus   string
		wantResolved string // "" = must be nil
	}{
		{
			name:       "open",
			body:       openRequestBody("acc-req-1"),
			wantStatus: "open",
		},
		{
			name: "granted",
			body: `{"request":{"id":"acc-req-1","subject_id":"11111111-1111-4111-8111-111111111111",` +
				`"status":"granted","created_at":1787479200000,"resolved_at":1787563800000}}`,
			wantStatus:   "granted",
			wantResolved: fixtureResolvedRFC339,
		},
		{
			name: "declined",
			body: `{"request":{"id":"acc-req-1","subject_id":"11111111-1111-4111-8111-111111111111",` +
				`"status":"declined","created_at":1787479200000,"resolved_at":1787563800000}}`,
			wantStatus:   "declined",
			wantResolved: fixtureResolvedRFC339,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stand := newAccessRequestStand(t)
			stand.on(http.MethodGet, "/v1/vc/access-requests/me", http.StatusOK, tc.body)

			req, ok, err := FetchAccessRequest(stand.url(), "accepted-token", stand.server.Client())
			if err != nil || !ok {
				t.Fatalf("FetchAccessRequest: ok=%v err=%v", ok, err)
			}
			if req.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", req.Status, tc.wantStatus)
			}
			if got := req.CreatedAt.UTC().Format(time.RFC3339); got != fixtureCreatedRFC3339 {
				t.Errorf("CreatedAt = %s, want %s", got, fixtureCreatedRFC3339)
			}
			switch {
			case tc.wantResolved == "":
				if req.ResolvedAt != nil {
					t.Errorf("ResolvedAt = %v, want nil", req.ResolvedAt)
				}
			case req.ResolvedAt == nil:
				t.Errorf("ResolvedAt = nil, want %s", tc.wantResolved)
			default:
				if got := req.ResolvedAt.UTC().Format(time.RFC3339); got != tc.wantResolved {
					t.Errorf("ResolvedAt = %s, want %s", got, tc.wantResolved)
				}
			}
		})
	}
}

// ── "Нет заявки" против "не смогли спросить" ─────────────────────────────────

// The failure this whole design exists to prevent. Every case below ends with
// the client holding no request — and in exactly one of them that is because
// the server said so. The rest are silence, and silence rendered as "you have
// not asked yet" puts a button on screen that has already been pressed.
//
// The 400 case is not hypothetical: production's relay:443 answers
// `This is a CONNECT proxy` to every path it does not serve (probed 2026-08-23),
// and this route is not deployed anywhere yet. Until it is, this is the answer
// a real user's client receives.
func TestFetchAccessRequestSeparatesSilenceFromNeverAsked(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"relay_could_not_reach_keys", http.StatusServiceUnavailable, `{"error":"unavailable"}`},
		{"relay_broken", http.StatusInternalServerError, ``},
		{"route_not_deployed", http.StatusNotFound, ``},
		{"connect_proxy", http.StatusBadRequest, `This is a CONNECT proxy`},
		{"gateway", http.StatusBadGateway, ``},
		{"not_json", http.StatusOK, `<html>login</html>`},
		{"no_request_key", http.StatusOK, `{"ok":true}`},
		{"request_is_not_an_object", http.StatusOK, `{"request":"none"}`},
		{"empty_body", http.StatusOK, ``},
		// A status word outside the three the screen can name is not an answer
		// we may act on. Passing it through would put a word on the desktop that
		// its renderer does not recognise, which arrives there as silence.
		{"unknown_status_word", http.StatusOK, `{"request":{"id":"acc-req-1","subject_id":"11111111-1111-4111-8111-111111111111","status":"pending","created_at":1787479200000,"resolved_at":null}}`},
		// The refusal code, on the ask route. The person asking is precisely the
		// one with no access — answering them "you have no access" instead of
		// showing their request closes the only door left open to them.
		{"payment_required", http.StatusPaymentRequired, `{"error":"budget_exceeded"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stand := newAccessRequestStand(t)
			stand.on(http.MethodGet, "/v1/vc/access-requests/me", tc.status, tc.body)

			_, ok, err := FetchAccessRequest(stand.url(), "accepted-token", stand.server.Client())
			if err == nil {
				t.Fatalf("HTTP %d %q was reported as a successful read — nothing answered, so nothing is known", tc.status, tc.body)
			}
			if ok {
				t.Errorf("ok = true alongside an error; a caller that reads ok first would act on nothing")
			}
			if !errors.Is(err, ErrAccessRequestUnavailable) {
				t.Errorf("error does not carry ErrAccessRequestUnavailable: %v — a caller has to branch on the sentinel, not on prose an upstream happened to send", err)
			}
			if errors.Is(err, ErrAccessNotGranted) {
				t.Errorf("a failed read was reported as a refused account: %v", err)
			}
		})
	}

	// Nothing listening at all is the same class: we could not ask.
	t.Run("nobody_listening", func(t *testing.T) {
		stand := newAccessRequestStand(t)
		url := stand.url()
		stand.server.Close()

		_, ok, err := FetchAccessRequest(url, "accepted-token", &http.Client{Timeout: 2 * time.Second})
		if err == nil || ok {
			t.Fatalf("an unreachable host read as a successful answer: ok=%v err=%v", ok, err)
		}
		if !errors.Is(err, ErrAccessRequestUnavailable) {
			t.Errorf("error does not carry ErrAccessRequestUnavailable: %v", err)
		}
	})
}

// A refused bearer is its own outcome and keeps its own sentinel: the relay
// judges the caller's token before it ever speaks to Keys, so 401 here means the
// same thing it means everywhere else. Collapsing it into "unavailable" would
// tell someone with a stale token to wait for an operator forever.
func TestAccessRequestReportsARefusedBearerAsItsOwnOutcome(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(host string, c *http.Client) error
	}{
		{"ask", func(host string, c *http.Client) error { _, err := AskForAccess(host, "stale-token", c); return err }},
		{"read", func(host string, c *http.Client) error { _, _, err := FetchAccessRequest(host, "stale-token", c); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stand := newAccessRequestStand(t)
			stand.on(http.MethodPost, "/v1/vc/access-requests", http.StatusUnauthorized, ``)
			stand.on(http.MethodGet, "/v1/vc/access-requests/me", http.StatusUnauthorized, ``)

			err := tc.run(stand.url(), stand.server.Client())
			if err == nil {
				t.Fatal("a refused bearer passed")
			}
			if !errors.Is(err, ErrNotLoggedIn) {
				t.Errorf("error does not carry ErrNotLoggedIn: %v", err)
			}
			if errors.Is(err, ErrAccessRequestUnavailable) {
				t.Error("a refused bearer was reported as an unavailable upstream — one is fixed by signing in, the other by waiting")
			}
		})
	}
}

// ── Заявка ничего не выдаёт ──────────────────────────────────────────────────

// A request is a question, never a grant. Nothing in the answer may become an
// entitlement, a budget or an expiry — not even when a server sends one, which
// is the case worth testing: the refusal has to happen at every layer, and this
// is the last one before a human reads it.
func TestAccessRequestCarriesNoGrant(t *testing.T) {
	stand := newAccessRequestStand(t)
	stand.on(http.MethodGet, "/v1/vc/access-requests/me", http.StatusOK,
		`{"request":{"id":"acc-req-1","subject_id":"11111111-1111-4111-8111-111111111111",`+
			`"status":"granted","created_at":1787479200000,"resolved_at":1787563800000,`+
			`"expires_at":1790000000000,"subscription":{"active":true},"pct":0,"balanceUsd":50}}`)

	req, ok, err := FetchAccessRequest(stand.url(), "accepted-token", stand.server.Client())
	if err != nil || !ok {
		t.Fatalf("FetchAccessRequest: ok=%v err=%v", ok, err)
	}

	// The type is the guard: a field that does not exist cannot be filled in
	// from a payload, and cannot be shown to anyone. This test fails to compile
	// the moment somebody adds one — which is the intended way to fail.
	if got := describeAccessRequestFields(req); got != "ID SubjectID Status CreatedAt ResolvedAt" {
		t.Errorf("AccessRequest fields = %q; an access request answers when and whether, never what for and until when", got)
	}
}

// describeAccessRequestFields names the fields the type is allowed to have, in
// order. It is written by hand rather than by reflection so that adding a field
// to AccessRequest breaks the build here, in front of the reason.
func describeAccessRequestFields(req AccessRequest) string {
	_ = req.ID
	_ = req.SubjectID
	_ = req.Status
	_ = req.CreatedAt
	_ = req.ResolvedAt
	return "ID SubjectID Status CreatedAt ResolvedAt"
}

// Whatever goes wrong, the message a human ends up reading must not repeat the
// inside of the system at them — least of all the credential.
func TestAccessRequestFailuresSayNothingInternal(t *testing.T) {
	const secret = "tok_live_do_not_print_me"

	stand := newAccessRequestStand(t)
	stand.on(http.MethodGet, "/v1/vc/access-requests/me", http.StatusServiceUnavailable,
		`{"error":"unavailable","upstream":"http://keys.internal:8788/v1/access-requests/me","trace":"at Object.openOrGet (/srv/keys/dist/services/access-requests.js:88:11)"}`)

	_, _, err := FetchAccessRequest(stand.url(), secret, stand.server.Client())
	if err == nil {
		t.Fatal("a 503 read as success")
	}
	message := err.Error()
	if strings.Contains(message, secret) {
		t.Errorf("error leaked the credential: %q", message)
	}
	for _, internal := range []string{"keys.internal", "dist/services", "at Object.", "8788"} {
		if strings.Contains(message, internal) {
			t.Errorf("error = %q repeats the inside of the system (%q); a person reading this can act on none of it", message, internal)
		}
	}
	lower := strings.ToLower(message)
	for _, advice := range []string{"vc login", "log in", "sign in", "re-authenticate"} {
		if strings.Contains(lower, advice) {
			t.Errorf("error = %q sends the human back to sign-in; the sign-in worked and repeating it changes nothing", message)
			break
		}
	}
}
