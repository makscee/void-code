package main

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

const (
	modelTerra = "gpt-5.6-terra"
	modelSol   = "gpt-5.6-sol"
	modelLuna  = "gpt-5.6-luna"
	modelAstra = "gpt-6-astra"
)

var modelDecisionEpoch = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

func decisionBundle(generation string, restricted bool, allowed []string, defaultModel string) piModelDecisionBundle {
	quotaState := "normal"
	if restricted {
		quotaState = "fallback"
	}
	return piModelDecisionBundle{
		Generation:                  generation,
		EvaluatedAt:                 modelDecisionEpoch,
		ValidUntil:                  modelDecisionEpoch.Add(2 * time.Minute),
		EffectiveAssignmentRevision: "assignment-" + generation,
		PolicyRevision:              "policy-" + generation,
		TierID:                      "t2",
		TierModelSetDigest:          "tier-digest-" + generation,
		InputFingerprint:            "fingerprint-" + generation,
		QuotaState:                  quotaState,
		Restricted:                  restricted,
		AllowedCodexModelIDs:        slices.Clone(allowed),
		DefaultCodexModelID:         defaultModel,
		FallbackCodexModelID:        modelLuna,
	}
}

func applyDecision(
	state piModelDecisionState,
	bundle piModelDecisionBundle,
	requestStartedAt time.Duration,
	responseReceivedAt time.Duration,
	skewGuard time.Duration,
) piModelDecisionState {
	return reducePiModelDecision(state, piModelDecisionEvent{
		Kind:               piModelDecisionApplyServerBundle,
		Bundle:             bundle,
		RequestStartedAt:   requestStartedAt,
		ResponseReceivedAt: responseReceivedAt,
		SkewGuard:          skewGuard,
	})
}

func selectLocalCodexModel(state piModelDecisionState, modelID string) piModelDecisionState {
	return reducePiModelDecision(state, piModelDecisionEvent{
		Kind:    piModelDecisionSelectLocalCodexModel,
		ModelID: modelID,
	})
}

func checkDecisionLease(state piModelDecisionState, now time.Duration) piModelDecisionState {
	return reducePiModelDecision(state, piModelDecisionEvent{
		Kind:         piModelDecisionCheckMonotonicLease,
		MonotonicNow: now,
	})
}

func applyOrdinaryDecision(state piModelDecisionState, bundle piModelDecisionBundle) piModelDecisionState {
	return applyDecision(state, bundle, 10*time.Second, 12*time.Second, 2*time.Second)
}

func assertDecisionCatalog(t *testing.T, state piModelDecisionState, allowed []string, defaultModel, selectedModel string) {
	t.Helper()
	if !slices.Equal(state.AllowedCodexModelIDs, allowed) {
		t.Errorf("allowed Codex models = %q, want %q", state.AllowedCodexModelIDs, allowed)
	}
	if state.DefaultCodexModelID != defaultModel {
		t.Errorf("default Codex model = %q, want %q", state.DefaultCodexModelID, defaultModel)
	}
	if state.SelectedCodexModelID != selectedModel {
		t.Errorf("selected Codex model = %q, want %q", state.SelectedCodexModelID, selectedModel)
	}
}

func assertDecisionFailClosed(t *testing.T, state piModelDecisionState) {
	t.Helper()
	assertDecisionCatalog(t, state, nil, "", "")
}

// Without strict numeric ordering, a fresh authority bundle can be ignored or a stale one can overwrite it.
func TestPiModelDecisionAppliesStrictlyGreaterGeneration(t *testing.T) {
	state := newPiModelDecisionState(modelTerra)
	state = applyOrdinaryDecision(state, decisionBundle("9", false, []string{modelTerra, modelLuna}, modelTerra))

	got := applyOrdinaryDecision(state, decisionBundle("10", false, []string{modelSol, modelLuna}, modelSol))

	if got.LastAppliedGeneration != "10" {
		t.Fatalf("last generation = %q, want 10", got.LastAppliedGeneration)
	}
	assertDecisionCatalog(t, got, []string{modelSol, modelLuna}, modelSol, modelSol)
}

// A delayed older response must not roll catalog authority or refresh its lease.
func TestPiModelDecisionIgnoresLowerGeneration(t *testing.T) {
	state := newPiModelDecisionState(modelAstra)
	state = applyOrdinaryDecision(state, decisionBundle("12", true, []string{modelLuna}, modelLuna))

	got := applyDecision(state, decisionBundle("11", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra), 30*time.Second, 31*time.Second, 0)

	if !reflect.DeepEqual(got, state) {
		t.Fatalf("lower generation changed state\n got: %#v\nwant: %#v", got, state)
	}
}

