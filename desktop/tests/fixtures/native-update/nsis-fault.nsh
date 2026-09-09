; Replaced by tooling only for the immutable Windows fault package.
; customInstall runs after electron-builder's ordinary extraction and registry work.
!macro customInstall
  CreateDirectory "__WITNESS_DIR__"
  FileOpen $0 "__WITNESS_DIR__\partial-install.json" w
  FileWrite $0 '{"v":1,"fault":"post-extraction"}'
  FileClose $0
  Delete "$INSTDIR\resources\full-resource-marker.txt"
  SetErrorLevel 86
  Quit
!macroend
