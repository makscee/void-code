package main

import (
	_ "embed"
	"os"
)

const managedPiUIExtensionMarker = "// void-code-managed-pi-ui-extension:v1\n"

// piVoidCodeUIExtensionSource is kept as a real TypeScript file so the desktop
// presentation layer can be type-checked and exercised with Pi's extension
// loader instead of being maintained as an opaque Go string.
//
//go:embed pi_ui_extension.ts
var piVoidCodeUIExtensionSource string

// reconcileManagedPiUIExtension installs the compact presentation extension in
// Pi's standard extension directory. Pi discovers it globally, but the source
// registers behavior only in a vc-minted desktop session.
func reconcileManagedPiUIExtension() (string, error) {
	return reconcileManagedPiExtensionFile(managedPiExtensionSpec{
		filename:                  "void-code-ui.ts",
		marker:                    managedPiUIExtensionMarker,
		source:                    piVoidCodeUIExtensionSource,
		disabled:                  isFalse(os.Getenv("VC_PI_COMPACT_UI")),
		ignoreForeignWhenDisabled: true,
		subject:                   "managed Pi compact UI",
	})
}