// An identical authority response at the same generation may safely renew only its measured lease.
func TestPiModelDecisionAcceptsEqualIdenticalAuthority(t *testing.T) {
	bundle := decisionBundle("7", false, []string{modelTerra, modelLuna}, modelTerra)
	state := applyDecision(newPiModelDecisionState(modelTerra), bundle, time.Second, 3*time.Second, time.Second)
	refreshed := bundle
	refreshed.EvaluatedAt = bundle.EvaluatedAt.Add(time.Minute)
	refreshed.ValidUntil = bundle.ValidUntil.Add(time.Minute)

	got := applyDecision(state, refreshed, 50*time.Second, 55*time.Second, time.Second)

	assertDecisionCatalog(t, got, []string{modelTerra, modelLuna}, modelTerra, modelTerra)
	if got.LastAppliedGeneration != "7" {
		t.Errorf("last generation = %q, want 7", got.LastAppliedGeneration)
	}
	const wantLease = 169 * time.Second
	if got.LeaseExpiresAt != wantLease {
		t.Errorf("lease deadline = %v, want %v", got.LeaseExpiresAt, wantLease)
	}
}

// Equal generation with a different ordered catalog is impossible authority and must remove every choice.
func TestPiModelDecisionEqualGenerationCatalogConflictFailsClosed(t *testing.T) {
	bundle := decisionBundle("4", false, []string{modelTerra, modelLuna}, modelTerra)
	state := applyOrdinaryDecision(newPiModelDecisionState(modelTerra), bundle)
	conflict := bundle
	conflict.AllowedCodexModelIDs = []string{modelLuna, modelTerra}

	got := applyOrdinaryDecision(state, conflict)

	assertDecisionFailClosed(t, got)
}

// Equal generation with a changed revision or fingerprint must not be mistaken for a harmless retry.
func TestPiModelDecisionEqualGenerationMetadataConflictFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*piModelDecisionBundle)
	}{
		{name: "policy revision", mutate: func(bundle *piModelDecisionBundle) { bundle.PolicyRevision = "policy-conflict" }},
		{name: "input fingerprint", mutate: func(bundle *piModelDecisionBundle) { bundle.InputFingerprint = "fingerprint-conflict" }},
		{name: "restriction", mutate: func(bundle *piModelDecisionBundle) { bundle.Restricted = true; bundle.QuotaState = "fallback" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := decisionBundle("4", false, []string{modelTerra, modelLuna}, modelTerra)
			state := applyOrdinaryDecision(newPiModelDecisionState(modelTerra), bundle)
			conflict := bundle
			tc.mutate(&conflict)

			got := applyOrdinaryDecision(state, conflict)

			assertDecisionFailClosed(t, got)
		})
	}
}

// Field-by-field merging can expose an old model under a new tier or fingerprint.
func TestPiModelDecisionReplacesBundleAtomically(t *testing.T) {
	state := newPiModelDecisionState(modelAstra)
	state = applyOrdinaryDecision(state, decisionBundle("20", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra))
	newer := decisionBundle("21", true, []string{modelLuna}, modelLuna)
	newer.EffectiveAssignmentRevision = "assignment-new"
	newer.PolicyRevision = "policy-new"
	newer.TierID = "t1"
	newer.TierModelSetDigest = "digest-new"
	newer.InputFingerprint = "fingerprint-new"

	got := applyOrdinaryDecision(state, newer)

	assertDecisionCatalog(t, got, []string{modelLuna}, modelLuna, modelLuna)
	if got.LastAppliedGeneration != "21" || got.EffectiveAssignmentRevision != "assignment-new" ||
		got.PolicyRevision != "policy-new" || got.TierID != "t1" ||
		got.TierModelSetDigest != "digest-new" || got.InputFingerprint != "fingerprint-new" ||
		got.QuotaState != "fallback" || !got.Restricted {
		t.Fatalf("authority bundle was not replaced atomically: %#v", got)
	}
}

