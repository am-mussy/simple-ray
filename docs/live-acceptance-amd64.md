# Live acceptance checklist: Ubuntu amd64 VPS

Этот checklist предназначен для disposable VPS, выданного именно для acceptance.
Он содержит destructive-команды, но сам аудит их не запускает. Выполнять шаги
нужно из двух одновременных SSH-сессий; provider console должна быть доступна до
изменения firewall.

## Current result summary — 2026-08-13

Overall verdict: **PARTIAL PASS / RELEASE BLOCKED**. A destructive lifecycle was
run on a user-provided disposable Ubuntu 26.04 amd64 VPS, then
all managed and acceptance artifacts were removed. The public release remains
blocked by the bootstrap trust gap and the untested platform matrix.

| Stage | Current result | Evidence / blocker |
|---|---|---|
| Local Go regression | PASS | `go test -count=1 ./...` on Go 1.25.6, Windows amd64 |
| Local vet | PASS | `go vet ./...` on the same toolchain |
| Linux compilation | PASS | `GOOS=linux CGO_ENABLED=0 go build ./...` for amd64 and arm64; no Linux tests or runtime were executed |
| Release/bootstrap | BLOCKED | Default URL contains `OWNER`; checksum and artifact share one unauthenticated channel; no signed metadata or repository job producing the expected archives exists |
| Fresh-VPS install | PASS on 26.04 amd64 | Direct locally built binary completed interactive and non-interactive installs; public bootstrap was not used |
| User/URI/QR and network probe | PASS, with limitation | Two clients and their VLESS Reality URIs were verified; external TCP probe saw only SSH 22 and VLESS 443 open. A real VLESS client data-plane transfer was not run |
| Backup creation/no-overwrite | PASS | Real 3x-ui database archive created; repeat destination was rejected |
| Restore | BLOCKED | CLI returns `RESTORE_UNAVAILABLE`, exit 3. API-only restore cannot guarantee rollback if the imported DB rotates token/port/base path or the API never returns |
| Repeat install/idempotency | PASS | State/secrets hashes and UFW status were unchanged |
| Reboot persistence | PASS | x-ui active/enabled, status/doctor healthy, state/secrets unchanged, external port policy retained |
| Update | BLOCKED | CLI returns `UPDATE_UNAVAILABLE`, exit 3; only 3x-ui v3.5.0 is pinned and no signed release A/B update channel exists |
| Uninstall | PASS, explicit policy | Inventory-owned service/resources removed; bootstrap-owned `/usr/local/bin/vpnctl` retained and then removed explicitly for acceptance cleanup |
| Exact baseline cleanup | PASS with documented UFW residue fix | Managed paths/user absent, packages/accounts/default UFW unchanged, UFW inactive, nftables empty, SSH active. The final tree now avoids global nft cleanup and fail-closes on post-install UFW drift; those last safety changes have regression coverage but were not rerun live |

The supported-platform claim for Ubuntu 22.04/24.04/26.04 and amd64/arm64 has
runtime evidence only for Ubuntu 26.04 amd64. Every other matrix
cell still requires provisioning, reboot, client probe and cleanup comparison.

### Source installer retest

The source-checkout path was exercised with the current tree on a fresh Ubuntu
26.04 amd64 VPS:

```bash
sudo bash start.sh
```

The script downloaded checksum-pinned Go 1.24.13, ran all Go tests and vet,
built and atomically installed vpnctl, opened the Recommended wizard and
completed installation. `status` and `doctor` passed, the first user existed,
x-ui was active/enabled, VLESS listened on 443, the panel listened on a random
privileged loopback port, and no subscription listener existed. The temporary
toolchain was removed. The service and all acceptance artifacts were then
uninstalled/deleted; after reboot UFW was inactive, nftables empty, SSH active,
and no vpnctl/3x-ui paths or service account remained.

This validates the source-checkout automation, not the public GitHub clone yet:
the current changes must be committed and pushed before the repository contains
`start.sh`.

## 1. Release prerequisites

Перед стартом записать:

