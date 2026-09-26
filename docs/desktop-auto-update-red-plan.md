# Desktop auto-update: RED-план A (R1–R5)

## Статус и gate

Этот документ не утверждает работоспособность updater, helper, NSIS, macOS replacement или native install.

**Контракт подтверждён пользователем 09.09.2026: «Давай да делаем».** Следующий этап: независимый test-only RED-коммит A; затем независимая реализация B без изменения тестов A. Реализатор не пишет crypto/type fixtures и не заменяет test-only ключи.

## Минимальный scope

- **R1:** строгая проверка signed envelope настоящей Ed25519-криптографией.
- **R2:** строгий manifest, semver, installed architecture и выбор артефакта.
- **R3:** anti-replay floor: persist-before-available, equivocation, corrupt/unwritable store.
- **R4:** consent FSM: check/download/quit не являются install.
- **R5:** single-flight, generation ownership, cancellation, timeout и stale completion.
- Вне slice: S0 и R6–R12 — HTTP/redirects, staging/archive, helper, IPC/UI, publisher, packaged upgrade/recovery, offline/performance.

## Proposed callable boundaries (не реализация)

- `verifyDesktopUpdateEnvelope(raw: Uint8Array, keyring): VerifiedPayload | VerifyError`
- `parseAndSelectDesktopRelease(payloadBytes, installed: InstalledBuild): UpdatePlan | SelectionResult`
- `createReplayFloorStore(dependencies): ReplayFloorStore`
- `createDesktopUpdateController(dependencies): DesktopUpdateController`
- `controller.check({ manual?: boolean })`, `download()`, `cancelDownload()`, `requestInstall()`, `dispose()`
- `InstalledBuild = { product, channel, version, platform, arch, packaged }`
- Внешние seams: metadata fetch, artifact download, store, injected **MAIN native dialog**, clock, progress/state observer, preparation/reverify/handoff.
- Никакие URL/path/key/argv не поступают из UI.
- `prepare` здесь — только неразрушающий preflight/staging; не shutdown/cleanup. Остановка owned runtimes допускается лишь после успешных trust checks и согласия, с отдельной native-проверкой фактического выхода.

`confirmInstall()` не экспортируется как public/controller/IPC действие. Только `requestInstall()` вызывает и **ожидает** injected MAIN native dialog. Renderer boolean, IPC payload или UI-состояние не являются доказательством согласия и не могут разрешать preparation, quit либо handoff.

## Приоритетный first-RED набор

