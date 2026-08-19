# VC-owned localization bundles

Schema version: **1**

Source files: `src/renderer/l10n/en.json` and `src/renderer/l10n/ru.json`. Build output copies these exact bundled assets into `dist/renderer/l10n/`.

Each bundle is one JSON object whose keys are the canonical English message strings used by VC-owned surfaces and whose values are localized strings. Both bundles must have the same nonempty string keys, every value must be a nonempty string, and numbered placeholders such as `{0}` must be preserved. English values are the first-party source copy; Russian values are first-party Void Code translations. Unsupported locales are not loaded.

The bundles contain no Microsoft/CEINTL VS Code language-pack content or other third-party language-pack content. They are first-party Void Code product assets and have no separate third-party license. `@vscode/l10n` is third-party runtime code under MIT; its exact version, source, and license are recorded in `THIRD_PARTY_NOTICES.md`. No statement here grants or assumes rights in an external language pack.

Bundles are installer-local resources. Locale selection never changes update/auth/path/hash logic and does not fetch localization content from a network.