- VPS ID и подтверждение, что его можно полностью уничтожить;
- Ubuntu `22.04`, `24.04` или `26.04`, архитектура `amd64`;
- release A для первоначальной установки и release B для update;
- HTTPS bootstrap URL, immutable artifact URLs, SHA-256 и подпись/attestation;
- ожидаемые 3x-ui/Xray версии;
- public IPv4/IPv6 и внешний probe-host;
- SSH port и provider-firewall rules;
- Reality destination, проверенный для сети этого VPS.

Acceptance блокируется, если VPS не disposable, нет provider console, bootstrap
содержит placeholder `OWNER`, release artifacts не опубликованы или нет способа
проверить publisher identity. Update отмечается `BLOCKED`, а не `PASS`, если нет
двух опубликованных совместимых версий.

Открыть SSH session A и B. Все secret-bearing команды выполнять только в A.

```bash
sudo -i
umask 077
acceptance_dir=/root/vpnctl-acceptance
install -d -m 0700 "$acceptance_dir"
```

## 2. Baseline before vpnctl

Проверить платформу и сохранить immutable baseline:

```bash
set -Eeuo pipefail
. /etc/os-release
printf 'ID=%s VERSION_ID=%s\n' "$ID" "$VERSION_ID"
test "$ID" = ubuntu
case "$VERSION_ID" in 22.04|24.04|26.04) ;; *) exit 3 ;; esac
test "$(dpkg --print-architecture)" = amd64
test "$(id -u)" -eq 0
```

```bash
dpkg-query -W -f='${binary:Package}\t${Version}\n' | sort >"$acceptance_dir/packages.before"
systemctl list-unit-files --no-legend --no-pager | sort >"$acceptance_dir/units.before"
systemctl list-units --all --no-legend --no-pager | sort >"$acceptance_dir/services.before"
ss -H -lntup | sort >"$acceptance_dir/sockets.before"
ip -details address show >"$acceptance_dir/address.before"
ip route show table all >"$acceptance_dir/routes.before"
nft --numeric list ruleset >"$acceptance_dir/nft.before"
ufw status verbose >"$acceptance_dir/ufw.before" 2>&1 || test "$?" -eq 1
find /etc/systemd/system /usr/lib/systemd/system -xdev -printf '%p\t%y\t%m\t%u\t%g\t%l\n' | sort >"$acceptance_dir/systemd-files.before"
getent passwd | sort >"$acceptance_dir/passwd.before"
getent group | sort >"$acceptance_dir/group.before"
```

Проверить отсутствие конфликтующих/управляемых путей. Любой найденный путь —
остановка теста, а не разрешение installer его перезаписать:

```bash
for path in \
  /usr/local/bin/vpnctl \
  /usr/local/x-ui \
  /etc/x-ui \
  /var/lib/vpnctl \
  /var/log/vpnctl \
  /etc/systemd/system/x-ui.service \
  /etc/default/x-ui; do
  if test -e "$path" || test -L "$path"; then
    printf 'Unexpected pre-existing path: %s\n' "$path" >&2
    exit 4
  fi
done
```

Из session B записать текущий SSH port и оставить непрерывный heartbeat:

```bash
printf 'SSH_CONNECTION=%s\n' "$SSH_CONNECTION"
while date -u +%FT%TZ; do sleep 5; done
```

## 3. Bootstrap and install

Current tree: **BLOCKED** until the release prerequisite in the result summary
is resolved. Do not substitute an unsigned ad-hoc archive and call the result a
release acceptance pass.

Сначала скачать и проверить bootstrap, затем запустить именно опубликованный
release A. URL и digest заменить на выданные значения:

```bash
curl -fSLo "$acceptance_dir/install.sh" 'https://<DOMAIN>/install.sh'
printf '<EXPECTED_INSTALL_SH_SHA256>  %s\n' "$acceptance_dir/install.sh" | sha256sum --check --strict
bash -n "$acceptance_dir/install.sh"
less "$acceptance_dir/install.sh"
sudo VPNCTL_VERSION='<RELEASE_A>' bash "$acceptance_dir/install.sh"
```

В wizard выбрать Recommended, создать пользователя `acceptance-main`, проверить
план ports/firewall и подтвердить. PASS требует:

