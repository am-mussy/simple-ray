Ты — lead engineer проекта по созданию максимально простого, безопасного и визуально качественного open-source installer/CLI для автоматической настройки VPN-сервера на базе **3x-ui + Xray + VLESS TCP Reality**.

Главная цель продукта:

Пользователь покупает чистый VPS, подключается по SSH и выполняет одну команду:

```bash
curl -fsSL https://<DOMAIN>/install.sh | sudo bash
```

После этого он проходит красивую интерактивную установку и получает полностью настроенный сервер, готовые VLESS-конфигурации и QR-коды.

Проект предназначен в том числе для людей, которые практически не знакомы с Linux.

---

# 1. ОБЯЗАТЕЛЬНО ИСПОЛЬЗУЙ СУБАГЕНТОВ

Не выполняй весь проект последовательно самостоятельно.

Ты выступаешь как **lead / orchestrator**.

Создай несколько независимых субагентов и максимально распараллель работу.

Минимально нужны следующие роли.

## Agent 1 — Architecture / Research

Задачи:

- изучить актуальную официальную документацию:
  - Xray-core;
  - 3x-ui;
  - используемые API/CLI;

- определить наиболее стабильный способ автоматической конфигурации 3x-ui;
- определить правильную архитектуру installer + CLI;
- проверить поддержку Ubuntu 22.04/24.04;
- определить необходимые system dependencies;
- определить способ генерации Reality keys, UUID, shortId;
- определить способ создания inbound и clients;
- выявить потенциально нестабильные API или внутренние implementation details.

Не использовать прямое редактирование SQLite database 3x-ui, если существует поддерживаемый API/CLI.

Результат:

`docs/architecture.md`

---

## Agent 2 — Core Development

Отвечает за основную реализацию.

Нужно реализовать:

```text
vpnctl install
vpnctl status

vpnctl user add <name>
vpnctl user remove <name>
vpnctl user list
vpnctl user show <name>

vpnctl qr <name>

vpnctl doctor

vpnctl backup
vpnctl restore

vpnctl update
vpnctl uninstall
```

Команды должны иметь:

- нормальные exit codes;
- понятные ошибки;
- idempotency;
- structured logging;
- безопасную работу с filesystem;
- обработку interrupted installation.

Повторный запуск команды не должен ломать существующую установку.

---

## Agent 3 — Security Engineer

Этот агент НЕ должен писать основную функциональность.

Его задача — независимо провести security review.

Использовать threat-model подход.

Проверить минимум:

### Secrets

- admin credentials;
- UUID;
- Reality private key;
- shortId;
- backup;
- временные файлы.

Secrets:

- не должны попадать в logs;
- не должны попадать в shell history;
- должны иметь корректные permissions;
- должны генерироваться через cryptographically secure RNG.

### Installer security

Отдельно проверить риски конструкции:

```bash
curl | bash
```

По возможности предложить более безопасный пользовательский путь с:

- HTTPS;
- release artifacts;
- SHA256;
- pinned versions;
- signed releases/checksums, если это оправдано.

### Network

Проверить:

- exposed ports;
- 3x-ui panel;
- firewall;
- panel API;
- brute force;
- unnecessary services;
- IPv4/IPv6;
- fail2ban;
- default credentials.

Применить принцип:

> deny by default

### Supply-chain

Запрещено слепо использовать:

```text
latest
```

Версии основных компонентов должны pin-иться.

Проверять скачиваемые artifacts.

### Shell

Для shell scripts:

```bash
set -Eeuo pipefail
```

Проверить:

- command injection;
- unsafe variable expansion;
- word splitting;
- TOCTOU;
- unsafe temp files;
- permissions;
- symlink attacks.

Использовать ShellCheck.

### Итог

Создать:

`SECURITY.md`

и:

`docs/security-audit.md`

Security-agent должен после завершения основной реализации провести **второй независимый аудит готового проекта**.

Critical/High проблемы должны быть исправлены до завершения задачи.

---

## Agent 4 — Installation UX / Terminal UI

Это отдельный полноценный workstream.

Установка должна выглядеть как законченный современный CLI-продукт, а не как набор:

```text
echo "Installing..."
echo "Done"
```

Продумай качественную Terminal UX.

Пример ощущения:

```text
╭────────────────────────────────────────────╮
│                                            │
│              VPNCTL                       │
│                                            │
│     Secure server setup                   │
│                                            │
╰────────────────────────────────────────────╯

  System

  ✓ Ubuntu 24.04 LTS
  ✓ amd64
  ✓ 2 GB RAM
  ✓ Public IP detected

  Installing

  ✓ Dependencies
  ✓ 3x-ui
  ✓ Xray
  ✓ Firewall
  ✓ Reality
  ✓ First user

  Running health checks...

  ✓ Xray service
  ✓ 3x-ui service
  ✓ inbound listening
  ✓ firewall
  ✓ configuration

╭────────────────────────────────────────────╮
│ Installation complete                     │
╰────────────────────────────────────────────╯
```

Использовать:

- spinners;
- progress states;
- Unicode;
- аккуратные ANSI colors;
- таблицы;
- отступы;
- понятную hierarchy;
- ✓ success;
- ! warning;
- ✗ errors.

