# vpnctl security audit and threat model

## Post-review remediation — 2026-08-13

Two additional reachable High findings were reported after the final
implementation retest and are fixed in the current tree:

- **Root source-test execution.** The documented `sudo bash start.sh` path ran
  `go test ./...` and `go vet ./...` as root, allowing test code and downloaded
  module code to execute before installation. `start.sh` now archives the
  checkout without executing repository-configured Git helpers, creates a
  private build workspace, clears the environment with `env -i`, disables
  `GOENV` and workspaces, and runs test, vet and build as the invoking
  non-root user or an unused isolated numeric UID with all capability sets
  cleared. It verifies the observed build UID is nonzero.
  Only the atomic `/usr/local/bin` install and the system configuration run
  with elevated privileges. The preferred command is `bash start.sh`, not
  `sudo bash start.sh`. Residual boundary: a source script explicitly started
  as root is itself trusted code; privilege dropping cannot authenticate a
  malicious script that has already begun executing.
- **Attacker-creatable mutation lock.** The CLI used
  `/run/lock/vpnctl.lock`, while `/run/lock` is mode `1777` on supported
  Ubuntu systems. A local user could pre-create the directory and a valid-looking
  `owner.json` referencing PID 1 to block every mutation. The lock is now the
  persistent file `/run/vpnctl/lock` inside an exact-owner `0700` runtime
  directory. Linux opens it with `O_NOFOLLOW|O_CLOEXEC`, validates a regular
  exact-owner `0600` inode, and holds `flock(LOCK_EX|LOCK_NB)` on the open file
  descriptor. Release closes the descriptor and never removes attacker-shaped
  trees. Kernel process teardown releases the lock after SIGKILL.

Regression coverage rejects unsafe/symlink runtime directories and unsafe lock
files, verifies mutual exclusion, and runs a Linux child-process death test.
The source-installer contract checks the sanitized non-root execution path.
The release remains blocked independently by the open Critical publisher-
authentication finding for `scripts/install.sh`.

## Final implementation retest — 2026-08-13

Release verdict: **BLOCKED**.

- **Critical open — bootstrap publisher authentication.** `scripts/install.sh`
  still downloads the executable and `checksums.txt` from the same release
  channel. HTTPS plus a same-channel checksum detects corruption but does not
  authenticate the publisher after a release-account/origin compromise. The
  default URL also contains `OWNER`, and no signed release workflow currently
  produces the expected artifacts.
- **No reachable High finding remains open in the reviewed direct-binary
  install/uninstall path.** High findings discovered during the live cycle were
  fixed: stop-before-firewall rollback, UFW post-install drift fingerprinting,
  explicit active-SSH listener verification, privileged loopback panel,
  isolated credential bootstrap, subscription disablement, inventory-only
  cleanup, restrictive data modes and Xray executable immutability.
- `restore` and `update` are unavailable with exit code 3. This is a product/DoD
  gap and the safe behavior until offline restore rollback and a signed A/B
  release channel exist.

Retest evidence: local `go test -count=1 ./...` and `go vet ./...` passed;
Linux amd64/arm64 cross-builds and Linux test-binary compilation passed. On a
disposable Ubuntu 26.04 amd64 VPS, direct-binary install, real 3x-ui/Xray API,
users, URI, backup no-overwrite, repeat install, reboot, external port probes
and uninstall completed. Bootstrap shell tests and `govulncheck` passed; no
called vulnerability was reported. The final UFW drift, SSH listener and
rollback-order hardening landed after the live cleanup and has regression/static
coverage only. Ubuntu 22.04/24.04, arm64, IPv6-origin and SIGKILL matrices remain
open release gates.

## Audit status

| Field | Value |
| --- | --- |
| Date | 2026-08-13 |
| Phase | Pre-implementation design review |
| Scope | `prompt.md`; proposed installer, CLI, 3x-ui/Xray integration, firewall, backup/restore, update/rollback |
| Implementation reviewed | None existed at the time of this review |
| Result | Design requirements defined; implementation security is **not yet verified** |
| Release gate | A second independent review must close every Critical and High finding |

This is a threat model and a set of acceptance criteria, not evidence that the eventual implementation is secure. All findings below are open until code and destructive VM tests demonstrate otherwise.

Severity is based on the expected impact on a root-managed VPN server:

- **Critical**: practical path to arbitrary root code execution or compromise of the release/update trust root at scale.
- **High**: disclosure of reusable VPN/admin secrets, public administrative access, root filesystem overwrite, persistent lockout, or unrecoverable destructive update.
- **Medium**: meaningful defense-in-depth failure, local information disclosure, downgrade, service compromise with constrained impact, or availability loss with recovery.
- **Low**: hardening or observability gap with limited direct exploitability.

## System model

```text
maintainer/source repository
        |
        | protected release workflow
        v
signed manifest + versioned artifacts + provenance
        |
        | HTTPS is transport protection; signature/provenance is publisher authentication
        v
small install.sh ---> verified vpnctl binary ---> privileged local operations
                                                |        |        |
                                                v        v        v
                                             3x-ui     Xray    firewall
                                                |        |
                                         loopback API   public VLESS
                                                |
                                           secret state
                                                |
                                         encrypted backup
```

### Assets

- Root integrity of the VPS and continued SSH access.
- `vpnctl`, 3x-ui, Xray, systemd units, firewall policy, and installed configuration.
- Panel username/password, API token, session material, and optional TOTP seed.
- VLESS UUIDs/links/QR codes, Reality private key, and short IDs.
- 3x-ui database, Xray configuration, local transaction snapshots, and portable backups.
- Release signing identity, protected source, CI credentials, manifests, checksums, attestations, and rollback artifacts.
- Availability of the VPN service and recoverability after interruption, reboot, update, or restore.

### Threat actors and failure sources

- An on-path attacker, DNS/CDN/domain compromise, malicious mirror, or compromised download endpoint.
- A compromised maintainer account, source dependency, upstream release, CI action, workflow token, or build runner.
- An unauthenticated internet client brute-forcing or exploiting the panel/API or Xray listener.
- A low-privileged local user attacking root processes through arguments, environment, temporary files, symlinks, or writable paths.
- A user supplying a hostile name, path, URL, configuration value, release archive, or backup archive.
- Power loss, SIGINT/SIGTERM, disk exhaustion, partial network response, reboot, or service health failure during a privileged transaction.
- Accidental operator disclosure through shell history, terminal capture, logs, support bundles, backups, or overly broad permissions.

