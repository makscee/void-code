package main

import "testing"

func TestLoginExposesOnlyDeviceAuthorization(t *testing.T) {
	if loginCmd.Flags().Lookup("email") != nil || loginCmd.Flags().Lookup("code") != nil || loginCmd.Flags().Lookup("device") != nil {
		t.Fatal("legacy login flags must not be exposed")
	}
	if loginCmd.Short == "" {
		t.Fatal("login help is empty")
	}
}
