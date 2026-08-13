#!/usr/bin/env bash
set -Eeuo pipefail

test_script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
test_installer="${test_script_dir}/install.sh"
test_root="$(mktemp -d /tmp/vpnctl-install-tests.XXXXXXXX)"
test_count=0
test_work_dirs=()

cleanup() {
  local work_dir
  for work_dir in "${test_work_dirs[@]}"; do
    if [[ "${work_dir}" =~ ^/tmp/vpnctl-bootstrap\.[0-9A-Za-z]{8}$ ]]; then
      rm --force --recursive -- "${work_dir}"
    fi
  done
  rm --force --recursive -- "${test_root}"
}

trap cleanup EXIT

fail() {
  printf 'not ok %s - %s\n' "${test_count}" "$1" >&2
  exit 1
}

pass() {
  printf 'ok %s - %s\n' "${test_count}" "$1"
}

new_case() {
  local name="$1"
  TEST_CASE="${test_root}/${name}"
  TEST_WORK="$(mktemp -d /tmp/vpnctl-bootstrap.XXXXXXXX)"
  test_work_dirs+=("${TEST_WORK}")
  export TEST_CASE
  export TEST_WORK
  mkdir --parents "${TEST_CASE}/fake-bin" "${TEST_CASE}/payload"
  printf '#!/bin/sh\nexit 0\n' >"${TEST_CASE}/payload/vpnctl"
  chmod 0755 "${TEST_CASE}/payload/vpnctl"
  tar --create --gzip --file "${TEST_CASE}/artifact.tar.gz" --directory "${TEST_CASE}/payload" vpnctl

  cat >"${TEST_CASE}/fake-bin/curl" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
output=""
url=""
printf '%s\n' '---' >>"${TEST_CASE}/curl.log"
while (($#)); do
  printf '<%s>\n' "$1" >>"${TEST_CASE}/curl.log"
  case "$1" in
    --output)
      output="$2"
      shift 2
      ;;
    https://*)
      url="$1"
      shift
      ;;
    *)
      shift
      ;;
  esac
done
[[ -n "${output}" && -n "${url}" ]]
case "${url}" in
  */checksums.txt) /bin/cp -- "${TEST_CASE}/checksums.txt" "${output}" ;;
  *) /bin/cp -- "${TEST_CASE}/artifact.tar.gz" "${output}" ;;
esac
EOF

  cat >"${TEST_CASE}/fake-bin/install" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '<%s>\n' "$@" >"${TEST_CASE}/install.log"
args=("$@")
source_path="${args[${#args[@]}-2]}"
target_path="${args[${#args[@]}-1]}"
/bin/cp -- "${source_path}" "${target_path}"
/bin/chmod 0755 "${target_path}"
EOF

  cat >"${TEST_CASE}/fake-bin/id" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "${1:-}" == "-u" ]]
printf '%s\n' 0
EOF

  cat >"${TEST_CASE}/fake-bin/mktemp" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
if [[ "${1:-}" == "-d" ]]; then
  printf '%s\n' "${TEST_WORK}"
else
  : >"${TEST_CASE}/stage"
  printf '%s\n' "${TEST_CASE}/stage"
fi
EOF

  cat >"${TEST_CASE}/fake-bin/mv" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '<%s>\n' "$@" >"${TEST_CASE}/mv.log"
args=("$@")
source_path="${args[${#args[@]}-2]}"
/bin/mv -- "${source_path}" "${TEST_CASE}/final-vpnctl"
EOF

  cat >"${TEST_CASE}/fake-bin/uname" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
case "${1:-}" in
  -s) printf '%s\n' Linux ;;
  -m) printf '%s\n' x86_64 ;;
  *) exit 2 ;;
esac
EOF

  chmod 0755 "${TEST_CASE}/fake-bin/"*
}

write_checksum() {
  local archive="${1:-${TEST_CASE}/artifact.tar.gz}"
  local digest
  digest="$(sha256sum "${archive}")"
  digest="${digest%% *}"
  printf '%s  vpnctl_0.1.0_linux_amd64.tar.gz\n' "${digest}" >"${TEST_CASE}/checksums.txt"
}

run_installer() {
  set +e
  PATH="${TEST_CASE}/fake-bin:/usr/bin:/bin" \
    VPNCTL_VERSION="${VPNCTL_TEST_VERSION:-0.1.0}" \
    VPNCTL_RELEASE_BASE="${VPNCTL_TEST_RELEASE_BASE:-https://releases.example.invalid/v0.1.0}" \
    bash "${test_installer}" </dev/null >"${TEST_CASE}/stdout" 2>"${TEST_CASE}/stderr"
  TEST_EXIT_CODE=$?
  set -e
}

test_count=$((test_count + 1))
new_case happy_path
write_checksum
run_installer
[[ "${TEST_EXIT_CODE}" -eq 3 ]] || fail "verified non-interactive install returned ${TEST_EXIT_CODE}"
cmp --silent "${TEST_CASE}/payload/vpnctl" "${TEST_CASE}/final-vpnctl" || fail "installed payload differs"
[[ ! -d "${TEST_WORK}" ]] || fail "temporary directory was not removed"
[[ "$(grep --count --fixed-strings '<--proto>' "${TEST_CASE}/curl.log")" -eq 2 ]] || fail "artifact downloads do not pin the initial protocol"
[[ "$(grep --count --fixed-strings '<--proto-redir>' "${TEST_CASE}/curl.log")" -eq 2 ]] || fail "artifact downloads do not pin redirect protocols"
[[ "$(grep --count --fixed-strings '<=https>' "${TEST_CASE}/curl.log")" -eq 4 ]] || fail "HTTPS-only curl values are missing"
[[ "$(grep --count --fixed-strings '<--tlsv1.2>' "${TEST_CASE}/curl.log")" -eq 2 ]] || fail "minimum TLS version is missing"
grep --quiet --fixed-strings 'no interactive terminal is available' "${TEST_CASE}/stderr" || fail "non-interactive guidance is missing"
pass "verified download, atomic install, curl policy and cleanup"

