//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

// cpUTF8 is the Windows code page ID for UTF-8 (chcp 65001).
// windows.CP_UTF8 was added in golang.org/x/sys v0.37+; use the literal for
// broader compatibility.
const cpUTF8 = 65001

func init() {
	// Set console output code page to UTF-8 so Cyrillic and other multibyte
	// characters render correctly in cmd.exe and PowerShell on Windows.
	// Equivalent to running `chcp 65001` before launching vc.
	_ = windows.SetConsoleCP(cpUTF8)
	_ = windows.SetConsoleOutputCP(cpUTF8)
}
