#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
script="${root}/start.sh"

bash -n "${script}"

for expected in \
	'set -Eeuo pipefail' \
	'umask 077' \
	'GO_VERSION="1.24.13"' \
	'https://go.dev/dl/go${GO_VERSION}.linux-${source_arch}.tar.gz' \
	'sha256sum --check --status' \
	'go_binary}" test -count=1 ./...' \
	'go_binary}" vet ./...' \
	'CGO_ENABLED=0 GOOS=linux' \
	'mv --force --no-target-directory' \
	'exec /usr/local/bin/vpnctl --interactive install' \
	'22.04 | 24.04 | 26.04'; do
	grep -Fq "${expected}" "${script}" || {
		printf 'missing source installer contract: %s\n' "${expected}" >&2
		exit 1
	}
done

if grep -Eq 'curl .*(-k|--insecure)|eval |latest' "${script}"; then
	printf 'unsafe source installer pattern detected\n' >&2
	exit 1
fi

printf 'source installer contract tests passed\n'
