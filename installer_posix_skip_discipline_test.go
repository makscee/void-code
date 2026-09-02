package installercontract

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestEveryTestThatRunsTheShellInstallerSkipsOnWindows reads this package's own
// test sources and requires that every test which ends up running
// `sh install.sh` calls skipInstallShOnWindows(t).
//
// # Why source inspection and not behaviour
//
// This is the same shape as checkHomeSetterDiscipline in
// internal/auth/home_isolation_test.go, and for the same reason: a behavioural
// check cannot see this defect. A test that runs install.sh without the skip
// behaves correctly on POSIX — it runs, it passes, nothing is wrong anywhere a
// developer or the `test` job on ubuntu can look. The defect exists only on
// Windows, where `sh` is the MSYS one and `uname -s` answers `MINGW64_NT-…`,
// which install.sh's detect_os does not know. So waiting for behaviour to prove
// the omission means waiting for a Windows run — and that wait is exactly what
// happened: the skips were added to ten tests in this branch, PR #33 landed an
// eleventh test running the same installer in main, and the two only met on the
// Windows runner of the merged result (run 33519625816: seven subtests of
// TestShellInstallerVerifiesThePrimaryDownloadToo red, while the push run of the
// same branch, 33518892683, was green because the file was not in it). Nobody
// wrote a bug; the rule simply lived in a comment instead of in a check.
//
// # What it looks at
//
// Narrow on purpose, so it cannot drift into a style checker: who runs the POSIX
// installer, and nothing else.
//
//   - a call is a runner when it is exec.Command / exec.CommandContext and one
//     of its string literals is the installer's path — either as its own
//     argument (`exec.Command("sh", "install.sh")`) or as a word inside a script
//     literal (`exec.Command("sh", "-c", "… ./install.sh …")`);
//   - a test reaches the installer if it runs one itself or calls, by name,
//     something in this package that does — the chain is followed through
//     helpers such as runMirrorInstall and through the closures passed to t.Run;
//   - a test that reaches it must call skipInstallShOnWindows somewhere in its
//     own body. Not in a helper: the ten tests marked so far all say it by name
//     as their first statement, and that is what lets a reader of one test know
//     it does not run on Windows. A skip hidden in a shared helper also decides
//     for callers that do not run the installer at all.
//
// # What it does not see
//
// Stated rather than defended against, because a guard that hides its blind
// spots is worse than one that names them:
//
//   - a computed command or path — `exec.Command(shell, script)`, or the
//     installer's name built from a variable or a constant. Only string literals
//     written at the call site are read.
//   - an indirect call: a runner reached through a function value, a struct
//     field, an interface or a table of funcs. The graph follows calls written
//     as a plain identifier in this package.
//   - a runner outside this package's *_test.go files, and any way of starting a
//     process that is not exec.Command / exec.CommandContext (os/exec.Cmd built
//     by hand, testscript, a shell started by a fixture).
//   - the reverse direction: a literal that merely names install.sh as data
//     (`exec.Command("grep", "install.sh", …)`) is counted as running it. That
//     error goes toward red, which is the safe side here.
//
// A test that evades all of the above is possible; the check is worth having
// anyway, because the way this defect actually arrived was a plain
// exec.Command through a named helper.
func TestEveryTestThatRunsTheShellInstallerSkipsOnWindows(t *testing.T) {
	problems, err := checkShellInstallerSkipDiscipline(".")
	if err != nil {
		t.Fatalf("НЕ СМОГ: сторож пропуска под Windows не прочитал исходники пакета: %v", err)
	}
	if len(problems) > 0 {
		t.Fatal(formatShellInstallerSkipReport(problems))
	}
}

// shellInstallerSkipName is the helper every install.sh test must name.
const shellInstallerSkipName = "skipInstallShOnWindows"

// shellInstallerSkipProblem is one test that runs the POSIX installer without
// the skip, with the chain by which it gets there so the message can show it.
type shellInstallerSkipProblem struct {
	test   string
	pos    string
	chain  []string
	runner string // where the exec call sits
	detail string
}

