# vpnctl architecture

Research date: **2026-08-13**. The integration contract in this document is pinned to **3x-ui v3.5.0**. It must not silently follow `latest` or `main`.

## Decision summary

`vpnctl` is a statically linked Go CLI and the only public management interface. The bootstrap script downloads a signed-project release of `vpnctl`, verifies a checksum bundled with the `vpnctl` release, and then runs `vpnctl install`. `vpnctl` installs a verified, pinned 3x-ui release archive directly and manages 3x-ui through its supported binary CLI and authenticated HTTP API.

3x-ui remains the sole owner of the Xray process and configuration. Its v3.5.0 release archive already contains the matching Xray binary (Xray-core v26.7.11). `vpnctl` must not install a second Xray package, create a second `xray.service`, or edit generated Xray JSON behind 3x-ui's back.

The supported target matrix is:

| OS | Architectures | Status |
|---|---|---|
| Ubuntu 22.04 LTS | amd64, arm64 | supported |
| Ubuntu 24.04 LTS | amd64, arm64 | supported |
| Ubuntu 26.04 LTS | amd64, arm64 | supported |

Go is selected because it produces one binary for each target architecture, has `crypto/rand`, atomic filesystem primitives, a mature HTTP client, and needs no runtime on a clean VPS.

## Components and ownership

```text
install.sh
  -> verifies vpnctl release artifact
  -> /usr/local/bin/vpnctl
       -> install/update transaction coordinator
       -> local 3x-ui API client
       -> UFW adapter
       -> state, backup and health checks

systemd
  -> x-ui.service
       -> /usr/local/x-ui/x-ui
            -> bundled /usr/local/x-ui/bin/xray-linux-{amd64|arm64}
```

Paths owned by `vpnctl`:

| Path | Purpose | Mode |
|---|---|---|
| `/usr/local/bin/vpnctl` | executable | `0755`, root:root |
| `/var/lib/vpnctl/state.json` | non-secret installation state and schema version | directory `0700`, file `0600` |
| `/var/lib/vpnctl/secrets.json` | local panel API token and generated installation secrets that must be retained | `0600`, root:root |
| `/var/lib/vpnctl/backups/` | local backups | directory `0700`, files `0600` |
| `/var/log/vpnctl/` | structured operational logs with secret redaction | directory `0750`; no VLESS URIs, tokens, passwords or private keys |
| `/usr/local/x-ui/` | pinned upstream 3x-ui release | upstream-compatible ownership and executable modes |
| `/etc/x-ui/` | 3x-ui database/configuration | owned by 3x-ui; never edited as SQLite by `vpnctl` |

`state.json` records at least the vpnctl version, 3x-ui version, architecture, managed inbound ID and remark, panel port/base path/listen address, public address, Reality target/SNI, firewall rules created by vpnctl, installation phase, and last known-good backup. Secrets are referenced separately so normal status and logs do not load or serialize them.

## Pinned 3x-ui installation

### Why the upstream installer is not executed

The official installer supports a version argument, but the v3.5.0 script still downloads `x-ui.sh` from the mutable `main` branch and does not verify a release archive checksum. Its update path also removes the existing installation directory before it has proved that the new archive extracts correctly. Those properties are unacceptable for an idempotent installer with rollback.

The selected approach is therefore a direct installation of the official release archive. This does not fork 3x-ui: it uses the exact files published in its release and its official service layout.

### Reference pin

| Target | Artifact | SHA-256 |
|---|---|---|
| linux/amd64 | `x-ui-linux-amd64.tar.gz` | `684cde5996098dc9384878faa99ac13b341883ec79b81948b1900e29511ee498` |
| linux/arm64 | `x-ui-linux-arm64.tar.gz` | `0205f7d0ffbb8f3deae3b45c047f08622b6ec7d9ac670a880e0ae77bdadb7514` |

The values above are the `digest` values published by the GitHub release API for tag `v3.5.0`; the amd64 archive was additionally downloaded and hashed during this research. Release URLs must contain the literal tag, for example:

