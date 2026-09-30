# Desktop auto-update v1 — Windows и macOS

Дата: 09.09.2026. База исследования: `f78d6cf4f9b31d287831154a25341e7c0505190d`.
Статус: **APPROVED для TDD 09.09.2026** — пользователь: «Давай да делаем».
Панель плана v2 и независимый Pass-2: GO. Это не разрешение на публикацию.
Заказ: автообновление десктопного клиента на Windows и Mac без обязательного перехода
на Apple Developer ID / Authenticode; проверить возможность переиспользовать CLI updater.

## 1. Что должно стать правдой

Установленный Void Code сам узнаёт о новой совместимой desktop-версии, не задерживая
запуск и чат. По кнопке скачивает и проверяет **всю сборку**; затем по отдельному
подтверждению закрывает свои сессии, устанавливает её и перезапускается. Обычный quit,
появление релиза, загрузка и проверка сами по себе ничего не устанавливают. На Mac
это замена `.app`, на Windows — установка полного NSIS-пакета в тот же каталог.
Доверие к обновлению даёт наша Ed25519-подпись метаданных, связывающая продукт, версию,
платформу, размер и SHA-256. Это **не** подпись Apple/Microsoft и не обещание отсутствия
Gatekeeper/SmartScreen. Пользовательские данные и чужие процессы не трогаются.

Этот документ задаёт полный контракт фичи и поэтапную проверку. Первый TDD-срез —
доверие/выбор артефакта и независимый контроллер согласия. Native replacement и
упаковка должны получить собственные RED-коммиты после платформенных экспериментов;
зеленые unit-тесты не означают, что приложение уже обновилось.

## 2. Что уже есть и что переиспользуем

Проверка исходников: `git show f78d6cf:<path>`; история:
`git log --all --oneline -- internal/update .github/workflows/release.yml`.

| Сейчас | Источник | Решение |
|---|---|---|
| `vc update` читает `${cfg.AuthHost}/vc/version.json`, выбирает CLI по OS/arch и заменяет `os.Executable()` | `cmd/vc/update.go`, `internal/update/update.go` | Не запускать для обновления десктопа |
| При старте CLI: фоновая проверка 2 с, sentinel 1 час; только подсказка | `cmd/vc/update_launch.go` | Переиспользовать UX-паттерн, не общий cache-файл |
| Explicit install: `http.DefaultClient`, `io.ReadAll`, нет checksum/signature | `internal/update/update.go` | Не переносить downloader без нового защищённого контракта |
| Версии сравниваются как три int, ошибки превращаются в 0 | `internal/update/update.go:CompareVersions` | Для desktop использовать строгие stable semver; dev/pre-release не обновлять автоматически |
| Unix заменяет один файл через temp+rename; Windows использует `.old` | `internal/update/replace_{unix,windows}.go` | Использовать принцип staging/replace, не выдавать single-file код за замену приложения |
| Десктоп проверяет SHA-256 `vc` по private manifest при старте | `desktop/src/main/private-runtime.js` | Обновление только private `vc` сломает manifest; поставлять целую сборку |
| Одни теги/GitHub Releases; desktop-attach ждёт обе платформы и публикует 3 сборки и SHA256SUMS-desktop | `.github/workflows/release.yml` | Переиспользовать канал выпуска/артефакты, добавить отдельный signed desktop manifest |
| `publish-auth` переносит только CLI; GitHub release и server deploy — разные операции | тот же workflow | Desktop v1 не зависит от выката void-auth; CLI schema/routes не менять |

**Нашёлся и старый desktop-updater, но НЕ в main:**
`origin/work/VC-desktop-updater-forward-port-20260819`, `5df5fb3`.
Проверка: `git branch -a --contains 5df5fb3` и
`git show 5df5fb3:desktop/docs/stable-update-contract.md`.
Там `ed25519-beta.ts`, `beta-update-controller.ts`, `stable-update.ts`,
`electron-updater-adapter.ts`: bounded metadata, signed envelope, single flight,
повторная проверка перед install, NSIS adapter. Это источник атак/паттернов, не готовый
cherry-pick: Windows-only, другая beta/stable политика, старые URL `vc.makscee.ru`,
привязка к внутреннему beta-signer и устаревшие IPC/cleanup. **Internal-beta key и
signer нельзя использовать для публичного обновления.** Наличие ветки не доказывает
выкат или работоспособность на установленном приложении.