### Trust boundaries

1. Source and CI to published release.
2. HTTPS endpoint to bootstrap process.
3. Downloaded manifest/artifact to root execution.
4. `vpnctl` input and environment to privileged filesystem/process operations.
5. Local `vpnctl` to the 3x-ui panel/API and Xray process.
6. Public network to VLESS, SSH, and any explicitly exposed panel endpoint.
7. Live state to backup, and untrusted backup back to root-owned live state.
8. Old installed version to updater, migration, health check, and rollback state.

### Explicit assumptions and limits

- Ubuntu 22.04/24.04/26.04 on amd64/arm64 is the only supported platform for the MVP.
- The initial SSH connection, VPS image, kernel, root account, and provider control plane are trusted. A malicious provider or existing root compromise is out of scope.
- Upstream 3x-ui and Xray are not assumed safe merely because they are popular. Exact reviewed versions and artifact digests remain required.
- The 3x-ui panel is an administrative application, not a camouflage boundary. Its random port and base path do not replace authentication, TLS, or firewall policy.
- A VLESS share URI and QR code are bearer credentials. Anyone who obtains one may use that client's access until it is revoked.
- Secure erasure cannot be guaranteed on VPS/SSD storage. Rotation and revocation are required after confirmed secret disclosure.

## Open design findings

| ID | Severity | Threat | Required disposition before release |
| --- | --- | --- | --- |
| C-01 | Critical | A mutable `curl ... | sudo bash` response receives root without independent inspection or publisher verification. A compromised domain, endpoint, or repository can take the VPS. | Provide a preferred download → verify publisher identity and digest → execute flow. Keep any one-liner as an explicitly weaker compatibility path. Make bootstrap minimal and require it to verify a signed, versioned manifest and artifact before executing `vpnctl`. Document the residual risk that a compromised bootstrap can bypass its own verifier. |
| C-02 | Critical | An updater that trusts `latest`, a mutable tag, or a checksum from the same compromised channel can install arbitrary root code across all servers. | Embed/pin an update trust root in the installed binary; accept only signed version manifests with exact artifact name, version, OS/arch, size, and SHA-256; verify before extraction/execution; reject unknown keys, stale/replayed metadata, wrong architecture, and unlisted files. Key rotation needs a signed transition procedure. |
| C-03 | Critical | Delegating installation to an upstream one-line installer transfers root trust to mutable upstream scripts and their current dependencies. | Do not execute upstream installers. Consume exact stable 3x-ui and Xray release versions through project-controlled, signed metadata and independently recorded digests. Review each version bump. |
| H-01 | High | A public 3x-ui panel/API, especially over HTTP, exposes credentials to interception, brute force, and upstream web vulnerabilities. | Bind panel/API to loopback by default and do not open its port in the firewall. Use SSH forwarding for normal administration. Public exposure must be a separate explicit operation requiring valid TLS, no default credential, IP allowlist where possible, rate limiting/fail2ban, trusted proxy configuration, and 2FA before the firewall opens. |
| H-02 | High | Admin/API credentials, Reality private key, UUID/URI/QR, short ID, or backup data can leak through logs, errors, process arguments, shell history, debug output, or non-TTY automation. | Centralize typed redaction; never accept secrets as ordinary CLI arguments; disable shell tracing; reveal client material only through explicit commands and only to a TTY by default; write non-interactive output to a caller-selected `0600` file or file descriptor. Test exact and encoded secret canaries against all logs and errors. |
| H-03 | High | A malicious backup can use `..`, absolute paths, symlinks, hardlinks, devices, ownership, or archive bombs to overwrite arbitrary root files during restore. | Treat every backup as hostile. Parse a strict versioned manifest, enforce file/count/size limits, reject special files, links and escaping paths, extract without following symlinks into a root-owned staging directory, validate all content, then atomically commit. Never invoke `tar` to extract an untrusted archive directly into `/`. |
| H-04 | High | Resetting or incompletely applying firewall rules can lock out SSH, expose the panel, or leave IPv6 open while IPv4 appears protected. | Detect the existing firewall manager and refuse unsupported/conflicting states without mutation. Snapshot rules, add the actual SSH allowance before deny rules, manage only project-owned rules, apply IPv4/IPv6 parity, validate, and restore the prior ruleset on failure. Never use `ufw reset` or disable an existing firewall. |
| H-05 | High | Interrupted update/config migration can leave incompatible binary, database, config, firewall, or systemd state and destroy the only working service. | Use an exclusive operation lock and a durable transaction journal, preflight disk space, create a consistent rollback snapshot, stage and validate the new version, atomically switch, run bounded health checks, and roll back every changed component on failure. Test interruption at every state transition. |
| H-06 | High | Portable plaintext backups contain all material needed to impersonate clients or administer/recreate the server; `0600` does not protect a copied or leaked archive. | Encrypt portable backups by default with an established authenticated-encryption format/library. Read passphrases or recipient keys from a TTY or file descriptor, never argv. A plaintext export, if retained at all, requires an explicit `--plaintext` acknowledgement and prominent warning. |
| H-07 | High | Root filesystem operations against attacker-controlled or pre-existing writable paths permit symlink/TOCTOU overwrite or replacement of privileged executables/configuration. | Use fixed managed roots, descriptor-relative traversal-resistant APIs, no-follow/exclusive create semantics, ownership/mode checks, same-filesystem staging, atomic rename, and refusal on unexpected symlinks, hardlinks, mount points, or owners. Never recursively delete a path derived only from user input or mutable state. |
| M-01 | Medium | Command injection, option injection, terminal escapes, or path traversal can enter through user names, paths, URLs, environment variables, or panel responses. | Use allowlisted structured inputs; for MVP restrict names to printable ASCII starting with an alphanumeric; pass argument arrays directly without a shell; use `--` where supported; sanitize control characters in terminal/log output; ignore unsafe inherited environment variables. |
| M-02 | Medium | Long-running root services or broad Linux capabilities amplify an upstream Xray/3x-ui compromise. | Run persistent services as dedicated unprivileged users wherever upstream supports it. Apply only required capabilities and systemd sandboxing, verify with `systemd-analyze security`, and document any hardening directive that cannot be enabled. `vpnctl` should be a short-lived privileged tool, not a root daemon. |
| M-03 | Medium | A validly signed old manifest can force a vulnerable downgrade or replay an expired update. | Persist the highest accepted release/schema metadata, include issuance/expiry or monotonic version data in signed manifests, reject downgrades by default, and require an explicit audited recovery flag for a deliberate downgrade. Rollback may use only the locally recorded previously installed artifact/digest. |
| M-04 | Medium | Brute-force controls can be bypassed or ban the wrong address when an exposed panel trusts arbitrary proxy headers. | Trust forwarded client IP headers only from a configured loopback/local reverse proxy. Verify the exact 3x-ui log format before enabling fail2ban and regression-test bans. Do not claim fail2ban protects a loopback-only panel. |
| M-05 | Medium | Uninstall or restore may delete unrelated pre-existing 3x-ui/Xray/firewall data. | Record an ownership inventory at install, distinguish adopted from project-created resources, present a destructive plan, require confirmation unless a narrowly scoped force flag is supplied, back up before restore/uninstall, and remove only validated managed paths/rules. |

