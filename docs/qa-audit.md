# Phase 5 QA / destructive audit

Audit date: **2026-08-13**. Scope: the complete repository state available at
the end of Phase 5, including Go packages, shell bootstrap, tests, CI, release
binaries and documentation.

## Verdict

The repository is **not releasable and does not meet the Definition of Done**.
The reachable management subset has no known open Critical/High issue after the
red-team fixes. However, `install`, `restore`, `update` and `uninstall`
intentionally fail closed. Therefore the advertised one-command deployment and
full lifecycle do not exist in this build.

The fail-closed commands are recorded as product/DoD gaps, not as passing
security features. The bootstrap currently verifies and installs the `vpnctl`
binary, then invokes an unavailable `install` command and exits non-zero.

## Executed checks

| Check | Result | Evidence / limitation |
|---|---|---|
| Complete source/test/script/doc review | Completed | All files returned by `rg --files` inspected; privileged paths, API, archives, state, locks and CLI traced |
| `go test ./...` before red-team regressions | Passed | All original packages passed on Windows amd64 |
| New regression suite | Passed locally | `go test ./...` passes after the red-team fixes; Linux-only permission assertion remains for CI |
| `go vet ./...` | Passed | Local Go 1.24 toolchain |
| Race detector | Not run | Local environment has no C compiler; `-race` failed before build with missing `gcc` |
| ShellCheck | Not run locally | `shellcheck` is unavailable; pinned CI job includes it but no CI run was observed |
| Bootstrap shell test | Not run locally | Available `bash.exe` is the Windows WSL launcher without a usable Linux environment; CI definition runs `scripts/install_test.sh` |
| Cross-build | Reviewed, not rerun by QA | CI and Makefile contain linux/amd64 and linux/arm64 builds; runtime execution not performed |
| Real 3x-ui v3.5.0 contract | Static verification only | Adapter compared with tag-pinned official OpenAPI; no real panel was provisioned |
| VM provisioning matrix | Not tested | No Ubuntu 22.04/24.04 amd64/arm64 disposable VMs available |
| Reboot/firewall/IPv4/IPv6/port scan | Not tested | Public installer and system mutation coordinator are absent |
| Fuzzing/govulncheck/security scanner | Not run | No configured local tools/workflows beyond vet and ShellCheck |

## Findings

### QA-001 — Invalid database accepted as a successful backup

- Severity: High
- Status: Fixed and regression-covered
- Affected: `internal/backup/backup.go`
- Reproduction: a fake `GetDatabase` returned empty/arbitrary bytes; `Create`
  published a self-consistent archive and `Read` accepted a self-consistent
  manifest containing arbitrary database bytes.
- Impact: users could receive a successful backup result for data that 3x-ui
  cannot restore.
- Fix evidence: create and read now require a SQLite header/minimum structure;
  `TestCreateRejectsInvalidDatabasePayload` and
  `TestReadRejectsInvalidDatabasePayload` cover both boundaries.
- Residual: a SQLite header is not a full integrity check. A release should run
  SQLite integrity/quick-check through a reviewed supported mechanism or prove
  restore compatibility against the pinned 3x-ui.

### QA-002 — Backup manifest allowed duplicate entries and omitted coverage

- Severity: High
- Status: Fixed and regression-covered
- Affected: `internal/backup/backup.go`
- Reproduction: declare the database entry three times; `len(Files)==3` passed,
  while state and secrets were not checksummed by the manifest.
- Impact: integrity metadata did not cover all restore-critical files.
- Fix evidence: exact unique member set is enforced;
  `TestReadRejectsDuplicateManifestEntries` passes.

### QA-003 — Backup publication could overwrite a raced destination

- Severity: High
- Status: Fixed and regression-covered
- Affected: `internal/backup/backup.go`
- Reproduction: create the requested destination from inside `GetDatabase`,
  after initial `Lstat` and before final publish. Plain `os.Rename` overwrote it.
- Impact: violates the explicit never-overwrite contract and can destroy an
  operator-created file.
- Fix evidence: publication uses atomic no-replace hard-link semantics;
  `TestCreateDoesNotOverwriteDestinationCreatedDuringBackup` passes.
- Residual: the parent directory is not explicitly fsynced after publication,
  so crash durability after a reported success is not yet proven.

### QA-004 — Unsafe VLESS strings reached terminal/redirect output