- exit 0 и ни одного raw subprocess/stack trace;
- session B не прерывается; новая session C открывается по тому же SSH port;
- completion показывается только после healthcheck;
- panel обозначена local-only;
- URI/QR видны только в controlling TTY и отсутствуют в persistent logs;
- `/usr/local/bin/vpnctl` — regular root-owned executable, не symlink;
- state/secrets — root-owned `0600`, их директория и backups — `0700`;
- x-ui и Xray active+enabled; слушают ожидаемые addresses/ports;
- public panel port отсутствует во внешнем port scan;
- firewall публично оставляет только SSH и VLESS TCP port.

```bash
vpnctl version
vpnctl status
vpnctl doctor
systemctl is-active x-ui
systemctl is-enabled x-ui
ss -H -lntup
nft --numeric list ruleset >"$acceptance_dir/nft.installed"
ufw status verbose >"$acceptance_dir/ufw.installed" 2>&1 || test "$?" -eq 1
find /var/lib/vpnctl -xdev -printf '%p\t%y\t%m\t%u\t%g\t%l\n' | sort
journalctl --unit x-ui --since '10 minutes ago' --no-pager >"$acceptance_dir/x-ui.install.journal"
```

С внешнего probe-host выполнить TCP/UDP scan по IPv4 и IPv6. PASS: panel не
доступна; unexpected ports отсутствуют; SSH и выбранный VLESS TCP доступны.

## 4. User, URI and QR

```bash
vpnctl user list
vpnctl user add acceptance-phone
vpnctl user list
vpnctl user show acceptance-phone >"$acceptance_dir/acceptance-phone.uri"
chmod 0600 "$acceptance_dir/acceptance-phone.uri"
test "$(wc -l <"$acceptance_dir/acceptance-phone.uri")" -eq 1
grep -Eq '^vless://[^[:space:]]+$' "$acceptance_dir/acceptance-phone.uri"
vpnctl qr acceptance-phone
```

QR вручную импортировать в совместимый Xray client и выполнить реальный probe
через VPN. URI должен содержать правильные address/port, `security=reality`,
`type=tcp`, `flow=xtls-rprx-vision`, `pbk`, `sid`, `sni` и `fp`.

Проверить отсутствие URI/UUID/token/private key во всех non-secret sinks:

```bash
sentinel_uri=$(cat "$acceptance_dir/acceptance-phone.uri")
if journalctl --since '20 minutes ago' --no-pager | grep -F -- "$sentinel_uri"; then
  exit 1
fi
if test -d /var/log/vpnctl && grep -R -F -- "$sentinel_uri" /var/log/vpnctl; then
  exit 1
fi
unset sentinel_uri
```

## 5. Backup and restore oracle

Current tree: backup can be exercised after a live install, but restore is
expected to return `RESTORE_UNAVAILABLE` with exit 3. Therefore the mutation and
restore sequence below is a future acceptance oracle and cannot currently pass.

Создать backup вне vpnctl-owned tree, чтобы финальный uninstall не удалил oracle:

```bash
backup_path="$acceptance_dir/pre-restore.tar.gz"
vpnctl backup --file "$backup_path" --plaintext
test -f "$backup_path" && test ! -L "$backup_path"
test "$(stat -c '%a:%U:%G' "$backup_path")" = '600:root:root'
sha256sum "$backup_path" >"$acceptance_dir/pre-restore.sha256"
tar -tzf "$backup_path" >"$acceptance_dir/pre-restore.members"
```

PASS: archive содержит ровно ожидаемые четыре regular members; checksum из CLI
совпадает; повтор с тем же `--file` fail и не меняет digest.

```bash
before_digest=$(sha256sum "$backup_path")
if vpnctl backup --file "$backup_path" --plaintext; then exit 1; fi
test "$(sha256sum "$backup_path")" = "$before_digest"
unset before_digest
```

Создать контролируемую post-backup мутацию и восстановить:

```bash
vpnctl user add acceptance-after-backup
vpnctl user list | grep -F acceptance-after-backup
vpnctl restore "$backup_path" --yes
vpnctl status
vpnctl doctor
vpnctl user list | grep -F acceptance-main
vpnctl user list | grep -F acceptance-phone
if vpnctl user list | grep -F acceptance-after-backup; then exit 1; fi
```

