#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

GO_VERSION="1.24.13"
SOURCE_VERSION="${VPNCTL_SOURCE_VERSION:-0.1.0-source}"

fail() {
	printf 'vpnctl source installer: %s\n' "$1" >&2
	exit "${2:-1}"
}

command -v id >/dev/null 2>&1 || fail "id is required" 3
[[ "$(id -u)" == "0" ]] || fail "run: sudo bash start.sh" 3
[[ "$(uname -s)" == "Linux" ]] || fail "only Linux is supported" 3

for command_name in awk curl grep install mktemp mv readlink rm sha256sum stat tar uname; do
	command -v "${command_name}" >/dev/null 2>&1 || fail "${command_name} is required" 3
done

[[ -r /etc/os-release ]] || fail "/etc/os-release is unavailable" 3
os_id="$(awk -F= '$1 == "ID" {gsub(/^"|"$/, "", $2); print $2}' /etc/os-release)"
os_version="$(awk -F= '$1 == "VERSION_ID" {gsub(/^"|"$/, "", $2); print $2}' /etc/os-release)"
[[ "${os_id}" == "ubuntu" ]] || fail "only Ubuntu is supported" 3
case "${os_version}" in
22.04 | 24.04 | 26.04) ;;
*) fail "supported Ubuntu releases are 22.04, 24.04 and 26.04" 3 ;;
esac

case "$(uname -m)" in
x86_64 | amd64)
	source_arch="amd64"
	go_checksum="1fc94b57134d51669c72173ad5d49fd62afb0f1db9bf3f798fd98ee423f8d730"
	;;
aarch64 | arm64)
	source_arch="arm64"
	go_checksum="74d97be1cc3a474129590c67ebf748a96e72d9f3a2b6fef3ed3275de591d49b3"
	;;
*) fail "only amd64 and arm64 are supported" 3 ;;
esac

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
[[ -f "${repo_dir}/go.mod" && -d "${repo_dir}/cmd/vpnctl" ]] || fail "run this script from the simple-ray checkout" 3
grep -qx 'module github.com/mussy/simple-ray' "${repo_dir}/go.mod" || fail "unexpected Go module" 3

install_parent="/usr/local/bin"
[[ "$(readlink -f -- "${install_parent}")" == "${install_parent}" && -d "${install_parent}" && ! -L "${install_parent}" ]] || fail "install directory is unsafe"
[[ "$(stat --format='%u' "${install_parent}")" == "0" ]] || fail "install directory must be owned by root"
install_mode="$(stat --format='%a' "${install_parent}")"
(((8#${install_mode} & 0022) == 0)) || fail "install directory must not be group/world-writable"

source_tmp="$(mktemp -d /var/tmp/vpnctl-source.XXXXXXXX)"
[[ "${source_tmp}" =~ ^/var/tmp/vpnctl-source\.[0-9A-Za-z]{8}$ && -d "${source_tmp}" && ! -L "${source_tmp}" ]] || fail "mktemp returned an unsafe directory"
readonly source_tmp
installed_stage=""

cleanup() {
	if [[ -n "${installed_stage}" && (-e "${installed_stage}" || -L "${installed_stage}") ]]; then
		rm --force -- "${installed_stage}"
	fi
	if [[ "${source_tmp}" =~ ^/var/tmp/vpnctl-source\.[0-9A-Za-z]{8}$ && -d "${source_tmp}" && ! -L "${source_tmp}" ]]; then
		rm --force --recursive -- "${source_tmp}"
	fi
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM

go_archive="${source_tmp}/go${GO_VERSION}.linux-${source_arch}.tar.gz"
printf 'Downloading verified Go %s toolchain...\n' "${GO_VERSION}" >&2
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
	--tlsv1.2 --connect-timeout 15 --max-time 300 --retry 3 --retry-delay 1 \
	--max-filesize 104857600 --output "${go_archive}" \
	"https://go.dev/dl/go${GO_VERSION}.linux-${source_arch}.tar.gz"

archive_size="$(stat --format='%s' "${go_archive}")" || fail "Go archive size cannot be read"
[[ "${archive_size}" =~ ^[0-9]+$ && "${archive_size}" -gt 0 && "${archive_size}" -le 104857600 ]] || fail "Go archive has an invalid size"
printf '%s  %s\n' "${go_checksum}" "${go_archive}" | sha256sum --check --status - || fail "Go toolchain checksum verification failed"
tar --extract --gzip --file "${go_archive}" --directory "${source_tmp}"
go_binary="${source_tmp}/go/bin/go"
[[ -x "${go_binary}" ]] || fail "verified Go toolchain is incomplete"

export GOTOOLCHAIN=local
export GOPATH="${source_tmp}/gopath"
export GOCACHE="${source_tmp}/gocache"
export GOPROXY="https://proxy.golang.org,direct"
export GOSUMDB="sum.golang.org"

printf 'Testing source...\n' >&2
(cd -- "${repo_dir}" && "${go_binary}" test -count=1 ./...)
(cd -- "${repo_dir}" && "${go_binary}" vet ./...)

printf 'Building vpnctl for linux/%s...\n' "${source_arch}" >&2
(cd -- "${repo_dir}" && CGO_ENABLED=0 GOOS=linux GOARCH="${source_arch}" \
	"${go_binary}" build -trimpath \
	-ldflags "-s -w -X github.com/mussy/simple-ray/internal/domain.ProductVersion=${SOURCE_VERSION}" \
	-o "${source_tmp}/vpnctl" ./cmd/vpnctl)

[[ -s "${source_tmp}/vpnctl" ]] || fail "vpnctl build produced no executable"
installed_stage="$(mktemp /usr/local/bin/.vpnctl-source.XXXXXXXX)"
[[ "${installed_stage}" =~ ^/usr/local/bin/\.vpnctl-source\.[0-9A-Za-z]{8}$ ]] || fail "mktemp returned an unsafe install path"
install --owner=root --group=root --mode=0755 "${source_tmp}/vpnctl" "${installed_stage}"
mv --force --no-target-directory -- "${installed_stage}" /usr/local/bin/vpnctl
installed_stage=""

printf 'vpnctl was built, tested and installed. Starting setup...\n' >&2
if [[ -t 0 && -t 1 && -t 2 ]] && exec 3<>/dev/tty 2>/dev/null; then
	cleanup
	trap - EXIT
	exec /usr/local/bin/vpnctl --interactive install <&3 >&3 2>&3
fi

printf '%s\n' 'vpnctl was installed, but no interactive terminal is available.' >&2
printf '%s\n' 'Continue with: sudo vpnctl --non-interactive install --user <name> --public-address <IP> --ssh-port <port>' >&2
exit 3
