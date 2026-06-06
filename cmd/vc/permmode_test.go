package main

import (
	"reflect"
	"testing"
)

func TestEnsureBypassPermissionMode(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "empty args get the flag prepended",
			in:   nil,
			want: []string{"--permission-mode", "bypassPermissions"},
		},
		{
			name: "ordinary passthrough args keep position after the injected flag",
			in:   []string{"--debug", "--verbose"},
			want: []string{"--permission-mode", "bypassPermissions", "--debug", "--verbose"},
		},
		{
			name: "explicit --permission-mode (space form) suppresses injection",
			in:   []string{"--permission-mode", "plan"},
			want: []string{"--permission-mode", "plan"},
		},
		{
			name: "explicit --permission-mode=value (equals form) suppresses injection",
			in:   []string{"--permission-mode=acceptEdits"},
			want: []string{"--permission-mode=acceptEdits"},
		},
		{
			name: "explicit --dangerously-skip-permissions suppresses injection",
			in:   []string{"--dangerously-skip-permissions"},
			want: []string{"--dangerously-skip-permissions"},
		},
		{
			name: "permission flag deeper in the list is still detected",
			in:   []string{"--print", "--permission-mode", "default"},
			want: []string{"--print", "--permission-mode", "default"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureBypassPermissionMode(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ensureBypassPermissionMode(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// The injected flag must not alias the package-level slice — a caller mutating
// the result must never corrupt bypassPermissionModeArgs for the next call.
func TestEnsureBypassPermissionMode_NoSharedBacking(t *testing.T) {
	got := ensureBypassPermissionMode([]string{"x"})
	got[0] = "MUTATED"
	if bypassPermissionModeArgs[0] != "--permission-mode" {
		t.Fatalf("package slice was mutated: %v", bypassPermissionModeArgs)
	}
}
