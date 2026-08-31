package installercontract

// The suite the workflows run must not need npm.
//
// `go test ./...` in this repository performs a real package installation:
// TestManagedWebSearchInstallOwnershipAndSetting reaches
// installManagedWebSearchDependencies, and in an untagged build that function
// shells out to `npm ci`. So the push gate and both release jobs depend on the
// npm registry answering, for a test whose four assertions -- ownership, the
// settings key, the foreign-path guard -- never read a dependency.
//
// The seam that removes the dependency already exists
// (cmd/vc/pi_web_search_install_vctest.go, build tag vctestfixture), and nothing
// selects it: the tag is chosen when the binary is built, so no assertion
// compiled into that binary can tell which half of the seam it got. That is why
// this test is a subprocess. It takes the `go test` invocation each workflow
// runs and runs the managed web-search test through that invocation with npm
// unreachable.
//
// What is asserted is that the command completes, not that the file contains a
// flag. The workflow text is read only to obtain the command; a run that
// installs the package with no npm on PATH proves the property directly, and a
// run that spells the tag wrong fails here the same way a run that omits it does.
//
// The child is given no VC_TEST_MANAGED_WEB_NODE_MODULES. The fixture has to
// come from the test itself, so that `go test -tags vctestfixture ./...` typed
// by hand behaves the same as CI; a fixture handed in through workflow env would
// leave the suite green in CI and red on a desk.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// realEnvironment is the process environment as it was before TestMain
// redirected HOME at a sandbox. Package-level initializers run first, which is
// what makes this the real one: the child needs it so the Go build and module
// caches it finds are the warm ones, not empty directories inside the sandbox.
// The child redirects HOME for itself, so nothing lands in the real home either
// way.
var realEnvironment = os.Environ()

const managedWebSearchTest = "TestManagedWebSearchInstallOwnershipAndSetting"

const fixtureEnvironmentVariable = "VC_TEST_MANAGED_WEB_NODE_MODULES"

// suiteWorkflows are the three files that run the whole suite. They are named
// rather than discovered, because "runs go test" and "must not need npm" are not
// the same set: a deliberate job that exercises the real `npm ci` on a schedule
// belongs in a workflow of its own, and discovery would fail it for doing its
// job. A new workflow that runs the suite is added here by hand; one of these
// three losing its `go test` fails below rather than passing quietly.
var suiteWorkflows = []string{
	filepath.Join(".github", "workflows", "test.yml"),
	filepath.Join(".github", "workflows", "release.yml"),
	filepath.Join(".github", "workflows", "canary-release.yml"),
}

type goTestInvocation struct {
	command string
	sources []string
}

func TestWorkflowSuiteInstallsManagedWebSearchWithoutNpm(t *testing.T) {
	goBinary, offlinePath := toolchainPathWithoutNpm(t)
	for _, invocation := range goTestInvocations(t) {
		t.Run(invocation.command, func(t *testing.T) {
			arguments, ok := invocation.arguments()
			if !ok {
				t.Skipf("НЕ СМОГ: %s is a shell expression, not a plain command, so it cannot be replayed here: %s",
					strings.Join(invocation.sources, ", "), invocation.command)
			}
			deadline, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()
			command := exec.CommandContext(deadline, goBinary, arguments...)
			command.Dir = "."
			command.Env = childEnvironment(offlinePath)
			output, err := command.CombinedOutput()
			if err == nil {
				return
			}
			t.Errorf(`the suite invocation from %s needs npm.

    run:   %s %s
    PATH:  %s
    error: %v

%s
The workflows run the untagged build, which compiles
cmd/vc/pi_web_search_install_prod.go and shells out to `+"`npm ci`"+`. With npm
absent it cannot finish, and with npm present it depends on the registry --
which is the dependency this asserts is gone. The seam that removes it is
cmd/vc/pi_web_search_install_vctest.go: run the suite as
`+"`go test -tags vctestfixture ./...`"+` in %s, and keep .rails/green.lock
saying the same thing.`,
				strings.Join(invocation.sources, ", "),
				goBinary, strings.Join(arguments, " "),
				offlinePath,
				err,
				indent(string(output)),
				strings.Join(invocation.sources, ", "))
		})
	}
}

