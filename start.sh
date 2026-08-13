#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

GO_VERSION="1.24.13"
SOURCE_VERSION="${VPNCTL_SOURCE_VERSION:-0.1.0-source}"

fail() {
	printf 'установщик vpnctl: %s\n' "$1" >&2
	exit "${2:-1}"
}

for command_name in awk chmod chown curl env getent grep id install mktemp mv readlink rm sha256sum stat tar uname; do
	command -v "${command_name}" >/dev/null 2>&1 || fail "нужна команда ${command_name}" 3
done

effective_uid="$(id -u)"
build_uid="${effective_uid}"
build_gid="$(id -g)"
use_setpriv=false

if [[ "${effective_uid}" == "0" ]]; then
	command -v setpriv >/dev/null 2>&1 || fail "нужна команда setpriv для непривилегированной сборки" 3
	if [[ "${SUDO_UID:-}" =~ ^[0-9]+$ && "${SUDO_UID}" != "0" && -n "${SUDO_USER:-}" ]] &&
		[[ "$(id -u "${SUDO_USER}" 2>/dev/null || true)" == "${SUDO_UID}" ]]; then
		build_uid="${SUDO_UID}"
		build_gid="${SUDO_GID:-$(id -g "${SUDO_USER}")}"
	else
		build_uid=""
		for _ in {1..32}; do
			candidate_uid="$((60000 + RANDOM % 5000))"
			if ! getent passwd "${candidate_uid}" >/dev/null 2>&1 &&
				! getent group "${candidate_uid}" >/dev/null 2>&1 &&
				! grep -qs -E "^Uid:[[:space:]]+${candidate_uid}([[:space:]]|$)" /proc/[0-9]*/status; then
				build_uid="${candidate_uid}"
				build_gid="${candidate_uid}"
				break
			fi
		done
		[[ -n "${build_uid}" ]] || fail "не удалось выбрать изолированный UID для сборки"
	fi
	[[ "${build_uid}" =~ ^[0-9]+$ && "${build_uid}" != "0" && "${build_gid}" =~ ^[0-9]+$ ]] || fail "сборка не должна выполняться от root"
	use_setpriv=true
else
	command -v sudo >/dev/null 2>&1 || fail "нужна команда sudo для установки в /usr/local/bin" 3
fi

[[ "$(uname -s)" == "Linux" ]] || fail "поддерживается только Linux" 3

[[ -r /etc/os-release ]] || fail "файл /etc/os-release недоступен" 3
os_id="$(awk -F= '$1 == "ID" {gsub(/^"|"$/, "", $2); print $2}' /etc/os-release)"
os_version="$(awk -F= '$1 == "VERSION_ID" {gsub(/^"|"$/, "", $2); print $2}' /etc/os-release)"
[[ "${os_id}" == "ubuntu" ]] || fail "поддерживается только Ubuntu" 3
case "${os_version}" in
22.04 | 24.04 | 26.04) ;;
*) fail "поддерживаются Ubuntu 22.04, 24.04 и 26.04" 3 ;;
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
*) fail "поддерживаются только amd64 и arm64" 3 ;;
esac

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
[[ -f "${repo_dir}/go.mod" && -d "${repo_dir}/cmd/vpnctl" && -d "${repo_dir}/.git" ]] || fail "запусти скрипт из git-клона simple-ray" 3
grep -qx 'module github.com/mussy/simple-ray' "${repo_dir}/go.mod" || fail "неожиданный Go-модуль" 3

