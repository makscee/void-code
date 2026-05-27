package main

import (
	"testing"
)

// TestLoginCodeFlag verifies that the --code flag is registered on loginCmd
// and accepts a value in AAAA-BBBB format without flag-parse errors.
func TestLoginCodeFlag(t *testing.T) {
	// Reset flag state between calls (cobra flag sets are stateful).
	loginCodeFlag = ""

	if err := loginCmd.Flags().Set("code", "ABCD-EFG2"); err != nil {
		t.Fatalf("setting --code flag: %v", err)
	}
	if loginCodeFlag != "ABCD-EFG2" {
		t.Errorf("loginCodeFlag = %q, want ABCD-EFG2", loginCodeFlag)
	}
}

// TestLoginCodeFlagEmpty verifies that loginCodeFlag defaults to empty string.
func TestLoginCodeFlagDefault(t *testing.T) {
	// Reset to default.
	loginCodeFlag = ""
	got := loginCmd.Flags().Lookup("code")
	if got == nil {
		t.Fatal("--code flag not registered on loginCmd")
	}
	if got.DefValue != "" {
		t.Errorf("--code default = %q, want empty string", got.DefValue)
	}
}

// TestPickerCodeExchConst verifies the picker constant for the operator-code
// option is distinct from the other picker choices.
func TestPickerCodeExchConst(t *testing.T) {
	if pickerCodeExch == pickerNone {
		t.Error("pickerCodeExch must not equal pickerNone")
	}
	if pickerCodeExch == pickerEmail {
		t.Error("pickerCodeExch must not equal pickerEmail")
	}
	if pickerCodeExch == pickerDevice {
		t.Error("pickerCodeExch must not equal pickerDevice")
	}
}

// TestNewPickerModelHasThreeChoices verifies the picker presents all three login methods.
func TestNewPickerModelHasThreeChoices(t *testing.T) {
	m := newPickerModel()
	if len(m.choices) != 3 {
		t.Errorf("picker choices = %d, want 3", len(m.choices))
	}
}