Но обязательно:

- корректный fallback без ANSI;
- поддержка `NO_COLOR`;
- работа в non-interactive terminal;
- нормальный вывод через SSH;
- отсутствие визуального мусора в logs.

### Interactive wizard

Установка должна максимально уменьшить количество вопросов.

Пример:

```text
Choose setup:

❯ Recommended
  Advanced
```

Recommended должен работать практически без ручных настроек.

При необходимости спросить:

```text
Server name:
> Amsterdam

Create first VPN user:
> mustafa
```

Остальное генерировать автоматически.

### Финальный экран

После установки красиво показать:

```text
Server
────────────────────────

Status       ● ONLINE
IP           1.2.3.4
Protocol     VLESS Reality
Users        1
```

Затем:

```text
Client: mustafa

█████████████████
████ QR CODE ████
█████████████████

vless://...
```

Sensitive information не должна автоматически попадать в persistent logs.

---

## Agent 5 — QA / Destructive Testing

Этот агент должен пытаться **сломать installer**.

Проверить сценарии:

- чистый Ubuntu 22.04;
- чистый Ubuntu 24.04;
- повторный `vpnctl install`;
- недостаточно RAM;
- заполненный диск;
- порт занят;
- Xray уже установлен;
- 3x-ui уже установлен;
- firewall уже существует;
- отсутствует IPv6;
- сервер IPv6-only;
- network temporary failure;
- GitHub unavailable;
- download corrupted;
- installation interrupted;
- SIGINT;
- reboot во время/после установки;
- повторная установка после failed install;
- user с одинаковым именем;
- удаление nonexistent user;
- malformed configuration.

Для каждого найденного бага:

1. создать reproduction;
2. исправить;
3. добавить regression test.

---

# 2. ТЕХНИЧЕСКАЯ АРХИТЕКТУРА

Предпочтительная архитектура:

```text
install.sh
    ↓
downloads verified release
    ↓
/usr/local/bin/vpnctl
```

`install.sh` должен быть максимально маленьким.

Он НЕ должен содержать всю бизнес-логику установки VPN.

Основная логика должна жить в `vpnctl`.

Выбери технологию CLI самостоятельно после анализа.

Предпочтительно:

**Go**

из-за:

- single static binary;
- отсутствия Node/Python runtime dependency;
- удобной cross-platform compilation;
- хорошего terminal ecosystem.

Если выбрана другая технология — аргументируй это в ADR.

---

# 3. MVP

Поддерживаем:

```text
Ubuntu 22.04 LTS
Ubuntu 24.04 LTS

amd64
arm64
```

Первоначальный protocol:

```text
VLESS
TCP
REALITY
```

Не пытайся сразу поддерживать десятки transport/protocol combinations.

---

# 4. INSTALL

Команда:

```bash
vpnctl install
```

должна:

1. проверить root;
2. определить OS;
3. определить architecture;
4. проверить RAM/disk;
5. проверить connectivity;
6. определить public IP;
7. установить dependencies;
8. установить pinned 3x-ui;
9. установить/configure Xray;
10. создать secure panel credentials;
11. создать случайный panel port;
12. создать random webBasePath;
13. создать Reality keypair;
14. создать shortId;
15. создать UUID;
16. создать VLESS Reality inbound;
17. создать первого пользователя;
18. настроить firewall;
19. запустить services;
20. выполнить полный healthcheck;
21. показать client configuration;
22. показать QR code.

---

# 5. SECURITY DEFAULTS

По умолчанию выбирай наиболее безопасное разумное поведение.

Например:

- cryptographically secure random;
- least privilege;
- chmod 600 secrets;
- atomic writes;
- temporary files через безопасные API;
- firewall deny-by-default;
- минимальное количество exposed ports;
- никакого default password;
- никакого hardcoded secret;
- никакого telemetry;
- никакой отправки данных разработчику.

Panel не должна без необходимости торчать в интернет в небезопасной конфигурации.

---

# 6. UPDATE SYSTEM

Продумай:

```bash
vpnctl update
```

Обновления должны:

1. проверить новую версию;
2. скачать artifact;
3. проверить checksum/signature;
4. сделать backup;
5. выполнить upgrade;
6. выполнить healthcheck;
7. rollback при критической ошибке.

Никаких silent auto-updates.

---

# 7. BACKUP

```bash
vpnctl backup
```

должен создавать переносимый backup необходимых данных.

Backup должен:

- иметь понятную структуру;
- не иметь world-readable permissions;
- иметь version metadata.

Restore:

```bash
vpnctl restore backup.tar...
```

Перед восстановлением должна выполняться проверка совместимости.

---

# 8. DOCTOR

Мне особенно нужна команда:

```bash
vpnctl doctor
```

Она должна быть полезна человеку без Linux-опыта.

Например:

```text
VPNCTL Doctor

System
  ✓ OS supported
  ✓ disk space
  ✓ memory

Network
  ✓ internet access
  ✓ public IP
  ✓ port 443 reachable

Services
  ✓ 3x-ui
  ✓ Xray

Configuration
  ✓ Reality keys
  ✓ inbound
  ✓ firewall

Result:

Everything looks good.
```