- Severity: High
- Status: Fixed and regression-covered
- Affected: `internal/xui/resources.go`
- Reproduction: return `vless://abc<ESC>]0;owned<BEL>` from the local API.
  Prefix-only validation accepted it.
- Impact: terminal control injection and malformed client configuration.
- Fix evidence: URI parsing, required scheme/user/host and Unicode control/space
  rejection; `TestClientLinksRejectsTerminalControlsAndMalformedURIs` passes.

### QA-005 — Global 3x-ui client operations did not prove inbound ownership

- Severity: High
- Status: Fixed and regression-covered in service behavior
- Affected: `internal/xui/resources.go`, `internal/app/service.go`
- Reproduction: a compatible global client with the requested email exists on
  another inbound. Old add returned idempotent success and old remove called the
  global delete endpoint, which deletes the client from every inbound.
- Impact: false ownership and deletion of unrelated 3x-ui configuration.
- Fix evidence: `ClientRecord.InboundIDs` is decoded; add/show/list require the
  managed inbound; global delete is refused for absent or multi-inbound
  membership. This matches the pinned v3.5.0 OpenAPI contract.
- Residual: a future multi-inbound UX must use the supported detach endpoint,
  not global deletion.

### QA-006 — API outages were misreported as missing users

- Severity: Medium
- Status: Fixed and regression-covered
- Affected: `internal/app/service.go`
- Reproduction: `GetClient` returns HTTP 500; remove/show previously emitted
  `USER_NOT_FOUND`.
- Impact: misleading destructive troubleshooting and wrong stable error code.
- Fix evidence: only explicit 404/not-found envelope responses map to missing;
  `TestUserLookupDoesNotMisreportAPIOutageAsNotFound` passes.

### QA-007 — Degraded Xray status exited successfully

- Severity: High
- Status: Fixed and regression-covered
- Affected: `internal/cli/cli.go`
- Reproduction: panel status succeeds with `xrayState=false`; old human output
  printed `DEGRADED` but returned exit 0.
- Impact: monitoring/automation treated a stopped VPN as healthy.
- Fix evidence: human and JSON status exit 5 and JSON sets `ok:false`;
  `TestStatusReturnsDegradedExitCodeWhenXrayIsStopped` passes.

### QA-008 — Malformed persisted state was rendered or routed without validation

- Severity: Medium
- Status: Fixed and regression-covered
- Affected: `internal/domain/types.go`
- Reproduction: valid-schema state with unsupported architecture, control bytes
  in public address/SNI, path traversal in panel base path, or malformed Reality
  target passed validation.
- Impact: terminal injection from root-writable corrupted state and unsafe API
  routing if restore is later enabled.
- Fix evidence: strict architecture, IP, privileged panel-port policy, base-path,
  target and SNI validation; `TestValidateStateRejectsUnsafeRoutingFields` passes.

### QA-009 — Crash before lock metadata created a permanent mutation lock

- Severity: High
- Status: Fixed and regression-covered
- Affected: `internal/lock/lock.go`
- Reproduction: create an old `/run/lock/vpnctl.lock` directory without
  `owner.json`, modelling SIGKILL after `Mkdir` and before metadata write. Every
  later `Acquire` returns `LOCKED` because unreadable metadata is never stale.
- Fix evidence: an incomplete directory older than one minute is quarantined;
  a fresh incomplete directory is not stolen;
  `TestAcquireRecoversOldLockDirectoryWithoutMetadata` passes.
- Impact: all user mutations and backup remain unavailable until manual cleanup;
  repeated command recovery is not guaranteed. Reboot usually clears `/run`, but
  SIGKILL without reboot is a required scenario.
- Residual: PID reuse is not distinguished from the original live owner. An
  OS-released advisory lock (`flock`) remains the stronger long-term design.

### QA-010 — Delete-client response parsing accepted trailing/oversized data

- Severity: Medium
- Status: Fixed and regression-covered
- Affected: `internal/xui/resources.go`
- Reproduction: HTTP 200 JSON success followed by trailing garbage, or response
  over the common API limit.
- Impact: inconsistent API parser policy and false successful deletion under a
  malformed/compromised local API response.
- Fix evidence: bounded full-body read, content-type check and exact one-document
  decode; `TestDeleteClientRejectsTrailingResponseGarbage` and
  `TestDeleteClientRejectsOversizedResponse` pass.

### QA-011 — Root-only privileged panel port is an explicit security tradeoff

