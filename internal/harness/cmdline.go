package harness

import "strings"

// isBatchShim reports whether the resolved binary is a Windows batch shim
// (.cmd / .bat, case-insensitive).  npm installs claude as such a shim
// (e.g. C:\Users\Darya Shmeleva\AppData\Roaming\npm\claude.cmd), and a script
// is not a PE image — CreateProcess cannot launch it directly via
// lpApplicationName.  It must go through the command processor instead.  A real
// .exe (or extension-less PE) returns false and takes the direct-exec path.
func isBatchShim(bin string) bool {
	lower := strings.ToLower(bin)
	return strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat")
}

// winCmdExeCmdLine builds the command line passed to cmd.exe (the comspec) to
// run a batch shim.  It is a pure string builder so the quoting contract can be
// exercised in unit tests on any OS without a Windows runtime; the Windows
// build (cmdline_windows.go) wires it into SysProcAttr.CmdLine while pointing
// cmd.Path at a real PE (cmd.exe).
//
// cmd.exe quoting is its own dialect, distinct from CreateProcess argv quoting.
// With `/s /c`, cmd.exe strips exactly one leading and one trailing quote from
// the remainder and runs the rest verbatim.  So we:
//   - quote the shim path and every arg individually (cmdExeQuote), and
//   - wrap the entire post-`/c` payload in an OUTER pair of quotes.
//
// The result for a spaced shim path looks like:
//
//	C:\Windows\System32\cmd.exe /s /c ""C:\Users\Darya Shmeleva\...\claude.cmd" "arg one" "arg two""
//
// cmd.exe peels the outer "" pair and is left with the inner-quoted tokens, so
// the spaced path survives as one argument.
func winCmdExeCmdLine(comspec, shimPath string, args []string) string {
	var b strings.Builder
	b.WriteString(cmdExeQuote(comspec)) // comspec itself may sit under a spaced path
	b.WriteString(" /s /c ")
	b.WriteByte('"') // open outer quote
	b.WriteString(cmdExeQuote(shimPath))
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(cmdExeQuote(a))
	}
	b.WriteByte('"') // close outer quote
	return b.String()
}

// cmdExeQuote wraps a single token in double quotes for cmd.exe and escapes
// embedded special characters.  Realistic inputs are filesystem paths and
// claude flags (e.g. --settings <path>), but we defend against embedded quotes
// and cmd.exe metacharacters so a hostile or unusual path cannot break out of
// its token and inject a command.
//
// Inside a double-quoted cmd.exe token, metacharacters (& | < > ^ ( ) etc.)
// lose their meaning, so the only character we must neutralise is the double
// quote that would otherwise close the token early.  cmd.exe has no clean
// in-quote quote escape, so we escape an embedded " with the caret (^")
// outside the run — but since we always wrap in quotes here, we instead double
// any embedded quote, which is the convention cmd.exe accepts inside a quoted
// string for the program being launched.
func cmdExeQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r == '"' {
			b.WriteByte('"') // double an embedded quote
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