**Итого по просьбе про CLI:** подключаемся к тому же release-процессу, но не к
`CheckAndUpdate` и не к его CLI `version.json`. Общий низкоуровневый downloader имеет
смысл выделять только при втором реальном потребителе нового строгого контракта;
рефакторинг CLI и изменение его поведения не входят в эту задачу.

## 3. Платформы и UX (согласованный контракт)

- Windows `win32/x64`, macOS `darwin/arm64` и `darwin/x64`. Выбирать архитектуру
  **установленной сборки**, а не переводить Intel под Rosetta на arm64 без спроса.
- Только packaged stable `MAJOR.MINOR.PATCH`. Dev, hash-only, prerelease/canary,
  unsupported OS/arch: `unsupported`, никаких update-network/install side effects.
- Автоматический check после готовности единственного основного окна; не внутри
  критического пути runtime/bootstrap. Затем раз в 6 часов, пока приложение работает;
  successful-check cache между запусками на 1 час, отдельный от CLI. Ручная кнопка
  «Проверить обновления» обходит TTL. Ошибка не маскируется как «последняя версия».
- Нет автоскачивания сотен МБ. «Доступна версия X» → «Скачать» → прогресс/«Отменить»
  → «Готово» → «Установить и перезапустить» / «Позже».
- Подтверждение предупреждает, что текущие задачи/чаты будут остановлены. Отмена
  подтверждения не завершает ни одного процесса. «Позже» и обычное закрытие не
  превращаются в отложенную автоматическую установку на следующем запуске.
- HTTP/верификационные ошибки показывают короткое состояние и «Повторить»; без
  токенов, signed redirect query, внутренних путей и содержимого чатов в UI/логах.
- Закрытие приложения во время download отменяет текущую операцию и оставляет
  действующую установку нетронутой. Незавершённые файлы чистятся своим владельцем;
  готовый cache перепроверяется при повторном использовании.

## 4. Доверие и release contract

### 4.1 Discovery не является разрешением на установку

Предлагаемый **независимый от CLI latest** locator:
`https://github.com/makscee/void-code/releases/download/desktop-update-channel/desktop-update.json`.

`desktop-update-channel` — отдельный технический GitHub release/tag, создаётся один раз
по release-гейту владельца (не этой задачей/не branch CI), с `prerelease=true` и
`make_latest=false`. Он содержит только указатель — копию последнего signed desktop
manifest. Создание такого release не добавляется в workflow; обновление указателя
выполняется существующим desktop publisher после успешной публикации версии.
Нет канала — fail closed updater с понятным unavailable; никаких implicit create.

После проверки manifest закреплён конкретный `vX.Y.Z`. Артефакты только:
`https://github.com/makscee/void-code/releases/download/vX.Y.Z/<fixed-basename>`.
Каждый версионный release также хранит свою immutable-by-policy копию manifest для
аудита. При новом CLI-only/неполном release канал продолжает указывать на последний
полный desktop release, а не превращается в 404 от repository-wide `latest`.

Нет manifest, 404/сбой самого канала — `unavailable`/retry, а не «обновление готово»
и не fallback на `vc-*`. Подписанный уже проверенный candidate может оставаться в
cache, но повторное использование требует тех же trust/replay/integrity проверок.

GitHub tag path — фиксированный locator, **не криптографическая неизменяемость**:
владелец может заменить asset. Поэтому итоговые байты обязаны совпасть с подписью
и digest при CDN-кэше, подмене asset и смене указателя в середине скачивания.
Указатель изменяемый, версионные assets/manifest — create-only или byte-identical rerun.
Публикации канала сериализованы и только монотонно повышают version: запоздавший старый
workflow не должен вернуть канал назад. Окно delete/upload GitHub asset не атомарно;
при сбое остаётся retryable unavailable, не разрешение доверять неподписанным данным.

### 4.2 Wire format v1