- Severity: Medium residual risk
- Status: Accepted for current design; document before release
- Affected: state policy and upstream x-ui service model
- Detail: panel ports are intentionally restricted to `1..1023`. This prevents
  an unprivileged local user from occupying the loopback bearer-API port after
  x-ui stops and receiving the token. It also keeps upstream x-ui running as
  root and conflicts with a future least-privilege service goal.
- Required future work: authenticated local transport/peer identity or TLS/token
  channel binding before moving the panel to an unprivileged service/port.

### QA-012 — Existing backups directory permissions are not repaired

- Severity: Low
- Status: Open on Linux; regression added
- Affected: `internal/state/store.go`
- Reproduction: pre-create `backups/` as mode `0755`, then call `Ensure`.
  `MkdirAll` leaves the broad directory mode unchanged.
- Impact: backup files remain `0600`, but their names and directory metadata may
  be listable contrary to the documented `0700` invariant.
- Regression: `TestEnsureSecuresExistingBackupsDirectory` (skipped on Windows,
  intended for Linux CI).

## Definition-of-Done gaps

| Requirement | Current state |
|---|---|
| One-command clean-VPS installation | Missing: bootstrap ends at `INSTALL_UNAVAILABLE` |
| Interactive recommended/advanced wizard | Missing from reachable CLI |
| Dependency, 3x-ui/Xray, Reality inbound and firewall provisioning | Staging/helper code exists; no public transaction coordinator |
| Repeat install/idempotent failed-install recovery | Not implementable/testable while install is disabled |
| SIGINT/reboot recovery at every install phase | Not implemented or VM-tested |
| `restore` with safety backup and verified rollback | Explicitly `RESTORE_UNAVAILABLE` |
| Verified update, backup, healthcheck and rollback | `UPDATE_UNAVAILABLE` |
| Inventory-scoped uninstall | `UNINSTALL_UNAVAILABLE` |
| Full `doctor` checks and safe repair | Current doctor is a small panel/state subset; repair absent |
| Persistent structured logging/redaction validation | No persistent logger implementation observed |
| Ubuntu 22.04/24.04 × amd64/arm64 provisioning | Not run |
| VPN survives reboot and real client probe | Not run |
| README command claims match implementation | No: install/update/uninstall examples describe unavailable workflows; backup example omits required `--plaintext` |

## Tested attack surfaces that held

- State/secrets use strict JSON, a 1 MiB read cap, regular-file checks, mode checks,
  atomic temp-file replacement and parent-directory fsync.
- State target symlinks are refused by tests.
- Bootstrap validates version/base URL, enforces HTTPS redirects/TLS minimum,
  caps downloads, requires a unique checksum record, checks the artifact before
  extraction, bounds payload size and atomically stages the binary.
- Installer archive extraction rejects traversal, links, unexpected top-level
  paths, duplicate names, special entries, setuid/setgid/sticky bits, excess
  entries and excess extracted bytes.
- Backup reader rejects URLs/stdin, non-regular/symlink sources, traversal,
  special members, unexpected/duplicate members, unsafe modes, excess size,
  strict-JSON violations, manifest mismatch and invalid state/secrets/database.
- The 3x-ui client pins loopback HTTP with an explicit port, disables proxy use
  in production, sets bounded timeouts/body limits, checks HTTP and application
  envelopes and does not log response bodies or bearer tokens.
- CLI JSON errors expose public domain messages rather than wrapped causes.
- User names reject shell/path metacharacters and are trimmed only at edges.
- Mutating user/backup commands share an exclusive lock; stale dead-PID recovery
  exists when valid metadata was written.
- CI actions are commit-SHA pinned and workflow permissions are read-only.

## Required retest before release

1. Run all Go tests plus the race detector on Linux.
2. Run ShellCheck and `scripts/install_test.sh` in the declared Ubuntu CI image.
3. Implement the four fail-closed lifecycle commands, then rerun the complete
   destructive matrix from `docs/test-strategy.md`.
4. Provision fresh Ubuntu 22.04/24.04 VMs on native amd64/arm64; include SIGINT,
   SIGKILL, hard reboot, disk-full, port conflict, pre-existing resources,
   IPv4-only and IPv6-only cases.
5. Exercise the adapter against real pinned 3x-ui v3.5.0 and perform a real Xray
   client connection using each generated URI/QR.
6. Prove backup restoreability, crash durability and update/restore rollback,
   including service credentials/base-path changes.
7. Perform external IPv4/IPv6 scans and verify SSH preservation/firewall
   ownership across success, rollback and reboot.