## Mandatory controls

### Secrets and sensitive output

| Data | Classification | Storage and output requirement |
| --- | --- | --- |
| Panel password | Secret | Generate with a CSPRNG; hand to 3x-ui without argv/shell; store only the upstream slow password hash after setup; reveal initial value once to a TTY. Never duplicate plaintext in vpnctl state. |
| Panel API token/session/TOTP seed | Secret | Prefer a scoped local token if upstream supports it; otherwise treat an admin token as full root-adjacent access. Store only where required with service-only access; never log response bodies containing tokens. |
| Reality private key | Secret | Generate through the pinned Xray binary's supported key-generation command or a reviewed X25519 library; keep server-side only; never include it in client links or normal diagnostic output. |
| VLESS UUID/share URI/QR | Bearer secret | Use OS CSPRNG for UUID generation; show only explicitly; redact the complete URI and UUID from logs. Removing a user must revoke it in active configuration, not only hide it in vpnctl state. |
| Reality short ID | Sensitive client credential | Generate the maximum supported random length from a CSPRNG; treat as secret in logs/backups even though it is distributed to that client's configuration. |
| Random panel path/port | Defense in depth | Generate without bias; do not describe either as an authentication factor. The panel remains loopback-only by default. |
| 3x-ui database/Xray config | Secret-bearing state | Owner is root or the dedicated service account as operationally required; no group/other access unless a narrowly scoped service group is necessary. Atomic writes and consistent snapshots are mandatory. |
| Portable backup | Secret aggregate | Authenticated encryption by default, outer file `0600`, parent directory `0700`, minimal non-sensitive filename, version metadata inside authenticated content. |
| Local rollback snapshot | Secret aggregate | Root-only, bounded retention, never copied automatically, removed only after successful health check and transaction completion. |
| Logs/transaction journal | Non-secret security metadata | Contain operation, version, phase, result, and opaque identifiers only; no credentials/config dumps/URIs. Sanitize CR/LF and terminal controls. Restrict write access and rotate. |

Required filesystem baseline:

- set `umask 077` before creating state or temporary files;
- create secret/state and backup directories as `0700` and secret files as `0600`, adjusting only the owner needed by a dedicated service;
- install executables as root-owned, non-writable `0755` files and unit/public metadata as root-owned `0644` only when they contain no secrets;
- use `/run/vpnctl` or another root-owned `0700` runtime directory for locks and ephemeral IPC;
- create temporary directories/files with secure APIs, not predictable names, and remove them on normal exit and handled signals;
- never use world-writable persistent locations for staging privileged replacements;
- do not rely on zeroing Go strings or on deletion for secure erasure; minimize lifetime, rotate on exposure, and disable core dumps for secret-handling services where practical.

Use the operating system CSPRNG (`crypto/rand` in Go), never `math/rand`, timestamps, process IDs, or hand-rolled entropy. Fail closed if key/UUID/password generation or parsing fails. Passwords and random web paths should carry at least 128 bits of generated entropy; use Xray's supported X25519 generation for Reality keys.

The logger must classify fields, not redact by fragile text replacement alone. At minimum it must reject/redact fields named or typed as password, token, authorization/cookie, private key, UUID/client URI, short ID, QR payload, database/config content, backup passphrase, and backup plaintext. `--debug`, panic output, HTTP tracing, doctor/support output, and third-party subprocess capture follow the same rule. Never enable `set -x` in bootstrap or maintenance scripts.

### Installer and `curl | bash`

The preferred installation flow must be:

1. Download the bootstrap/release files without root.
2. Verify publisher identity/signature or provenance against a trust root obtained independently of the artifact response.
3. Verify the selected version's expected SHA-256, OS/architecture, and size.
4. Inspect the small bootstrap if desired.
5. Execute the verified file with `sudo`.

Downloading an artifact and its checksum from the same compromised endpoint only detects accidental corruption. HTTPS certificate validation is necessary but does not replace signed release identity. GitHub artifact attestations are useful only when the consumer actually verifies them against the expected repository identity.

If the project also publishes `curl -fsSL https://.../install.sh | sudo bash`, the documentation must state that it trusts the response completely and is weaker than the verified flow. The server must return the same immutable bootstrap to all clients; do not vary executable content by user agent, IP, headers, or query parameters. Never put secrets in the one-line command, URL, environment, or installer arguments.

`install.sh` must stay small and reviewable. It may detect the supported OS/architecture, download a signed version manifest and the corresponding `vpnctl` artifact, verify both, atomically install the binary, and invoke it. It must not contain the VPN configuration business logic or run upstream installation scripts.

For shell bootstrap code:

