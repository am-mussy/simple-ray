# Phase 5 QA / destructive audit

Audit date: **2026-08-13**. Scope: the complete repository state available at
the end of Phase 5, including Go packages, shell bootstrap, tests, CI, release
binaries and documentation.

## Verdict

The repository is **not releasable and does not meet the Definition of Done**.
The local Go regression suite and vet are green, and Linux binaries cross-build
for amd64 and arm64. `install` and inventory-scoped `uninstall` completed a real
Ubuntu 26.04 amd64 lifecycle against 3x-ui v3.5.0, including idempotency, users,
backup, external port probes, reboot and cleanup. `restore` and `update` still
deliberately fail closed with exit code 3; those are product/DoD gaps, not
passing features.

Release remains blocked by the open Critical bootstrap trust finding: the
artifact and `checksums.txt` come from the same unauthenticated publisher
channel. The default URL also contains `OWNER`, and the repository has no
workflow or command that produces the archives/checksum/signature set expected
by `scripts/install.sh`. The one tested matrix cell does not establish support
for the other Ubuntu/architecture combinations.

## Executed checks

| Check | Result | Evidence / limitation |
|---|---|---|
| Complete source/test/script/doc review | Completed | All files returned by `rg --files` inspected; privileged paths, API, archives, state, locks and CLI traced |
| `go test ./...` before red-team regressions | Passed | All original packages passed on Windows amd64 |
| Regression suite | Passed | `C:\pet-projects\simple-ray\.tools\go\bin\go.exe test -count=1 ./...`; Go 1.25.6, Windows amd64 |
| `go vet ./...` | Passed | `C:\pet-projects\simple-ray\.tools\go\bin\go.exe vet ./...`; Go 1.25.6, Windows amd64 |
| Race detector | Not run | Local environment has no C compiler; `-race` failed before build with missing `gcc` |
| ShellCheck | Not run locally | `shellcheck` is unavailable; pinned CI job includes it but no CI run was observed |
| Bootstrap shell test | Passed on VPS | `bash -n` and `scripts/install_test.sh` passed on Ubuntu 26.04; publisher authentication is still absent |
| Linux cross-build | Passed | `GOOS=linux CGO_ENABLED=0 go build ./...` for amd64 and arm64; this compiles Linux production files but does not run Linux-only tests |
| Real 3x-ui v3.5.0 contract | Passed for exercised endpoints | Real panel was provisioned and used for status, users, links, backup and Xray restart |
| VM provisioning matrix | Not tested | No Ubuntu 22.04/24.04/26.04 amd64/arm64 disposable VMs available |
| Install/uninstall transaction | Passed on 26.04 amd64 | Interactive install, repeat install, reboot and uninstall were executed; several failure paths also demonstrated full rollback |
| Live amd64 acceptance | Partial pass | User-supplied disposable VPS completed the lifecycle; no real client traffic transfer and no IPv6-origin probe were run |
| Reboot/firewall/port scan | Passed with limitations | External probe host saw only 22/443 open; panel/subscription/Zabbix closed. IPv6-origin probe not run |
| govulncheck | Passed | No called vulnerabilities found; one required module contains an uncalled vulnerable package |

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
- Status: Fixed; regression-covered on Linux
- Affected: `internal/state/store.go`
- Reproduction: pre-create `backups/` as mode `0755`, then call `Ensure`.
  `MkdirAll` leaves the broad directory mode unchanged.
- Impact: backup files remain `0600`, but their names and directory metadata may
  be listable contrary to the documented `0700` invariant.
- Fix evidence: `Ensure` now applies `0700` after `MkdirAll`;
  `TestEnsureSecuresExistingBackupsDirectory` is skipped on Windows and must run
  in Linux CI.

### QA-013 — Bootstrap and release artifact contract cannot be published safely

- Severity: Critical release blocker
- Status: Open
- Affected: `scripts/install.sh`, `Makefile`, `.github/workflows/ci.yml`
- Evidence: the default release base contains placeholder `OWNER`; both the
  artifact and checksum are downloaded from that one channel; no signature,
  trusted identity, provenance or expiry is verified. `Makefile release` emits
  raw binaries in directories, while the bootstrap expects versioned
  `.tar.gz` archives plus `checksums.txt`; CI does not publish either contract.
- Impact: there is no consumable default bootstrap release, and compromise of
  the release channel can replace both payload and checksum.
- Required fix: define a real immutable release origin, build the exact archive
  names expected by the bootstrap, publish authenticated metadata/signatures
  from a protected release workflow, and add consumer verification tests.

### QA-014 — Uninstall does not meet the exact-cleanup acceptance contract

- Severity: Medium product/DoD gap
- Status: Open
- Affected: `internal/cli/cli.go`, `scripts/install.sh`,
  `docs/live-acceptance-amd64.md`
