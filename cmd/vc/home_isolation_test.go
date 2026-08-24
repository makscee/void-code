package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/makscee/void-code/internal/ccsettings"
	"github.com/makscee/void-code/internal/config"
)

// TestMain points HOME at a throwaway directory for the whole package run and
// fails the package if any test left VC-owned state behind in it.
//
// The invariant is about behaviour, not about source: a test that resolves the
// home directory instead of isolating itself writes into whatever HOME names, so
// pointing HOME at a sandbox turns "this test touches the real home" into "this
// file exists". No test file is inspected, so a violator added tomorrow is caught
// the same way as one added today.
//
// Redirect rather than snapshot-and-compare: the state at risk is ~/.void-code/token
// and ~/.claude/settings.json — the live session credential and the user's own
// Claude Code configuration. A check that lets the write land in the real home and
// reports it afterwards has already caused the damage it describes. Here the write
// lands in the sandbox, and its presence is the evidence.
//
// Scoped to VC-owned roots rather than the whole home: tests shell out to go and
// npm, and those tools populate their own caches under HOME (~/go/pkg/mod,
// ~/Library/Caches/go-build, ~/.npm). That is the toolchain's state, not this
// package's, and flagging it would make the guard fire on any machine where npm is
// on PATH. The roots are derived from the app's own path functions, so if
// ~/.void-code ever moves, the guard moves with it.
//
// Known limit: package-level variable initializers run before TestMain, so a home
// path captured at init would still be the real one. Nothing in this package does
// that today.
func TestMain(m *testing.M) {
	sandbox, err := os.MkdirTemp("", "vc-home-isolation-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "НЕ СМОГ: сторож изоляции HOME не создал песочницу: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("HOME", sandbox)
	os.Setenv("USERPROFILE", sandbox)

	code := m.Run()

	roots, err := vcOwnedHomeRoots(sandbox)
	if err != nil {
		fmt.Fprintf(os.Stderr, "НЕ СМОГ: сторож изоляции HOME не определил свои каталоги: %v\n", err)
		os.Exit(1)
	}
	leaks := collectHomeLeaks(sandbox, roots)
	_ = os.RemoveAll(sandbox)

	if len(leaks) > 0 {
		fmt.Fprint(os.Stderr, formatHomeLeakReport(roots, leaks))
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// vcOwnedHomeRoots returns the top-level directory names under home that this
// application writes into, asked of the application itself rather than hardcoded.
func vcOwnedHomeRoots(home string) ([]string, error) {
	cacheDir, err := config.CacheDir()
	if err != nil {
		return nil, fmt.Errorf("config.CacheDir: %w", err)
	}
	settings, err := ccsettings.SettingsPath()
	if err != nil {
		return nil, fmt.Errorf("ccsettings.SettingsPath: %w", err)
	}
	owned := []string{cacheDir, settings}
	// piAgentDir honours PI_CODING_AGENT_DIR; when that points outside home it is
	// not a home-derived door and topLevelUnder drops it.
	if dir := piAgentDir(); dir != "" {
		owned = append(owned, dir)
	}

	seen := map[string]bool{}
	var roots []string
	for _, path := range owned {
		root, ok := topLevelUnder(home, path)
		if !ok || seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("ни один путь приложения не ведёт в %s", home)
	}
	sort.Strings(roots)
	return roots, nil
}

// topLevelUnder reduces a path inside home to its first segment: the namespace
// the application claims in the home directory.
func topLevelUnder(home, path string) (string, bool) {
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if i := strings.IndexRune(rel, filepath.Separator); i >= 0 {
		rel = rel[:i]
	}
	return rel, rel != ""
}

type homeLeak struct {
	rel     string
	dir     bool
	size    int64
	preview string
}

func collectHomeLeaks(home string, roots []string) []homeLeak {
	var leaks []homeLeak
	for _, root := range roots {
		base := filepath.Join(home, root)
		_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, relErr := filepath.Rel(home, path)
			if relErr != nil {
				return nil
			}
			leak := homeLeak{rel: rel, dir: d.IsDir()}
			if info, statErr := d.Info(); statErr == nil && !d.IsDir() {
				leak.size = info.Size()
				leak.preview = previewFile(path)
			}
			leaks = append(leaks, leak)
			return nil
		})
	}
	sort.Slice(leaks, func(i, j int) bool { return leaks[i].rel < leaks[j].rel })
	return leaks
}

// previewFile returns a short printable excerpt; the content of a leaked file is
// usually what identifies the test that wrote it.
func previewFile(path string) string {
	const limit = 160
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	truncated := len(data) > limit
	if truncated {
		data = data[:limit]
	}
	var b strings.Builder
	for _, r := range string(data) {
		if r == '\n' || r == '\t' {
			b.WriteRune(' ')
			continue
		}
		if unicode.IsPrint(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('.')
	}
	out := strings.TrimSpace(b.String())
	if truncated {
		out += "…"
	}
	return out
}

func formatHomeLeakReport(roots []string, leaks []homeLeak) string {
	var b strings.Builder
	b.WriteString("\n--- FAIL: изоляция домашнего каталога\n")
	fmt.Fprintf(&b, "    Тест этого пакета записал состояние VC в домашний каталог.\n")
	fmt.Fprintf(&b, "    HOME был подменён на песочницу, поэтому настоящий ~/ уцелел — но без\n")
	fmt.Fprintf(&b, "    подмены эти записи легли бы в него, поверх ~/.void-code/token и\n")
	fmt.Fprintf(&b, "    ~/.claude/settings.json.\n\n")
	fmt.Fprintf(&b, "    Под наблюдением: %s\n\n", strings.Join(roots, ", "))
	for _, leak := range leaks {
		if leak.dir {
			fmt.Fprintf(&b, "      ~/%s/\n", leak.rel)
			continue
		}
		fmt.Fprintf(&b, "      ~/%s (%d Б)\n", leak.rel, leak.size)
		if leak.preview != "" {
			fmt.Fprintf(&b, "        %s\n", leak.preview)
		}
	}
	b.WriteString("\n    Починка: тест должен заводить свой дом — home := t.TempDir();\n")
	b.WriteString("    t.Setenv(\"HOME\", home); t.Setenv(\"USERPROFILE\", home). В этом пакете\n")
	b.WriteString("    уже есть withTempHome(t) (authcache_test.go).\n")
	b.WriteString("    Кто именно: go test ./cmd/vc/ -count=1 -run '^ИмяТеста$' — сторож\n")
	b.WriteString("    отрабатывает и на одном тесте.\n\n")
	return b.String()
}