```text
https://github.com/MHSanaei/3x-ui/releases/download/v3.5.0/x-ui-linux-amd64.tar.gz
```

Both archives contain the complete runtime layout:

```text
x-ui/
  x-ui
  x-ui.sh
  x-ui.service.debian
  x-ui.service.rhel
  x-ui.service.arch
  bin/
    xray-linux-<arch>
    geoip.dat
    geosite.dat
    ...
```

### Transaction

1. Require root; acquire an exclusive lock at `/run/lock/vpnctl.lock`.
2. Validate `/etc/os-release` and `uname -m`; map `x86_64 -> amd64`, `aarch64|arm64 -> arm64` and reject all other combinations.
3. Install only required distribution packages.
4. Download to a root-owned `0700` temporary directory created with `os.MkdirTemp`; refuse redirects to non-HTTPS destinations.
5. Verify the exact embedded SHA-256 before extracting.
6. Reject absolute paths, `..` traversal, symlinks and unexpected top-level entries while extracting.
7. Stage as `/usr/local/x-ui.new`; validate the required binary, bundled Xray and service file.
8. If upgrading, stop `x-ui`, retain `/usr/local/x-ui` as a rollback directory, atomically rename the staged tree into place, and keep `/etc/x-ui` unchanged.
9. Install the service unit, run `systemctl daemon-reload`, `enable --now x-ui`, and wait for local readiness.
10. Configure settings through the 3x-ui CLI, then configure inbounds and clients through its API.
11. Run the full health check. On failure, restore the previous program tree and supported database backup, reload systemd and verify rollback health.
12. Commit state only after health checks pass; always remove temporary data and release the lock.

The upstream Debian unit in v3.5.0 is the compatibility baseline:

```ini
[Unit]
Description=x-ui Service
After=network.target
Wants=network.target

[Service]
EnvironmentFile=-/etc/default/x-ui
Environment="XRAY_VMESS_AEAD_FORCED=false"
Type=simple
WorkingDirectory=/usr/local/x-ui/
ExecStart=/usr/local/x-ui/x-ui
ExecReload=/bin/kill -USR1 $MAINPID
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

The MVP should install this file from the verified archive, not download a unit from `main`. Hardening changes to the unit require separate compatibility testing because 3x-ui launches and supervises the bundled Xray child process.

### Dependencies

| Package/facility | Reason |
|---|---|
| `ca-certificates` | HTTPS verification |
| `curl` | tiny bootstrap only; vpnctl itself uses Go HTTP |
| `tar` | diagnostic/operator compatibility; production extraction should be in Go |
| `tzdata` | panel time and scheduled expiry/reset behaviour |
| `iproute2` | `ss`, addresses and route diagnostics |
| `ufw` | supported Ubuntu firewall adapter |
| `systemd` | service lifecycle and reboot persistence |
| `cron`, `socat`, `openssl` | upstream-compatible panel/ACME tooling; install when that panel functionality is enabled |

Do not install a distribution `xray` package. QR generation and cryptographic randomness belong in the Go binary and must not add Python/Node runtime dependencies.

## Panel isolation and bootstrap authentication

The recommended default binds the panel to `127.0.0.1` and does not open its port in UFW. Administrators reach it through SSH port forwarding. The public firewall surface is SSH plus the selected VLESS Reality inbound (normally TCP 443). The reviewed pre-release state contract restricts the panel to a randomly selected privileged loopback port (`1..1023`) so an unprivileged local process cannot impersonate a stopped root-run panel and capture its Bearer token. A future non-root service design must replace this mitigation with authenticated local TLS or a permissioned Unix socket. The random base path remains defense in depth, not authentication.

Supported binary settings interface in v3.5.0:

```bash
/usr/local/x-ui/x-ui setting \
  -username "$username" \
  -password "$password" \
  -port "$panel_port" \
  -webBasePath "$web_base_path" \
  -listenIP 127.0.0.1
