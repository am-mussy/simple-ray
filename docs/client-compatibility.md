# Client compatibility

What a vpnctl link is verified against, and what that verification cannot
cover. The distinction matters: the failure mode of a wrong client parameter is
a tunnel that connects and carries no traffic, so an untested combination does
not announce itself.

## Method

Client cores were driven from the literal share URI `vpnctl qr` emits, parsed
the way an app parses a scanned QR, and used to fetch a public echo service.
A run counts as passing only if the echo returns the VPN server's address.

Cores were run from a second VPS on a different network, 46 ms away, so the
handshake crossed a real internet path with real MTU. This matters: dialling
the server's own public address from the server itself is routed over loopback
by the kernel and exercises none of that.

## Verified

Server: Xray 26.7.11 as shipped by 3x-ui 3.5.0, VLESS TCP Reality,
`minClientVer=0.0.0`, decoy `www.cloudflare.com:443`.

| Client core | safari | chrome | firefox | ios | edge |
| --- | --- | --- | --- | --- | --- |
| Xray 1.8.24 | pass | pass | pass | pass | pass |
| Xray 24.9.30 | pass | pass | pass | pass | pass |
| Xray 25.3.6 | pass | pass | pass | pass | pass |
| Xray 25.9.11 | pass | pass | pass | pass | pass |
| Xray 26.7.11 | pass | pass | pass | pass | pass |
| sing-box 1.12.4 | pass | pass | pass | pass | pass |

That range spans the cores mainstream client apps embed: v2rayNG, v2rayTun and
v2rayA carry an Xray core, Hiddify carries sing-box.

## Rejected profiles, and why

`vpnctl qr --fingerprint` accepts only the five profiles above. The rest are
refused at the point of link generation rather than emitted and left to fail
silently on the user's device.

| Profile | Result |
| --- | --- |
| `android` | fails the handshake on every core tested |
| `360` | fails the handshake on every core tested |
| `random`, `randomized` | passes on Xray 24.9.30 and 26.7.11, fails on 1.8.24, 25.3.6 and 25.9.11 |

A profile that works on some cores is worse than one that works on none: it
turns a deterministic failure into one that depends on which app version the
user happens to have installed.

## Mandatory fields

Verified by omission, on every core in the table:

- **`flow=xtls-rprx-vision`** — a link without it connects and carries no
  traffic on every single client tested, including sing-box. vpnctl provisions
  every client with this flow and pins it into the link rather than inheriting
  whatever 3x-ui returns.
- **`type=tcp`** — Xray renamed this transport to `raw` in 25.x and kept `tcp`
  as an alias. Cores older than 25.x understand only `tcp`, so `tcp` is what
  the link carries.
- **`encryption=none`** — required explicitly by several client apps.

## What this does not cover

- **Mobile carrier paths.** Both test endpoints are VPS hosts. A handset on a
  mobile network takes a path with different MTU and different middleboxes, and
  one field report exists of `chrome` failing there where `safari` worked on
  the same server and the same handset. That report is why `safari` is the
  default, and it is not reproducible from a VPS.
- **App-level link parsing.** The matrix drives cores directly from the URI
  fields. It does not prove a given app builds the same config from the same
  URI.
- **Censorship response.** Nothing here measures whether a network operator
  blocks the endpoint.

So the honest claim is this: the link vpnctl hands out is correct against every
client core in common use, over a real network path. It is not a guarantee that
every app on every network will pass traffic, and no check that runs on the
server can make that guarantee.