Outer JSON, exact keys: `schema`, `keyId`, `payload`, `signature`.
`schema=1`; keyId — непустой ASCII идентификатор до 64 символов `[A-Za-z0-9._-]+`.
`payload` — canonical padded base64 UTF-8 JSON; `signature` — canonical padded
base64 ровно 64 байт Ed25519. Canonical — RFC 4648 standard alphabet + padding:
строгое decode и повторное encode должны дать исходную строку byte-for-byte;
whitespace, base64url alphabet, неполный padding и ненулевые pad bits отвергаются.
Envelope ≤64 KiB, decoded payload ≤32 KiB. Envelope и payload — strict UTF-8 JSON
без duplicate members; для envelope эта синтаксическая проверка предшествует verify.
Проверяется подпись **исходных decoded payload bytes**, не повторно сериализованного JSON.
Неизвестный ключ, неверный размер/encoding/signature: reject, не читать payload как manifest.

Payload, exact keys:

```json
{
  "schema": 1,
  "product": "works.voidcode.desktop",
  "channel": "stable",
  "version": "0.2.54",
  "tag": "v0.2.54",
  "artifacts": [
    {"platform":"darwin","arch":"arm64","file":"void-code-mac-arm64.zip","size":123,"sha256":"<64 lowercase hex>"},
    {"platform":"darwin","arch":"x64","file":"void-code-mac-x64.zip","size":123,"sha256":"<64 lowercase hex>"},
    {"platform":"win32","arch":"x64","file":"Void-Code-windows-x64.exe","size":123,"sha256":"<64 lowercase hex>"}
  ]
}
```

123 и digest здесь иллюстративны, не release metadata. **Имена в следующей таблице
нормативны и case-sensitive**, не примеры:

| platform | arch | file |
|---|---|---|
| darwin | arm64 | void-code-mac-arm64.zip |
| darwin | x64 | void-code-mac-x64.zip |
| win32 | x64 | Void-Code-windows-x64.exe |

Размер: положительное safe integer ≤1 GiB. Array от 1 до 32 записей, каждая с exact
keys; пары `(platform,arch)` и filenames уникальны. `platform`/`arch` — ASCII
`[a-z0-9][a-z0-9-]{0,31}`, `file` — basename `[A-Za-z0-9][A-Za-z0-9._-]{0,127}`,
не `.`/`..`, без разделителей, URL, query. Для известных выше targets обязано совпасть
точное имя. Структурно корректные подписанные новые targets (например win32/arm64)
старый клиент игнорирует, а не отвергает весь release. Malformed запись отвергает
manifest. Выбранный target должен существовать ровно один раз; отсутствующий означает
`unsupported-target`, не fallback. Количество платформ publisher проверяет по своему
qualified build set (сегодня все три), клиент не хранит вторую копию этого количества.

Версия: strict stable semver, без leading zeros, `v`, знаков, prerelease, build metadata;
каждый компонент safe integer. `tag` обязан быть ровно `v${version}`.

Production keyring зашит в приложение. Ключи не читаются из manifest, IPC, argv,
окружения, проекта или пользовательского файла. Тесты используют собственные
эпhemeral keys через чистую функцию; production wiring не предоставляет override.
Нужен отдельный публичный release-signing key, его owner/backup и ограниченное
место подписи. В этот этап входят только fixture keys, **не создание боевого ключа**.

Replay: никогда не downgrade относительно установленной версии; persist максимальную
проверенную release-версию вместе с digest payload до объявления `available`.
Та же версия с другим digest отвергается; та же версия с тем же digest допускает retry.
Ошибка записи/битое anti-replay состояние — fail closed для updater, чат работает.
Явная ручная переустановка старого installer не автоматически снижает этот floor.
Без общей секретной/аппаратной опоры не защищаемся от того же OS-пользователя, который
может изменить приложение/его данные; root, украденный release key тоже вне границы.

Нет TTL на signed release v1: релизы могут быть редкими. Злоумышленник на transport
может скрыть новую версию (freeze/availability attack), но не подсунуть другой код.
Не называть эту политику гарантией свежести. Не повторять beta-policy с истекающим
manifest, из-за которой последняя исправная версия перестала бы быть доступной.

### 4.3 Publication

Генератор читает реальные байты всего qualified build set (сегодня все три artifacts),
считает size/SHA-256, сверяет platform/version с build witness. Подписанный версионный
manifest публикуется **последним**, только когда все assets прикреплены и повторно
сверены. После этого теми же signed bytes повышается locator `desktop-update-channel`.
Не использовать glob, который случайно загрузит manifest раньше artifacts; безопасный
rerun: identical/no-op либо отказ при попытке заменить уже опубликованный signed release
другим содержимым. Только channel pointer имеет специально выделенный replace-путь.

