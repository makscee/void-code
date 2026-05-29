package main

import (
	"strings"
	"testing"
)

func TestSubscriptionGate_Buckets(t *testing.T) {
	cases := []struct {
		name      string
		days      int
		wantBlock bool
		wantWarn  bool
	}{
		{"unlimited", -1, false, false},
		{"expired_zero", 0, true, false},
		{"warn_one", 1, false, true},
		{"warn_three", 3, false, true},
		{"healthy_four", 4, false, false},
		{"healthy_thirty", 30, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := subscriptionGate(c.days)
			if d.Block != c.wantBlock {
				t.Errorf("days=%d Block=%v, want %v", c.days, d.Block, c.wantBlock)
			}
			if d.Warn != c.wantWarn {
				t.Errorf("days=%d Warn=%v, want %v", c.days, d.Warn, c.wantWarn)
			}
		})
	}
}

func TestSubscriptionGate_ExpiredMessage(t *testing.T) {
	d := subscriptionGate(0)
	if !strings.Contains(d.Message, "@makscee") {
		t.Errorf("expired message %q must name @makscee", d.Message)
	}
	if !strings.Contains(strings.ToLower(d.Message), "expir") {
		t.Errorf("expired message %q must state expiry plainly", d.Message)
	}
	if !strings.Contains(strings.ToLower(d.Message), "soon") &&
		!strings.Contains(strings.ToLower(d.Message), "coming") {
		t.Errorf("expired message %q must signal automation is coming", d.Message)
	}
}

func TestSubscriptionGate_WarnMessageNamesDaysAndHandle(t *testing.T) {
	d := subscriptionGate(2)
	if !strings.Contains(d.Message, "@makscee") {
		t.Errorf("warn message %q must name @makscee", d.Message)
	}
	if !strings.Contains(d.Message, "2") {
		t.Errorf("warn message %q must state day count", d.Message)
	}
}

// Additional boundary tests.
func TestSubscriptionGate_BoundaryNegativeOne(t *testing.T) {
	d := subscriptionGate(-1)
	if d.Block || d.Warn {
		t.Errorf("days=-1 (unlimited) must be clean, got Block=%v Warn=%v", d.Block, d.Warn)
	}
}

func TestSubscriptionGate_BoundaryZeroBlock(t *testing.T) {
	d := subscriptionGate(0)
	if !d.Block {
		t.Error("days=0 must block")
	}
	if d.Warn {
		t.Error("days=0 must not warn (it blocks)")
	}
}

func TestSubscriptionGate_BoundaryOneWarn(t *testing.T) {
	d := subscriptionGate(1)
	if d.Block {
		t.Error("days=1 must not block")
	}
	if !d.Warn {
		t.Error("days=1 must warn")
	}
}

func TestSubscriptionGate_BoundaryThreeWarn(t *testing.T) {
	d := subscriptionGate(3)
	if d.Block {
		t.Error("days=3 must not block")
	}
	if !d.Warn {
		t.Error("days=3 must warn")
	}
}

func TestSubscriptionGate_BoundaryFourClean(t *testing.T) {
	d := subscriptionGate(4)
	if d.Block || d.Warn {
		t.Errorf("days=4 must be clean, got Block=%v Warn=%v", d.Block, d.Warn)
	}
}
