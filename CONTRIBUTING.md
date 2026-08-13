# Contributing

## Development

Requirements: Go version from `go.mod`, Git and ShellCheck.

```bash
go test ./...
go vet ./...
shellcheck scripts/*.sh
```

Keep the CLI dependency-light and Linux-focused. Do not edit the 3x-ui database, download mutable branches, add telemetry, expose the panel by default or log complete API/config payloads.

## Pull requests

Describe the user-visible change, security impact, rollback behavior and tests. Changes to install/update, archives, filesystem ownership, firewall, subprocess execution, API authentication or secret handling require threat-model review and destructive regression coverage.

Any 3x-ui version bump must update pinned artifact digests, tag-matched API fixtures, compatibility notes and disposable-VM install/upgrade/rollback results.

## Reporting vulnerabilities

Do not open public issues for exploitable findings. Follow [SECURITY.md](SECURITY.md).
