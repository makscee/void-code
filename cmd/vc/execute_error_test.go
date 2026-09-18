package main

import (
	"errors"
	"testing"
)

// handleExecuteError is how every command's failure becomes the process's exit
// code, and until the exit went through a seam it could not be tested at all:
// the real os.Exit would have taken the test binary with it. So the mapping it
// performs — a command's own code, or 1 for anything else — has never been
// pinned, on a path every non-spawn invocation of vc ends on.
//
// The "exactly once" half is not pedantry. Under the seam exitProcess returns,
// which production's os.Exit never does, so a missing `return` lets a second
// call follow the first and overwrite the code a command chose with a 1. That
// is invisible in production and wrong everywhere the seam is used.

// exitCodeError is a command failure that names its own exit code, the shape
// cobra and exec both produce.
type exitCodeError struct {
	code int
	msg  string
}

func (e exitCodeError) Error() string { return e.msg }
func (e exitCodeError) ExitCode() int { return e.code }

// recordExits captures every exit the code under test asks for, in order.
func recordExits(t *testing.T) *[]int {
	t.Helper()
	codes := &[]int{}
	previous := exitProcess
	exitProcess = func(code int) { *codes = append(*codes, code) }
	t.Cleanup(func() { exitProcess = previous })
	return codes
}

func TestHandleExecuteErrorMapsFailuresToExitCodes(t *testing.T) {
	cases := map[string]struct {
		err  error
		want []int
	}{
		// A command that names its code owns it: 2 must arrive as 2, not as the
		// catch-all 1, and not as 2 followed by a 1 that overwrites it.
		"codeFromTheCommand":      {err: exitCodeError{code: 2, msg: "boom"}, want: []int{2}},
		"anotherCodeFromTheSame":  {err: exitCodeError{code: 127, msg: "not found"}, want: []int{127}},
		"zeroIsStillTheCommands":  {err: exitCodeError{code: 0, msg: "handled"}, want: []int{0}},
		"plainErrorBecomesOne":    {err: errors.New("cobra already printed this"), want: []int{1}},
		"negativeCodeIsNotACode":  {err: exitCodeError{code: -1, msg: "killed by signal"}, want: []int{1}},
		"nothingWrongEndsNothing": {err: nil, want: []int{}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			codes := recordExits(t)

			handleExecuteError(tc.err)

			if len(*codes) != len(tc.want) {
				t.Fatalf("exits = %v, want %v — the number of endings is part of the contract, not only their codes", *codes, tc.want)
			}
			for i := range tc.want {
				if (*codes)[i] != tc.want[i] {
					t.Fatalf("exits = %v, want %v", *codes, tc.want)
				}
			}
		})
	}
}

// Stated on its own because it is the one the seam itself created: the code a
// command chose must be the last word, with nothing following it.
func TestHandleExecuteErrorDoesNotOverwriteACommandsCodeWithOne(t *testing.T) {
	codes := recordExits(t)

	handleExecuteError(exitCodeError{code: 3, msg: "three"})

	if len(*codes) != 1 {
		t.Fatalf("exits = %v, want exactly one — a second ending overwrites the code the command asked for", *codes)
	}
	if (*codes)[0] != 3 {
		t.Fatalf("exit code = %d, want 3", (*codes)[0])
	}
}
