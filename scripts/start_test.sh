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
	'env -i' \
	'GOENV=off' \
	'GOFLAGS=-modcacherw' \
	'GOWORK=off' \
	'setpriv --reuid="${build_uid}" --regid="${build_gid}" --clear-groups' \
	'observed_build_uid="$(run_build id -u)"' \
	'run_build "${go_binary}" -C "${build_repo}" test -count=1 ./...' \
	'run_build "${go_binary}" -C "${build_repo}" vet ./...' \
	'as_root install --owner=root --group=root --mode=0755' \
	'as_root mv --force --no-target-directory' \
	'exec /usr/local/bin/vpnctl --interactive install' \
	'exec sudo -- /usr/local/bin/vpnctl --interactive install' \
	'22.04 | 24.04 | 26.04'; do
	grep -Fq "${expected}" "${script}" || {
		printf 'missing source installer contract: %s\n' "${expected}" >&2
		exit 1
	}
done

if grep -Eq 'curl .*(-k|--insecure)|eval |latest|sudo bash start\.sh' "${script}"; then
	printf 'unsafe source installer pattern detected\n' >&2
	exit 1
fi

if grep -Eq '^\(cd .*go_binary.* (test|vet) ' "${script}"; then
	printf 'Go checks must not run directly in the privileged shell\n' >&2
	exit 1
fi

printf 'source installer contract tests passed\n'