// Invalid catalogs must narrow to no Codex choice instead of retaining a stale expensive model.
func TestPiModelDecisionInvalidCatalogFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name         string
		allowed      []string
		defaultModel string
	}{
		{name: "empty set", allowed: nil, defaultModel: ""},
		{name: "default absent", allowed: []string{modelTerra, modelLuna}, defaultModel: modelAstra},
		{name: "duplicate model", allowed: []string{modelTerra, modelTerra}, defaultModel: modelTerra},
		{name: "empty model id", allowed: []string{modelTerra, ""}, defaultModel: modelTerra},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := applyOrdinaryDecision(newPiModelDecisionState(modelAstra), decisionBundle("1", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra))

			got := applyOrdinaryDecision(state, decisionBundle("2", false, tc.allowed, tc.defaultModel))

			assertDecisionFailClosed(t, got)
		})
	}
}

// A server interval that is negative or fully consumed by RTT and skew has no usable lease.
func TestPiModelDecisionRejectsNonPositiveLease(t *testing.T) {
	for _, tc := range []struct {
		name       string
		validUntil time.Time
		started    time.Duration
		received   time.Duration
		skew       time.Duration
	}{
		{name: "negative server interval", validUntil: modelDecisionEpoch.Add(-time.Nanosecond), started: 0, received: 0, skew: 0},
		{name: "zero after RTT and skew", validUntil: modelDecisionEpoch.Add(25 * time.Second), started: 100 * time.Second, received: 120 * time.Second, skew: 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := decisionBundle("1", false, []string{modelTerra, modelLuna}, modelTerra)
			bundle.ValidUntil = tc.validUntil

			got := applyDecision(newPiModelDecisionState(modelTerra), bundle, tc.started, tc.received, tc.skew)

			assertDecisionFailClosed(t, got)
		})
	}
}

// The exact positive boundary must not be rounded down to an expired lease.
func TestPiModelDecisionAcceptsOneNanosecondLease(t *testing.T) {
	bundle := decisionBundle("1", false, []string{modelTerra, modelLuna}, modelTerra)
	bundle.ValidUntil = bundle.EvaluatedAt.Add(25*time.Second + time.Nanosecond)

	got := applyDecision(newPiModelDecisionState(modelTerra), bundle, 100*time.Second, 120*time.Second, 5*time.Second)

	assertDecisionCatalog(t, got, []string{modelTerra, modelLuna}, modelTerra, modelTerra)
	if got.LeaseExpiresAt != 120*time.Second+time.Nanosecond {
		t.Errorf("lease deadline = %v, want %v", got.LeaseExpiresAt, 120*time.Second+time.Nanosecond)
	}
}

// Omitting either measured RTT or skew carries authority beyond the server-bounded interval.
func TestPiModelDecisionDerivesLeaseFromMeasuredRTTAndSkew(t *testing.T) {
	bundle := decisionBundle("1", false, []string{modelTerra, modelLuna}, modelTerra)
	bundle.ValidUntil = bundle.EvaluatedAt.Add(2 * time.Minute)

	got := applyDecision(newPiModelDecisionState(modelTerra), bundle, 50*time.Second, 70*time.Second, 5*time.Second)

	const wantLease = 165 * time.Second
	if got.LeaseExpiresAt != wantLease {
		t.Errorf("lease deadline = %v, want response 70s + (120s - RTT 20s - skew 5s) = %v", got.LeaseExpiresAt, wantLease)
	}
}

// At the exact monotonic deadline, a stale catalog must disappear without consulting wall time.
func TestPiModelDecisionExpiresExactlyAtMonotonicDeadline(t *testing.T) {
	bundle := decisionBundle("1", false, []string{modelTerra, modelLuna}, modelTerra)
	state := applyDecision(newPiModelDecisionState(modelTerra), bundle, 50*time.Second, 70*time.Second, 5*time.Second)

	before := checkDecisionLease(state, 165*time.Second-time.Nanosecond)
	expired := checkDecisionLease(before, 165*time.Second)

	assertDecisionCatalog(t, before, []string{modelTerra, modelLuna}, modelTerra, modelTerra)
	assertDecisionFailClosed(t, expired)
}

// A backward monotonic observation is invalid timing evidence and cannot authorize models.
func TestPiModelDecisionRejectsBackwardMonotonicObservation(t *testing.T) {
	got := applyDecision(
		newPiModelDecisionState(modelAstra),
		decisionBundle("1", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra),
		100*time.Second,
		99*time.Second,
		0,
	)

	assertDecisionFailClosed(t, got)
}

// A T1 catalog must evict persisted Astra without remembering it as a fallback preference.
func TestPiModelDecisionT1BundleRemovesStaleAstra(t *testing.T) {
	state := newPiModelDecisionState(modelAstra)
	state = applyOrdinaryDecision(state, decisionBundle("1", false, []string{modelTerra, modelLuna}, modelTerra))
	assertDecisionCatalog(t, state, []string{modelTerra, modelLuna}, modelTerra, modelTerra)

	got := applyOrdinaryDecision(state, decisionBundle("2", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra))

	assertDecisionCatalog(t, got, []string{modelTerra, modelLuna, modelAstra}, modelTerra, modelTerra)
}