- begin with `set -Eeuo pipefail` and `umask 077`; do not suppress meaningful failures with `|| true`;
- use a controlled `PATH`, quote every expansion, use arrays, delimit options with `--`, and never use `eval` or interpolate input into `sh -c`/`bash -c`;
- use `mktemp -d` in a trusted location, validate owner/type, install cleanup traps, and refuse symlink targets;
- require HTTPS, normal certificate/hostname verification, HTTPS-only redirects, bounded connect/total timeouts, bounded retries, and download size limits; never use `-k`/`--insecure`;
- verify the complete download before parsing, extracting, or executing it;
- use ShellCheck with no unexplained suppressions and test partial downloads and pipeline failures.

The Go CLI must use `exec.CommandContext`-style argument vectors, not a shell. It must use absolute paths or a fixed trusted executable lookup, a minimal child environment, context timeouts, bounded captured output, and centralized redaction. User-controlled values must never become command names, flags, unit names, archive member destinations, or format strings without strict validation.

### Release and supply chain

- Pin vpnctl, 3x-ui, Xray, Go modules, build images/toolchains, and CI actions. Never consume `latest`, `main`, `master`, a floating container tag, or an unqualified release URL at install/update time.
- Pin third-party GitHub Actions to full commit SHAs and grant each job the minimum token permissions. Do not expose release credentials to pull-request builds or untrusted code.
- Build releases on a hosted isolated workflow from a protected, immutable release revision. Require 2FA for maintainers, protected branches/tags, review for release/workflow changes, and environment approval for publishing.
- Produce a signed manifest covering every artifact's exact version, commit, OS/architecture, filename, byte size, and SHA-256. Publish SBOM and build provenance/attestation; verify the attestation in the preferred install path.
- An installed release must contain the update verification trust root and current manifest identity. Key rotation requires a transition signed by the old key and a documented emergency process for old-key compromise.
- Generate `go.sum`, run `govulncheck`, and review direct/transitive dependency changes. Pin scanner/tool versions in CI rather than installing mutable tool heads.
- Record the origin and digest of upstream 3x-ui/Xray assets. Do not assume upstream `.dgst` files authenticate the publisher; cross-check against the project's signed manifest and review upstream release provenance/signing when available.
- Release artifacts must be immutable. A changed artifact for an existing version is a security incident and requires a new version, disclosure, and signing-key/workflow investigation.

Target SLSA Build Level 2 provenance for the first stable release, with a documented path toward Level 3. This is a target, not a claim; conformance must be assessed against the release workflow before it is advertised.

### Panel, API, network, and firewall

The secure default exposure is:

| Listener | Default bind/exposure |
| --- | --- |
| SSH | Preserve the server's detected existing configuration; never assume port 22 |
| VLESS TCP Reality | Public on the explicitly selected TCP port, IPv4/IPv6 as supported |
| 3x-ui panel/API | Loopback only; no public firewall rule |
| Other services | No new exposure |

Use deny-by-default inbound filtering while preserving established traffic, loopback, necessary ICMP/ICMPv6, the actual SSH listener, and the selected VLESS TCP port. Apply equivalent intent to IPv4 and IPv6; when IPv6 is disabled, verify it is truly unavailable rather than silently skipping its firewall audit. Do not disable a cloud/provider firewall or claim to have configured it; `doctor` should explain external firewall mismatches.

Firewall changes must be transactional and coexist with a supported pre-existing UFW ruleset. If nftables/iptables rules are managed by another tool or their ownership is ambiguous, stop before mutation and print a safe remediation. Add SSH and loopback allowances before enabling a default deny policy, use a dry run where supported, tag project-owned rules, and verify the final live rules/listeners. Preserve the previous ruleset for automatic rollback. A fresh SSH connection test from outside the host belongs in destructive VM testing.

The panel must start with generated non-default credentials. Verify after configuration that neither upstream defaults nor an empty password remains. Store only the credential form required by supported 3x-ui CLI/API; do not edit its SQLite database directly. Use the local API over loopback with bounded timeouts, response-size limits, expected content types/status codes, and no response-body logging. Protect API tokens and rotate/revoke them during uninstall, restore to a different host, or suspected disclosure.

If public panel access is explicitly requested:

- require a hostname/IP certificate that validates at the client or a loopback reverse proxy terminating trusted TLS;
- bind the backend to loopback and trust forwarded IP headers only from that proxy;
- require the user to complete 2FA before opening the firewall;
- enforce login rate limits and validate fail2ban against the pinned version's actual logs;
- prefer an IP allowlist; restrict the panel port at both host and provider firewalls where possible;
- avoid exposing the API route unless needed and never rely on a secret URL path as the primary control;
- run an external IPv4 and IPv6 port scan after configuration.

Systemd services should use dedicated users and the smallest viable `CapabilityBoundingSet`/`AmbientCapabilities`. Evaluate `NoNewPrivileges`, `ProtectSystem`, `ProtectHome`, `PrivateTmp`, `PrivateDevices`, `ProtectKernelTunables`, `ProtectKernelModules`, `ProtectControlGroups`, `RestrictSUIDSGID`, `LockPersonality`, `MemoryDenyWriteExecute`, `RestrictAddressFamilies`, `SystemCallFilter`, and explicit `ReadWritePaths`. Enable each compatible control and document/test justified exceptions. Do not grant `CAP_SYS_ADMIN`; Xray should not require root merely to bind a high port.

### Backup and restore

A backup manifest must include format/schema version, vpnctl/3x-ui/Xray versions, architecture-independent compatibility data, creation time, file list, modes, sizes, and per-file digests. It must not put secrets in the outer filename or unauthenticated metadata.

Portable backups must use a reviewed authenticated-encryption library/format. Do not design a custom cipher or unauthenticated `openssl enc` pipeline. For passphrase-based encryption, use a modern memory-hard KDF with library-managed salt/parameters; for recipient-based encryption, clearly identify the expected recipient before writing. Passphrases/keys enter through a no-echo TTY, protected file descriptor, or explicitly permission-checked file, never argv, URI, shell environment, or log.

Create a consistent snapshot using a supported 3x-ui mechanism, SQLite online-backup behavior, or a brief coordinated service stop. Copying a live database file without a consistency guarantee is forbidden. Write to a new root-owned file, flush/close it, then atomically rename; never overwrite the only known-good backup in place.

Restore must:

