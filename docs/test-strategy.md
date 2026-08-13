# Test strategy

## 1. Цель и критерий выпуска

Тестирование должно доказывать не только успешную установку, но и сохранность
системы при любом отказе. Релиз блокируется, если выполняется хотя бы одно
условие:

- поддерживаемая комбинация Ubuntu/архитектуры не проходит полный E2E;
- повторная команда меняет секреты или дублирует ресурсы;
- прерывание оставляет сервисы, firewall или конфигурацию в неопределённом
  состоянии без возможности безопасного resume/rollback;
- скачанный артефакт используется до проверки целостности;
- секрет попадает в диагностический вывод или persistent log;
- `vpnctl` изменяет не принадлежащий ему Xray, 3x-ui или firewall-ресурс;
- после reboot не восстанавливается здоровое состояние;
- Critical/High security findings остаются неисправленными.

Обязательный release gate на каждой из шести платформ:

```text
fresh VM -> bootstrap -> install -> status -> doctor
         -> user add/show/qr/list/remove
         -> backup -> destructive state change -> restore
         -> reboot -> status/doctor -> repeated install -> uninstall
```

Контейнеры разрешены для быстрых integration-тестов. Они не заменяют VM-тесты:
нужны настоящий systemd, kernel firewall, reboot и сетевой стек.

## 2. Уровни тестирования

| Уровень | Что проверяет | Где запускается |
|---|---|---|
| Unit | Валидацию, планирование, state machine, redaction, checksum, формат данных, правила владения ресурсами, UI | `go test ./...` без root и сети |
| Component integration | Взаимодействие команд с fake OS, downloader, filesystem, services, firewall, 3x-ui/Xray adapter | `go test` с временной директорией и fault injection |
| Host integration | Реальные файлы, subprocess, permissions и systemd/firewall в disposable окружении | privileged ephemeral VM; контейнер только для неполного smoke |
| Provisioning E2E | Bootstrap и все публичные команды на чистом образе | disposable VM для каждой поддерживаемой OS/arch |
| Destructive/red-team | Disk pressure, network faults, signals, reboot, повреждения и конфликты | только disposable VM со snapshot/serial console |

Unit/component тесты должны быть детерминированными и параллельными. VM-тесты
могут быть отдельным ручным или scheduled workflow, но шесть release-gate
сценария обязательны перед публикацией артефакта.

## 3. Test seams и fakes

Бизнес-логика не должна напрямую читать `/proc`, вызывать `systemctl`, менять
firewall или ходить в сеть. Минимальные узкие seams:

| Seam | Production | Fake и обязательные возможности |
|---|---|---|
| `Platform` | EUID, `/etc/os-release`, arch, RAM, disk, hostname | Задать OS/arch/resources/root; записать вызовы |
| `Runner` | Безопасный запуск subprocess | Сценарии exit/stdout/stderr/delay; сигнал/контекст; журнал argv без исполнения |
| `FileStore` | Atomic write, rename, chmod, fsync, ownership | In-memory/temp-dir; ENOSPC/EACCES; crash до/после rename/fsync; symlink |
| `Downloader` | HTTPS download во временный файл | Chunked body; timeout; disconnect; retry; wrong bytes; статус; call count |
| `Verifier` | SHA-256/signature перед publish/exec | Correct/mismatch/invalid signature; доказательство порядка `verify -> publish` |
| `Network` | Connectivity, public IP, port probe | IPv4/IPv6/dual-stack; temporary/permanent failure; occupied port |
| `ServiceManager` | systemd install/enable/start/status | Stateful fake; fail на каждой операции; reboot очищает runtime, сохраняет enabled |
| `Firewall` | Действующий backend | Stateful ruleset; pre-existing unrelated rules; SSH-safety oracle; ownership tags |
| `XUI` | Поддерживаемый 3x-ui API/CLI | Stateful users/inbounds; duplicate/conflict; unavailable/malformed response |
| `Xray` | Key generation/config validation/runtime | Deterministic public fixtures; invalid config; start/validation failures |
| `Clock` | Время, deadlines, backoff, имена backup | Manual clock; без sleep; проверка bounded retries |
| `Entropy` | `crypto/rand` | Детерминированный reader только в тестах; short read/failure; uniqueness tests |
| `CheckpointStore` | Durable install/update/restore state | Состояния фаз; corrupt/missing checkpoint; crash вокруг commit |
| `Prompt` | `/dev/tty` и interactive input | Ответы/invalid/EOF/SIGINT; доказывает отсутствие чтения stdin в automation |
| `Renderer/Logger` | TTY/JSON и allow-list log | Buffer writers; TTY/width/color clock; сбор всех sinks для secret scan |

