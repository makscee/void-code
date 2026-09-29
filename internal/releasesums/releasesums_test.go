package releasesums

import "testing"

func TestLookup(t *testing.T) {
	const a = "ec0a4d76f0285184dc4b8ec93b1767105b71e57aa6deeabc02adbaa801be4ac8"
	const b = "92A702DB50DF4C17BBB169E315B29A31BFB28130CD8A5141730BDCC645654517"
	sums := []byte(a + "  vc-darwin-amd64\n" + b + " *vc-darwin-arm64\nshort  vc-linux-amd64\n")
	if got, ok := Lookup(sums, "vc-darwin-amd64"); !ok || got != a {
		t.Errorf("vc-darwin-amd64 = %q, %v", got, ok)
	}
	if got, ok := Lookup(sums, "vc-darwin-arm64"); !ok || got != "92a702db50df4c17bbb169e315b29a31bfb28130cd8a5141730bdcc645654517" {
		t.Errorf("starred, upper-case entry = %q, %v", got, ok)
	}
	if _, ok := Lookup(sums, "vc-linux-amd64"); ok {
		t.Error("an entry without a 64-digit hash must not match")
	}
	if _, ok := Lookup(sums, "vc-darwin"); ok {
		t.Error("a name must match exactly")
	}
}