- Evidence: the bootstrap installs `/usr/local/bin/vpnctl`, while successful
  uninstall explicitly returns `binaryRetained:true` and says the binary was
  retained. The live checklist's exact-cleanup oracle requires that path to be
  absent.
- Impact: the documented end-to-end uninstall acceptance cannot pass even if
  all inventory-owned 3x-ui resources are removed correctly.
- Required decision: either safely remove the invoking binary after the command
  completes, or explicitly define retained CLI bootstrap cleanup as supported
  behavior and change the product acceptance contract.

### QA-015 — Lifecycle documentation is stale relative to reachable code

- Severity: Medium documentation/operational risk
- Status: Open
- Affected: `README.md`, `docs/security-audit.md`
- Evidence: both documents say `install` and `uninstall` fail closed or are not
  wired, while the CLI dispatches them to reachable Linux implementations.
- Impact: operators and reviewers cannot tell which destructive paths are live;
  the old safety conclusion no longer applies to the current tree.
- Required fix: rerun the independent security review against the reachable
  manager and update public documentation only after live VM evidence exists.

## Definition-of-Done gaps

| Requirement | Current state |
|---|---|
| One-command clean-VPS installation | Linux implementation is reachable, but default bootstrap is unusable (`OWNER`), release artifacts are not produced, and no fresh-VPS run exists |
| Interactive recommended/advanced wizard | Reachable; no controlling-TTY/bootstrap acceptance run |
| Dependency, 3x-ui/Xray, Reality inbound and firewall provisioning | Implemented in the Linux manager; only helper/static coverage, not a full real-system transaction test |
| Repeat install/idempotent failed-install recovery | Existing-install and journal paths exist; no crash-point or live idempotency proof |
| SIGINT/reboot recovery at every install phase | Journal rollback exists; signal/hard-reboot matrix not executed |
| `restore` with safety backup and verified rollback | Explicitly `RESTORE_UNAVAILABLE` |
| Verified update, backup, healthcheck and rollback | Backup creation exists; `UPDATE_UNAVAILABLE`; no A-to-B artifact/channel exists |
| Inventory-scoped uninstall | Reachable and marker/hash guarded; no full Linux cleanup test, and CLI binary is intentionally retained |
| Full `doctor` checks and safe repair | Current doctor is a small panel/state subset; repair absent |
| Persistent structured logging/redaction validation | No persistent logger implementation observed |
| Ubuntu 22.04/24.04/26.04 × amd64/arm64 provisioning | Version predicate and cross-build pass; no native VM runtime evidence on any cell |
| VPN survives reboot and real client probe | Not run |
| README command claims match implementation | No: README incorrectly says reachable install/uninstall fail closed and are not wired; security audit reachability conclusion is also stale |
| Authenticated release/bootstrap | Missing: checksum shares the artifact trust channel; no signed release workflow or matching packaged artifacts |
| Live amd64 acceptance | `BLOCKED / NOT RUN`; see `docs/live-acceptance-amd64.md` result summary |

## Tested attack surfaces that held

- State/secrets use strict JSON, a 1 MiB read cap, regular-file checks, mode checks,
  atomic temp-file replacement and parent-directory fsync.
- State target symlinks are refused by tests.
- Bootstrap validates version/base URL, enforces HTTPS redirects/TLS minimum,
  caps downloads, requires a unique checksum record, checks the artifact before
  extraction, bounds payload size and atomically stages the binary.
- Linux install code has an ownership journal, marker/hash checks, staged
  archive extraction, explicit firewall rule inventory and rollback helpers.
  These properties are not a substitute for executing the whole transaction.
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

1. Resolve QA-013: publish authenticated, immutable release metadata and the
   exact archives/checksum contract consumed by the bootstrap.
2. Run all Go tests plus the race detector on Linux; run the Linux-only manager,
   permission, firewall and panel-bootstrap tests rather than only cross-builds.
3. Run ShellCheck and `scripts/install_test.sh` in the declared Ubuntu CI image.
4. Provision fresh Ubuntu 22.04/24.04/26.04 VMs on native amd64/arm64; include SIGINT,
   SIGKILL, hard reboot, disk-full, port conflict, pre-existing resources,
   IPv4-only and IPv6-only cases.
5. Exercise the adapter against real pinned 3x-ui v3.5.0 and perform a real Xray
   client connection using each generated URI/QR.
6. Keep restore disabled until an offline service-level transaction can replace
   the database and restore the pre-operation snapshot without relying on a
   token/port/base-path that the imported database may rotate. API-only restore
   cannot provide the required rollback guarantee.
7. Define a meaningful update target and two compatible signed releases. With
   3x-ui fixed at v3.5.0, reinstalling the same payload is repair, not update;
   current `update --check` and A-to-B rollback acceptance remain unavailable.
8. Resolve the retained-binary mismatch, then prove uninstall exact scope and
   baseline equivalence on a disposable host.
9. Perform external IPv4/IPv6 scans and verify SSH preservation/firewall
   ownership across success, rollback and reboot.