```

Readback uses `setting -show true` and `setting -getListen true`. Secrets must be passed by an `execve` argument array, never by concatenated shell, and the command line exposure window should be documented. No value may be copied to a persistent log.

For bootstrap, v3.5.0 provides:

```bash
/usr/local/x-ui/x-ui setting -getApiToken true
```

If no token exists, it creates one named `install` and prints it once as `apiToken: ...`. If tokens already exist, plaintext cannot be recovered because only hashes are stored; the command creates a new `cli-fallback-<unix-time>` token instead. `vpnctl` must parse only the anchored `apiToken:` line, store the token in `secrets.json` with mode `0600`, redact it everywhere, and fail closed if the output is ambiguous. Repeated install must reuse its stored token rather than minting another fallback token.

All later requests use:

```http
Authorization: Bearer <token>
Content-Type: application/json
```

Bearer authentication bypasses browser CSRF handling. The local API base is `http://127.0.0.1:<panel-port>/<web-base-path>`; append documented `/panel/api/...` paths. The HTTP client must prohibit proxies for loopback requests, use timeouts, cap response bodies and check both the HTTP status and the 3x-ui response envelope (`success`, `msg`, `obj`).

## 3x-ui v3.5.0 API contract

The contract is the OpenAPI document committed at the same tag as the installed binary. Do not generate against `main` and do not use legacy endpoints found in third-party scripts.

### Inbound lifecycle

Before creation, call `GET /panel/api/inbounds/list` and find the managed inbound by a stable vpnctl remark/tag recorded in state. This is the idempotency key. If it exists, validate it; do not create another inbound merely because local state was lost.

Create with `POST /panel/api/inbounds/add`. Nested JSON objects are preferred in v3.5.0; legacy JSON-encoded strings are accepted but should not be used. The required shape for the MVP is:

```json
{
  "enable": true,
  "remark": "vpnctl-vless-reality",
  "listen": "",
  "port": 443,
  "protocol": "vless",
  "expiryTime": 0,
  "total": 0,
  "settings": {
    "clients": [],
    "decryption": "none",
    "fallbacks": []
  },
  "streamSettings": {
    "network": "tcp",
    "security": "reality",
    "externalProxy": [],
    "realitySettings": {
      "show": false,
      "xver": 0,
      "target": "www.microsoft.com:443",
      "serverNames": ["www.microsoft.com"],
      "privateKey": "<server-private-key>",
      "minClientVer": "",
      "maxClientVer": "",
      "maxTimediff": 0,
      "shortIds": ["<16-hex-chars>"],
      "mldsa65Seed": "",
      "settings": {
        "publicKey": "<client-public-key>",
        "fingerprint": "chrome",
        "serverName": "",
        "spiderX": "/",
        "mldsa65Verify": ""
      }
    },
    "tcpSettings": {
      "acceptProxyProtocol": false,
      "header": {"type": "none"}
    }
  },
  "sniffing": {
    "enabled": true,
    "destOverride": ["http", "tls", "quic"],
    "metadataOnly": false,
    "routeOnly": false
  }
}
```

The inner `realitySettings.settings.publicKey` is panel metadata used to generate correct share links; 3x-ui removes panel-only metadata when assembling the Xray runtime config. After creation, re-fetch with `GET /panel/api/inbounds/get/{id}` and verify the effective value through `GET /panel/api/server/getConfigJson` without logging the response.

Update through `POST /panel/api/inbounds/update/{id}` with the complete object returned by `get/{id}` plus intentional changes. This is replace semantics, not a patch. Deletion is `POST /panel/api/inbounds/del/{id}`. Structural calls can restart/reload Xray; serialize them under the vpnctl lock.

### Client lifecycle

Use the first-class client API, not mutation of the inbound `settings.clients` array:

| Operation | Endpoint |
|---|---|
| list | `GET /panel/api/clients/list` |
| get | `GET /panel/api/clients/get/{email}` |
| create and attach | `POST /panel/api/clients/add` |
| delete | `POST /panel/api/clients/del/{email}?keepTraffic=0` |
| share URI | `GET /panel/api/clients/links/{email}` |

The vpnctl username maps to the unique 3x-ui `email` field after strict validation and URL path escaping. Create body:

