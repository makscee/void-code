package pibin

import "testing"

func TestMissingMessage(t *testing.T) {
	const want = "pi CLI not found — install Pi before starting VC\n" +
		"Install Pi coding agent:\n\n" +
		"  npm install -g @earendil-works/pi-coding-agent\n\n" +
		"Then restart your terminal if `pi` is still not on PATH."

	if got := MissingMessage(); got != want {
		t.Fatalf("MissingMessage() = %q, want %q", got, want)
	}
}
