package version_test

import (
	"testing"

	"github.com/makscee/void-code/internal/version"
)

func TestVersionDefault(t *testing.T) {
	// When not injected by ldflags the sentinel "dev" must be returned.
	if version.Version == "" {
		t.Fatal("Version must not be empty")
	}
}
