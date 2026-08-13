# Terminal UX specification

This document is the product contract for `vpnctl` terminal output. It covers the
interactive installer, automation, accessibility, secret handling, and every MVP
command. User-facing copy is English in the MVP.

## Principles

1. A first-time Linux user must always know what is happening, whether it failed,
   and what to run next.
2. Secure defaults require fewer questions than insecure customization.
3. Color and Unicode improve scanning, but never carry meaning by themselves.
4. Human UI, machine output, and persistent logs are separate channels.
5. A repeated command describes the existing state and makes only the changes
   required; it does not replay a misleading installation animation.
6. The UI uses normal terminal flow. It does not enter the alternate screen, hide
   the cursor for the whole operation, or require mouse support.

## Output channels and modes

| Channel | Content | Secrets |
|---|---|---|
| `stdout` | Command result, table, or JSON requested by the caller | Only for commands that explicitly request a client configuration: `user show` and `qr` |
| `stderr` | Progress, prompts, warnings, and actionable errors | Never |
| Persistent log | Timestamped operation and diagnostic events | Never; fields are allow-listed and redacted before serialization |
| `/dev/tty` | Interactive prompts and the install completion secret block | Allowed only for the current session |

Progress must not corrupt piped data. For example,
`vpnctl user list --output json | jq .` emits one JSON document on `stdout`; any
diagnostic is emitted on `stderr`.

### Mode detection

Interactive mode is enabled only when all of the following are true:

- no `--non-interactive` flag is present;
- a controlling terminal can be opened;
- input and the human-output stream are terminals;
- `--output json` is not selected.

`--interactive` may explicitly request the wizard, but fails with an actionable
error when no controlling terminal exists. It must never wait on redirected stdin.

The `curl -fsSL .../install.sh | sudo bash` bootstrap has piped stdin. After it
downloads and verifies `vpnctl`, it must open `/dev/tty` and launch the equivalent
of `vpnctl install --interactive </dev/tty >/dev/tty 2>/dev/tty`. The bootstrap
must not implement a second, simpler installation UI: the verified binary owns the
banner, wizard, progress, and completion screen. If `/dev/tty` cannot be opened,
the bootstrap prints a non-interactive command with required flags and exits with
code 3. It must not silently choose names or accept destructive defaults.

### Rendering profiles

| Condition | Profile |
|---|---|
| TTY, UTF-8, color supported | Spinner, ANSI color, Unicode symbols and rules |
| TTY with `NO_COLOR`, `--color never`, or `TERM=dumb` | No ANSI; Unicode only if UTF-8 is supported |
| Non-TTY | ASCII, one append-only line per state transition; no carriage returns, cursor movement, animation, boxes, or ANSI |
| Non-UTF-8 terminal | ASCII symbols and borders |
| Terminal narrower than 60 columns | No outer boxes; key/value rows wrap with a two-space continuation indent |

`NO_COLOR` is honored when present with a non-empty value. Precedence is explicit
`--color` > `NO_COLOR` > terminal detection. `CLICOLOR_FORCE` is not used. Color
policy is `auto`, `always`, or `never`; `always` is rejected for `--output json`.

Unicode fallbacks are deterministic:

| Meaning | Unicode | ASCII |
|---|---:|---:|
| Success | `✓` | `OK` |
| Warning | `!` | `WARN` |
| Failure | `✗` | `ERROR` |
| Running | spinner frame | `...` |
| Online | `● ONLINE` | `ONLINE` |
| Rule | `────` | `----` |

Green is used for success, yellow for warnings, red for failures, cyan for the
active step, and dim text for secondary information. Every colored item also has
a word or symbol. Background colors are not used.

### Global flags

```text
--output human|json       Result format; default human
--color auto|always|never Color policy; default auto
--log-format text|json    Diagnostic stderr format; default text
--quiet                   Suppress successful progress, not warnings or errors
--verbose                 Add safe diagnostic context; never reveal secrets
--non-interactive         Never prompt; missing required input is an error
--interactive             Require a controlling terminal and allow prompts
--yes                     Confirm the exact operation already named by the command
```

