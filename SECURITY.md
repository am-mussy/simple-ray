# Security Policy

`vpnctl` installs and updates root-level networking software. A compromise of the installer, update channel, release artifact, panel credential, or backup can therefore compromise the entire VPS.

The detailed threat model and release gates are in [docs/security-audit.md](docs/security-audit.md).

## Supported versions

The project is currently pre-release. There is no production-supported version yet.

Before the first stable release, this section must be replaced with an explicit support table. The intended policy is to provide security fixes for the current stable release; older releases are unsupported unless a release announcement says otherwise.

## Reporting a vulnerability

Use GitHub private vulnerability reporting from the repository's **Security** tab. Private vulnerability reporting must be enabled before the first public release.

Do not open a public issue for a suspected vulnerability and do not include secrets, live server addresses, client links, QR codes, panel credentials, API tokens, Reality private keys, or backups in a report.

Include, where available:

- affected `vpnctl`, 3x-ui, and Xray versions;
- Ubuntu version and architecture;
- impact and required attacker access;
- minimal reproduction steps or a proof of concept using dummy credentials;
- sanitized logs and whether IPv4, IPv6, or both are affected;
- suggested remediation or disclosure constraints.

Maintainers should acknowledge a report within three business days, provide an initial severity assessment within seven business days, and coordinate disclosure after a fix is available. These are targets, not a contractual SLA.

## Disclosure and remediation

Security reports are handled privately until a mitigation is available. A fix should include a regression test and, when appropriate, a GitHub Security Advisory and CVE. Release notes must identify affected versions, required user action, credential rotation requirements, and whether backups or client configurations should be replaced.

Critical and High findings block a release. A compromised signing identity, release workflow, update manifest, or distributed binary is treated as a security incident even when no exploitation has been confirmed.

## Secure installation and operation

- Prefer the documented download, verification, and execution flow over piping a network response directly to a root shell.
- Install only versioned release artifacts whose digest and release identity have been verified. A checksum downloaded from the same unauthenticated location as the artifact detects corruption but does not establish publisher identity.
- Keep the 3x-ui panel and its API bound to loopback by default and access them through SSH port forwarding. A random port or path is only defense in depth.
- Never expose the panel over plaintext HTTP. Public exposure requires explicit opt-in, trusted TLS, rate limiting, an IP allowlist where possible, and 2FA.
- Treat VLESS URLs, QR codes, UUIDs, short IDs, panel/API credentials, Reality private keys, and backups as secrets. Reveal them only on explicit request and never place them in persistent logs or command-line arguments.
- Keep portable backups encrypted. Plaintext backups are secret-bearing archives even when their mode is `0600`.
- Run `vpnctl update` explicitly. The project does not perform silent automatic updates.

## Security boundaries

The project aims to protect against network attackers, unauthenticated panel access, malicious restore archives, accidental disclosure, low-privileged local users, interrupted privileged operations, and tampering with distributed artifacts.

It cannot protect a server after the VPS provider, kernel, root account, trusted release signing identity, or an already-running privileged `vpnctl` binary has been compromised. Traffic-analysis resistance, anonymity guarantees, and security of third-party client applications are outside this project's direct control.

