#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

VPNCTL_VERSION="${VPNCTL_VERSION:-0.1.0}"
VPNCTL_RELEASE_BASE="${VPNCTL_RELEASE_BASE:-https://github.com/OWNER/vpnctl/releases/download/v${VPNCTL_VERSION}}"

fail() {
  printf 'vpnctl bootstrap: %s\n' "$1" >&2
  exit "${2:-1}"
}

command -v id >/dev/null 2>&1 || fail "id is required" 3
[[ "$(id -u)" == "0" ]] || fail "run this command through sudo" 3
[[ ${#VPNCTL_VERSION} -le 64 && "${VPNCTL_VERSION}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]] || fail "VPNCTL_VERSION is invalid" 3
[[ "${VPNCTL_RELEASE_BASE}" == https://* && ! "${VPNCTL_RELEASE_BASE}" =~ [[:space:][:cntrl:]] ]] || fail "VPNCTL_RELEASE_BASE must be an HTTPS URL without whitespace" 3
VPNCTL_RELEASE_BASE="${VPNCTL_RELEASE_BASE%/}"

for vpnctl_command in awk curl head install mktemp mv rm sha256sum stat tar uname; do
  command -v "${vpnctl_command}" >/dev/null 2>&1 || fail "${vpnctl_command} is required" 3
done

case "$(uname -s)" in
  Linux) ;;
  *) fail "only Linux is supported" 3 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) vpnctl_arch="amd64" ;;
  aarch64|arm64) vpnctl_arch="arm64" ;;
  *) fail "only amd64 and arm64 are supported" 3 ;;
esac

vpnctl_tmp_dir="$(mktemp -d /tmp/vpnctl-bootstrap.XXXXXXXX)"
[[ "${vpnctl_tmp_dir}" =~ ^/tmp/vpnctl-bootstrap\.[0-9A-Za-z]{8}$ && -d "${vpnctl_tmp_dir}" && ! -L "${vpnctl_tmp_dir}" ]] || fail "mktemp returned an unsafe bootstrap directory"
readonly vpnctl_tmp_dir
vpnctl_stage_path=""

cleanup() {
  if [[ -n "${vpnctl_stage_path}" && ( -e "${vpnctl_stage_path}" || -L "${vpnctl_stage_path}" ) ]]; then
    rm --force -- "${vpnctl_stage_path}"
  fi
  if [[ -d "${vpnctl_tmp_dir}" && ! -L "${vpnctl_tmp_dir}" ]]; then
    rm --force --recursive -- "${vpnctl_tmp_dir}"
  fi
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM

vpnctl_artifact="vpnctl_${VPNCTL_VERSION}_linux_${vpnctl_arch}.tar.gz"
vpnctl_artifact_path="${vpnctl_tmp_dir}/${vpnctl_artifact}"
vpnctl_manifest_path="${vpnctl_tmp_dir}/checksums.txt"
vpnctl_artifact_url="${VPNCTL_RELEASE_BASE}/${vpnctl_artifact}"
vpnctl_checksums_url="${VPNCTL_RELEASE_BASE}/checksums.txt"

curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
  --tlsv1.2 --connect-timeout 15 --max-time 300 --retry 3 --retry-delay 1 \
  --max-filesize 104857600 --output "${vpnctl_artifact_path}" "${vpnctl_artifact_url}"
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
  --tlsv1.2 --connect-timeout 15 --max-time 60 --retry 3 --retry-delay 1 \
  --max-filesize 1048576 --output "${vpnctl_manifest_path}" "${vpnctl_checksums_url}"

vpnctl_artifact_size="$(stat --format='%s' "${vpnctl_artifact_path}")" || fail "release artifact size cannot be read"
vpnctl_manifest_size="$(stat --format='%s' "${vpnctl_manifest_path}")" || fail "release manifest size cannot be read"
[[ "${vpnctl_artifact_size}" =~ ^[0-9]+$ && "${vpnctl_artifact_size}" -gt 0 && "${vpnctl_artifact_size}" -le 104857600 ]] || fail "release artifact has an invalid size"
[[ "${vpnctl_manifest_size}" =~ ^[0-9]+$ && "${vpnctl_manifest_size}" -gt 0 && "${vpnctl_manifest_size}" -le 1048576 ]] || fail "release manifest has an invalid size"

vpnctl_expected="$(awk -v expected="${vpnctl_artifact}" '
    {
      digest = $1
      filename = $2
      sub(/^\*/, "", filename)
      if (filename == expected) {
        matches++
        if (NF == 2 && length(digest) == 64 && digest !~ /[^0-9A-Fa-f]/) {
          valid++
          checksum = tolower(digest)
        }
      }
    }
    END {
      if (matches == 1 && valid == 1) {
        print checksum
      } else {
        exit 1
      }
    }
  ' "${vpnctl_manifest_path}")" || fail "release manifest has no unique valid checksum for ${vpnctl_artifact}"

printf '%s  %s\n' "${vpnctl_expected}" "${vpnctl_artifact_path}" | sha256sum --check --status - || fail "artifact checksum verification failed"

vpnctl_archive_listing="$(tar --list --gzip --file "${vpnctl_artifact_path}" | head --bytes=4097)" || fail "release archive cannot be read safely"
[[ "${vpnctl_archive_listing}" == "vpnctl" ]] || fail "release archive must contain exactly one top-level vpnctl file"
tar --extract --gzip --to-stdout --file "${vpnctl_artifact_path}" -- vpnctl | head --bytes=104857601 >"${vpnctl_tmp_dir}/vpnctl" || fail "release archive does not contain a bounded regular vpnctl payload"
vpnctl_payload_size="$(stat --format='%s' "${vpnctl_tmp_dir}/vpnctl")" || fail "release payload size cannot be read"
[[ "${vpnctl_payload_size}" =~ ^[0-9]+$ && "${vpnctl_payload_size}" -gt 0 && "${vpnctl_payload_size}" -le 104857600 ]] || fail "release archive contains an invalid vpnctl payload size"

vpnctl_stage_path="$(mktemp /usr/local/bin/.vpnctl.XXXXXXXX)"
install --owner=root --group=root --mode=0755 "${vpnctl_tmp_dir}/vpnctl" "${vpnctl_stage_path}"
mv --force --no-target-directory -- "${vpnctl_stage_path}" /usr/local/bin/vpnctl
vpnctl_stage_path=""

if [[ -t 1 || -t 2 ]]; then
  if exec 3<>/dev/tty 2>/dev/null; then
    cleanup
    trap - EXIT
    exec /usr/local/bin/vpnctl install --interactive <&3 >&3 2>&3
  fi
fi

printf '%s\n' 'vpnctl was verified and installed, but no interactive terminal is available.' >&2
printf '%s\n' 'Continue with: sudo vpnctl install --non-interactive --mode recommended --user <name>' >&2
exit 3