Fakes должны быть stateful, иметь fault injection на конкретный номер вызова и
сохранять упорядоченный call log. Fake, который всегда отвечает success, не
подходит для проверки rollback и idempotency.

Для каждого orchestration-теста используются sentinel secrets, например
`S3NTINEL_UUID`, `S3NTINEL_REALITY_PRIVATE`, `S3NTINEL_PANEL_PASSWORD` и
`vless://S3NTINEL_URI`. После команды сканируются stdout, stderr, structured log,
subprocess diagnostics и error object. Исключение — явно secret-bearing stdout
команд `user show` и `qr`; даже для них persistent log и stderr должны быть
чистыми.

## 4. Матрица платформ и provisioning

| ID | Image | Arch | Network minimum | Обязательный gate |
|---|---|---|---|---|
| P-2204-A64 | Ubuntu Server 22.04 LTS cloud image | amd64 | Public IPv4 | Полный lifecycle |
| P-2204-R64 | Ubuntu Server 22.04 LTS cloud image | arm64 | Public IPv4 | Полный lifecycle |
| P-2404-A64 | Ubuntu Server 24.04 LTS cloud image | amd64 | Public IPv4 | Полный lifecycle |
| P-2404-R64 | Ubuntu Server 24.04 LTS cloud image | arm64 | Public IPv4 | Полный lifecycle |
| P-2604-A64 | Ubuntu Server 26.04 LTS cloud image | amd64 | Public IPv4 | Полный lifecycle |
| P-2604-R64 | Ubuntu Server 26.04 LTS cloud image | arm64 | Public IPv4 | Полный lifecycle |

Provisioning plan:

1. Использовать официальные minimal cloud images, проверяя опубликованный digest.
2. Создать VM из неизменяемого base image через cloud-init: один sudo-user, SSH
   key, serial console, без Xray/3x-ui и без дополнительных firewall-правил.
3. Зафиксировать image release/build ID, kernel, initial package list и initial
   ruleset в артефактах теста.
4. Снимать snapshot `fresh` до bootstrap. Каждый сценарий стартует с clone этого
   snapshot, а не с VM после другого теста.
5. Доставить ровно release-кандидат `install.sh`, checksums/signatures и бинарник
   через контролируемый HTTPS origin. Нельзя тестировать локальный бинарник при
   проверке release bootstrap.
6. Запустить bootstrap тем же способом, что указан в README, затем public CLI
   lifecycle. Собрать journal, vpnctl logs, exit codes, ownership/modes, listening
   sockets и firewall diff.
7. Перезагрузить VM через hypervisor/API, дождаться SSH и проверить `status`,
   `doctor`, enabled/active services, inbound и firewall.
8. Уничтожить VM после экспорта sanitized diagnostics.

amd64 можно запускать на hosted/self-hosted x86 runner. arm64 должен выполняться
на native arm64 runner или disposable arm64 VPS; QEMU допустим для smoke, но не
как единственный release gate. IPv6-only сценарий требует провайдера или lab
network с настоящей IPv6 connectivity, а не только loopback.

## 5. Acceptance matrix: install и окружение

Во всех failure-сценариях дополнительно проверяются: точный exit code и domain
error code, одна безопасная suggested action, отсутствие stack trace/секретов,
неизменность unmanaged ресурсов и успешный последующий `doctor` либо честный
degraded result.