После restore повторить реальный client probe ранее сохранённым URI. PASS требует,
чтобы original users/credentials работали, post-backup user исчез, healthcheck
прошёл, а safety backup был создан и имеет `0600`.

## 6. Install idempotency

До повторного install сохранить normalized manifest без вывода secret content:

```bash
vpnctl user show acceptance-main >"$acceptance_dir/main.before.uri"
chmod 0600 "$acceptance_dir/main.before.uri"
find /var/lib/vpnctl -xdev -type f -printf '%p\t%m\t%u\t%g\n' | sort >"$acceptance_dir/state-files.before-repeat"
sha256sum /var/lib/vpnctl/state.json /var/lib/vpnctl/secrets.json >"$acceptance_dir/state.before-repeat.sha256"
nft --numeric list ruleset >"$acceptance_dir/nft.before-repeat"
```

```bash
vpnctl install --non-interactive --mode recommended --user acceptance-main
vpnctl user show acceptance-main >"$acceptance_dir/main.after.uri"
cmp "$acceptance_dir/main.before.uri" "$acceptance_dir/main.after.uri"
sha256sum /var/lib/vpnctl/state.json /var/lib/vpnctl/secrets.json >"$acceptance_dir/state.after-repeat.sha256"
cmp "$acceptance_dir/state.before-repeat.sha256" "$acceptance_dir/state.after-repeat.sha256"
nft --numeric list ruleset >"$acceptance_dir/nft.after-repeat"
cmp "$acceptance_dir/nft.before-repeat" "$acceptance_dir/nft.after-repeat"
```

PASS: `Nothing to change`, exit 0, secrets/URI/ports/rules unchanged, no duplicate
users/inbounds/rules, journal does not show unnecessary service restart.

## 7. Reboot persistence

```bash
sync
systemctl reboot
```

Ожидать disconnect. После возвращения SSH:

```bash
sudo -i
set -Eeuo pipefail
acceptance_dir=/root/vpnctl-acceptance
systemctl is-active x-ui
systemctl is-enabled x-ui
vpnctl status
vpnctl doctor
vpnctl user show acceptance-main >"$acceptance_dir/main.after-reboot.uri"
cmp "$acceptance_dir/main.before.uri" "$acceptance_dir/main.after-reboot.uri"
```

Повторить внешний port scan и client probe. PASS: SSH доступен, service/firewall
persistent, users и URI не изменились.

## 8. Explicit update and rollback safety

Current tree: both commands below are expected to return `UPDATE_UNAVAILABLE`
with exit 3. Keep this section `BLOCKED` until two compatible authenticated
releases exist; reinstalling pinned 3x-ui v3.5.0 is repair, not an update.

Проверить read-only path:

```bash
vpnctl update --check
vpnctl version >"$acceptance_dir/version.before-update"
sha256sum /usr/local/bin/vpnctl >"$acceptance_dir/binary.before-update.sha256"
```

Затем update на конкретный release B, никогда не `latest`:

```bash
vpnctl update --version '<RELEASE_B>' --yes
vpnctl version >"$acceptance_dir/version.after-update"
vpnctl status
vpnctl doctor
vpnctl user show acceptance-main >"$acceptance_dir/main.after-update.uri"
cmp "$acceptance_dir/main.before.uri" "$acceptance_dir/main.after-update.uri"
```

PASS: показаны exact current/target, artifact проверен до mutation, создан rollback
backup, версия стала B, users/URI работают. Повтор update на B выдаёт
`Already up to date` и ничего не меняет. Отдельный negative update требует
контролируемого corrupted release origin на другом disposable clone; нельзя
подменять production release на этом VPS.

После update повторить reboot, status/doctor, scan и client probe.

## 9. User removal

```bash
vpnctl user remove acceptance-phone --yes
if vpnctl user show acceptance-phone; then exit 1; fi
vpnctl user list
vpnctl doctor
```

PASS: exit 0, remaining count точен, `acceptance-main` работает. Повторное remove
даёт `USER_NOT_FOUND`, exit 4 и не меняет других users.

## 10. Exact uninstall and cleanup comparison

