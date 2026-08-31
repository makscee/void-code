package auth

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// homeEnvVars are the two variables os.UserHomeDir chooses between: HOME on
// unix, USERPROFILE on Windows. Neither is authoritative everywhere, so for a
// test they are one thing with two names and have to move together.
var homeEnvVars = []string{"HOME", "USERPROFILE"}

// TestMain guards this package against writing into the developer's real home
// directory, in two ways that catch two different moments.
//
// This package is the one that writes ~/.void-code/token — the live session
// credential. A test here that resolves the home directory instead of isolating
// itself does not produce a wrong answer; it overwrites the developer's own
// login. cmd/vc has carried a leak guard since the day it caught five such
// tests, but a guard in cmd/vc does not watch this package, and five tests here
// were setting HOME without USERPROFILE. On Windows, where os.UserHomeDir reads
// USERPROFILE, every one of them wrote a token into the real profile — and
// nothing turned red, because nothing was looking.
//
// # First: the pair is set in one place, to one value (before any test runs)
//
// checkHomeSetterDiscipline reads this package's own test sources and requires
// that the two variables are only ever set together, by withTempHome, from the
// same expression.
//
// This is source inspection, which the cmd/vc guard deliberately refuses, and
// the reason for the difference is worth stating rather than glossing. A
// behavioural check cannot see this defect: setting HOME alone IS correct
// behaviour on unix and IS a leak on Windows, and the two are indistinguishable
// from inside a POSIX test run — nothing is written anywhere it should not be.
// Waiting for the filesystem to prove it therefore means waiting for a Windows
// run, which is exactly the wait that let five tests through. The rule here is
// about the source because the defect is only visible in the source.
//
// It is kept narrow on purpose — who may call Setenv with these two names,
// nothing else — so it cannot drift into a style checker. What it does not see:
// a Setenv whose variable name is computed rather than written as a literal,
// and a package-level variable that happens to be called os. Both are noted
// rather than defended against; the leak guard below is what covers whatever
// the reading misses.
//
// # Second: the leak itself (after every test has run)
//
// Both variables are redirected at a sandbox, so a test that resolves the home
// directory instead of isolating itself lands its writes there and the
// leftovers are the evidence. Redirect rather than snapshot-and-compare: a
// check that lets the write land in the real home and reports it afterwards has
// already caused the damage it describes.
//
// This half is platform-shaped and stays that way: a HOME-only test leaks into
// the sandbox on Windows, a USERPROFILE-only test leaks into it on POSIX. It
// fires on whichever platform would actually have been damaged, which is why it
// is not enough on its own and why the source rule runs first.
//
// The whole sandbox is watched, not a derived list of application roots the way
// cmd/vc does it. cmd/vc has to be selective because its tests shell out to go
// and npm, and those tools populate their own caches under HOME. Nothing in
// this package starts a child process, so anything at all appearing under the
// sandbox was put there by a test in this package — including ~/.claudev, which
// the legacy-credential test creates and which no application path function
// would name.
//
// Known limit, inherited: package-level variable initializers run before
// TestMain, so a home path captured at init would still be the real one.
// Nothing in this package does that today.
func TestMain(m *testing.M) {
	if problems, err := checkHomeSetterDiscipline("."); err != nil {
		fmt.Fprintf(os.Stderr, "НЕ СМОГ: сторож изоляции HOME не прочитал исходники пакета: %v\n", err)
		os.Exit(1)
	} else if len(problems) > 0 {
		fmt.Fprint(os.Stderr, formatSetterReport(problems))
		os.Exit(1)
	}

	sandbox, err := os.MkdirTemp("", "vc-auth-home-isolation-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "НЕ СМОГ: сторож изоляции HOME не создал песочницу: %v\n", err)
		os.Exit(1)
	}
	for _, name := range homeEnvVars {
		os.Setenv(name, sandbox)
	}

	code := m.Run()

	diverged := homeVariablesDiverged(sandbox)
	leaks := collectSandboxLeaks(sandbox)
	_ = os.RemoveAll(sandbox)

	if diverged != "" {
		fmt.Fprint(os.Stderr, diverged)
		if code == 0 {
			code = 1
		}
	}
	if len(leaks) > 0 {
		fmt.Fprint(os.Stderr, formatHomeLeakReport(leaks))
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// homeVariablesDiverged reports the two variables no longer agreeing once the
// run is over. t.Setenv restores what it changed, so this cannot see a
// per-test omission — that is the source rule's job. What it does see is an
// os.Setenv, which has no cleanup: a test that permanently repoints one of the
// pair leaves every test after it resolving a different home than the guard is
// watching, and the leak check downstream would then be looking in the wrong
// place and reporting nothing.
func homeVariablesDiverged(sandbox string) string {
	var wrong []string
	for _, name := range homeEnvVars {
		if got := os.Getenv(name); got != sandbox {
			wrong = append(wrong, fmt.Sprintf("      %s = %q", name, got))
		}
	}
	if len(wrong) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n--- FAIL: изоляция домашнего каталога\n")
	b.WriteString("    После прогона HOME и USERPROFILE больше не указывают на песочницу.\n")
	b.WriteString("    Значит кто-то звал os.Setenv вместо t.Setenv: у него нет отката, и\n")
	b.WriteString("    все последующие тесты резолвили не тот дом, за которым следит сторож —\n")
	b.WriteString("    проверка на утечки ниже смотрела не туда и потому молчит.\n\n")
	fmt.Fprintf(&b, "      песочница = %q\n", sandbox)
	b.WriteString(strings.Join(wrong, "\n"))
	b.WriteString("\n\n    Починка: в тестах только t.Setenv (через withTempHome).\n\n")
	return b.String()
}

// homeSetterProblem is one violation of the "one place, one value" rule, with
// the position it was read at so the message points at the line.
type homeSetterProblem struct {
	pos    string
	detail string
}

// checkHomeSetterDiscipline reads every _test.go in dir and returns the ways
// this package sets the home variables that are not "withTempHome sets both to
// the same thing".
//
// Three rules, in the order they are checked per call site:
//
//   - os.Setenv with these names belongs to TestMain and nowhere else — that is
//     the sandbox redirect above, and it is the only setter that must survive
//     the test that made it.
//   - every other Setenv of these names must sit inside withTempHome. A test
//     that sets the pair correctly by hand is still refused: three correct
//     hand-written copies in cmd/vc are what the fourth one got forgotten
//     against, so the rule is one place rather than one habit.
//   - inside withTempHome both names must be set, from the same expression —
//     "the pair points at one place", checked as source rather than hoped for.
func checkHomeSetterDiscipline(dir string) ([]homeSetterProblem, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("в %s не нашлось ни одного _test.go — проверять нечего, а значит и утверждать нечего", dir)
	}
	sort.Strings(files)

	fset := token.NewFileSet()
	var problems []homeSetterProblem
	helperValues := map[string]string{}
	helperSeen := false

	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if fn.Name.Name == "withTempHome" {
				helperSeen = true
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				receiver, name, value, pos, ok := homeSetenvCall(fset, n)
				if !ok {
					return true
				}
				switch {
				case receiver == "os":
					if fn.Name.Name != "TestMain" {
						problems = append(problems, homeSetterProblem{pos, fmt.Sprintf(
							"os.Setenv(%q) в %s — os.Setenv не откатывается после теста; в тестах только t.Setenv через withTempHome", name, fn.Name.Name)})
					}
				case fn.Name.Name != "withTempHome":
					problems = append(problems, homeSetterProblem{pos, fmt.Sprintf(
						"Setenv(%q) в %s, а не в withTempHome — переменную, названную здесь, здесь же и забудут", name, fn.Name.Name)})
				default:
					if previous, dup := helperValues[name]; dup {
						problems = append(problems, homeSetterProblem{pos, fmt.Sprintf(
							"withTempHome ставит %s дважды: %s и %s", name, previous, value)})
					}
					helperValues[name] = value
				}
				return true
			})
		}
	}

	if !helperSeen {
		problems = append(problems, homeSetterProblem{dir, "в пакете нет withTempHome — паре переменных негде быть вместе"})
		return problems, nil
	}
	var missing []string
	for _, name := range homeEnvVars {
		if _, ok := helperValues[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		problems = append(problems, homeSetterProblem{"withTempHome", fmt.Sprintf(
			"withTempHome не ставит %s — на платформе, где именно она авторитетна, тест пишет в настоящий профиль", strings.Join(missing, " и "))})
	} else if helperValues["HOME"] != helperValues["USERPROFILE"] {
		problems = append(problems, homeSetterProblem{"withTempHome", fmt.Sprintf(
			"withTempHome ставит HOME = %s, а USERPROFILE = %s — это два разных дома, и какой из них настоящий, решает платформа",
			helperValues["HOME"], helperValues["USERPROFILE"])})
	}
	return problems, nil
}

