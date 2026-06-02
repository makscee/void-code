package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunHook_DenyRmRfRoot(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /"},"cwd":"/x"}`)
	var out bytes.Buffer
	_ = runHook(in, &out)
	if !strings.Contains(out.String(), `"permissionDecision":"deny"`) {
		t.Fatalf("rm -rf / => %q, want deny", out.String())
	}
}

func TestRunHook_AllowGitStatus(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"git status"},"cwd":"/x"}`)
	var out bytes.Buffer
	_ = runHook(in, &out)
	if !strings.Contains(out.String(), `"permissionDecision":"allow"`) {
		t.Fatalf("git status => %q, want allow", out.String())
	}
}

func TestRunHook_MalformedInput_FailsOpen(t *testing.T) {
	in := strings.NewReader(`{not json`)
	var out bytes.Buffer
	_ = runHook(in, &out)
	// Malformed input must fail-open (allow), not block the user.
	if !strings.Contains(out.String(), `"permissionDecision":"allow"`) {
		t.Fatalf("malformed input => %q, want allow (fail-open)", out.String())
	}
}

func TestRunHook_OutputSchema(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Read","tool_input":{"file_path":"/tmp/x"},"cwd":"/tmp"}`)
	var out bytes.Buffer
	_ = runHook(in, &out)

	var parsed map[string]any
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v — got: %q", err, out.String())
	}
	hso, ok := parsed["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("hookSpecificOutput missing or wrong type: %v", parsed)
	}
	if hso["hookEventName"] != "PreToolUse" {
		t.Fatalf("hookEventName = %v, want PreToolUse", hso["hookEventName"])
	}
	if hso["permissionDecision"] == "" {
		t.Fatal("permissionDecision is empty")
	}
}

func TestRunHook_DenySudoCommand(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"sudo apt-get install vim"},"cwd":"/x"}`)
	var out bytes.Buffer
	_ = runHook(in, &out)
	if !strings.Contains(out.String(), `"permissionDecision":"deny"`) {
		t.Fatalf("sudo => %q, want deny", out.String())
	}
}

func TestRunHook_AllowGoTest(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"go test ./..."},"cwd":"/proj"}`)
	var out bytes.Buffer
	_ = runHook(in, &out)
	if !strings.Contains(out.String(), `"permissionDecision":"allow"`) {
		t.Fatalf("go test => %q, want allow", out.String())
	}
}

func TestRunHook_AlwaysAllow_NoClassifier(t *testing.T) {
	// Commands the OLD classifier/rules would have DENIED must now ALLOW.
	for _, cmd := range []string{
		`{"tool_name":"Bash","tool_input":{"command":"rm -rf /"},"cwd":"/x"}`,
		`{"tool_name":"Bash","tool_input":{"command":"sudo apt-get install vim"},"cwd":"/x"}`,
	} {
		var out bytes.Buffer
		if err := runHook(strings.NewReader(cmd), &out); err != nil {
			t.Fatalf("runHook err: %v", err)
		}
		if !strings.Contains(out.String(), `"permissionDecision":"allow"`) {
			t.Fatalf("%s => %q, want allow", cmd, out.String())
		}
	}
}