Обычный CI формирует manifest с fixture key в песочнице и упражняет тот же генератор.
Нет боевого ключа в PR/branch jobs, нет `electron-builder --publish`, production deploy,
новых release triggers или авторизации публикации самим этим PR. Старые CLI checksums,
`version.json` и `publish-auth` не меняются. Открытие нового desktop publication gate,
публичный ключ в первом клиенте и release-tag требуют отдельного решения владельца.

## 5. Runtime architecture

**Electron main** владеет подписанным планом обновления, network, staging и FSM.
Renderer получает только snapshot (state/version/progress/error code) и может вызвать
`check`, `download`, `cancel`, `install`. Все IPC проходят существующую
`rendererAuthority`; ни путь установки, ни URL, hash, ключ, command line не приходят из UI.
Подтверждение перезапуска — native main-process dialog, не renderer boolean.

Новые модули отделяют чистый manifest verifier/selector от bounded transport, controller
и platform installer. Windows и Mac используют один signed plan и одинаковую модель
согласия. Кандидат для helper: отдельный маленький Go entrypoint из нашего repo,
копируемый из verified private resources в process-owned staging **до** shutdown;
не `vc update`, не Node из PATH, не скачанный shell script. Выбор helper фиксируется
после S0 ниже; не встраивать второй большой updater framework без преимущества,
подтверждённого экспериментом. Sparkle — сравниваемая альтернатива для Mac, не обещание
готовой Electron-интеграции. Windows может использовать NSIS installer напрямую;
дублирующий unsigned `latest.yml` не должен становиться второй trust authority.

FSM: `idle → checking → up-to-date | available | unavailable | unsupported`;
`available → downloading → verifying → ready`;
`ready → confirming → preparing → installing`.
Ошибки check/download/verify/preflight — retryable `failed` с фазой;
отмена download — обратно `available`; отмена confirmation — `ready`.
`installing` означает только передачу установки helper, **не установленную версию**.
Подтверждённый результат приходит из нового процесса после bootstrap.

Одна операция за раз; double click/check/coincident timer объединяются, а не создают
вторую загрузку/диалог/helper. Каждая async operation имеет generation; cancel,
dispose или новый job делает прежние progress/completion инертными. Disposal не
должен автоматически уничтожать работающий install-helper.

### 5.1 Network и staging

- Только HTTPS на compile-time allowlist. Начальный exact GitHub repo/tag/asset;
  redirects ≤5, только ожидаемый release path и
  `release-assets.githubusercontent.com/github-production-release-asset/…`.
  Credentials/fragment/custom port запрещены. Signed CDN query допускается только
  как наблюдённый redirect с разрешённого предыдущего hop, не как вход из manifest.
  Не принимать arbitrary `*.githubusercontent.com`, HTTP downgrade, file URL.
- Metadata hard deadline 10 с, лимит streamed bytes; download idle timeout 30 с,
  total 10 минут, retry максимум 2 повтора на transient network/429/5xx с bounded
  Retry-After/backoff. Подпись, schema, size/digest mismatch, 403/404 не ретраить
  автоматически в той же операции. Cancellation прерывает ожидание независимо от
  того, уважает ли нижний transport AbortSignal; поздний результат игнорируется.
- Скачивать потоком в `.partial`, не в RAM; размер проверять по байтам даже без
  Content-Length/при ложном заголовке. Не возобновлять чужую partial по имени.
- Process-owned staging, случайный transaction ID, private права; на Windows
  настоящий DACL (current user, SYSTEM, Administrators), не chmod как доказательство.
  Staging не содержит token, session JSONL или environment dump.
- Проверить hash/size до распаковки/запуска, повторить после consent/preparation прямо
  перед helper handoff; helper независимо проверяет signed metadata и artifact.
  Архивы извлекаются в новый owned каталог с контролем traversal, symlink escape,
  device/hardlink entries и expanded-size limits (суммарно ≤4 GiB, ≤100 000 entries,
  один regular file ≤1 GiB, длина relative entry path ≤4096 UTF-8 bytes). Корректные внутренние symlink-ы Mac
  Frameworks и executable bits сохраняются. Нельзя сначала сделать опасную распаковку,
  а затем объявить её безопасной по post-scan.