// Restricted mode must use the current server allowlist/default rather than a client-hard-coded Luna.
func TestPiModelDecisionFallbackUsesServerAllowlistAndDefault(t *testing.T) {
	const futureFallback = "gpt-5.7-economy"
	state := applyOrdinaryDecision(newPiModelDecisionState(modelSol), decisionBundle("1", false, []string{modelSol, futureFallback}, modelSol))
	fallback := decisionBundle("2", true, []string{futureFallback}, futureFallback)
	fallback.FallbackCodexModelID = futureFallback

	got := applyOrdinaryDecision(state, fallback)

	assertDecisionCatalog(t, got, []string{futureFallback}, futureFallback, futureFallback)
}

// Fallback entry must save the local unrestricted choice before selecting the restrictive default.
func TestPiModelDecisionFallbackExitRestoresAllowedMemory(t *testing.T) {
	state := applyOrdinaryDecision(newPiModelDecisionState(modelAstra), decisionBundle("1", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra))
	state = applyOrdinaryDecision(state, decisionBundle("2", true, []string{modelLuna}, modelLuna))

	got := applyOrdinaryDecision(state, decisionBundle("3", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra))

	assertDecisionCatalog(t, got, []string{modelTerra, modelLuna, modelAstra}, modelTerra, modelAstra)
}

// A forbidden attempt during fallback must not replace the model that will be restored later.
func TestPiModelDecisionForbiddenFallbackAttemptDoesNotOverwriteMemory(t *testing.T) {
	state := applyOrdinaryDecision(newPiModelDecisionState(modelAstra), decisionBundle("1", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra))
	state = applyOrdinaryDecision(state, decisionBundle("2", true, []string{modelLuna}, modelLuna))

	state = selectLocalCodexModel(state, modelTerra)
	assertDecisionCatalog(t, state, []string{modelLuna}, modelLuna, modelLuna)
	got := applyOrdinaryDecision(state, decisionBundle("3", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra))

	assertDecisionCatalog(t, got, []string{modelTerra, modelLuna, modelAstra}, modelTerra, modelAstra)
}

// A newer restricted bundle is not a new entry transition and must not replace pre-fallback memory.
func TestPiModelDecisionConsecutiveRestrictedBundleDoesNotOverwriteMemory(t *testing.T) {
	const futureFallback = "gpt-5.7-economy"
	state := applyOrdinaryDecision(newPiModelDecisionState(modelAstra), decisionBundle("1", false, []string{modelTerra, modelLuna, modelAstra, futureFallback}, modelTerra))
	state = applyOrdinaryDecision(state, decisionBundle("2", true, []string{modelLuna}, modelLuna))
	secondFallback := decisionBundle("3", true, []string{futureFallback}, futureFallback)
	secondFallback.FallbackCodexModelID = futureFallback
	state = applyOrdinaryDecision(state, secondFallback)

	got := applyOrdinaryDecision(state, decisionBundle("4", false, []string{modelTerra, modelLuna, modelAstra, futureFallback}, modelTerra))

	assertDecisionCatalog(t, got, []string{modelTerra, modelLuna, modelAstra, futureFallback}, modelTerra, modelAstra)
}

// Entitlement downgrade during fallback must prevent restoration of a remembered Astra.
func TestPiModelDecisionDowngradeMakesMemoryInvalidAndChoosesDefault(t *testing.T) {
	state := applyOrdinaryDecision(newPiModelDecisionState(modelAstra), decisionBundle("1", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra))
	state = applyOrdinaryDecision(state, decisionBundle("2", true, []string{modelLuna}, modelLuna))

	got := applyOrdinaryDecision(state, decisionBundle("3", false, []string{modelTerra, modelLuna}, modelTerra))

	assertDecisionCatalog(t, got, []string{modelTerra, modelLuna}, modelTerra, modelTerra)
}

// A normal response delayed behind fallback must not re-add expensive Codex models.
func TestPiModelDecisionDelayedOlderNormalCannotWidenFallback(t *testing.T) {
	state := applyOrdinaryDecision(newPiModelDecisionState(modelAstra), decisionBundle("11", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra))
	state = applyOrdinaryDecision(state, decisionBundle("12", true, []string{modelLuna}, modelLuna))

	got := applyOrdinaryDecision(state, decisionBundle("11", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra))

	if !got.Restricted || got.LastAppliedGeneration != "12" {
		t.Fatalf("delayed normal changed authority: %#v", got)
	}
	assertDecisionCatalog(t, got, []string{modelLuna}, modelLuna, modelLuna)
}