// homeSetenvCall recognises a call of the form <receiver>.Setenv("HOME"|"USERPROFILE", value)
// and returns the receiver name, the variable name, the value as written, and
// the source position.
func homeSetenvCall(fset *token.FileSet, n ast.Node) (receiver, name, value, pos string, ok bool) {
	call, isCall := n.(*ast.CallExpr)
	if !isCall || len(call.Args) != 2 {
		return "", "", "", "", false
	}
	sel, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel || sel.Sel.Name != "Setenv" {
		return "", "", "", "", false
	}
	ident, isIdent := sel.X.(*ast.Ident)
	if !isIdent {
		return "", "", "", "", false
	}
	lit, isLit := call.Args[0].(*ast.BasicLit)
	if !isLit || lit.Kind != token.STRING {
		return "", "", "", "", false
	}
	unquoted, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", "", "", "", false
	}
	if unquoted != "HOME" && unquoted != "USERPROFILE" {
		return "", "", "", "", false
	}
	return ident.Name, unquoted, exprText(fset, call.Args[1]), fset.Position(call.Pos()).String(), true
}

func exprText(fset *token.FileSet, expr ast.Expr) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, expr); err != nil {
		return "<не прочиталось>"
	}
	return b.String()
}

func formatSetterReport(problems []homeSetterProblem) string {
	var b strings.Builder
	b.WriteString("\n--- FAIL: изоляция домашнего каталога (тесты не запускались)\n")
	b.WriteString("    os.UserHomeDir читает HOME на unix и USERPROFILE на Windows. Тест,\n")
	b.WriteString("    подменивший одну из них, изолирован ровно на одной платформе, а на\n")
	b.WriteString("    другой пишет в настоящий ~/.void-code/token, поверх живого логина\n")
	b.WriteString("    разработчика. Прогон на маке этого не покажет: там HOME в одиночку\n")
	b.WriteString("    работает, и всё зелено до первого запуска на Windows.\n\n")
	for _, p := range problems {
		fmt.Fprintf(&b, "      %s\n        %s\n", p.pos, p.detail)
	}
	b.WriteString("\n    Починка: одна точка на пакет —\n")
	b.WriteString("      func withTempHome(t *testing.T) string {\n")
	b.WriteString("          home := t.TempDir()\n")
	b.WriteString("          t.Setenv(\"HOME\", home)\n")
	b.WriteString("          t.Setenv(\"USERPROFILE\", home)\n")
	b.WriteString("          return home\n")
	b.WriteString("      }\n")
	b.WriteString("    Своя правильная копия в тесте тоже не проходит: в cmd/vc их было три,\n")
	b.WriteString("    и забыли пару именно в четвёртой.\n\n")
	return b.String()
}

