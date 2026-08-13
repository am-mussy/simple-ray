# vpnctl

Security-focused pre-release scaffold for a one-command Xray VPN installer.

vpnctl installs a pinned 3x-ui release with its bundled Xray core and configures one VLESS TCP Reality inbound. The panel stays on loopback; only SSH and the VPN port are public by default.

> This repository is not installable or production-ready. `install`, `update`, `restore`, and `uninstall` deliberately exit with code 3 until their security and rollback gates are implemented. Do not publish or run the bootstrap yet.

## Planned install flow

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

The planned wizard asks for the first VPN user, shows the firewall exposure, installs pinned components and displays the QR code. It is not wired in the current build.

Planned cloud-init form:

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

## Everyday commands

```bash
sudo vpnctl status
sudo vpnctl doctor
sudo vpnctl user add phone
sudo vpnctl user list
sudo vpnctl qr phone
sudo vpnctl user remove phone
```

User commands require existing valid vpnctl state and verify membership in the managed inbound.

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

Uninstall is fail-closed in the current build. The intended implementation will remove only inventory-owned resources and keep backups by default.

## Security

Read [SECURITY.md](SECURITY.md). The current bootstrap verifies checksums but does not authenticate publisher identity because the checksum comes from the same channel; the second audit classifies this as Critical and blocks publication.

## Current validation status

Local compilation and automated tests are necessary but insufficient. A production release additionally requires the full Ubuntu 22.04/24.04 × amd64/arm64 provisioning matrix, reboot and rollback tests, external IPv4/IPv6 scans, and a second independent security audit with no open Critical/High findings.