Current tree: `uninstall` is reachable, but explicitly reports
`binaryRetained:true`. The exact-cleanup oracle below therefore fails on
`/usr/local/bin/vpnctl` until product policy changes. Do not manually remove the
binary during acceptance, because that would hide the contract mismatch.

Перед uninstall сохранить installed diff:

```bash
dpkg-query -W -f='${binary:Package}\t${Version}\n' | sort >"$acceptance_dir/packages.pre-uninstall"
systemctl list-unit-files --no-legend --no-pager | sort >"$acceptance_dir/units.pre-uninstall"
nft --numeric list ruleset >"$acceptance_dir/nft.pre-uninstall"
```

Проверить displayed scope, затем удалить также managed backups:

```bash
vpnctl uninstall --remove-backups --yes
```

После команды проверить каждый owned target, не используя broad recursive delete:

```bash
for path in \
  /usr/local/bin/vpnctl \
  /usr/local/x-ui \
  /etc/x-ui \
  /var/lib/vpnctl \
  /var/log/vpnctl \
  /etc/systemd/system/x-ui.service \
  /etc/default/x-ui; do
  if test -e "$path" || test -L "$path"; then
    printf 'Owned path remains: %s\n' "$path" >&2
    exit 1
  fi
done
```

```bash
if systemctl list-unit-files --no-legend --no-pager | grep -Eq '^x-ui\.service[[:space:]]'; then exit 1; fi
if systemctl is-active --quiet x-ui; then exit 1; fi
if ss -H -lntup | grep -E 'x-ui|xray'; then exit 1; fi
nft --numeric list ruleset >"$acceptance_dir/nft.after-uninstall"
ufw status verbose >"$acceptance_dir/ufw.after-uninstall" 2>&1 || test "$?" -eq 1
systemctl list-unit-files --no-legend --no-pager | sort >"$acceptance_dir/units.after-uninstall"
dpkg-query -W -f='${binary:Package}\t${Version}\n' | sort >"$acceptance_dir/packages.after-uninstall"
```

Сравнение:

```bash
diff -u "$acceptance_dir/nft.before" "$acceptance_dir/nft.after-uninstall"
diff -u "$acceptance_dir/units.before" "$acceptance_dir/units.after-uninstall"
comm -13 "$acceptance_dir/packages.before" "$acceptance_dir/packages.after-uninstall" >"$acceptance_dir/packages.remaining-added"
comm -23 "$acceptance_dir/packages.before" "$acceptance_dir/packages.after-uninstall" >"$acceptance_dir/packages.missing-baseline"
test ! -s "$acceptance_dir/packages.missing-baseline"
```

PASS требует:

- SSH session B и новая session C остаются доступными;
- firewall ruleset семантически совпадает с baseline; если counters/handles дают
  текстовый diff, нормализованный diff не содержит rule/policy changes;
- baseline systemd units/users/groups не удалены;
- vpnctl/x-ui/Xray processes, listeners, units, configs, state, logs и managed
  backups отсутствуют;
- unrelated packages/files/rules не удалены;
- `packages.remaining-added` либо пуст, либо содержит только заранее
  документированные retained dependencies. Retained packages не считать exact
  cleanup без явной принятой policy;
- внешний scan совпадает с baseline;
- root-only acceptance artifacts и portable backup остаются только в
  `/root/vpnctl-acceptance`, вне uninstall scope.

После экспорта sanitized evidence уничтожить disposable VPS через provider API.
Не удалять `/root/vpnctl-acceptance` до разбора результатов: там находятся
secret-bearing URI и plaintext backup, поэтому хранить/передавать каталог можно
только как секретный артефакт, затем уничтожить вместе с VPS.

## 11. Required evidence bundle

Итоговый verdict содержит:

- VPS image/build, kernel, architecture и public networking;
- release A/B versions, artifact digests и signature verification;
- exit code каждого шага;
- status/doctor before and after reboot/update/restore;
- external IPv4/IPv6 scan summaries;
- sanitized journal/log secret scan result;
- before/after firewall, units and package diffs;
- filesystem ownership/mode inventory;
- URI equality checks только как PASS/FAIL, без самих URI;
- backup checksum/restore PASS без публикации archive;
- все deviations с verdict `FAIL`, `BLOCKED` или `NOT TESTED`, но не `PASS`.