```json
{
  "client": {
    "email": "mustafa",
    "totalGB": 0,
    "expiryTime": 0,
    "tgId": 0,
    "limitIp": 0,
    "enable": true,
    "flow": "xtls-rprx-vision"
  },
  "inboundIds": [1]
}
```

For VLESS, v3.5.0 generates the UUID server-side if it is omitted. This is preferable because the API owns validation and persistence. `vpnctl user add` first performs `get/{email}`: an existing compatible user is an idempotent success; an existing incompatible user is a conflict. The share/QR command must use `clients/links/{email}` so it outputs exactly the URI generated by the installed panel.

### Key and identifier generation

| Value | Preferred source | Fallback/validation |
|---|---|---|
| Reality X25519 pair | `GET /panel/api/server/getNewX25519Cert` | bundled `xray-linux-<arch> x25519`; parse version-aware output |
| VLESS UUID | omit from `clients/add` | `GET /panel/api/server/getNewUUID` or Go UUID v4 from `crypto/rand` |
| short ID | Go `crypto/rand`, 8 bytes, lowercase hex | exactly 16 hex characters |
| panel credentials/path | Go `crypto/rand` with rejection sampling | never shell `$RANDOM`, timestamps or UUID truncation |

Xray's official REALITY format permits an even number of hexadecimal characters up to 16. vpnctl standardizes on the full 8-byte/16-character value to avoid padding ambiguity. The private key stays server-side; only the public-key-bearing VLESS URI is shown to clients.

Reality target selection is configuration, not a universal constant. The installer must test reachability and certificate/SNI compatibility of its proposed target and allow an advanced override. The official docs require a reachable TLS target and matching SNI; v3.5.0 recommends `xtls-rprx-vision` and a common fingerprint such as `chrome`.

## Backup, update and rollback

Do not copy a live SQLite file and do not modify it. Use the supported `GET /panel/api/server/getDb` endpoint, which streams the current database backup. Restore uses the destructive multipart `POST /panel/api/server/importDB` with form field `db`, followed by readiness and configuration checks. The backup envelope created by vpnctl contains:

```text
metadata.json
3x-ui/database.db (or database.dump)
vpnctl/state.json
vpnctl/secrets.json
manifest.sha256
```

The archive is root-only and records vpnctl schema, installed 3x-ui version, database type, architecture, creation time and component hashes. Compatibility is checked before any restore. A restore first creates a supported backup of the current state and rolls back to it if health checks fail.

`vpnctl update` never calls `x-ui update`, because that resolves the current stable release dynamically. It selects an explicit version from vpnctl's signed release metadata, downloads the matching architecture archive, verifies its embedded digest, creates a supported DB backup, stages the program tree, swaps it transactionally, starts it, waits for migrations, and runs the same API/Xray/firewall checks as installation. Silent auto-update is forbidden.

## Health and status interfaces

Checks should be based on public operating-system and panel interfaces:

- `systemctl is-active/is-enabled x-ui` and its stable unit name;
- `GET /panel/api/server/status` for the panel/Xray snapshot;
- `GET /panel/api/inbounds/get/{id}` for the managed inbound;
- `GET /panel/api/server/getConfigJson` for effective configuration validation, with secret-safe field inspection only;
- TCP listener ownership from `ss`/proc, not merely a successful bind probe;
- UFW state and the exact managed rules;
- a bounded outbound TLS probe to the configured Reality target;
- local panel API reachability and confirmation that its port is not allowed publicly.

`doctor` must distinguish a failed check from an unavailable diagnostic. It may suggest a repair, but must not mutate state unless the user explicitly selects a repair action.

## Stability risks

