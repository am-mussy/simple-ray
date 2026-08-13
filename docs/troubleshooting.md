# Troubleshooting

Start with:

```bash
sudo vpnctl doctor
```

## Xray or 3x-ui is not running

Inspect the safe summary from `vpnctl doctor`, then the service journal:

```bash
sudo systemctl status x-ui
sudo journalctl -u x-ui --since '15 minutes ago'
```

Do not paste panel tokens, VLESS URIs, UUIDs, Reality keys or complete database/config dumps into an issue.

## VPN port is not reachable

Verify the port in `vpnctl status`, the local UFW rule and the VPS provider firewall. vpnctl cannot configure a provider firewall. Test from another network; testing the public address from the server itself may fail because of NAT loopback behavior.

## Panel is not reachable

This is expected from the public internet. The secure default binds it to loopback. Create the SSH tunnel shown by `vpnctl status` and open the local forwarded URL.

## Installation was interrupted

Run the same install command again. vpnctl reads its transaction journal, validates committed state and resumes or rolls back from a safe boundary. Do not manually remove `/usr/local/x-ui`, `/etc/x-ui` or `/var/lib/vpnctl`.

## GitHub or download failure

No unverified artifact is installed. Restore connectivity and repeat the command. A checksum mismatch is not a transient warning: stop and verify the requested version and published release metadata.

## Existing 3x-ui or Xray installation

The MVP refuses to adopt unmanaged installations because it cannot safely infer ownership or rollback scope. Back up the existing service and migrate it through a documented future adoption flow; do not force-delete it.

## Backup restore failed

vpnctl validates compatibility and takes a safety backup before live mutation. Read the rollback result from the command and run `vpnctl doctor`. Preserve the named safety backup and logs until the cause is understood.
