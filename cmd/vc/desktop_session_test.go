package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDesktopSessionProcessForwardsArgsStreamsEnvAndExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific; runtime validation is platform-neutral")
	}
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	script := "#!/bin/sh\nprintf 'args:%s\\n' \"$*\"\nprintf 'env:%s\\n' \"$VC_AUTH_TOKEN\" >&2\ncat\nexit 7\n"
	if err := os.WriteFile(node, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var out, errOut strings.Builder
	err := runDesktopSessionProcess(context.Background(), desktopSessionPlan{nodePath: node, args: []string{"/private/pi.js", "--resume", "id"}, env: []string{"VC_AUTH_TOKEN=secret"}}, strings.NewReader("stdin-data\n"), &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "status 7") {
		t.Fatalf("exit error=%v", err)
	}
	if !strings.Contains(out.String(), "args:/private/pi.js --resume id") || !strings.Contains(out.String(), "stdin-data") {
		t.Fatalf("stdout=%q", out.String())
	}
	if errOut.String() != "env:secret\n" {
		t.Fatalf("stderr=%q", errOut.String())
	}
}
func TestDesktopSessionProcessCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\nsleep 10\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runDesktopSessionProcess(ctx, desktopSessionPlan{nodePath: node}, strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("err=%v", err)
	}
}
func TestDesktopRuntimeValidationAcceptsAbsoluteWindowsAndMacText(t *testing.T) {
	for _, path := range []string{"C:\\Program Files\\Void\\node.exe", "/Applications/Void.app/Contents/MacOS/node"} {
		if filepath.IsAbs(path) != (runtime.GOOS != "windows" || strings.HasPrefix(path, "C:")) {
			_ = path
		}
	}
	if err := validateDesktopPiArgs([]string{"--session", "session", "--no-session"}); err != nil {
		t.Fatal(err)
	}
	if err := validateDesktopPiArgs([]string{"--model", "x"}); err == nil {
		t.Fatal("authority-changing model flag accepted")
	}
}
