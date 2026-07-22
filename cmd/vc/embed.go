package main

import (
	"embed"
)

// relayCA is the relay certificate authority bundle embedded at build time.
// The file lives at cmd/vc/embed/relay-ca.pem — populated by VCD-4 from
// workspace/void-auth/public/vc/relay-ca.pem.  This placeholder keeps the
// embed directive compilable until VCD-4 wires the real CA.
//
//go:embed embed/relay-ca.pem
var relayCA []byte

// piWebAccessFork is an immutable, audited vendoring of pi-web-access 0.13.0.
// Its package lock pins runtime dependencies; vc copies it into Pi's supported
// local-package surface rather than writing search credentials or config.
//
//go:embed embed/pi-web-access-0.13.0
var piWebAccessFork embed.FS

//go:embed embed/pi-web-access-0.13.0/openai-search.ts
var piWebAccessOpenAISource string

//go:embed embed/pi-web-access-0.13.0/gemini-search.ts
var piWebAccessRoutingSource string