`--quiet` and `--verbose` are mutually exclusive. JSON output is a single stable
document on `stdout`. JSON diagnostic logging is JSON Lines on `stderr`. Prompts,
spinners, banners, and ANSI are disabled for both JSON streams. Unknown fields may
be added to JSON, but existing fields do not change meaning within a major version.

## Interactive install wizard

The wizard uses arrow keys when available and also accepts `1`, `2`, and Enter.
Enter always selects the displayed default. Ctrl+C restores the current line,
prints `Installation cancelled. No active step was left running.`, and exits 130.

### Welcome and setup choice

```text
╭────────────────────────────────────────────╮
│                  VPNCTL                    │
│          Secure server setup              │
╰────────────────────────────────────────────╯

Choose setup

❯ Recommended   Secure defaults; about 2 minutes
  Advanced      Choose network and panel settings

↑/↓ move  •  enter select  •  ctrl+c cancel
```

Do not show the banner for routine commands, non-TTY output, or a resumed install.

### Recommended path

Recommended asks one question:

```text
Create your first VPN user
Name [vpn]: mustafa
```

The server display name is derived from the hostname and may be changed later.
The generated client name is validated as the user types: 1–32 characters,
letters, numbers, `_`, and `-`; it is normalized only by trimming surrounding
spaces. The UI never silently changes the submitted name.

Recommended automatically selects:

- the detected public address;
- a secure supported Reality destination after a connectivity check;
- the standard VLESS listener selected by the installer policy;
- generated UUID, Reality key pair, short ID, panel credentials, privileged loopback panel port, and
  web base path;
- a local-only 3x-ui panel, reachable through an SSH tunnel;
- deny-by-default firewall rules while preserving the detected SSH connection.

Before mutation, show a compact plan and require one confirmation:

```text
Ready to install

  Server       vps-ams-01
  VPN user     mustafa
  Protocol     VLESS TCP Reality
  Admin panel  Local only (SSH tunnel)

Install now? [Y/n]
```

Thus the normal path is: Enter, type a user name, Enter, Enter.

### Advanced path

Advanced asks only values that change behavior, in this order:

1. Server display name, defaulting to the hostname.
2. First VPN user name.
3. Public address, defaulting to the detected address.
4. VLESS listen port, defaulting to the installer policy.
5. Reality server name and destination, defaulting to a validated recommended
   pair and displayed as one choice.
6. Panel access: `Local only (recommended)` or `Allow one trusted CIDR`.
7. Trusted CIDR, only when the second panel option is selected.
8. SSH port, only when it cannot be detected reliably.

Generated credentials and cryptographic material are not editable wizard fields.
Values are validated immediately, with the previous answers preserved. The final
plan explicitly lists ports and firewall changes before confirmation. If the
current SSH connection could be blocked, installation stops rather than offering
a casual override.

### Non-interactive install

Recommended automation:

```bash
sudo vpnctl install \
  --non-interactive \
  --mode recommended \
  --user mustafa
```

Supported install flags:

```text
--mode recommended|advanced
--server-name <label>
--user <name>                    Required in non-interactive mode
--public-address <IPv4-or-IPv6>
--listen-port <1-65535>
--reality-server-name <name>
--reality-destination <host:port>
--panel-access local|cidr
--panel-cidr <CIDR>              Required when panel-access=cidr
--ssh-port <1-65535>             Override only when detection is unavailable
```

`--mode advanced` is inferred when any advanced network flag is supplied, but an
explicit contradictory mode is a usage error. Secrets have no flags because
command lines are commonly captured by shell history and process inspection.

In non-interactive mode the generated URI and QR are not printed as part of
`install`; the result tells the operator to run `sudo vpnctl qr <name>`. This keeps
automation logs secret-free. The client configuration remains in root-readable
storage.

## Installation progress

Preflight appears before mutation. Each check resolves in place on a TTY and is an
append-only event outside a TTY.

```text
System

  ✓ Ubuntu 24.04 LTS
  ✓ amd64
  ✓ 2.0 GB RAM
  ✓ 18 GB disk available
  ✓ Public IPv4 detected

Installing

  ✓ System dependencies                         4s
  ✓ 3x-ui 2.x.y                                 8s
  ✓ Xray 2x.y.z                                 2s
  ✓ Firewall                                    1s
  ⠹ Creating Reality configuration
  · First VPN user
```