install_parent="/usr/local/bin"
[[ "$(readlink -f -- "${install_parent}")" == "${install_parent}" && -d "${install_parent}" && ! -L "${install_parent}" ]] || fail "каталог установки небезопасен"
[[ "$(stat --format='%u' "${install_parent}")" == "0" ]] || fail "каталог установки должен принадлежать root"
install_mode="$(stat --format='%a' "${install_parent}")"
(((8#${install_mode} & 0022) == 0)) || fail "каталог установки не должен быть доступен для записи группе или всем пользователям"

source_tmp="$(mktemp -d /var/tmp/vpnctl-source.XXXXXXXX)"
[[ "${source_tmp}" =~ ^/var/tmp/vpnctl-source\.[0-9A-Za-z]{8}$ && -d "${source_tmp}" && ! -L "${source_tmp}" ]] || fail "mktemp вернул небезопасный каталог"
readonly source_tmp
installed_stage=""

as_root() {
	if [[ "${effective_uid}" == "0" ]]; then
		"$@"
	else
		sudo -- "$@"
	fi
}

cleanup() {
	if [[ -n "${installed_stage}" && (-e "${installed_stage}" || -L "${installed_stage}") ]]; then
		as_root rm --force -- "${installed_stage}"
	fi
	if [[ "${source_tmp}" =~ ^/var/tmp/vpnctl-source\.[0-9A-Za-z]{8}$ && -d "${source_tmp}" && ! -L "${source_tmp}" ]]; then
		chmod --recursive u+rwX -- "${source_tmp}"
		rm --force --recursive -- "${source_tmp}"
	fi
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM

go_archive="${source_tmp}/go${GO_VERSION}.linux-${source_arch}.tar.gz"
printf 'Скачиваю проверенный Go %s...\n' "${GO_VERSION}" >&2
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
	--tlsv1.2 --connect-timeout 15 --max-time 300 --retry 3 --retry-delay 1 \
	--max-filesize 104857600 --output "${go_archive}" \
	"https://go.dev/dl/go${GO_VERSION}.linux-${source_arch}.tar.gz"

archive_size="$(stat --format='%s' "${go_archive}")" || fail "не удалось определить размер архива Go"
[[ "${archive_size}" =~ ^[0-9]+$ && "${archive_size}" -gt 0 && "${archive_size}" -le 104857600 ]] || fail "некорректный размер архива Go"
printf '%s  %s\n' "${go_checksum}" "${go_archive}" | sha256sum --check --status - || fail "не прошла проверка контрольной суммы Go"
tar --extract --gzip --file "${go_archive}" --directory "${source_tmp}"
go_binary="${source_tmp}/go/bin/go"
[[ -x "${go_binary}" ]] || fail "проверенный комплект Go неполный"

build_root="${source_tmp}/build"
build_repo="${build_root}/checkout"
build_home="${build_root}/home"
build_cache="${build_root}/cache"
build_gopath="${build_root}/gopath"
build_tmp="${build_root}/tmp"

if [[ "${use_setpriv}" == "true" ]]; then
	chmod 0711 "${source_tmp}"
	install --directory --mode=0700 "${build_root}" "${build_repo}" "${build_home}" "${build_cache}" "${build_gopath}" "${build_tmp}"
	chown "${build_uid}:${build_gid}" "${build_root}" "${build_repo}" "${build_home}" "${build_cache}" "${build_gopath}" "${build_tmp}"
else
	install --directory --mode=0700 "${build_root}" "${build_repo}" "${build_home}" "${build_cache}" "${build_gopath}" "${build_tmp}"
fi

source_archive="${source_tmp}/checkout.tar"
tar --create --file "${source_archive}" --directory "${repo_dir}" \
	--exclude='./.git' --exclude='./.tools' --exclude='./bin' --exclude='./dist' \
	--exclude='./vpnctl' --exclude='./.tmp-*' --one-file-system .
run_unprivileged() {
	if [[ "${use_setpriv}" == "true" ]]; then
		setpriv --reuid="${build_uid}" --regid="${build_gid}" --clear-groups \
			--inh-caps=-all --ambient-caps=-all --bounding-set=-all --no-new-privs -- "$@"
	else
		"$@"
	fi
}

if [[ "${use_setpriv}" == "true" ]]; then
	chown "${build_uid}:${build_gid}" "${source_archive}"
	run_unprivileged tar --extract --file "${source_archive}" --directory "${build_repo}" --no-same-owner --no-same-permissions
else
	tar --extract --file "${source_archive}" --directory "${build_repo}" --no-same-owner --no-same-permissions
fi
rm --force -- "${source_archive}"

run_build() {
	local environment=(
		env -i
		"HOME=${build_home}"
		"PATH=/usr/bin:/bin"
		"TMPDIR=${build_tmp}"
		"CGO_ENABLED=0"
		"GOENV=off"
		"GOFLAGS=-modcacherw"
		"GONOPROXY="
		"GONOSUMDB="
		"GOPATH=${build_gopath}"
		"GOPRIVATE="
		"GOPROXY=https://proxy.golang.org,direct"
		"GOSUMDB=sum.golang.org"
		"GOTOOLCHAIN=local"
		"GOWORK=off"
	)
	if [[ "${use_setpriv}" == "true" ]]; then
		run_unprivileged "${environment[@]}" "$@"
	else
		"${environment[@]}" "$@"
	fi
}

observed_build_uid="$(run_build id -u)"
[[ "${observed_build_uid}" == "${build_uid}" && "${observed_build_uid}" != "0" ]] || fail "не удалось сбросить root перед сборкой"

printf 'Проверяю исходный код без прав root...\n' >&2
run_build "${go_binary}" -C "${build_repo}" test -count=1 ./...
run_build "${go_binary}" -C "${build_repo}" vet ./...

built_binary="${build_root}/vpnctl"
printf 'Собираю vpnctl для linux/%s без прав root...\n' "${source_arch}" >&2
run_build env "GOOS=linux" "GOARCH=${source_arch}" "${go_binary}" -C "${build_repo}" build -trimpath \
	-ldflags "-s -w -X github.com/mussy/simple-ray/internal/domain.ProductVersion=${SOURCE_VERSION}" \
	-o "${built_binary}" ./cmd/vpnctl

[[ -f "${built_binary}" && ! -L "${built_binary}" && -s "${built_binary}" ]] || fail "сборка vpnctl не создала обычный исполняемый файл"
[[ "$(stat --format='%u' "${built_binary}")" == "${build_uid}" ]] || fail "собранный vpnctl имеет неожиданного владельца"
built_mode="$(stat --format='%a' "${built_binary}")"
(((8#${built_mode} & 0022) == 0)) || fail "собранный vpnctl доступен для записи посторонним"

installed_stage="$(as_root mktemp /usr/local/bin/.vpnctl-source.XXXXXXXX)"
[[ "${installed_stage}" =~ ^/usr/local/bin/\.vpnctl-source\.[0-9A-Za-z]{8}$ ]] || fail "mktemp вернул небезопасный путь установки"
as_root install --owner=root --group=root --mode=0755 "${built_binary}" "${installed_stage}"
as_root mv --force --no-target-directory -- "${installed_stage}" /usr/local/bin/vpnctl
installed_stage=""

printf 'vpnctl собран, проверен и установлен. Запускаю настройку...\n' >&2
if [[ -t 0 && -t 1 && -t 2 ]] && exec 3<>/dev/tty 2>/dev/null; then
	cleanup
	trap - EXIT
	if [[ "${effective_uid}" == "0" ]]; then
		exec /usr/local/bin/vpnctl --interactive install <&3 >&3 2>&3
	fi
	exec sudo -- /usr/local/bin/vpnctl --interactive install <&3 >&3 2>&3
fi

printf '%s\n' 'vpnctl установлен, но интерактивный терминал недоступен.' >&2
printf '%s\n' 'Продолжи командой: sudo vpnctl --non-interactive install --user <имя> --public-address <IP> --ssh-port <порт>' >&2
exit 3