1. Refuse stdin/network URLs and accept only a validated local regular file for MVP.
2. Authenticate/decrypt before trusting metadata or writing live state.
3. Enforce maximum compressed/uncompressed size, file count, path depth, and compression ratio.
4. Reject absolute paths, `..`, duplicates, NUL/control characters, symlinks, hardlinks, sockets, FIFOs, devices, setuid/setgid bits, unexpected owners, and undeclared files.
5. Use traversal-resistant descriptor-relative APIs such as Go `os.Root`/`os.OpenInRoot` where the selected Go version supports them.
6. Extract as unprivileged as practical into a new root-owned staging tree; never preserve archive ownership/xattrs/capabilities.
7. Validate schema/version compatibility and all configs with the pinned binaries before stopping the live service.
8. Take a pre-restore snapshot, commit atomically, health-check, and roll back on failure.

Restoring onto another host must regenerate or explicitly review host-bound panel TLS, public address, firewall, and API/session material. The workflow should offer client/admin credential rotation because a backup may have been copied beyond the original trust boundary.

### Update and rollback

Updates are explicit; there are no silent auto-updates. `vpnctl update` may report an available version but must show the exact current and target versions and request confirmation for breaking migrations.

Required transaction order:

1. Acquire a process-wide exclusive lock and recover/finish any prior transaction journal.
2. Fetch and verify signed update metadata before trusting URLs or version fields.
3. Select a concrete stable version; reject prerelease/dev channels and downgrades by default.
4. Download into a private staging directory with protocol, redirect, timeout, retry, size, OS/arch, signature, and digest enforcement.
5. Preflight disk/RAM/ports, parse migration compatibility, and create consistent rollback snapshots of every component to be changed.
6. Validate new binaries and configs without replacing live files; reject an artifact that fails self-version or architecture checks.
7. Stop only required services, perform staged/atomic replacements and supported migrations, reload systemd/firewall only if changed, then start services.
8. Run bounded local health checks for process state, config validity, expected listeners, local panel API, firewall intent, and a functional Xray probe where feasible.
9. On any critical failure, restore binary, database/config, units, and firewall from the recorded transaction, restart the old version, and verify old-version health.
10. Mark success durably, then remove/expire staging and old snapshots according to a bounded retention policy.

Never migrate the only copy of state. A database migration needs an explicit backward-compatibility/rollback decision; if it is irreversible, the updater must restore the pre-update database when rolling back the binary. If rollback itself fails, stop destructive work, preserve evidence without secrets, keep the safest known firewall state and SSH access, and print recovery commands referencing the snapshot.

### Atomic filesystem and interrupted-operation safety

- Use an exclusive lock for install, update, restore, uninstall, and user mutation. Read-only status may run concurrently only against a consistent snapshot.
- Journal transaction phase and paths using opaque identifiers, never secret content. Flush critical state before making the next irreversible transition.
- Stage replacement files on the same filesystem as their target, validate owner/mode/type and that no path component is a symlink, then atomically rename.
- Keep old live files until new state and health checks succeed. Never truncate in place.
- Reject unexpected pre-existing files, directories, links, mount points, service units, firewall rules, users, or ports rather than silently adopting/deleting them.
- Validate free disk both for the new artifact and for rollback/backup overhead. Disk-full behavior must leave the original service intact.
- Signal handlers set cancellation and allow the transactional layer to stop at a safe boundary; they must not perform complex filesystem mutation directly.

## Second implementation audit

### Scope and result

| Field | Value |
| --- | --- |
| Date | 2026-08-13 |
| Scope | All current Go source, shell bootstrap/tests, GitHub Actions workflow, build metadata, checked-in documentation, and generated-artifact locations |
| Method | Independent source review, privileged/data-flow tracing, static searches, unit tests, `go vet`, and a clean build |
| Reachable mutating commands | `user add`, `user remove`, and plaintext `backup`; `install`, `update`, `uninstall`, and `restore` fail closed |
| Overall result | **Release blocked:** one reachable Critical and one reachable High remain open |

This review did not trust the pre-implementation findings as proof. It traced the current entry point in `cmd/vpnctl/main.go` through the CLI, service, state, backup, local API, installer staging, bootstrap, and CI workflow. Code and files changed concurrently during the audit, so the final pass re-enumerated the tree and re-ran the available checks against the resulting snapshot.

The current tree contains no published `bin/`, `dist/`, or root-level `vpnctl` artifact; those paths are ignored and stale binaries were quarantined under ignored `.tools/`. This closes the observed stale-binary distribution issue for the repository snapshot, but a release workflow must still rebuild artifacts from the reviewed revision and publish their authenticated digests/provenance.

### Findings