Only the current row animates. Pending rows use `·`; completed durations are
rounded and dim. A step taking longer than 10 seconds adds a changing, safe detail
such as `Downloading (12 MB / 31 MB)` or `Waiting for service (18s)`. It never
prints raw subprocess output during normal operation. `--verbose` prints sanitized
subprocess summaries below the active step.

The phase names are stable:

1. `Checking system`
2. `Preparing installation`
3. `Installing components`
4. `Configuring VPN`
5. `Securing network`
6. `Starting services`
7. `Running health checks`

On a resumed run, previously committed work is shown as `✓ Already configured`.
If an existing healthy installation is detected, the command prints its version
and `Nothing to change.` without regenerating any secret.

Signals are handled at a safe boundary. The active row becomes `! Interrupted`,
then the UI says whether rollback completed or whether `vpnctl install` will resume
from a recorded checkpoint. It must never claim rollback unless it succeeded.

## Completion screen

The server summary is always safe to display. The client block is written only to
the controlling TTY during interactive installation, never through the logger.

```text
╭────────────────────────────────────────────╮
│          Installation complete             │
╰────────────────────────────────────────────╯

Server
──────────────────────────────────────────────
Status       ● ONLINE
Address      203.0.113.10
Protocol     VLESS TCP Reality
Users        1
Panel        Local only

Client: mustafa
──────────────────────────────────────────────

█████████████████████████
██ ▄▄▄▄▄ █▀▄█ ▄▄▄▄▄ ██
██ █   █ ▄▀ █ █   █ ██
██ █▄▄▄█ █▄▀█ █▄▄▄█ ██
█████████████████████████

vless://…

Next steps
  1. Scan the QR code in your VPN client.
  2. Add another user:  sudo vpnctl user add <name>
  3. Check the server:  sudo vpnctl doctor

Show this QR again: sudo vpnctl qr mustafa
```

The URI must not wrap into multiple visually ambiguous lines. If it exceeds the
terminal width, print it on the following line without inserting bytes, allowing
the terminal itself to wrap. Clipboard integration is not attempted over SSH.
Panel credentials are not shown unless a future command explicitly requests them.

If final health checks are degraded, do not show `Installation complete` or the
client secret block. Show `Installation needs attention`, failed checks, rollback
state, and the exact `vpnctl doctor` command instead.

## Error contract

Errors identify the failed thing, give a plain-language reason, and provide one
safe next action. Raw Go errors, stack traces, HTTP bodies, and secret-bearing
commands are verbose/debug artifacts, not primary copy.

```text
✗ VPN port 443 is already in use

  Used by      nginx (PID 742)
  Why it matters
    Xray cannot listen on the selected port.

  Suggested action
    Stop the conflicting service, or run:
    sudo vpnctl install --listen-port 8443

  Details were saved to /var/log/vpnctl/vpnctl.log
  Error code: PORT_IN_USE
```

The machine form is:

```json
{
  "ok": false,
  "error": {
    "code": "PORT_IN_USE",
    "message": "VPN port 443 is already in use",
    "hint": "Stop the conflicting service or select another port"
  }
}
```

Stable process exit codes:

| Code | Meaning |
|---:|---|
| 0 | Requested state reached; warnings may be present in the result |
| 1 | Operation failed |
| 2 | Invalid command, flag, argument, or input |
| 3 | Precondition failed or environment unsupported |
| 4 | Named resource not found or conflicts with existing state |
| 5 | Operation completed but service health is degraded |
| 130 | Cancelled by SIGINT |

Domain error codes such as `PORT_IN_USE`, `NETWORK_UNAVAILABLE`, and
`CHECKSUM_MISMATCH` are more specific and appear in both human and JSON output.
They are uppercase snake case and stable within a major version.

## Command UX

### `vpnctl status`

Fast and read-only. It does not display the banner or run deep network probes.

```text
Server
──────────────────────────────────────────────
Status       ● ONLINE
Address      203.0.113.10
Protocol     VLESS TCP Reality
Users        3
Xray         running
3x-ui        running (local only)
Version      vpnctl 0.1.0

Last checked just now
```

