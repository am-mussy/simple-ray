# vpnctl implementation plan

Date: 2026-08-13. This plan implements the architecture pinned to 3x-ui v3.5.0 and its bundled Xray-core v26.7.11.

## Architecture

`install.sh` is a minimal verified bootstrap. It downloads a concrete vpnctl release for `linux/amd64` or `linux/arm64`, verifies SHA-256 from a versioned manifest, installs `/usr/local/bin/vpnctl` atomically, and attaches `vpnctl install --interactive` to `/dev/tty`.

The statically linked Go CLI owns orchestration, state, firewall intent, backups, health checks and UX. 3x-ui remains the only owner of Xray and its database. vpnctl installs the pinned 3x-ui archive directly, configures panel settings with the supported `x-ui setting` CLI, and performs all inbound/client operations through the tag-matched HTTP API. It never edits SQLite or generated Xray JSON.

## Components

- `cmd/vpnctl`: command parsing, exit codes and dependency wiring.
- `internal/app`: command use cases and transaction coordination.
- `internal/config`: validated state/secrets models and atomic `0600` persistence.
- `internal/xui`: bounded loopback HTTP API and supported CLI bootstrap.
- `internal/installer`: preflight, verified download/extraction, staging and rollback.
- `internal/firewall`: UFW inspection and project-owned rule reconciliation.
- `internal/health`: deterministic status/doctor checks.
- `internal/backup`: versioned archive, digest manifest, restore staging and rollback.
- `internal/ui`: semantic events, TTY/non-TTY rendering, prompts and secret-safe output.

External processes are invoked only through argument arrays behind narrow interfaces. Mutating commands share an exclusive lock and a durable phase journal.

## Threat model and security gates

Trust boundaries are the bootstrap URL, release metadata/artifacts, the root terminal, the local 3x-ui API, subprocesses, archives, filesystem and firewall. The main assets are SSH availability, panel credentials/token, Reality private key, UUID/share URI, backups and rollback snapshots.

Release blockers from `docs/security-audit.md` apply: no unauthenticated bootstrap trust claim, no mutable release inputs, no secrets in argv/logs where avoidable, traversal-safe extraction, loopback panel, deny-by-default firewall preserving SSH, consistent backups, bounded downloads/responses, atomic writes and proven rollback. The second independent audit must close every Critical/High issue before a production release.

## Data storage

```text
/var/lib/vpnctl/state.json       non-secret schema/version/ownership inventory, 0600
/var/lib/vpnctl/secrets.json     local panel API token only, 0600
/var/lib/vpnctl/transactions/    secret-free operation journals, directory 0700
/var/lib/vpnctl/backups/         versioned root-only backups, directory 0700
/var/log/vpnctl/vpnctl.log       allow-listed structured events, 0640
```

Writes use same-directory temporary files, `fsync`, mode/owner validation and atomic rename. Unexpected symlinks, owners or file types fail closed.

## Command interface

```text
vpnctl install [--interactive|--non-interactive --user NAME] [advanced flags]
vpnctl status [--output human|json]
vpnctl user add|remove|list|show ...
vpnctl qr NAME [--format terminal|uri]
vpnctl doctor [--repair --yes]
vpnctl backup [--file PATH]
vpnctl restore BACKUP [--yes]
vpnctl update [--check|--version VERSION] [--yes]
vpnctl uninstall [--keep-data|--remove-backups] [--yes]
```

Exit codes: 0 success/idempotent state, 1 failure, 2 usage, 3 failed precondition, 4 conflict/not found, 5 degraded health, 130 SIGINT. Normal non-interactive install never prints share URIs; `user show` and `qr` are explicit secret-bearing commands.

## Install transaction

1. Acquire lock; validate root, Ubuntu version/architecture, memory/disk, network, public address, ports and existing ownership.
2. Record transaction intent without secrets; generate credentials with `crypto/rand`.
3. Download the exact architecture archive over HTTPS with redirect/size/time limits; verify embedded SHA-256 before extraction.
4. Extract into a root-owned staging directory while rejecting traversal, links, special files and unexpected layout.
5. Install dependencies; stage 3x-ui and its verified Debian systemd unit; preserve the old tree for rollback.
6. Start loopback-only panel, obtain/reuse the API token, create/validate the Reality inbound and first client through official APIs.
7. Reconcile UFW only after preserving detected SSH access; expose only the selected TCP Reality port.
8. Run service, API, effective config, listener, firewall and Reality-target checks.
9. Atomically commit state; otherwise restore binary/config/firewall snapshots and verify rollback.

Repeated install discovers the managed inbound/client, validates compatibility and changes nothing when healthy.

## Upgrade and rollback

Update metadata selects an explicit stable version; `latest`, mutable branches and dev channels are rejected. Download and verification occur before service mutation. Update creates a supported DB backup and local rollback snapshot, stages the complete 3x-ui/Xray artifact, swaps atomically, waits for migrations and runs the full health suite. Failure restores the previous program tree, database, unit and firewall from local verified material. Silent automatic updates are forbidden.

## Backup and restore

Backup obtains a consistent database through `GET /panel/api/server/getDb`, adds vpnctl state/secrets and a metadata/digest manifest, writes a new `0600` archive and never overwrites. MVP backups are root-only and explicitly documented as containing secrets; authenticated portable encryption is required before describing off-host backups as secure.

Restore accepts only a local regular file, caps size/count/depth/ratio, rejects links/special files/traversal/duplicates, verifies manifest and compatibility before live mutation, takes a safety backup, uses `POST /panel/api/server/importDB`, health-checks and rolls back on failure.

## Testing strategy

- Unit: validation, CSPRNG formats, atomic storage, redaction, response envelopes, payload fixtures, archive rejection, renderer profiles and state transitions.
- Integration with fakes: every command, retry/idempotency, interrupted transactions, full disk, occupied ports, network corruption, API failures and rollback.
- Contract: tag-matched v3.5.0 API fixtures and real disposable 3x-ui instance.
- Provisioning: clean Ubuntu 22.04/24.04/26.04 on amd64/arm64; install, reboot, status/doctor, create/delete, backup/restore, repeat install, update rollback and uninstall.
- Security: ShellCheck, `go test`, race tests, `go vet`, `govulncheck`, action/dependency pin review, canary-secret scans, malicious archives and external IPv4/IPv6 port scans.

The local Windows build proves compilation and unit behavior only. Definition of Done additionally requires disposable supported VPS/VM runs and an independent merged-code security/QA audit.
