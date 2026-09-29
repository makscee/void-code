package main

import (
	"embed"
)

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