| Surface | Risk | Mitigation |
|---|---|---|
| 3x-ui HTTP API | Versioned by project release rather than a separate stable API version; payload fields can change | pin binary and tag-matched OpenAPI fixture; contract tests against real v3.5.0; explicit adapter per supported version |
| API response envelope | Many application failures are HTTP 200 with `success: false` | check HTTP status and envelope |
| Full-object inbound update | Omitted fields can be lost | GET, decode losslessly, modify, PUT-like full POST; fixture round-trip tests |
| API token CLI output | Text output is not a machine-stable JSON interface and repeated calls mint tokens | use only for one-time bootstrap, anchored parsing, persistent token, integration test for pinned version |
| Xray `x25519` output | Labels changed across Xray versions | prefer panel key endpoint; if falling back, test parser against the bundled core and reject ambiguity |
| `tcp` versus `raw` terminology | Current Xray docs call the transport RAW while 3x-ui v3.5.0 API persists `network: tcp` | send the tag-matched panel representation, not fields copied from newer Xray docs |
| Panel-only Reality metadata | `realitySettings.settings.publicKey` is not part of the runtime Xray object but is needed for panel share links | use the v3.5.0 panel schema and validate generated links contain `pbk`, `sid`, `sni`, `fp`, and `flow` |
| Bundled Xray coupling | Replacing only Xray may break panel validation/config generation | upgrade 3x-ui and bundled Xray as one artifact |
| Mutable upstream scripts | `main` can change under a pinned installer invocation | never execute or download mutable upstream scripts during install/update |
| Release integrity | GitHub asset digests authenticate content only to the release metadata channel; they are not an offline project signature | vpnctl release metadata pins SHA-256; future improvement is maintainer-signed checksums/provenance |
| Public panel | Random port/path alone do not prevent credential interception or discovery | loopback bind by default; SSH tunnel; no UFW panel rule |

Any 3x-ui version bump is a deliberate code change: update artifact digests, archive-layout assertions, the vendored OpenAPI fixture/client types, payload fixtures, CLI-output fixtures, a fresh-install integration test, upgrade/rollback tests, and this document.

## Official sources

All sources below were read on **2026-08-13**:

- [3x-ui v3.5.0 release](https://github.com/MHSanaei/3x-ui/releases/tag/v3.5.0) — pinned release, bundled Xray-core v26.7.11 and release notes.
- [3x-ui v3.5.0 release API](https://api.github.com/repos/MHSanaei/3x-ui/releases/tags/v3.5.0) — artifact URLs, sizes and SHA-256 asset digests.
- [3x-ui v3.5.0 install script](https://github.com/MHSanaei/3x-ui/blob/v3.5.0/install.sh) — supported Linux layout, dependencies, CLI usage and the mutable-script/checksum limitations described above.
- [3x-ui v3.5.0 binary CLI source](https://github.com/MHSanaei/3x-ui/blob/v3.5.0/main.go) — `setting` flags and `-getApiToken` bootstrap behaviour.
- [3x-ui v3.5.0 OpenAPI contract](https://github.com/MHSanaei/3x-ui/blob/v3.5.0/frontend/public/openapi.json) — authentication, inbound, client, key, backup and status endpoints.
- [3x-ui API documentation: inbounds](https://github.com/MHSanaei/3x-ui/blob/v3.5.0/docs/content/docs/en/reference/api/inbounds.mdx) and [clients](https://github.com/MHSanaei/3x-ui/blob/v3.5.0/docs/content/docs/en/reference/api/clients.mdx).
- [3x-ui REALITY documentation](https://github.com/MHSanaei/3x-ui/blob/v3.5.0/docs/content/docs/en/config/reality.mdx) — target/SNI, X25519, short IDs, Vision flow and share-link parameters.
- [3x-ui installation documentation](https://github.com/MHSanaei/3x-ui/wiki/Installation) and [update/uninstall documentation](https://github.com/MHSanaei/3x-ui/blob/v3.5.0/docs/content/docs/en/guide/update-uninstall.mdx).
- [Xray-core REALITY configuration](https://xtls.github.io/en/config/transports/reality.html) — current key generation and exact short-ID constraints.
- [Xray transport configuration](https://xtls.github.io/en/config/transport.html) and [RAW transport](https://xtls.github.io/en/config/transports/raw.html) — current transport/security terminology.
- [Xray-core releases](https://github.com/XTLS/Xray-core/releases) — upstream core history. vpnctl uses the core bundled and tested by the pinned 3x-ui release rather than independently following this feed.