// installerCaller is what the reading keeps about one function in the package's
// test sources.
type installerCaller struct {
	pos          string
	isTest       bool
	runsHere     string   // position of an exec call that runs install.sh, "" if none
	calls        []string // names called as plain identifiers, in source order
	skipsHere    bool     // calls the skip helper in its own control flow
	skipsInAFunc bool     // calls it only inside a nested closure
}

// checkShellInstallerSkipDiscipline reads every _test.go in dir and returns the
// tests that reach `sh install.sh` without calling skipInstallShOnWindows.
func checkShellInstallerSkipDiscipline(dir string) ([]shellInstallerSkipProblem, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("в %s не нашлось ни одного _test.go — проверять нечего, а значит и утверждать нечего", dir)
	}
	sort.Strings(files)

	fset := token.NewFileSet()
	funcs := map[string]*installerCaller{}
	helperSeen := false

	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv != nil {
				continue
			}
			if fn.Name.Name == shellInstallerSkipName {
				helperSeen = true
				continue
			}
			info := &installerCaller{
				pos:    fset.Position(fn.Pos()).String(),
				isTest: isGoTestFunc(fn),
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				if pos, runs := shellInstallerRunCall(fset, call); runs && info.runsHere == "" {
					info.runsHere = pos
				}
				if ident, isIdent := call.Fun.(*ast.Ident); isIdent {
					info.calls = append(info.calls, ident.Name)
				}
				return true
			})
			// The skip has to be in the test's own control flow. Descending into
			// a closure would accept a skip that only covers one t.Run, which is
			// the thing the message below tells such a test apart from.
			info.skipsHere = callsSkipHelper(fn.Body, false)
			info.skipsInAFunc = !info.skipsHere && callsSkipHelper(fn.Body, true)
			if _, clash := funcs[fn.Name.Name]; !clash {
				funcs[fn.Name.Name] = info
			}
		}
	}

	if !helperSeen {
		return nil, fmt.Errorf("в пакете нет %s — сторожу нечего требовать; помощник объявлен в installer_mirror_test.go", shellInstallerSkipName)
	}
	runners := 0
	for _, info := range funcs {
		if info.runsHere != "" {
			runners++
		}
	}
	if runners == 0 {
		return nil, fmt.Errorf("ни один тест пакета не запускает install.sh — так не бывает, значит сторож читает не то, что думает")
	}

	var names []string
	for name, info := range funcs {
		if info.isTest {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var problems []shellInstallerSkipProblem
	for _, name := range names {
		info := funcs[name]
		chain, runner, reaches := installerReachChain(funcs, name, map[string]bool{})
		if !reaches || info.skipsHere {
			continue
		}
		detail := fmt.Sprintf("не зовёт %s(t)", shellInstallerSkipName)
		if info.skipsInAFunc {
			detail = fmt.Sprintf("зовёт %s только внутри вложенной функции — это прикрывает один подтест, а установщик запускают и остальные", shellInstallerSkipName)
		}
		problems = append(problems, shellInstallerSkipProblem{
			test:   name,
			pos:    info.pos,
			chain:  chain,
			runner: runner,
			detail: detail,
		})
	}
	return problems, nil
}

// installerReachChain walks the package's call graph from name and returns the
// first path it finds to a function that runs install.sh.
func installerReachChain(funcs map[string]*installerCaller, name string, seen map[string]bool) (chain []string, runner string, ok bool) {
	info, known := funcs[name]
	if !known || seen[name] {
		return nil, "", false
	}
	seen[name] = true
	if info.runsHere != "" {
		return []string{name}, info.runsHere, true
	}
	for _, callee := range info.calls {
		if rest, at, found := installerReachChain(funcs, callee, seen); found {
			return append([]string{name}, rest...), at, true
		}
	}
	return nil, "", false
}

// callsSkipHelper reports whether body calls the skip helper. With insideClosure
// it looks only inside nested function literals; otherwise only outside them.
func callsSkipHelper(body *ast.BlockStmt, insideClosure bool) bool {
	found := false
	var walk func(n ast.Node, nested bool)
	walk = func(n ast.Node, nested bool) {
		if n == nil || found {
			return
		}
		ast.Inspect(n, func(node ast.Node) bool {
			if found {
				return false
			}
			if lit, isLit := node.(*ast.FuncLit); isLit {
				if lit.Body != nil {
					walk(lit.Body, true)
				}
				return false
			}
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			ident, isIdent := call.Fun.(*ast.Ident)
			if isIdent && ident.Name == shellInstallerSkipName && nested == insideClosure {
				found = true
				return false
			}
			return true
		})
	}
	walk(body, false)
	return found
}

// shellInstallerRunCall recognises exec.Command / exec.CommandContext that runs
// the POSIX installer, and returns the source position of the call.
func shellInstallerRunCall(fset *token.FileSet, call *ast.CallExpr) (pos string, ok bool) {
	sel, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel {
		return "", false
	}
	if sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext" {
		return "", false
	}
	pkg, isIdent := sel.X.(*ast.Ident)
	if !isIdent || pkg.Name != "exec" {
		return "", false
	}
	for _, arg := range call.Args {
		lit, isLit := arg.(*ast.BasicLit)
		if !isLit || lit.Kind != token.STRING {
			continue
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil {
			continue
		}
		// Split so a script passed to `sh -c` is read the same way a shell would
		// read it; a bare argument is a one-word script.
		for _, word := range strings.Fields(text) {
			word = strings.Trim(word, `"';`)
			if word == "install.sh" || strings.HasSuffix(word, "/install.sh") {
				return fset.Position(call.Pos()).String(), true
			}
		}
	}
	return "", false
}

// isGoTestFunc reports whether fn is a test the `go test` runner will call:
// TestXxx(t *testing.T). TestMain takes *testing.M and is not one.
func isGoTestFunc(fn *ast.FuncDecl) bool {
	if !strings.HasPrefix(fn.Name.Name, "Test") || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	star, isStar := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !isStar {
		return false
	}
	sel, isSel := star.X.(*ast.SelectorExpr)
	return isSel && sel.Sel.Name == "T"
}

func formatShellInstallerSkipReport(problems []shellInstallerSkipProblem) string {
	var b strings.Builder
	b.WriteString("\n--- FAIL: пропуск под Windows у тестов, запускающих install.sh\n")
	b.WriteString("    install.sh — POSIX-путь установки, и только он: Windows ставится через\n")
	b.WriteString("    install.ps1, туда её отправляет и страница загрузки, и release.yml. Тест,\n")
	b.WriteString("    который запускает install.sh, обязан звать skipInstallShOnWindows(t) —\n")
	b.WriteString("    иначе на маке и на ubuntu он зелен, а красен только на виндовом раннере,\n")
	b.WriteString("    и увидит это не автор, а CI на слитой ветке.\n\n")
	for _, p := range problems {
		fmt.Fprintf(&b, "      %s (%s)\n", p.test, p.pos)
		fmt.Fprintf(&b, "        запускает установщик: %s → exec.Command в %s\n", strings.Join(p.chain, " → "), p.runner)
		fmt.Fprintf(&b, "        %s\n", p.detail)
	}
	b.WriteString("\n    Починка: первой строкой тела теста —\n")
	b.WriteString("      skipInstallShOnWindows(t)\n")
	b.WriteString("    Помощник объявлен в installer_mirror_test.go, там же написано, почему\n")
	b.WriteString("    виндового поведения у этих тестов нет вовсе.\n")
	b.WriteString("    Пропуск нужен именно в теле теста, а не в общем помощнике: читатель\n")
	b.WriteString("    одного теста должен видеть, что тот не идёт под Windows.\n\n")
	return b.String()
}
