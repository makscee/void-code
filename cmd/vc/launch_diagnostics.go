package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type launchPhase string

type launchOutcome string

type launchSource string

const (
	phaseLocalStateLoad    launchPhase = "local_state_load"
	phaseFirstRender       launchPhase = "first_render"
	phaseAuthComplete      launchPhase = "auth_complete"
	phaseProvidersComplete launchPhase = "providers_complete"
	phaseUpdateComplete    launchPhase = "update_complete"
	phaseSelection         launchPhase = "selection"
	phaseSpawnHandoff      launchPhase = "spawn_handoff"

	outcomeComplete launchOutcome = "complete"
	outcomeRejected launchOutcome = "rejected"

	sourceLocal     launchSource = "local"
	sourceFresh     launchSource = "fresh"
	sourceStale     launchSource = "stale"
	sourceTransient launchSource = "transient"
	sourceRejected  launchSource = "rejected"
)

type launchDiagnosticRecord struct {
	phase      launchPhase
	outcome    launchOutcome
	source     launchSource
	durationMS int64
}

// launchDiagnostics accepts only closed enums and durations. Callers cannot
// attach errors, request data, identity, credentials, or other arbitrary text.
type launchDiagnostics struct {
	enabled bool
	now     func() time.Time
	out     io.Writer
	started time.Time

	mu      sync.Mutex
	seen    map[launchPhase]bool
	pending []launchDiagnosticRecord
	flushed bool
}

func newLaunchDiagnosticsFromEnv(now func() time.Time, out io.Writer) *launchDiagnostics {
	return newLaunchDiagnostics(os.Getenv("VC_LAUNCH_DIAGNOSTICS") == "1", now, out)
}

func newLaunchDiagnostics(enabled bool, now func() time.Time, out io.Writer) *launchDiagnostics {
	return &launchDiagnostics{
		enabled: enabled,
		now:     now,
		out:     out,
		started: now(),
		seen:    make(map[launchPhase]bool),
	}
}

func (d *launchDiagnostics) record(phase launchPhase, outcome launchOutcome, source launchSource) {
	if d == nil || !d.enabled || !validLaunchRecord(phase, outcome, source) {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen[phase] {
		return
	}
	d.seen[phase] = true
	duration := d.now().Sub(d.started).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	r := launchDiagnosticRecord{phase: phase, outcome: outcome, source: source, durationMS: duration}
	if d.flushed {
		d.write(r)
		return
	}
	d.pending = append(d.pending, r)
}

func (d *launchDiagnostics) flush() {
	if d == nil || !d.enabled {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.flushed {
		return
	}
	d.flushed = true
	for _, r := range d.pending {
		d.write(r)
	}
	d.pending = nil
}

func (d *launchDiagnostics) write(r launchDiagnosticRecord) {
	_, _ = fmt.Fprintf(d.out, "vc-launch phase=%s outcome=%s source=%s duration_ms=%d\n", r.phase, r.outcome, r.source, r.durationMS)
}

func validLaunchRecord(phase launchPhase, outcome launchOutcome, source launchSource) bool {
	switch phase {
	case phaseLocalStateLoad, phaseFirstRender, phaseAuthComplete, phaseProvidersComplete, phaseUpdateComplete, phaseSelection, phaseSpawnHandoff:
	default:
		return false
	}
	if outcome != outcomeComplete && outcome != outcomeRejected {
		return false
	}
	switch source {
	case sourceLocal, sourceFresh, sourceStale, sourceTransient, sourceRejected:
	default:
		return false
	}
	return (outcome == outcomeRejected) == (source == sourceRejected)
}