| ID | Severity | Reachability/status | Location | Finding, reproduction, and required remediation |
| --- | --- | --- | --- | --- |
| SIA-01 | **Critical** | **Open; reachable bootstrap** | `scripts/install.sh:6`, `scripts/install.sh:55-92`, `scripts/install.sh:102` | The root bootstrap downloads both `vpnctl_*.tar.gz` and `checksums.txt` from the same caller-selectable HTTPS release base and authenticates the artifact only with that checksum. Compromise of the release account, domain/CDN, or configured endpoint lets an attacker publish matching malicious bytes and checksum; the script then installs them as `/usr/local/bin/vpnctl` and executes the binary as root. Reproduction: serve an attacker binary and matching `checksums.txt` at an HTTPS `VPNCTL_RELEASE_BASE`, then run the documented bootstrap. **Remediation:** verify a signed, versioned manifest or provenance against an independently pinned publisher identity/trust root before installing anything; bind version, repository identity, filename, OS/architecture, size, and digest; define signed key rotation/revocation. The checksum remains useful for corruption detection but is not publisher authentication. Do not publish the one-line root installer before this is implemented and tested. |
| SIA-02 | **High** | **Open; reachable management path** | `internal/app/service.go:49-68`; `internal/xui/client.go:46-74`, `internal/xui/client.go:106-115`, `internal/xui/client.go:155-161` | A persistent administrative API bearer token is sent over unauthenticated HTTP to a high loopback port. If 3x-ui is stopped, a low-privileged local user can discover the listener port through normal local socket enumeration, bind it, receive the next `vpnctl status`, `doctor`, `user`, or `backup` request, and capture `Authorization: Bearer …`; the token can later administer the real panel. Reproduction: observe the panel listener, stop or wait for failure of 3x-ui, bind an HTTP listener on `127.0.0.1:<panel-port>` as an unprivileged user, then have root run `vpnctl status`. **Remediation:** use an upstream-supported Unix socket with peer permissions or authenticated local TLS with a pinned server identity. A process/port ownership pre-check alone is TOCTOU and is insufficient. Until then, the supported threat model must not claim protection from low-privileged local users. |
| SIA-03 | High | **Mitigated by reachability gate; functionality incomplete** | `internal/cli/cli.go:309-317`; former restore service flow removed | The reviewed intermediate tree imported an untrusted backup database through the current API, then performed health/rollback through an API client constructed from backup-controlled port/path/token. This could leave the live DB replaced and send a token to an attacker-controlled loopback service. The CLI now returns `RESTORE_UNAVAILABLE` before reading a backup, and the dangerous service method was removed. The regression test `TestRestoreFailsClosed` verifies the gate. **Before enabling restore:** implement an offline service-level transaction whose rollback endpoint and credentials are anchored in pre-restore state; authenticate/encrypt the archive; validate the staged DB and migration; kill/restart services deliberately; test token/port/path changes and interruption at every phase. |
| SIA-04 | High | **Mitigated by reachability gate and code removal; functionality incomplete** | `internal/cli/cli.go:85-90`; `internal/installer/installer.go` is staging-only | An intermediate installer orchestrator exposed panel passwords in process arguments, installed an upstream systemd unit without hardening, could overwrite/delete a pre-existing unit, and could leave UFW deny enabled while deleting the SSH allow rule during rollback. That orchestrator was removed. `install`, `update`, and `uninstall` now return `*_UNAVAILABLE`, covered for install by `TestInstallFailsClosed`; no CLI path calls installer staging. **Before enabling install:** implement transactional firewall restoration, inventory/pre-existing-resource checks, secret-safe configuration transfer, authenticated local API, hardened dedicated service identities, interruption recovery, and destructive Ubuntu VM tests. |
| SIA-05 | High | **Remediated in repository snapshot** | `.gitignore`; ignored `.tools/` | Previously checked-in executables contained the old reachable restore implementation even after source was changed to fail closed. They were moved out of publishable paths; `bin/`, `dist/`, `/vpnctl`, `.tools/`, and executable test outputs are ignored. **Release requirement:** build from a clean immutable revision, fail if the worktree is dirty, verify embedded version/revision, publish only workflow outputs, and attach signed manifest/provenance. Never copy `.tools/` artifacts into a release. |
| SIA-06 | Medium | **Open; documentation/DoD gap** | `README.md:3-32`, `README.md:74-90` | README still describes a working installer wizard, idempotent repeated installation, verified update/rollback, and ownership-aware uninstall even though all four commands fail closed. The pre-release warning does not cure executable examples and present-tense claims. Reproduction: build current source and run any documented `vpnctl install`, `update`, or `uninstall` command; it exits 3 with `*_UNAVAILABLE`. **Remediation:** label the repository as a non-functional security prototype at the top, list the actually usable commands, remove the one-line install and unsupported examples/claims until their release gates pass, and keep the Definition of Done explicitly unmet. |
| SIA-07 | Medium | **Open; explicit but insecure export** | `internal/cli/cli.go:290-307`; `internal/backup/backup.go:65-198`; `README.md:55-65`; `SECURITY.md:43` | Backups are plaintext secret aggregates. Creation correctly requires explicit `--plaintext`, refuses overwrite, uses `0600`, validates a SQLite header, and writes atomically, but README's example omits the required flag and `SECURITY.md` says portable backups should be encrypted. File permissions do not protect a copied archive. **Remediation:** make authenticated encryption the normal portable-backup format; keep plaintext export as an exceptional, clearly named opt-in if needed; align documentation and add canary tests showing secrets never enter logs. This does not become High while plaintext creation remains explicit and restore remains disabled. |
| SIA-08 | Medium | **Open; release process incomplete** | `.github/workflows/ci.yml`; `Makefile` | CI pins current actions to full commit SHAs and uses read-only contents permission, but there is no release workflow, signed manifest, attestation/provenance verification, SBOM, `govulncheck`, secret scan, or published reproducibility check. `Makefile release` produces unsigned binaries without archive/checksum metadata. **Remediation:** add a least-privilege protected release workflow, security/dependency scanning, authenticated build provenance, SBOM, deterministic release packaging, and consumer verification tests. Do not claim a SLSA level until assessed. |
| SIA-09 | Medium | **Open; reachable terminal output** | `internal/xui/resources.go:100-135`; `internal/cli/cli.go:201-208`, `internal/cli/cli.go:234-257` | VLESS links are accepted by string prefix plus CR/LF exclusion only, and panel-provided client names/links are printed without general control-character sanitization or a strict length/URI grammar. A compromised or impersonated local panel can return ANSI/OSC/C0/C1 content in a `vless://` value or client email and inject terminal control sequences into a root operator's terminal. Reproduction: return a successful links/list response containing `vless://` followed by an ESC/OSC sequence, then run `vpnctl qr --format uri` or `vpnctl user show/list`. **Remediation:** strictly parse and canonicalize VLESS URIs, cap field/link lengths, reject all control/format characters, and use one terminal-safe rendering routine for every untrusted string. Keep JSON semantically unmodified but bounded. |
| SIA-10 | Medium | **Open; tests failing** | `internal/domain/types.go:66-80`; `internal/domain/types_test.go` | State validation accepts unsupported architectures, malformed/control-character public addresses and SNI values, a traversing panel base path, and a Reality target without a port. State files are root-only, which limits direct exploitation, but these fields feed API routing and terminal/status output and would become backup-controlled if restore is enabled. The current regression test `TestValidateStateRejectsUnsafeRoutingFields` reproduces all six cases and fails. **Remediation:** allowlist supported architectures, parse the public address and Reality host/port, enforce a canonical local panel path, reject controls and ambiguous encodings, and keep the regression test green before release. |

