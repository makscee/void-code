package installercontract

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestMain points HOME at a throwaway directory for the whole package run and
// fails the package if a test left installer-owned state behind in it.
//
// This package runs install.sh for real. The installer writes ~/.void-code, it
// appends to the user's shell rc file, and on macOS it hands the relay CA to
// `security add-trusted-cert` — so a test that forgets to redirect HOME does not
// produce a wrong assertion, it edits the machine of whoever ran `go test`.
// Redirect rather than snapshot-and-compare: a check that lets the write land in
// the real home and reports it afterwards has already caused the damage it
// describes. Here the write lands in the sandbox, and its presence is the
// evidence. Same shape as cmd/vc/home_isolation_test.go and
// internal/auth/home_isolation_test.go, for the same reason.
//
// The watched paths are read out of install.sh itself rather than hardcoded: if
// the installer opens a new door into $HOME, the guard follows it there without
// anyone remembering to update this file. Scoped to those paths and not to the
// whole sandbox on purpose — the tests also spawn pwsh, and its own caches under
// HOME are the toolchain's business, not this package's.
func TestMain(m *testing.M) {
	watched, err := homePathsWrittenByInstaller("install.sh")
	if err != nil {
		fmt.Fprintf(os.Stderr, "НЕ СМОГ: сторож изоляции HOME не разобрал install.sh: %v\n", err)
		os.Exit(1)
	}

	sandbox, err := os.MkdirTemp("", "installer-home-isolation-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "НЕ СМОГ: сторож изоляции HOME не создал песочницу: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("HOME", sandbox)
	os.Setenv("USERPROFILE", sandbox)

	code := m.Run()

	leaks := collectInstallerHomeLeaks(sandbox, watched)
	_ = os.RemoveAll(sandbox)

	if len(leaks) > 0 {
		fmt.Fprint(os.Stderr, formatInstallerHomeLeakReport(watched, leaks))
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

var (
	// "$HOME/.void-code", "$HOME/.config/fish", "$HOME/Library/Keychains/…"
	homeVarPathRe = regexp.MustCompile(`\$\{?HOME\}?/([A-Za-z0-9._/-]+)`)
	// detect_rc_file builds its paths the other way round:
	//   printf '%s/.zshrc' "$HOME"
	homePrintfPathRe = regexp.MustCompile(`'%s/([A-Za-z0-9._/-]+)'`)
)

// homePathsWrittenByInstaller returns the paths, relative to $HOME, that the
// shell installer names — asked of the installer rather than remembered here.
// Paths nested inside another are dropped: watching the outer one covers them.
func homePathsWrittenByInstaller(installer string) ([]string, error) {
	data, err := os.ReadFile(installer)
	if err != nil {
		return nil, err
	}
	content := string(data)

	seen := map[string]bool{}
	var found []string
	add := func(rel string) {
		rel = strings.TrimSuffix(rel, "/")
		if rel == "" || seen[rel] {
			return
		}
		seen[rel] = true
		found = append(found, rel)
	}
	for _, m := range homeVarPathRe.FindAllStringSubmatch(content, -1) {
		add(m[1])
	}
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, `"$HOME"`) {
			continue
		}
		for _, m := range homePrintfPathRe.FindAllStringSubmatch(line, -1) {
			add(m[1])
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("ни один путь под $HOME не найден в %s", installer)
	}

	sort.Slice(found, func(i, j int) bool {
		if len(found[i]) != len(found[j]) {
			return len(found[i]) < len(found[j])
		}
		return found[i] < found[j]
	})
	var watched []string
	for _, rel := range found {
		nested := false
		for _, kept := range watched {
			if rel == kept || strings.HasPrefix(rel, kept+"/") {
				nested = true
				break
			}
		}
		if !nested {
			watched = append(watched, rel)
		}
	}
	sort.Strings(watched)
	return watched, nil
}

type installerHomeLeak struct {
	rel  string
	dir  bool
	size int64
}

func collectInstallerHomeLeaks(home string, watched []string) []installerHomeLeak {
	var leaks []installerHomeLeak
	for _, rel := range watched {
		base := filepath.Join(home, rel)
		if _, err := os.Lstat(base); err != nil {
			continue
		}
		_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			leakRel, relErr := filepath.Rel(home, path)
			if relErr != nil {
				return nil
			}
			leak := installerHomeLeak{rel: leakRel, dir: d.IsDir()}
			if info, statErr := d.Info(); statErr == nil && !d.IsDir() {
				leak.size = info.Size()
			}
			leaks = append(leaks, leak)
			return nil
		})
	}
	sort.Slice(leaks, func(i, j int) bool { return leaks[i].rel < leaks[j].rel })
	return leaks
}

func formatInstallerHomeLeakReport(watched []string, leaks []installerHomeLeak) string {
	var b strings.Builder
	b.WriteString("\n--- FAIL: изоляция домашнего каталога\n")
	b.WriteString("    Тест этого пакета дал установщику записать в домашний каталог.\n")
	b.WriteString("    HOME был подменён на песочницу, поэтому настоящий ~/ уцелел — но без\n")
	b.WriteString("    подмены сюда легли бы ~/.void-code и правка rc-файла пользователя.\n\n")
	fmt.Fprintf(&b, "    Под наблюдением (взято из install.sh): %s\n\n", strings.Join(watched, ", "))
	for _, leak := range leaks {
		if leak.dir {
			fmt.Fprintf(&b, "      ~/%s/\n", leak.rel)
			continue
		}
		fmt.Fprintf(&b, "      ~/%s (%d Б)\n", leak.rel, leak.size)
	}
	b.WriteString("\n    Починка: тест обязан заводить свой дом — home := t.TempDir() и\n")
	b.WriteString("    HOME=home в окружении запускаемого установщика (см. runMirrorInstall\n")
	b.WriteString("    в installer_mirror_test.go: окружение там собрано с нуля).\n")
	b.WriteString("    Кто именно: go test . -count=1 -run '^ИмяТеста$' — сторож\n")
	b.WriteString("    отрабатывает и на одном тесте.\n\n")
	return b.String()
}