При проблеме выводить:

```text
✗ Xray is not running

Reason:
configuration validation failed

Suggested action:

vpnctl repair xray
```

Если безопасно — предложить автоматический repair.

---

# 9. TESTING

Нужны:

- unit tests;
- integration tests;
- ShellCheck;
- lint;
- security scanning;
- GitHub Actions.

По возможности создать автоматические provisioning tests.

Ideal:

```text
fresh VM
   ↓
install
   ↓
healthcheck
   ↓
create user
   ↓
delete user
   ↓
backup
   ↓
restore
   ↓
uninstall
```

Для integration тестов использовать disposable environments, где это возможно.

---

# 10. PROJECT STRUCTURE

Примерно:

```text
vpnctl/

├── cmd/
├── internal/
│   ├── installer/
│   ├── xray/
│   ├── xui/
│   ├── firewall/
│   ├── users/
│   ├── backup/
│   ├── health/
│   └── ui/
│
├── scripts/
│   └── install.sh
│
├── tests/
│
├── docs/
│   ├── architecture.md
│   ├── security-audit.md
│   └── troubleshooting.md
│
├── AGENTS.md
├── SECURITY.md
├── CONTRIBUTING.md
├── README.md
└── LICENSE
```

Не воспринимай эту структуру как обязательную, если можешь предложить лучше.

---

# 11. README

README должен быть ориентирован в первую очередь на пользователя, а не разработчика.

Начало должно выглядеть примерно:

```markdown
# vpnctl

Deploy your own Xray VPN server in one command.

## Install

\`\`\`bash
curl -fsSL https://... | sudo bash
\`\`\`
```

Дальше:

- screenshot/terminal demo;
- supported systems;
- installation;
- add user;
- QR;
- backup;
- troubleshooting;
- security;
- uninstall.

---

# 12. DEVELOPMENT PROCESS

Работай фазами.

## Phase 1 — Research

Запусти research/architecture/security субагентов параллельно.

Не начинай большую реализацию, пока не собрана архитектура.

---

## Phase 2 — Design

Создай:

```text
IMPLEMENTATION_PLAN.md
```

В нем:

- architecture;
- components;
- threat model;
- data storage;
- command interface;
- upgrade strategy;
- rollback strategy;
- testing strategy.

---

## Phase 3 — Parallel implementation

После утверждения архитектуры распараллель:

- core CLI;
- 3x-ui integration;
- Xray integration;
- terminal UI;
- installer/bootstrap;
- testing infrastructure.

Субагенты должны по возможности работать в независимых областях проекта.

---

## Phase 4 — Integration

Lead-agent:

- объединяет изменения;
- проверяет interfaces;
- запускает весь test suite;
- исправляет integration issues.

---

## Phase 5 — Red Team

Security-agent и QA-agent получают готовую реализацию.

Их задача:

**не подтвердить, что всё хорошо, а найти максимальное количество проблем.**

После этого исправить найденное.

---

## Phase 6 — Polish

Отдельно провести UX review установки.

Критерий:

человек, который впервые открыл терминал, должен понимать:

- что сейчас происходит;
- произошла ли ошибка;
- что делать дальше;
- где взять VPN-конфигурацию.

---

# 13. ВАЖНЫЕ ОГРАНИЧЕНИЯ

Не:

- редактировать внутреннюю DB 3x-ui без крайней необходимости;
- использовать undocumented internals без документации риска;
- использовать `latest`;
- отключать firewall;
- использовать слабые passwords;
- писать secrets в logs;
- подавлять ошибки через `|| true` без обоснования;
- игнорировать failed commands;
- использовать небезопасный shell interpolation;
- делать destructive actions без проверки.

---

# 14. DEFINITION OF DONE

Задача считается завершенной только если:

на чистом поддерживаемом VPS работает:

```bash
curl -fsSL https://.../install.sh | sudo bash
```

и после установки:

```bash
vpnctl status
vpnctl doctor
vpnctl user add test
vpnctl qr test
vpnctl backup
```

работают успешно.

Также:

- после reboot VPN продолжает работать;
- повторный install ничего не ломает;
- security audit не содержит Critical/High;
- tests проходят;
- installation UX выглядит как законченный продукт;
- документация позволяет установить проект человеку без опыта администрирования Linux.

---

# 15. НАЧАЛО РАБОТЫ

Начни сейчас.

Сначала:

1. создай/запусти независимых субагентов для:
   - research/architecture;
   - security;
   - installation UX;
   - QA/test strategy;

2. параллельно изучи существующий repository;

3. собери результаты;

4. создай `IMPLEMENTATION_PLAN.md`;

5. затем самостоятельно продолжай реализацию по этому плану.

Не останавливайся после создания плана.

После планирования переходи к реализации, тестированию и исправлению найденных проблем.

Не считай задачу выполненной только потому, что код компилируется.

Цель — **реально работающий, безопасный и визуально отличный installer, который не стыдно дать тысячам пользователей после YouTube-видео.**