### Reachability conclusion

`install`, `update`, `uninstall`, and `restore` are currently safe only in the narrow sense that they refuse to operate. Their implementation requirements and original Definition of Done remain unmet. Installer archive staging is compiled but has no CLI caller; it does not justify any installation claim.

The reachable `status`, `doctor`, `user`, and `backup` paths use root-owned validated state and a loopback-only URL, bound response sizes/timeouts, no proxy, strict user-name validation, direct HTTP request construction, and explicit client-secret output. No command/shell injection or archive path traversal was found in those paths. However, SIA-02 breaks the local-adversary boundary because loopback location alone does not authenticate the server.

Restore parsing rejects URLs/stdin, non-regular inputs, links/special archive members, unexpected paths, duplicates, broad modes, oversized content, incomplete/duplicate manifests, checksum mismatches, unknown JSON fields, incompatible schemas, incomplete secrets, and invalid state. Since restore is unreachable, these are defense-in-depth parser properties rather than evidence of a safe restore transaction.

### Verification evidence and limits

Run against the final snapshot with the bundled Go toolchain executable available in `.tools`:

```text
go version: go1.25.6 windows/amd64
go test ./...: FAIL — TestValidateStateRejectsUnsafeRoutingFields accepts six unsafe state variants (SIA-10)
go vet ./...: PASS
go build -trimpath ./cmd/vpnctl: PASS (audit output kept under ignored .tools)
```

Static searches covered external process execution, shell invocation, unsafe permissions, recursive deletion, temporary files, rename/write operations, archive readers, network clients, authorization headers, secret fields/output, mutable versions, checksum use, and CLI reachability.

The following required evidence was unavailable in this Windows audit environment and remains open:

- ShellCheck and bootstrap execution because no usable Bash/ShellCheck executable was installed locally; CI declares both but this audit did not observe a CI run.
- Go race testing because the available Windows toolchain has CGO disabled; Linux CI also does not currently run `go test -race`.
- Ubuntu 22.04/24.04/26.04 destructive VM provisioning, reboot, interrupted transaction, UFW/nftables coexistence, SSH preservation, systemd hardening, and external IPv4/IPv6 scans.
- Live integration against pinned 3x-ui v3.5.0 and verification of the recorded upstream artifact digests from an independent authenticated source.
- A real release artifact, signature, attestation, SBOM, clean-build provenance, or reproducibility comparison.

### Release decision

**Do not release or publish the bootstrap.** SIA-01 (Critical) and SIA-02 (High) are open. The requirement that the second audit contain no Critical/High findings is not met. After fixes, both findings need regression tests and an independent retest; the VM and release-workflow gaps above must also close before any production claim.

### Post-audit remediation addendum

The lead applied and locally retested the following changes after the second-audit snapshot:

- panel state now requires a privileged loopback port (`1..1023`), preventing an unprivileged local process from impersonating the stopped panel on that recorded port under the current root-run upstream service model;
- VLESS links are parsed, require user/host fields, and reject whitespace and control characters;
- state validation now allowlists architecture, parses the public IP and Reality target, validates SNI and requires a canonical panel path;
- backup manifests require the exact unique file set, validate SQLite on create/read, and publish with a no-replace hard link;
- user operations verify membership in the managed inbound; global deletion refuses multi-inbound clients;
- README now describes the project as a non-installable pre-release scaffold.

Local retest after these changes: `go test ./...` PASS and `go vet ./...` PASS. SIA-02 is mitigated for the current root-service design but still requires an independent Linux/local-adversary retest and a documented decision before release; moving 3x-ui to a non-root service requires authenticated TLS or a permissioned Unix socket instead. SIA-01 remains Critical and open, so the release decision remains **blocked**.

## Predefined second-audit checklist

The second review must inspect the merged implementation rather than confirm this design document. Record every finding with severity, file/line, exploit scenario, reproduction, remediation, owner, and retest evidence. Critical/High issues must be fixed and independently retested before release.

### Source and static review

- [ ] Inventory every privileged entry point, external command, network request, writable path, systemd unit, firewall mutation, archive parser, and secret field.
- [ ] Confirm `install.sh` is minimal, starts with `set -Eeuo pipefail` and `umask 077`, uses no `eval`, shell interpolation, `set -x`, predictable temp path, `curl -k`, upstream installer execution, `latest`, or unexplained `|| true`.
- [ ] Run ShellCheck on all shell files with pinned tool version; review every suppression manually.
- [ ] Run `go test ./...`, race tests where useful, `go vet`, `govulncheck`, and the project's pinned security/static scanners; review results rather than accepting a green exit alone.
- [ ] Search for `math/rand`, `InsecureSkipVerify`, `exec.Command("sh"|"bash")`, `0777`/`0666`, direct SQLite edits, raw config/HTTP body logging, weak hashes, insecure archive extraction, and secrets in flags/environment.
- [ ] Trace tainted names, paths, URLs, manifest fields, archive members, API responses, and environment variables to every filesystem, command, log, terminal, and network sink.
- [ ] Confirm dependency/action/toolchain versions and release artifact digests are immutable and no install/update path resolves a floating reference.
- [ ] Review release workflow token permissions, branch/tag protection, environment approval, provenance generation, signing identity, key rotation, and compromise response.

### Secret tests

- [ ] Inject unique canaries for every secret type; run install, user add/show/remove, QR, status, doctor, backup/restore, update failure/rollback, and uninstall; assert no canary or encoded VLESS URI appears in stdout/stderr when non-interactive, journald, files, panic/error output, process list, shell history, or support output.
- [ ] Verify exact owner/mode for state, configs, logs, runtime files, temp files, database, keys, units, executables, backups, and rollback snapshots after success and every injected failure.
- [ ] Confirm no default/empty panel credential survives and passwords are stored only using upstream's supported slow hash behavior.
- [ ] Confirm UUID/password/path/short-ID generation uses the OS CSPRNG and Reality keys come from the pinned supported generator.
- [ ] Verify QR and share URI appear only after explicit request and non-TTY behavior cannot leak them to routine logs.
- [ ] Inspect a portable backup for plaintext canaries and verify wrong passphrase/key and modified ciphertext fail before extraction.

