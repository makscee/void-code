package main

import _ "embed"

// Keep the installed single-file extension byte-identical to the authority exercised by Pi.
//
//go:embed pi_extension.ts
var piVoidCodexExtensionSource string
