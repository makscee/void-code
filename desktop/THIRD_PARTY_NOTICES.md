# Third-party notices

## @vscode/l10n 0.0.18

Source: https://github.com/microsoft/vscode-l10n/tree/v0.0.18
License: MIT

Copyright (c) Microsoft Corporation.

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

Void Code uses the package's browser `config({ contents })` and `t()` runtime with VC-owned flat JSON bundles. The full Microsoft/CEINTL VS Code Russian language pack is not bundled: Void Code does not embed the VS Code workbench or its NLS module-map loader, so that pack is structurally inapplicable. No Marketplace or language-pack network fetch is used.

## VC-owned localization bundles (not third-party content)

`src/renderer/l10n/en.json` and `src/renderer/l10n/ru.json` implement the Void Code localization bundle schema version 1 documented in `docs/localization.md`. Their source is first-party Void Code product copy and first-party Russian translation maintained in this repository. They contain no third-party language-pack content. They are first-party product assets, so no separate third-party license applies; this statement does not grant or invent rights in any external language pack.