### Installer and supply-chain tests

- [ ] Test wrong signature/key/repository identity, corrupted artifact, wrong checksum, wrong OS/arch, oversized/truncated response, HTTP downgrade redirect, redirect to an unapproved host, timeout, DNS failure, GitHub outage, stale/expired/replayed manifest, revoked/rotated key, prerelease, and downgrade.
- [ ] Verify no bytes from an unverified artifact are parsed by a risky extractor or executed.
- [ ] Compare release provenance/SBOM to the tag, workflow, pinned toolchain, dependencies, and published digests. Rebuild reproducibly where claimed.
- [ ] Verify the preferred install path authenticates publisher identity independently; explicitly document and accept/reject the one-line bootstrap residual risk.

### Filesystem and restore adversarial tests

- [ ] Attempt install/restore/update/uninstall through symlinked parents/targets, hardlinks, mount points, attacker-writable directories, replaced paths between validation/use, unexpected owners, read-only filesystem, full disk/inodes, and cross-filesystem staging.
- [ ] Restore archives containing absolute paths, `../`, duplicate/ambiguous paths, symlink-then-file, hardlinks, devices/FIFOs/sockets, setuid bits, huge sparse files, excessive count/depth, zip/tar bombs, invalid UTF-8/control names, forged modes/owners/xattrs, truncated members, and manifest/file mismatches.
- [ ] Verify extraction remains inside staging and no write occurs to live state before authentication and complete validation.
- [ ] Confirm uninstall deletes only inventory-owned resources and preserves adopted/pre-existing 3x-ui/Xray/firewall data.

### Network and service tests

- [ ] On clean Ubuntu 22.04, 24.04, and 26.04 VMs for amd64/arm64 where available, scan all TCP/UDP ports externally over IPv4 and IPv6. Only detected SSH and selected VLESS TCP may be public by default.
- [ ] Confirm panel/API bind only to loopback and cannot be reached through public addresses, IPv6, container interfaces, or provider networking.
- [ ] Validate deny-by-default firewall policy, required ICMPv6 behavior, reboot persistence, rule ownership, idempotency, and no broad source ranges/ports.
- [ ] Exercise existing UFW rules, nonstandard SSH port, nftables/iptables conflicts, IPv6 disabled, IPv6-only host, and active SSH while applying/rolling back firewall. Verify a second SSH session before accepting success.
- [ ] If public panel mode exists, test TLS hostname/certificate validation, HTTP rejection/redirect policy, 2FA gate, rate limits/fail2ban, trusted proxy headers, IP allowlist, session invalidation, and API route exposure.
- [ ] Run `systemd-analyze security` on installed units and manually verify users, capabilities, writable paths, address families, syscall policy, core dumps, restarts, and secret-free journald output.

### Transaction, update, and rollback tests

- [ ] Kill the process and reboot at every transaction phase for install, user mutation, backup, restore, update, rollback, and uninstall. Re-running must recover safely and remain idempotent.
- [ ] Inject service start failure, config validation failure, API failure, occupied port, firewall failure, incompatible database, corrupted local snapshot, and health-check timeout.
- [ ] Prove failed update/restore returns binary, database, config, units, and firewall to the previous healthy version; verify VPN and SSH after rollback.
- [ ] Verify concurrent mutating commands serialize and stale locks are recovered without allowing simultaneous writers.
- [ ] Confirm rollback cannot fetch mutable network content and uses only locally recorded verified artifact/digest/state.

### Final report gate

- [ ] No open Critical or High findings.
- [ ] Every fixed Critical/High has regression coverage and independent retest evidence.
- [ ] Medium residual risks have an owner, documented operational mitigation, and explicit release acceptance.
- [ ] `SECURITY.md` contains a working private reporting channel and accurate supported-version policy.
- [ ] Install, update, backup, restore, public-panel, and incident-response documentation match actual secure behavior.
- [ ] Release claims about signing, provenance, SLSA level, supported platforms, encryption, firewall, or telemetry are evidence-backed.

## References

Sources were checked on 2026-08-13 unless a publication date is shown.

- [NIST SP 800-218, Secure Software Development Framework 1.1](https://csrc.nist.gov/pubs/sp/800/218/final), published 2022-02-03.
- [SLSA specification v1.2](https://slsa.dev/spec/v1.2/) and [threats and mitigations](https://slsa.dev/spec/v1.2/threats).
- [GitHub Actions secure use reference](https://docs.github.com/en/actions/reference/security/secure-use), including full-commit action pinning and least-privilege guidance.
- [GitHub artifact attestations](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations) and [attestation concepts](https://docs.github.com/en/enterprise-cloud@latest/actions/concepts/security/artifact-attestations), which explicitly require consumer verification for security benefit.
- [Ubuntu Server firewall documentation](https://ubuntu.com/server/docs/security-firewall/) and [Ubuntu 24.04 UFW manual](https://manpages.ubuntu.com/manpages/noble/en/man8/ufw.8.html).
- [Ubuntu 24.04 `systemd.exec` manual](https://manpages.ubuntu.com/manpages/noble/man5/systemd.exec.5.html) and [`systemd-analyze security`](https://manpages.ubuntu.com/manpages/noble/man1/systemd-analyze.1.html).
- [Go `crypto/rand` documentation](https://pkg.go.dev/crypto/rand), [Go `os.CreateTemp` documentation](https://pkg.go.dev/os#CreateTemp), and [Go traversal-resistant file APIs](https://go.dev/blog/osroot), published 2025-03-12.
- [OWASP Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html) and [Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).
- [3x-ui panel settings](https://github.com/MHSanaei/3x-ui/blob/main/docs/content/docs/en/config/panel.mdx) and [3x-ui Reality settings](https://github.com/MHSanaei/3x-ui/blob/main/docs/content/docs/en/config/reality.mdx). These are mutable upstream documents; the implementation must pin and review a concrete release.
- [Official Xray installer checksum verification](https://github.com/XTLS/Xray-install/blob/main/install-release.sh). Its checksum step is useful for integrity, but vpnctl must still authenticate its own signed release manifest and pin the selected Xray version.
- [ShellCheck](https://www.shellcheck.net/) for shell static analysis.