// Converting generations through float64 collapses adjacent values above JavaScript's safe integer limit.
func TestPiModelDecisionOrdersGenerationsAboveJavaScriptSafeInteger(t *testing.T) {
	state := applyOrdinaryDecision(newPiModelDecisionState(modelAstra), decisionBundle("9007199254740993", true, []string{modelLuna}, modelLuna))
	state = applyOrdinaryDecision(state, decisionBundle("9007199254740992", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra))

	if state.LastAppliedGeneration != "9007199254740993" || !state.Restricted {
		t.Fatalf("unsafe generation comparison widened state: %#v", state)
	}
	got := applyOrdinaryDecision(state, decisionBundle("9007199254740994", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra))
	if got.LastAppliedGeneration != "9007199254740994" || got.Restricted {
		t.Fatalf("strictly greater bigint generation was not applied: %#v", got)
	}
}

// Monotonic authority must converge on the greatest generation regardless of delivery order.
func TestPiModelDecisionGenerationOrderProperty(t *testing.T) {
	bundles := []piModelDecisionBundle{
		decisionBundle("2", false, []string{modelTerra, modelLuna}, modelTerra),
		decisionBundle("10", false, []string{modelTerra, modelSol, modelLuna}, modelSol),
		decisionBundle("3", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra),
	}
	for _, order := range modelDecisionPermutations([]int{0, 1, 2}) {
		state := newPiModelDecisionState(modelTerra)
		for _, index := range order {
			state = applyOrdinaryDecision(state, bundles[index])
		}
		if state.LastAppliedGeneration != "10" || state.PolicyRevision != "policy-10" || state.TierModelSetDigest != "tier-digest-10" {
			t.Errorf("order %v converged on mixed/stale authority: %#v", order, state)
		}
		assertDecisionCatalog(t, state, []string{modelTerra, modelSol, modelLuna}, modelSol, modelTerra)
	}
}

// Repeating the same observation must not create a second transition or mutate local memory.
func TestPiModelDecisionEqualIdenticalObservationIsIdempotent(t *testing.T) {
	bundle := decisionBundle("1", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra)
	event := piModelDecisionEvent{
		Kind:               piModelDecisionApplyServerBundle,
		Bundle:             bundle,
		RequestStartedAt:   10 * time.Second,
		ResponseReceivedAt: 12 * time.Second,
		SkewGuard:          2 * time.Second,
	}
	state := reducePiModelDecision(newPiModelDecisionState(modelAstra), event)

	got := reducePiModelDecision(state, event)

	if !reflect.DeepEqual(got, state) {
		t.Fatalf("identical observation was not idempotent\n got: %#v\nwant: %#v", got, state)
	}
}

// Storing preference globally would make one local client restore another client's model.
func TestPiModelDecisionKeepsMemoryPerLocalClient(t *testing.T) {
	normal := decisionBundle("1", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra)
	fallback := decisionBundle("2", true, []string{modelLuna}, modelLuna)
	restored := decisionBundle("3", false, []string{modelTerra, modelLuna, modelAstra}, modelTerra)
	clientA := applyOrdinaryDecision(newPiModelDecisionState(modelAstra), normal)
	clientB := applyOrdinaryDecision(newPiModelDecisionState(modelTerra), normal)
	clientA = applyOrdinaryDecision(clientA, fallback)
	clientB = applyOrdinaryDecision(clientB, fallback)

	clientA = applyOrdinaryDecision(clientA, restored)
	clientB = applyOrdinaryDecision(clientB, restored)

	if clientA.SelectedCodexModelID != modelAstra || clientB.SelectedCodexModelID != modelTerra {
		t.Fatalf("local memories crossed clients: A=%q B=%q", clientA.SelectedCodexModelID, clientB.SelectedCodexModelID)
	}
}

func modelDecisionPermutations(values []int) [][]int {
	if len(values) == 0 {
		return [][]int{{}}
	}
	var result [][]int
	for index, value := range values {
		rest := append(slices.Clone(values[:index]), values[index+1:]...)
		for _, suffix := range modelDecisionPermutations(rest) {
			result = append(result, append([]int{value}, suffix...))
		}
	}
	return result
}