test_count=$((test_count + 1))
new_case checksum_mismatch
printf '%064d  vpnctl_0.1.0_linux_amd64.tar.gz\n' 0 >"${TEST_CASE}/checksums.txt"
run_installer
[[ "${TEST_EXIT_CODE}" -eq 1 ]] || fail "checksum mismatch returned ${TEST_EXIT_CODE}"
[[ ! -e "${TEST_CASE}/final-vpnctl" ]] || fail "checksum mismatch reached installation"
grep --quiet --fixed-strings 'artifact checksum verification failed' "${TEST_CASE}/stderr" || fail "checksum mismatch was not reported"
pass "checksum mismatch fails closed"

test_count=$((test_count + 1))
new_case duplicate_checksum
write_checksum
/bin/cp -- "${TEST_CASE}/checksums.txt" "${TEST_CASE}/checksums.first"
/bin/cat "${TEST_CASE}/checksums.first" "${TEST_CASE}/checksums.first" >"${TEST_CASE}/checksums.txt"
run_installer
[[ "${TEST_EXIT_CODE}" -eq 1 ]] || fail "duplicate checksum returned ${TEST_EXIT_CODE}"
grep --quiet --fixed-strings 'no unique valid checksum' "${TEST_CASE}/stderr" || fail "duplicate checksum was not rejected"
pass "duplicate manifest entries are ambiguous"

test_count=$((test_count + 1))
new_case malformed_checksum
digest="$(sha256sum "${TEST_CASE}/artifact.tar.gz")"
digest="${digest%% *}"
printf '%s  vpnctl_0.1.0_linux_amd64.tar.gz trailing-field\n' "${digest}" >"${TEST_CASE}/checksums.txt"
run_installer
[[ "${TEST_EXIT_CODE}" -eq 1 ]] || fail "malformed matching entry returned ${TEST_EXIT_CODE}"
grep --quiet --fixed-strings 'no unique valid checksum' "${TEST_CASE}/stderr" || fail "malformed matching entry was not rejected"
pass "malformed matching manifest entries fail closed"

test_count=$((test_count + 1))
new_case extra_archive_member
printf '%s\n' extra >"${TEST_CASE}/payload/extra"
tar --create --gzip --file "${TEST_CASE}/artifact.tar.gz" --directory "${TEST_CASE}/payload" vpnctl extra
write_checksum
run_installer
[[ "${TEST_EXIT_CODE}" -eq 1 ]] || fail "archive with extra member returned ${TEST_EXIT_CODE}"
grep --quiet --fixed-strings 'exactly one top-level vpnctl file' "${TEST_CASE}/stderr" || fail "extra archive member was not rejected"
pass "unexpected archive layout is rejected before extraction"

test_count=$((test_count + 1))
new_case traversal_archive
tar --create --gzip --file "${TEST_CASE}/artifact.tar.gz" --transform='s#^vpnctl$#../vpnctl#' --directory "${TEST_CASE}/payload" vpnctl
write_checksum
run_installer
[[ "${TEST_EXIT_CODE}" -eq 1 ]] || fail "traversal archive returned ${TEST_EXIT_CODE}"
[[ ! -e "${TEST_CASE}/final-vpnctl" ]] || fail "traversal archive reached installation"
pass "traversal archive is never materialized"

test_count=$((test_count + 1))
new_case oversized_payload
truncate --size=104857601 "${TEST_CASE}/payload/vpnctl"
tar --create --gzip --file "${TEST_CASE}/artifact.tar.gz" --directory "${TEST_CASE}/payload" vpnctl
write_checksum
run_installer
[[ "${TEST_EXIT_CODE}" -eq 1 ]] || fail "oversized payload returned ${TEST_EXIT_CODE}"
[[ ! -e "${TEST_CASE}/final-vpnctl" ]] || fail "oversized payload reached installation"
pass "compressed payload expansion is bounded"

test_count=$((test_count + 1))
new_case invalid_version
write_checksum
VPNCTL_TEST_VERSION='0.1.0/../../root'
run_installer
unset VPNCTL_TEST_VERSION
[[ "${TEST_EXIT_CODE}" -eq 3 ]] || fail "invalid version returned ${TEST_EXIT_CODE}"
[[ ! -e "${TEST_CASE}/curl.log" ]] || fail "invalid version reached the network"
grep --quiet --fixed-strings 'VPNCTL_VERSION is invalid' "${TEST_CASE}/stderr" || fail "invalid version was not reported"
pass "version cannot alter local paths or release URLs"

test_count=$((test_count + 1))
new_case insecure_release_base
write_checksum
VPNCTL_TEST_RELEASE_BASE='http://releases.example.invalid/v0.1.0'
run_installer
unset VPNCTL_TEST_RELEASE_BASE
[[ "${TEST_EXIT_CODE}" -eq 3 ]] || fail "insecure release base returned ${TEST_EXIT_CODE}"
[[ ! -e "${TEST_CASE}/curl.log" ]] || fail "insecure release base reached the network"
grep --quiet --fixed-strings 'must be an HTTPS URL' "${TEST_CASE}/stderr" || fail "insecure release base was not reported"
pass "release base must use HTTPS"

printf '1..%s\n' "${test_count}"