| ID | Сценарий / инъекция | Ожидаемый результат и оракул |
|---|---|---|
| I-01 | Чистая Ubuntu 22.04, обе arch | Install exit 0; 3x-ui/Xray active+enabled; VLESS Reality inbound; deny-by-default firewall сохраняет SSH; первый user и QR/URI валидны; doctor healthy |
| I-02 | Чистая Ubuntu 24.04, обе arch | Те же assertions, включая корректную работу используемого firewall/systemd backend |
| I-02A | Чистая Ubuntu 26.04, обе arch | Те же assertions; отдельно проверить изменения systemd, UFW/nftables, cloud image defaults и доступность pinned dependencies |
| I-03 | Не root | До mutation: exit 3, stable `ROOT_REQUIRED`; нет файлов, packages или правил |
| I-04 | Unsupported OS/arch | До download/mutation: exit 3; указаны поддерживаемые варианты |
| I-05 | RAM ниже минимума | До mutation: exit 3 с фактическим и минимальным объёмом; повтор после увеличения RAM успешен |
| I-06 | Недостаточно disk на preflight | Exit 3 до mutation с безопасным required/free; не создаётся partial install |
| I-07 | ENOSPC во время каждой atomic write/download/backup | Операция fail; старый committed файл цел; temp удалён или безопасно распознаётся при resume; checkpoint не опережает durable state |
| I-08 | VPN port занят реальным listener | До firewall/service mutation: exit 4/3 по утверждённому contract, `PORT_IN_USE`, процесс/PID только если безопасно; другой порт позволяет продолжить |
| I-09 | Privileged loopback panel-port collision | Генерация повторяется ограниченное число раз; существующий listener не затрагивается; exhaustion даёт стабильную ошибку |
| I-10 | Unmanaged Xray уже установлен | Installer не перезаписывает и не останавливает его. До mutation сообщает conflict и безопасный путь; adoption допустим только если это явно спроектированная и проверяемая операция |
| I-11 | Unmanaged 3x-ui уже установлен | Те же ownership assertions; БД не редактируется напрямую и не заменяется |
| I-12 | Firewall уже содержит SSH и unrelated rules | Установка добавляет только namespaced/owned rules; SSH остаётся доступен; unrelated rules byte-for-byte/semantically сохранены |
| I-13 | Firewall backend отсутствует/неработоспособен | Не запускать публичный inbound без deny-by-default policy; rollback/resume state точен |
| I-14 | IPv4-only, IPv6 отсутствует | Установка успешна; нет обязательных IPv6 probes/rules; URI содержит IPv4 address |
| I-15 | IPv6-only server | Connectivity, public address и destination работают по IPv6; URI host корректно bracketed; firewall не открывает IPv4; E2E client probe по IPv6 |
| I-16 | Dual-stack | Выбран и показан один проверенный public address по policy; firewall защищает оба family; поведение детерминировано |
| I-17 | Public IP detection даёт malformed/private/multiple result | Значение не принимается молча; advanced explicit valid address работает; до подтверждения mutation нет |
| I-18 | Temporary network failure: timeout/reset/503 в каждом download/probe | Только idempotent requests повторяются с bounded backoff; затем success или `NETWORK_UNAVAILABLE`; нет infinite wait и partial publish |
| I-19 | GitHub/release origin unavailable | Понятная ошибка и retry action; установленная версия не меняется; install можно продолжить после восстановления origin |
| I-20 | DNS unavailable, TLS failure, expired/wrong certificate | Fail closed; HTTP downgrade/`-k` запрещены; ошибка различает connectivity и verification без raw body |
| I-21 | Download corrupted/truncated/extra bytes | `CHECKSUM_MISMATCH`; файл не исполняется/не публикуется; текущий binary/component не меняется; temp очищен |
| I-22 | Checksum/signature metadata corrupted | Fail closed до publish; checksum, полученный с того же непроверенного payload без trust root, не считается достаточным |
| I-23 | Interactive recommended path | Нормальный путь требует только mode, user и final confirm; defaults/validation соответствуют UX contract; client block идёт только в controlling TTY |
| I-24 | Advanced path и все conditional fields | Поля/порядок/validation точны; ответы сохраняются после ошибки; конфликт SSH/firewall блокирует mutation |
| I-25 | Non-interactive без обязательного user/conditional CIDR | Exit 2, usage; stdin не читается; `--yes` не заполняет значения |
| I-26 | Contradictory mode/flags, invalid port/CIDR/name | Exit 2 до mutation, стабильная ошибка поля |
| I-27 | `curl | sudo bash` с controlling TTY | Prompt читается из `/dev/tty`, не из pipe; bootstrap проверяет release до запуска |
| I-28 | `curl | sudo bash` без controlling TTY | Немедленный failure с полным non-interactive примером; не зависает и не мутирует host |
| I-29 | Финальный healthcheck degraded | Нет `Installation complete`, URI или QR; exit 5; показаны failed checks, rollback/resume state и `vpnctl doctor` |
| I-30 | Malformed existing vpnctl config/checkpoint | Не перезаписывать автоматически; status/doctor сообщают точный corrupt artifact без утечки содержимого; install/repair сохраняет forensic copy только с mode 0600 |

Для I-10/I-11 архитектура должна формально определить ownership/adoption policy.
До такого решения безопасный ожидаемый результат — отказ до mutation, а не
попытка «починить» неизвестную установку.

## 6. Idempotency и failed-install recovery

Перед первым и повторным запуском собирается normalized manifest:

- UUID, Reality public-key fingerprint, short ID fingerprint, panel port,
  webBasePath fingerprint и user IDs;
- normalized owned config без timestamps;
- enabled/active services;
- listening sockets;
- firewall rules и counters без volatile значений;
- file path/type/owner/group/mode/content digest;
- unmanaged resource manifest.