Healthy exits 0, degraded exits 5, and unreadable/missing installation exits 1 or
4. JSON returns typed service states, never a formatted status string.

### `vpnctl user add <name>`

Validates the name before mutation, shows one short progress row, and on an
interactive terminal displays the same client QR and URI block as installation.
Non-TTY output prints a safe success result and the `vpnctl qr <name>` follow-up.
`--show` explicitly allows the URI/QR on redirected human output; it is rejected
with `--output json` in favor of `user show --output json`.

Adding an existing user is idempotent: it reports `User "name" already exists. No
changes made.` and does not rotate the UUID. Exit 0 when the existing user is
healthy; exit 4 only when requested flags conflict with that user.

### `vpnctl user remove <name>`

Shows the exact name and asks `Remove VPN user "name"? [y/N]`. In automation,
`--yes` is required. It never accepts a wildcard. A missing user reports a
not-found error and exits 4. Success includes the remaining user count.

### `vpnctl user list`

```text
VPN users (3)

NAME       STATUS    CREATED
mustafa    active    2026-08-13
phone      active    2026-08-13
tablet     disabled  2026-07-29
```

Rows sort by name for deterministic human and JSON output. UUIDs and URIs are not
listed. Empty state: `No VPN users. Add one with: sudo vpnctl user add <name>`.

### `vpnctl user show <name>`

This command is an explicit request for one client's secret configuration. Human
TTY output shows metadata, QR, and URI. Redirected output is permitted and emits
the URI without decoration so `vpnctl user show name > client.txt` is useful and
deterministic. JSON includes the URI and carries `"sensitive": true`. Nothing from
this result is copied to persistent logs or diagnostic stderr.

### `vpnctl qr <name>`

TTY default is a terminal QR sized to available columns. Non-TTY default is the
plain URI, because terminal QR block art is unsafe in logs and pipes. Flags:

```text
--format terminal|uri
--compact                 Use a smaller two-row-per-cell terminal QR
```

`--format terminal` on a non-TTY fails with a hint to use `--format uri`; it never
emits ANSI escape codes into a file. The command is explicitly secret-bearing.

### `vpnctl doctor`

Doctor groups checks by System, Network, Services, and Configuration. Checks run
concurrently where safe but render in a stable order.

```text
VPNCTL Doctor

System
  ✓ Ubuntu 24.04 is supported
  ✓ Disk space: 18 GB available
  ✓ Memory: 2.0 GB

Network
  ✓ Internet access
  ✓ Public address: 203.0.113.10
  ✗ VPN port 443 is not reachable

Services
  ✓ 3x-ui is running
  ✓ Xray is running

Configuration
  ✓ Reality keys are valid
  ✓ VLESS inbound is configured
  ✓ Firewall policy is active

Result
  8 passed, 1 failed

Suggested action
  Check the provider firewall, then run:
  sudo vpnctl doctor
```

A failed check includes `Reason` only when known. The UI does not invent a cause.
Safe repairs are offered as `Run safe repairs now? [y/N]`; the corresponding
automation is `vpnctl doctor --repair --yes`. Repairs that rotate credentials,
change public ports, remove data, or risk SSH access are never classified as safe.

### `vpnctl backup`

Default file name is deterministic enough to identify but never overwrites:
`vpnctl-backup-YYYYMMDD-HHMMSS.tar.gz`. `--file <path>` selects a destination.
Progress lists safe categories, not file contents. Success prints the absolute
path, size, format version, and SHA-256 checksum. If stdout is JSON, metadata is
JSON; archive bytes are never mixed with status output. Existing destinations
fail rather than prompt to overwrite.

### `vpnctl restore <backup>`

First renders compatibility, source version, creation time, and the state that
will be replaced. Interactive mode asks `Restore this backup? [y/N]`.
Non-interactive restore requires `--yes`. The command creates a safety backup,
restores, runs health checks, and reports rollback if checks fail. It never prints
archive contents, credentials, or client URIs.

### `vpnctl update`

`vpnctl update --check` is read-only. A normal update shows current and target
versions, release source, backup intent, and asks `Update to vX.Y.Z? [y/N]`.
`--version <semver>` selects a published version; `latest` is not accepted as a
version token. Non-interactive update requires `--yes`.