type homeLeak struct {
	rel     string
	dir     bool
	size    int64
	preview string
}

func collectSandboxLeaks(sandbox string) []homeLeak {
	var leaks []homeLeak
	_ = filepath.WalkDir(sandbox, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == sandbox {
			return nil
		}
		rel, relErr := filepath.Rel(sandbox, path)
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

func formatHomeLeakReport(leaks []homeLeak) string {
	var b strings.Builder
	b.WriteString("\n--- FAIL: изоляция домашнего каталога\n")
	b.WriteString("    Тест этого пакета записал состояние в домашний каталог.\n")
	b.WriteString("    HOME и USERPROFILE были подменены на песочницу, поэтому настоящий ~/\n")
	b.WriteString("    уцелел — но без подмены эти записи легли бы в него, поверх\n")
	b.WriteString("    ~/.void-code/token, то есть поверх живого логина разработчика.\n\n")
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
	b.WriteString("\n    Починка: тест должен заводить свой дом — withTempHome(t) в\n")
	b.WriteString("    tokenstore_test.go. Обе переменные, а не одна: os.UserHomeDir читает\n")
	b.WriteString("    HOME на unix и USERPROFILE на Windows, и та, что забыта, ведёт в\n")
	b.WriteString("    настоящий профиль.\n")
	b.WriteString("    Кто именно: go test ./internal/auth/ -count=1 -run '^ИмяТеста$' —\n")
	b.WriteString("    сторож отрабатывает и на одном тесте.\n\n")
	return b.String()
}
