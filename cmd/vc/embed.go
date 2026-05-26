package main

import _ "embed"

// relayCA is the relay certificate authority bundle embedded at build time.
// The file lives at cmd/vc/embed/relay-ca.pem — populated by VCD-4 from
// workspace/void-auth/public/vc/relay-ca.pem.  This placeholder keeps the
// embed directive compilable until VCD-4 wires the real CA.
//
//go:embed embed/relay-ca.pem
var relayCA []byte
