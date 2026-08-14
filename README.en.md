# vpnctl

*[Русская версия](README.md)*

Security-focused pre-release scaffold for a one-command Xray VPN installer.

vpnctl installs a pinned 3x-ui release with its bundled Xray core and configures one VLESS TCP Reality inbound. The panel stays on loopback; only SSH and the VPN port are public by default.

> This repository is not ready for a public release. Direct `vpnctl install` and `uninstall` work, but the public bootstrap is blocked until publisher-authenticated release metadata exists. `update` and `restore` deliberately exit with code 3.

## Install flow

Until signed GitHub Releases are available, install directly from a reviewed source checkout:

```bash
git clone https://github.com/am-mussy/simple-ray.git
cd simple-ray
bash start.sh
```

`start.sh` downloads a checksum-pinned temporary Go toolchain, runs tests, vet and the build without root privileges, elevates only for the atomic install and system setup, then starts the interactive wizard. It does not install Go system-wide.

Download the bootstrap first if you want to inspect it:

```bash
curl -fSLo install.sh https://<DOMAIN>/install.sh
less install.sh
sudo bash install.sh
```

After a signed release channel and VM gates exist, the intended short form is:

```bash
curl -fsSL https://<DOMAIN>/install.sh | sudo bash
```

The wizard asks for the first VPN user, shows the firewall exposure, installs pinned components and displays the QR code.

Non-interactive form:

```bash
sudo vpnctl install --non-interactive --mode recommended --user mustafa
sudo vpnctl qr mustafa
```

`vpnctl qr` prints sensitive client configuration. Do not send it to shared logs.

## Supported systems

| OS | Architecture |
|---|---|
| Ubuntu 22.04 LTS | amd64, arm64 |
| Ubuntu 24.04 LTS | amd64, arm64 |
| Ubuntu 26.04 LTS | amd64, arm64 |

## Everyday commands

```bash
sudo vpnctl status
sudo vpnctl doctor
sudo vpnctl user add phone
sudo vpnctl user list
sudo vpnctl qr phone
sudo vpnctl qr phone --fingerprint chrome
sudo vpnctl user remove phone
```

User commands require existing valid vpnctl state and verify membership in the managed inbound.

## Client links

`vpnctl qr` does not pass through whatever 3x-ui generates. It rewrites the URI
into a canonical form, so the same user always gets the same link and every
field a client needs is present and known-good: `flow=xtls-rprx-vision`,
`type=tcp`, `encryption=none`, `spx=/` and a pinned uTLS fingerprint. Each of
those fields, left to drift, produces the same failure — the tunnel connects
and carries no traffic — so none of them are inherited.

The default fingerprint is `safari`. Use `--fingerprint` to hand a device
another profile when its app connects but moves nothing; `chrome`, `firefox`,
`ios` and `edge` are the alternatives. Profiles outside that set are rejected
rather than emitted, because they fail the Reality handshake on at least one
client core in common use. See
[docs/client-compatibility.md](docs/client-compatibility.md) for the verified
matrix and its limits.

## Backup and restore

```bash
sudo vpnctl backup --plaintext
sudo vpnctl restore /var/lib/vpnctl/backups/vpnctl-backup-YYYYMMDD-HHMMSS.tar.gz
```

Backups are root-readable and contain VPN and panel secrets. The MVP archive is not encrypted; encrypt it with an audited external tool before moving it off the server.

Restore is fail-closed in the current development build. It will be enabled only after an offline service-level transaction can prove rollback even when the restored database changes the panel token, port, or base path.

## Panel access

The panel binds to `127.0.0.1`. Open an SSH tunnel from your computer using the panel port printed by `vpnctl status`, then use the local forwarded address. vpnctl does not expose plain HTTP administration to the internet.

## Update

```bash
sudo vpnctl update --check
sudo vpnctl update --version <version>
```

Update is fail-closed in the current build. The intended design uses explicit versions, verified artifacts, backup, health checks and rollback; silent updates and `latest` are forbidden.

## Troubleshooting

Run `sudo vpnctl doctor`. It separates system, network, service and configuration checks and gives a safe next action. See [docs/troubleshooting.md](docs/troubleshooting.md) for common failures.

## Uninstall

```bash
sudo vpnctl uninstall
```

Uninstall removes only inventory-owned resources and keeps backups by default. Use `--remove-backups` to remove them too. The `vpnctl` executable is retained because it is owned by the bootstrap/release layer, not the managed VPN inventory.

## Security

Read [SECURITY.md](SECURITY.md). The current bootstrap verifies checksums but does not authenticate publisher identity because the checksum comes from the same channel; the second audit classifies this as Critical and blocks publication.

## Current validation status

Local compilation and automated tests are necessary but insufficient. A production release additionally requires the full Ubuntu 22.04/24.04/26.04 × amd64/arm64 provisioning matrix, reboot and rollback tests, external IPv4/IPv6 scans, and a second independent security audit with no open Critical/High findings.

Client links are verified against six client cores over a real network path
between two networks; see [docs/client-compatibility.md](docs/client-compatibility.md).
That matrix does not extend to mobile carrier paths, so a green `vpnctl doctor`
means the server is sound, not that every app on every network will pass
traffic.
