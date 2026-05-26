//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

func init() {
	// Set console output code page to UTF-8 so Cyrillic and other multibyte
	// characters render correctly in cmd.exe and PowerShell on Windows.
	// Equivalent to running `chcp 65001` before launching vc.
	_ = windows.SetConsoleCP(windows.CP_UTF8)
	_ = windows.SetConsoleOutputCP(windows.CP_UTF8)
}