Progress states are `Download`, `Verify signature/checksum`, `Backup`, `Upgrade`,
`Health checks`, and, if needed, `Rollback`. `Already up to date.` exits 0. A
failed upgrade reports whether rollback succeeded; failed rollback is visually
prominent and exits 1.

### `vpnctl uninstall`

The destructive scope is listed before confirmation: services, firewall rules,
configuration, users, and whether backups are retained. The default keeps backup
archives. Flags are `--keep-data` and `--remove-backups`; the latter requires
`--yes` even in an interactive terminal. The ordinary prompt is
`Uninstall vpnctl and stop the VPN? [y/N]`. Non-interactive mode requires `--yes`.
Success states what remains and how to recover it. It does not delete unrelated
Xray, firewall, or 3x-ui resources that vpnctl does not own.

## Prompt and validation behavior

- Defaults appear in brackets and Enter accepts them.
- Invalid input is explained directly below the prompt; the user is not sent back
  to the first screen.
- Passwords or other future secret input use no echo and support confirmation.
- Pasted whitespace is trimmed only at the edges.
- EOF behaves like cancellation, not confirmation.
- Destructive prompts default to No. Install's final confirmation defaults to Yes
  because the user has just reviewed a non-destructive plan and invoked install.
- `--yes` confirms prompts but never supplies missing values or bypasses validation.

## Logging and redaction

The logger uses an allow-list. It may record operation ID, command name, phase,
component version, duration, result, stable error code, OS, architecture, and safe
network metadata. It must not serialize whole config objects or arbitrary command
output.

Always redact:

- VLESS URIs, UUIDs, Reality private keys, short IDs, panel credentials, cookies,
  authorization headers, query strings, and backup contents;
- full subprocess command lines when arguments may contain credentials;
- request/response bodies from 3x-ui;
- terminal prompt input not explicitly classified as public.

Error wrapping must preserve the redacted public message separately from the
private cause. Redaction is applied before both text and JSON formatting. Tests
should use sentinel secrets and assert their absence from stdout diagnostics,
stderr, and the persistent log for every non-secret-bearing command.

## Implementation boundary for `internal/ui`

Business logic must publish semantic events instead of ANSI strings:

```go
type Event struct {
    Phase    string
    Step     string
    State    State
    Detail   string
    Duration time.Duration
}
```

The UI layer owns terminal detection, symbols, color, width-aware tables, spinner
lifecycle, and prompt rendering. Commands own copy and domain error codes. The
logger consumes sanitized events independently; it must never receive the
completion secret block.

A small renderer interface is sufficient:

```go
type Renderer interface {
    Banner(title, subtitle string)
    Section(title string)
    Event(Event)
    Table([]Column, [][]string)
    Notice(Severity, string)
    Close() error
}
```

`Close` finalizes an active spinner and restores terminal state. Rendering is
serialized through one goroutine so concurrent health checks cannot interleave
lines. ANSI and spinner timing use injectable writer/clock abstractions for
deterministic tests. Prompting is a separate interface so non-interactive command
paths cannot accidentally read stdin.

No terminal library should introduce a full-screen TUI. A small ANSI renderer plus
robust TTY/width detection is enough; dependency choice belongs to the core
architecture decision.

## UX acceptance tests

1. Golden output for color TTY, `NO_COLOR`, ASCII, 50-column TTY, and non-TTY.
2. Captured non-TTY output contains no `\x1b`, `\r`, spinner frames, or box glyphs.
3. JSON stdout parses as exactly one document while JSON diagnostics parse as
   independent lines on stderr.
4. All prompts reject EOF and invalid input without mutating state.
5. SIGINT finalizes the active line and exits 130.
6. A slow step updates one TTY row but produces bounded append-only log events.
7. Sentinel secrets never reach diagnostics or persistent logs.
8. Repeated install reports existing state without rotating user configuration.
9. Restore, update, user removal, and uninstall cannot proceed non-interactively
   without explicit confirmation.
10. `curl | sudo bash` uses `/dev/tty`; absence of a controlling terminal fails
    immediately with a complete non-interactive command example.