| ID | Действие | Ожидаемый результат |
|---|---|---|
| R-01 | `vpnctl install` на healthy install | Exit 0, `Nothing to change`; manifests равны; секреты/ports не ротируются; create API не вызывается |
| R-02 | Два последовательных `user add same` | Exit 0 на втором; тот же UUID/URI/created time; user count не меняется |
| R-03 | Повтор команды после failure до первой mutation | Полный fresh install без ручной очистки |
| R-04 | Повтор после каждой committed phase | Resume с первого незавершённого checkpoint; завершённые ресурсы не пересоздаются |
| R-05 | Failure после side effect, но до checkpoint commit | Reconciliation распознаёт фактический owned resource и не дублирует его |
| R-06 | Rollback успешен | Сообщение утверждает rollback только после проверки restored manifest/health |
| R-07 | Rollback failed | Exit 1, prominent failure, точный checkpoint; не заявляет clean state |
| R-08 | Malformed checkpoint при healthy resources | Fail safe и doctor; не регенерирует секреты из-за отсутствующей записи |

Сравнение должно игнорировать только документированные volatile поля. Изменение
mtime само по себе считается лишней mutation, если команда обещает `Nothing to
change`.

## 7. Signals, crash и reboot

Fault points ставятся до side effect, после side effect, до durable checkpoint и
после него для каждой стабильной фазы UX:

1. Checking system.
2. Preparing installation.
3. Installing components.
4. Configuring VPN.
5. Securing network.
6. Starting services.
7. Running health checks.

| ID | Fault | Assertions |
|---|---|---|
| S-01 | SIGINT во время prompt | EOF/cancel, exit 130, mutation не начинается, terminal восстановлен |
| S-02 | SIGINT во время download/step | Новая работа прекращается на safe boundary; active row финализирован `Interrupted`; rollback/resume сообщение соответствует факту; exit 130 |
| S-03 | Второй SIGINT во время cleanup | Процесс завершается без зависания; checkpoint/old config остаются пригодны для recovery; terminal восстановлен |
| S-04 | SIGTERM | Graceful cancellation по той же state policy; документированный shell-compatible exit; никаких ложных success/rollback сообщений |
| S-05 | SIGKILL после каждой точки | Следующий install детерминированно reconcile/resume; нет half-written committed config |
| S-06 | Hypervisor hard reboot после каждой точки | То же, плюс SSH/firewall safety: VM снова доступна; сервисы либо previous healthy, либо явно resumable |
| S-07 | Normal reboot сразу после success | Xray/3x-ui active+enabled, firewall persistent, status/doctor healthy, URI пользователя неизменён |
| S-08 | Reboot после failed install/rollback | Previous healthy state работает либо install может resume; disabled partial units не стартуют случайно |
| S-09 | Signal во время update/restore | Safety backup сохраняется; old/new state не смешаны; health-gated rollback или точное recovery действие |

Hard reboot нельзя эмулировать cancel context: VM выключается через hypervisor
без guest shutdown. После каждого crash теста проверяются orphan temp files,
world-readable files, stale locks и возможность одного успешного повторного
запуска без ручной очистки.

## 8. Public command matrix

| ID | Команда/сценарий | Основные assertions |
|---|---|---|
| C-01 | `status`: healthy/degraded/missing/unreadable | Read-only; exit 0/5/4/1 по contract; JSON typed; нет deep network probes/секретов |
| C-02 | `doctor`: healthy и каждая отдельная failure | Stable order при concurrent probes; только известные причины; safe repair требует confirm/`--repair --yes` |
| C-03 | `user add valid` TTY/non-TTY | Один user; TTY показывает secret block, non-TTY — безопасный follow-up; persistent sinks clean |
| C-04 | `user add duplicate` | Idempotency R-02 |
| C-05 | `user remove missing` | Exit 4; ни один user/inbound не меняется |
| C-06 | `user remove` prompt/EOF/non-TTY | Default No и EOF cancel; automation без `--yes` не мутирует; wildcard никогда не принимается |
| C-07 | `user list` empty/multiple | Детерминированная сортировка; UUID/URI отсутствуют; JSON стабилен |
| C-08 | `user show` TTY/redirect/JSON | Redirect — ровно URI; JSON `sensitive:true`; stderr/log clean |
| C-09 | `qr` форматы и width | TTY QR сканируется; non-TTY default URI; terminal format в pipe отклоняется без ANSI |
| C-10 | `backup` | Mode не шире 0600; metadata/version; checksum верен; existing destination не overwritten; нет secrets в status/log |
| C-11 | `restore` compatible/incompatible/corrupt | Compatibility до mutation; `--yes`; safety backup; healthcheck; atomic rollback; archive traversal/symlink запрещены |
| C-12 | `update --check` | Read-only; установленный binary/config не меняются |
| C-13 | update success/already current/pinned version | Verify до backup/upgrade; no `latest` token; healthcheck; exit 0; данные сохранены |
| C-14 | update failure на каждом step | Current version остаётся healthy или verified rollback; corrupted download покрывает I-21; rollback failure exit 1 |
| C-15 | uninstall normal/keep-data/remove-backups | Exact scope; default keeps backups; confirmation contract; только owned resources удалены; recovery text точен |
| C-16 | malformed config для каждой read/mutate command | Никакой panic/destructive default; error указывает safe recovery; содержимое/секреты не печатаются |
| C-17 | unknown command/flag/argument | Exit 2, concise usage, никаких side effects |
| C-18 | Все команды с `--output json` | stdout — ровно один JSON document; diagnostics stderr — независимые JSON lines; schema/error codes стабильны |