### 5.2 Установка и восстановление

Перед shutdown: canonical install identity/path, bundle ID, version, installed arch,
write capability, место под staging/backup, отсутствие другой транзакции. Read-only,
DMG/App Translocation, чужая/неподдерживаемая установка — отказ с ручной инструкцией,
пока текущие чаты живы. Не угадывать `/Applications/Void Code.app` по имени. Не повышать
права, не отключать Gatekeeper/Defender, не удалять quarantine рекурсивно.

Закрытие: заморозить новые starts, остановить только owned runtimes, **дождаться их
фактического выхода** и подтверждения готовности helper, затем quit. Нынешний
`teardownAll()` удаляет объекты до доказательства выхода — одного его возврата мало.
Timeout выхода означает отмену install, без kill-by-name. Helper удерживает отдельный
transaction lock, чтобы повторный запуск старого приложения не пересёкся с replacement.

Mac: распакованный verified `.app` подготовить на том же filesystem, что target;
сохранить старый bundle и заменить целиком, не перезаписывать mapped executable inode.
Crash recovery по устойчивому journal, проверка identity перед rename/delete, rollback
при неудачной замене/неподтверждённом старте. Один backup до успешного bootstrap; исходные
пользовательские данные остаются вне транзакции. Два rename — **не** атомарная транзакция:
journal/recovery обязаны закрыть окно между ними.

Windows: только поддержанная per-user NSIS установка, точный существующий target;
installer запускается напрямую без shell, `/D=` и пробелы/Unicode проверяются на Windows.
Helper не лежит в каталоге, который installer удаляет/перезаписывает. Успех spawn/exit 0
не равен успешному обновлению: новый packaged app должен подтвердить версию/runtime
после bootstrap. NSIS не обещает atomic rollback: **до реализации install** S0 обязан
выбрать и доказать восстановление после частичного отказа (retained predecessor installer
и точный target/identity либо verified directory backup). Если это не доказано, Windows
installation slice остаётся blocked, а не считается готовым по mock `quitAndInstall`.

Если новое приложение не стартовало: не удалять predecessor; писать ограниченный
recovery receipt и показывать восстановление, не «успех». Автоматически вернувшийся
predecessor не должен немедленно повторять то же плохое обновление; failed-version
marker и повтор только по явному действию. Старый/новый процессы подтверждают один
transaction ID и ожидаемую версию; случайный живой процесс не считается witness.
Без вмешательства в `.pi/agent/sessions`, `.void-code/token`, workspace.json и настройки.
Миграции пользовательских данных, несовместимые с rollback, в v1 запрещены.

## 6. План TDD и граница доказательств

Каждый slice: тест — агент A; координатор лично снимает RED в исполнившемся assert и
коммитит test-only; реализация — агент B, тесты ему менять запрещено; затем мутации
в отдельном checkout, repo CI/preflight и панель. Отсутствующий модуль — явный assert
контракта, не случайный import error. Не выдавать прогнозы атак за прошлые баги.

| ID | Срез/проверки | Приоритет | Свидетельство |
|---|---|---|---|
| S0 | Независимый test-only native RED, затем две disposable packaged версии N→N+1: Mac без Developer ID; Win NSIS + recovery | P0 | До helper-кода агент A фиксирует проверки замены/recovery; агент B реализует эксперимент. Реальные platform helpers/installer, изолированные appId/HOME, не приложение пользователя |
| R1 | Envelope: настоящая Ed25519 verify, wrong/missing key, altered bytes, malformed base64/schema/limits | P0 | Vitest + настоящая crypto, fixture keys; без mocks verifier |
| R2 | Exact product/channel/version/tag, уникальные bindings, точное имя selected installed arch, newer/equal/older/dev, расширение targets | P0 | Чистые функции; negative matrices и property-based генераторы |
| R3 | Replay floor: persist-before-available, same-version equivocation, restart, corrupt/unwritable state | P0 | Temp FS + controller; сбои записи, без настоящего HOME |
| R4 | Check никогда не download/install; download не quit; cancel/normal quit не install; consent cancel inert | P0 | State machine, подставлены только внешний I/O и native dialog |
| R5 | Double click, timeout, late progress/result, retry после fail/cancel, generation ownership | P0 | Fake clock + deferred promises, property-based event sequences |
| R6 | Bounded streaming HTTP и redirects: chunked, wrong Content-Length, mid-stream reset, idle silence, status errors | P0 | Настоящий downloader с loopback server; внешняя сеть запрещена |
| R7 | Archive/staging: traversal, unsafe symlinks, zip bombs, permissions, disk-full, hash mismatch before extraction | P0 | Temp FS, реальные архивы; native DACL на Windows |
| R8 | Helper: app+owned process exit, stale PID/transaction, duplicate restart, unsafe target, install/rollback crash points | P0 | Subprocess/FS integration на обеих ОС; не только mock adapter |
| R9 | Trusted IPC и видимый UI, renderer не выбирает executable/URL, native confirmation | P0 | Real Electron preload→main→controller и DOM кнопки |
| R10 | Полный комплект assets; CLI-only не сбивает desktop channel; manifest-last; byte-identical rerun, no clobber signed manifest; stale/concurrent channel publisher | P0 | Генератор/изолированный publisher recorder + CI artifact graph |
| R11 | Успешный update сохраняет data/architecture; failed-version не крутится циклом | P0 | Packaged N→N+1 и fault/recovery witness |
| R12 | Оффлайн запуск не ждёт updater; usable chat при каждом отказе; CPU/memory/time bounds | P1 | Fake timers + packaged offline start |

