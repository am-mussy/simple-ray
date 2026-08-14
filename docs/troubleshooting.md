# Troubleshooting

Start with:

```bash
sudo vpnctl doctor
```

## The client connects but no traffic flows

This is the characteristic Reality failure and the hardest to read, because
nothing looks broken. When the server cannot authenticate a client it does not
refuse the connection — it relays that client to the decoy site. The app shows
a healthy tunnel, the handshake succeeds against a genuine certificate, and no
byte reaches the internet. Client logs name it directly:

```
REALITY: received real certificate (potential MITM or redirection)
```

Work through it in this order.

1. Run `sudo vpnctl doctor`. The tunnel check pushes real traffic through the
   link a user is given. If it fails, the server side is at fault and the check
   names the next action.
2. If doctor is green, re-import the link. `sudo vpnctl qr <name>` emits a
   canonical link on every call; a profile edited by hand or imported from an
   older link may carry a stale fingerprint, a missing `flow`, or a `type` the
   client core does not understand. Any of the three produces exactly this
   symptom.
3. If a re-imported link still carries nothing on one device while other
   devices work, hand that device another uTLS profile:

   ```bash
   sudo vpnctl qr phone --fingerprint chrome
   ```

   Available profiles are `safari` (default), `chrome`, `firefox`, `ios` and
   `edge`. All five are verified against the Xray cores client apps embed, but
   an individual app build or carrier path can still reject one of them, and no
   check on the server can predict which.

Note that a green doctor is not a promise that every client app will work.
The probe dials the server's own address, which the kernel routes over
loopback, so it proves the server and says nothing about a phone's network
path.

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