Для URI/QR E2E проверяется не только формат: конфигурация импортируется в
совместимый Xray client и выполняет probe через развернутый inbound.

## 9. Corrupted-download harness

Нужен локальный HTTPS fault origin с test CA, которому доверяет только VM fixture.
Он должен уметь независимо для bootstrap binary, 3x-ui/Xray artifacts и update:

- вернуть корректный payload и digest;
- изменить один byte после публикации digest;
- truncate на каждом chunk boundary;
- добавить trailing bytes;
- оборвать соединение и успешно ответить при retry;
- вернуть 404/429/500/503 и slow body;
- подменить checksum/signature metadata;
- вернуть redirect на HTTP или неожиданный host.

Проверка порядка делается двумя оракулами: executable bit/final path появляются
только после successful verification, а fake `Runner` запрещает исполнение пути,
не отмеченного verifier. Для update сохраняется digest текущего бинарника; при
любой verification failure он обязан совпасть после команды.

## 10. UI, logging и security regressions

Обязательны golden tests из `docs/terminal-ux.md`: color TTY, `NO_COLOR`, ASCII,
50 columns и non-TTY. Дополнительные assertions:

- non-TTY не содержит `ESC`, `\r`, spinner frames и box glyphs;
- SIGINT/renderer error всегда вызывает `Renderer.Close`;
- concurrent events не interleave;
- long URI сохраняет исходные bytes;
- slow step даёт bounded append-only log, а не spinner spam;
- JSON stdout всегда парсится целиком как один document;
- sentinel scan проходит для human/JSON, normal/verbose и каждого error path;
- secret/config/backup/temp modes проверяются после success, failure и reboot;
- ShellCheck, `go vet`, race detector, vulnerability scan и archive/path fuzzing
  проходят без suppressions, не имеющих зафиксированного обоснования.

Минимальные fuzz targets: user/server name, CIDR/address/destination, config and
checkpoint decoder, backup metadata/archive entries, release metadata, 3x-ui
responses, URI/QR builder и redactor. Оракулы: no panic, bounded allocations,
no path escape, no secret disclosure, no mutation on invalid input.

## 11. Test artifacts и bug workflow

Каждый VM run сохраняет:

- platform/image/build ID и release artifact digests;
- команды с exit codes без secret arguments;
- before/after normalized manifests;
- sanitized vpnctl logs, journal excerpts и serial console;
- systemd state, listening sockets, firewall diff и health result;
- fault point/seed и timings.

Для каждого найденного дефекта обязательны:

1. Минимальный reproducible test или `scripts/qa` scenario с pinned image/fault.
2. Зафиксированные expected/actual result и invariant, который нарушен.
3. Исправление в основном workstream владельца кода.
4. Regression test на самом низком достаточном уровне и, для crash/network/
   ownership багов, VM scenario.

Планируемая инфраструктура после стабилизации CLI и checkpoint interfaces:

```text
tests/
  integration/       public CLI black-box tests
  fixtures/          non-secret configs, archives and release metadata
  golden/            terminal output
  provisioning/      cloud-init and platform matrix definitions
scripts/qa/
  run-platform       create/snapshot/destroy one disposable VM
  run-lifecycle      full public-command gate
  inject-network     controlled origin/proxy faults
  inject-crash       signal/hard-reboot at named checkpoints
  collect-artifacts  normalized manifests and sanitized diagnostics
```

Скрипты не создаются до утверждения фактических CLI flags, paths, ownership
metadata и checkpoint names: иначе они зафиксируют выдуманный интерфейс. Все
будущие destructive scripts обязаны проверять VM identity/tag и отказываться
работать против не-disposable host.