Первый RED-набор: **R1/R2, минимальный R3 и базовые R4/R5**. R3 уже здесь:
persist-before-available, same-version digest, corrupt/unwritable store fail closed.
Он не подменяет полную restart/recovery интеграцию R3, R6–R12 и S0.
После план-ревью автор тестов фиксирует узкий callable API в `desktop/tests`, рядом
со списком AC, до появления production implementation. ≥70% кейсов — отказы/гонки.
Ожидаемая краснота сама по себе не доказывает силу всех будущих проверок: каждое
правило становится green-ready только после убийства соответствующего мутанта.

Обязательные мутации: убрать signature verify; вернуть всегда newest; перепутать
win x64/mac x64; разрешить CLI artifact; убрать stream limit; ослабить exact URL;
install до consent/hash/exit; разрешить install на normal quit; потерять generation;
поменять артефакт между ready/install; удалить rollback/recovery и сохранение floor.
Каждая мутация должна менять исполняемое поведение, не комментарий/соседний job.

## 7. Этапы и стопы

1. Спека, независимый test plan, панель решений → test-only RED commit.
2. R1/R2/minimal-R3/R4/R5 implementation отдельным исполнителем → мутации;
   production IPC/startup wiring не подключать до полного R3 и остальных native gates.
3. S0: отдельный агент A пишет native acceptance test-only, координатор фиксирует
   исполненную RED-проверку отдельным коммитом; агент B создаёт disposable эксперимент
   и не меняет его тесты. До успеха S0 не фиксировать «helper работает».
4. Остальные R-срезы red-first отдельными парами; упаковка и native witnesses.
5. `.rails/preflight.sh <worktree>` до PR/review диффа; `npm test`, build/lint и
   Go+Ruby+reusable desktop CI согласно `.rails/green.lock`, не выбранное подмножество.
6. Отдельный release-gate: key ownership/backup, embed public key, signer permissions,
   первая ручная установка updater-capable version, реальный upgrade на обеих ОС,
   rollback, publish manifest-last, канал доступен извне без auth.

Старые `v0.2.53` не имеют updater; **первую версию с ним надо установить вручную**.
Результат этого этапа не релиз и не включение фичи у пользователей.

## 8. Внешние основания (проверены 09.09.2026)

- Electron: Squirrel.Mac требует подпись; Windows built-in updater не NSIS:
  https://www.electronjs.org/docs/latest/api/auto-updater
- Apple: обновлять executable заменой файла, не inplace write:
  https://developer.apple.com/documentation/security/updating-mac-software
- Sparkle: Developer ID «if possible», Ed25519 archives, read-only/translocation:
  https://sparkle-project.org/documentation/
- electron-builder: NSIS и ограничения trust/update:
  https://www.electron.build/nsis.html
  https://www.electron.build/docs/features/security/

Внешние docs не являются доказательством работы нашего unsigned packaged upgrade;
это обязанность S0/R11. Версии библиотек и их ограничения перепроверяются перед native
реализацией, а не переносятся из старой ветки на веру.