// arguments turns the workflow's command line into the argument list for one
// package and one test: the flags are the workflow's, the target is ours.
// -count=1 is added because a cached result would report on an earlier PATH.
func (invocation goTestInvocation) arguments() ([]string, bool) {
	if strings.ContainsAny(invocation.command, "|&;<>$`()") {
		return nil, false
	}
	fields := strings.Fields(invocation.command)
	if len(fields) < 2 || fields[0] != "go" || fields[1] != "test" {
		return nil, false
	}
	arguments := []string{"test"}
	for _, field := range fields[2:] {
		if strings.HasPrefix(field, "./") || strings.Contains(field, "...") || strings.HasPrefix(field, "github.com/") {
			continue
		}
		arguments = append(arguments, field)
	}
	arguments = append(arguments, "-count=1", "-run", "^"+managedWebSearchTest+"$", "./cmd/vc")
	return arguments, true
}

// goTestInvocations reads every `go test` command the suite workflows run, one
// entry per distinct command line: fixing test.yml alone has to stay visible.
func goTestInvocations(t *testing.T) []goTestInvocation {
	t.Helper()
	var invocations []goTestInvocation
	position := map[string]int{}
	for _, workflow := range suiteWorkflows {
		data, err := os.ReadFile(workflow)
		if err != nil {
			t.Fatalf("НЕ СМОГ: не прочитан воркфлоу %s: %v", workflow, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			at := strings.Index(trimmed, "go test")
			if at < 0 || (at > 0 && !strings.ContainsAny(trimmed[at-1:at], " \t:")) {
				continue
			}
			command := strings.TrimSpace(trimmed[at:])
			if index, seen := position[command]; seen {
				invocations[index].sources = appendUnique(invocations[index].sources, workflow)
				continue
			}
			position[command] = len(invocations)
			invocations = append(invocations, goTestInvocation{command: command, sources: []string{workflow}})
		}
	}
	if len(invocations) == 0 {
		t.Fatalf("НЕ СМОГ: ни в одном из %s не найдено `go test` -- воркфлоу перестроены, и эта проверка перестала смотреть на то, что запускает CI",
			strings.Join(suiteWorkflows, ", "))
	}
	return invocations
}

// toolchainPathWithoutNpm returns the go binary and a PATH that reaches the
// toolchain and nothing else. Directories are used as they are rather than
// populated with links, so the absence of npm is a fact about this machine that
// is checked, not a property of a directory we just created.
func toolchainPathWithoutNpm(t *testing.T) (string, string) {
	t.Helper()
	goBinary, err := exec.LookPath("go")
	if err != nil {
		if root := os.Getenv("GOROOT"); root != "" {
			candidate := filepath.Join(root, "bin", "go")
			if _, statErr := os.Stat(candidate); statErr == nil {
				goBinary, err = candidate, nil
			}
		}
	}
	if err != nil {
		t.Skipf("НЕ СМОГ: не нашёл go: %v", err)
	}
	directories := []string{filepath.Dir(goBinary)}
	if gitBinary, gitErr := exec.LookPath("git"); gitErr == nil {
		directories = appendUnique(directories, filepath.Dir(gitBinary))
	}
	for _, tool := range []string{"npm", "npm.cmd", "npm.exe", "npx", "npx.cmd", "npx.exe"} {
		for _, directory := range directories {
			if _, statErr := os.Stat(filepath.Join(directory, tool)); statErr == nil {
				t.Skipf("НЕ СМОГ: %s лежит в %s рядом с тулчейном -- на этой машине npm не спрятать, не спрятав go", tool, directory)
			}
		}
	}
	return goBinary, strings.Join(directories, string(os.PathListSeparator))
}

// childEnvironment is the real environment with PATH replaced and the fixture
// variable removed.
func childEnvironment(path string) []string {
	environment := make([]string, 0, len(realEnvironment)+1)
	for _, entry := range realEnvironment {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if strings.EqualFold(name, "PATH") || name == fixtureEnvironmentVariable {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "PATH="+path)
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func indent(text string) string {
	var builder strings.Builder
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		fmt.Fprintf(&builder, "    %s\n", line)
	}
	return builder.String()
}
