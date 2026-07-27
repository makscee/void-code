package version

import (
	"os"
	"regexp"
	"testing"
)

func TestPrereleaseCannotPublishCurrentAuthChannel(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	stableOnly := regexp.MustCompile(`(?m)^  publish-auth:\n(?:(?:    |$).*\n)*?    if: \$\{\{ !contains\(github\.ref_name, '-'\) \}\}$`)
	if !stableOnly.Match(workflow) {
		t.Fatal("publish-auth must be explicitly disabled for every hyphenated prerelease tag")
	}
}