| # | Pri | Spec | Случай и RED-assert |
|---:|:--:|:--:|---|
| 1 | P0 | 4.2/R1 | Валидная fixture Ed25519-пара принимает подписанные исходные payload bytes. |
| 2 | P0 | 4.2/R1 | Изменённый один payload byte без переподписания отвергается, `verified` отсутствует. |
| 3 | P0 | 4.2/R1 | Unknown/missing `keyId` отвергается до parse manifest. |
| 4 | P0 | 4.2/R1 | Signature не 64 bytes отвергается. |
| 5 | P0 | 4.2/R1 | Неканоничный base64, base64url, whitespace, неполный padding и ненулевые pad bits отвергаются. |
| 6 | P0 | 4.2/R1 | Envelope >64 KiB или decoded payload >32 KiB отвергается. |
| 7 | P0 | 4.2/R1 | Malformed UTF-8 в envelope/payload отвергается. |
| 8 | P0 | 4.2/R1 | Duplicate JSON member в envelope отвергается до verify; в payload — до selection. |
| 9 | P0 | 4.2/R1 | Лишний/отсутствующий ключ envelope или неверный `schema` отвергается. |
| 10 | P0 | 4.2/R2 | Принимается только exact product `works.voidcode.desktop` и channel `stable`. |
| 11 | P0 | 4.2/R2 | `version` принимает только stable semver без `v`, leading zero, prerelease, build или sign. |
| 12 | P0 | 4.2/R2 | `tag !== v${version}` отвергается. |
| 13 | P0 | 3,4.2/R2 | Installed `win32/x64` выбирает ровно `Void-Code-windows-x64.exe`. |
| 14 | P0 | 3,4.2/R2 | Installed `darwin/arm64` и `darwin/x64` выбирают свои exact case-sensitive ZIP. |
| 15 | P0 | 4.2/R2 | Не подменяет installed x64 на arm64, включая Rosetta-сценарий. |
| 16 | P0 | 4.2/R2 | Duplicate `(platform,arch)` или filename отвергает manifest. |
| 17 | P0 | 4.2/R2 | Известный target с иным именем, path/query/separator, bad SHA/size отвергается. |
| 18 | P0 | 4.2/R2 | Отсутствующий installed target даёт `unsupported-target`, без fallback. |
| 19 | P0 | 4.2/R2 | Подписанный структурно корректный будущий target (`win32/arm64`) игнорируется старым клиентом. |
| 20 | P0 | 3,4.2/R2 | Equal/older candidate не available; dev/unpackaged/unsupported build не имеет network/install side effects. |
| 21 | P0 | 4.2/R3 | Новый max version+digest записывается в store **до** snapshot `available`. |
| 22 | P0 | 4.2/R3 | Persisted floor новее candidate, даже если candidate новее installed build, отвергает candidate без `available`. |
| 23 | P0 | 4.2/R3 | Ошибка записи floor даёт fail-closed unavailable/failed, без available/download. |
| 24 | P0 | 4.2/R3 | Corrupt replay state даёт fail-closed, чат/controller остаётся usable. |
| 25 | P0 | 4.2/R3 | Та же version с другим payload digest отвергается; тот же digest разрешает retry. |
| 26 | P0 | 5/R4 | `check()` делает только metadata fetch: download, dialog, quit, helper — zero calls. |
| 27 | P0 | 3,5/R4 | `download()` до available либо после failure не quit/install; normal quit не install. |
| 28 | P0 | 3,5/R4 | Валидный download с hash/size verification достигает `ready`, но не вызывает dialog, quit, cleanup, prepare или handoff. |
| 29 | P0 | 3,5/R4 | Cancel download возвращает `available`, aborts transport, не вызывает install/quit. |
| 30 | P0 | 3,5/R4 | `requestInstall()` ожидает только injected MAIN native dialog; renderer boolean/IPC payload не заменяет consent и не вызывает preparation/handoff. |
| 31 | P0 | 3,5/R4 | Cancel native confirmation оставляет `ready`, не останавливает runtime и не вызывает helper. |
| 32 | P0 | 5/R4 | Успешный native consent запускает ровно `prepare → reverify → handoff` для того же pinned plan; до handoff нет quit/install. Mock — только boundary native dialog и не доказывает native install. |
| 33 | P0 | 5/R4 | Unsigned либо tampered ready cache при reverify не достигает cleanup, quit или handoff; состояние retryable failure. |
| 34 | P0 | 5/R5 | Double check/timer объединяются в один metadata request; double download — в один download. |
| 35 | P0 | 5/R5 | Cancel/dispose делает late progress, success и failure старой generation инертными. |
| 36 | P0 | 5/R5 | Retry после failed/cancel создаёт новую generation; старое completion не меняет новый state. |
| 37 | P0 | 5/R5 | Concurrent check во время download не создаёт второй job и не сбрасывает verified pinned plan. |
| 38 | P0 | 5,5.1/R5 | Timeout при transport, игнорирующем `AbortSignal`, завершает обещание и FSM без hanging; поздний resolve/reject инертен и не меняет state. |

Негативные, failure и concurrency cases: 31/38 (82%).

## Fixtures, properties и техника RED

- Fixture keys: реальные test-only Ed25519 keypairs в test assets; private fixture никогда не production key и не internal-beta key.
- Crypto/type fixtures фиксирует test author в RED-коммите; реализатор их не создаёт и не меняет.
- Strict JSON проверяется на raw UTF-8 bytes собственным duplicate-aware parser boundary; `JSON.parse`-нормализация недостаточна.
- `fast-check` properties:
  - гарантированно изменяющая байты (non-identity) single-bit mutation canonical payload либо signature **без переподписания** не проходит verify;
  - генераторы malformed UTF-8, forbidden base64 и duplicate-key вариантов всегда дают reject;
  - произвольные safe semver triples: available только при strict `candidate > installed` и candidate не ниже persisted floor;
  - произвольные artifact arrays: duplicate bindings/names или malformed known target всегда reject;
  - event-sequences `check/download/cancel/resolve/dispose/retry`: не более одного active job, stale generation не меняет snapshot, handoff возможен только после `ready → native consent → prepare → reverify`.
- Не заявлять property «произвольная мутация всегда rejected»: no-op и корректно переподписанные варианты могут быть валидны.
- RED обязан падать на конкретном `expect` контракта и счётчиках seams; не использовать catch-all import с превращением import error в `undefined`/ложный pass. Отсутствующий согласованный module/API — отдельный явный assertion, не swallowed exception.

## Dependency preparation и verification

Зависимости для `fast-check`/crypto test infrastructure — отдельный test-infra commit до test-only RED-коммита, с lockfile review; production dependencies не добавляются этим планом.

Предлагаемая команда из worktree root, без новых scripts:

```sh
cd desktop && npm test
```

После будущей реализации также выполняются требуемые repo preflight, build/lint и CI согласно спецификации; этот slice не заменяет S0/R6–R12.

## Future mutation mapping

Планируемые мутации: удалить Ed25519 verify; принять неканоничный base64/duplicate JSON; всегда выбрать newest; игнорировать replay floor; перепутать win/mac или arch; разрешить CLI artifact; объявить available до store; принять equivocation; начать download на check; считать renderer boolean consent; install на download/normal quit/без native consent; пропустить reverify ready cache; передать иной plan в handoff; потерять generation или проигнорировать timeout. Каждая должна быть убита исполняемым assert выше. Мутации пока не запускались.
