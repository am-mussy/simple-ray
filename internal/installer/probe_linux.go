//go:build linux

package installer

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ProbeResult is the outcome of one environment check. Skipped marks a probe
// that could not run at all, which must not be reported to the user as a
// failure of their VPN.
type ProbeResult struct {
	OK      bool
	Skipped bool
	Detail  string
}

func probeOK(detail string) ProbeResult      { return ProbeResult{OK: true, Detail: detail} }
func probeFail(detail string) ProbeResult    { return ProbeResult{Detail: detail} }
func probeSkipped(detail string) ProbeResult { return ProbeResult{Skipped: true, Detail: detail} }

// Diagnostics runs read-only environment checks. It changes nothing.
type Diagnostics struct {
	Runner Runner
}

func NewDiagnostics() Diagnostics { return Diagnostics{Runner: ExecRunner{}} }

// ServiceActive reports whether the managed 3x-ui unit is running.
func (d Diagnostics) ServiceActive(ctx context.Context) ProbeResult {
	output, err := d.Runner.Run(ctx, "systemctl", "is-active", "x-ui")
	state := strings.TrimSpace(string(output))
	if state == "active" {
		return probeOK("active")
	}
	if state == "" {
		if err != nil {
			return probeSkipped("systemctl недоступен")
		}
		state = "unknown"
	}
	return probeFail("состояние службы: " + state)
}

// ClockSynchronized checks NTP synchronisation. Reality embeds a timestamp in
// its handshake, so a server clock that has drifted makes every client fail
// authentication while the connection still appears to establish.
func (d Diagnostics) ClockSynchronized(ctx context.Context) ProbeResult {
	output, err := d.Runner.Run(ctx, "timedatectl", "show", "-p", "NTPSynchronized", "--value")
	if err != nil {
		return probeSkipped("timedatectl недоступен")
	}
	switch strings.TrimSpace(string(output)) {
	case "yes":
		return probeOK("синхронизированы")
	case "no":
		return probeFail("часы сервера не синхронизированы по NTP")
	default:
		return probeSkipped("состояние синхронизации неизвестно")
	}
}

// PortListening verifies something accepts TCP on the VPN port locally.
func (d Diagnostics) PortListening(port int) ProbeResult {
	endpoint := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	connection, err := net.DialTimeout("tcp", endpoint, 3*time.Second)
	if err != nil {
		return probeFail(fmt.Sprintf("порт %d не принимает подключения", port))
	}
	_ = connection.Close()
	return probeOK(fmt.Sprintf("порт %d принимает подключения", port))
}

// FirewallPortOpen verifies UFW allows the VPN port from outside.
func (d Diagnostics) FirewallPortOpen(ctx context.Context, port int) ProbeResult {
	output, err := d.Runner.Run(ctx, "ufw", "status", "verbose")
	if err != nil {
		return probeSkipped("ufw недоступен")
	}
	status := string(output)
	if !strings.Contains(status, "Status: active") {
		return probeFail("UFW выключен: сервер открыт целиком")
	}
	if !ufwAllows(status, port) {
		return probeFail(fmt.Sprintf("UFW не пропускает порт %d/tcp", port))
	}
	return probeOK(fmt.Sprintf("порт %d/tcp разрешён", port))
}

// InternetReachable checks the server itself can reach the internet, without
// which no proxied traffic can leave regardless of VPN configuration.
func (d Diagnostics) InternetReachable(ctx context.Context) ProbeResult {
	hosts := []string{"1.1.1.1:443", "8.8.8.8:443"}
	var failures []string
	for _, host := range hosts {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		connection, err := dialer.DialContext(ctx, "tcp4", host)
		if err == nil {
			_ = connection.Close()
			return probeOK("исходящие соединения работают")
		}
		failures = append(failures, host)
	}
	return probeFail("нет исходящей связи: " + strings.Join(failures, ", "))
}

// DNSWorking checks the system resolver, which the proxy uses for every
// destination a client asks for.
func (d Diagnostics) DNSWorking(ctx context.Context) ProbeResult {
	resolveCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupHost(resolveCtx, "cloudflare.com")
	if err != nil || len(addresses) == 0 {
		return probeFail("система не резолвит доменные имена")
	}
	return probeOK("резолвер отвечает")
}

// RealityTargetHealthy checks the decoy site Reality hides behind. If it stops
// offering TLS 1.3 the masquerade breaks for every client at once.
func (d Diagnostics) RealityTargetHealthy(ctx context.Context, target, sni string) ProbeResult {
	if target == "" || sni == "" {
		return probeSkipped("сайт-прикрытие не задан")
	}
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 8 * time.Second},
		Config: &tls.Config{
			ServerName: sni,
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"h2", "http/1.1"},
		},
	}
	connection, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return probeFail(fmt.Sprintf("сайт-прикрытие %s недоступен", target))
	}
	defer connection.Close()
	tlsConnection, ok := connection.(*tls.Conn)
	if !ok {
		return probeFail("не удалось установить TLS с сайтом-прикрытием")
	}
	state := tlsConnection.ConnectionState()
	if state.Version < tls.VersionTLS13 {
		return probeFail(fmt.Sprintf("сайт-прикрытие %s не поддерживает TLS 1.3", sni))
	}
	if state.NegotiatedProtocol != "h2" {
		return ProbeResult{OK: true, Detail: fmt.Sprintf("TLS 1.3 без HTTP/2 (%s)", sni)}
	}
	return probeOK("TLS 1.3 и HTTP/2 доступны")
}

// DiskSpace reports free space on the volume holding the installation.
func (d Diagnostics) DiskSpace() ProbeResult {
	const minimumBytes = 512 << 20
	available, err := freeDisk(filepath.Dir(programDir))
	if err != nil {
		return probeSkipped("не удалось прочитать объём диска")
	}
	if available < minimumBytes {
		return probeFail(fmt.Sprintf("свободно %d МБ, нужно минимум %d МБ", available>>20, minimumBytes>>20))
	}
	return probeOK(fmt.Sprintf("свободно %d МБ", available>>20))
}

// RunningConfig is what the Xray process actually loaded from disk.
type RunningConfig struct {
	Port         int
	ServerNames  []string
	ShortIDs     []string
	Emails       []string
	UUIDs        []string
	MinClientVer string
}

type rawXrayConfig struct {
	Inbounds []struct {
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
		Tag      string `json:"tag"`
		Settings struct {
			Clients []struct {
				Email string `json:"email"`
				ID    string `json:"id"`
			} `json:"clients"`
		} `json:"settings"`
		StreamSettings struct {
			Security        string `json:"security"`
			RealitySettings struct {
				ServerNames  []string `json:"serverNames"`
				ShortIDs     []string `json:"shortIds"`
				MinClientVer string   `json:"minClientVer"`
			} `json:"realitySettings"`
		} `json:"streamSettings"`
	} `json:"inbounds"`
}

func xrayConfigPath() string { return filepath.Join(programDir, "bin", "config.json") }

// ReadRunningConfig parses the Xray config file on disk for the VPN inbound.
func (d Diagnostics) ReadRunningConfig(port int) (RunningConfig, error) {
	data, err := os.ReadFile(xrayConfigPath())
	if err != nil {
		return RunningConfig{}, err
	}
	var parsed rawXrayConfig
	if err := json.Unmarshal(data, &parsed); err != nil {
		return RunningConfig{}, fmt.Errorf("конфигурация Xray не разбирается: %w", err)
	}
	for _, inbound := range parsed.Inbounds {
		if inbound.Port != port || inbound.StreamSettings.Security != "reality" {
			continue
		}
		result := RunningConfig{
			Port:         inbound.Port,
			ServerNames:  inbound.StreamSettings.RealitySettings.ServerNames,
			ShortIDs:     inbound.StreamSettings.RealitySettings.ShortIDs,
			MinClientVer: inbound.StreamSettings.RealitySettings.MinClientVer,
		}
		for _, client := range inbound.Settings.Clients {
			result.Emails = append(result.Emails, client.Email)
			result.UUIDs = append(result.UUIDs, client.ID)
		}
		sort.Strings(result.Emails)
		sort.Strings(result.UUIDs)
		return result, nil
	}
	return RunningConfig{}, fmt.Errorf("в конфигурации Xray нет Reality-подключения на порту %d", port)
}

// ConfigMatchesUsers compares the users 3x-ui knows about with the users
// present in the Xray config file. 3x-ui applies changes to the live process
// through its API without rewriting the file, so a difference here is stale
// state on disk rather than a broken tunnel.
func (d Diagnostics) ConfigMatchesUsers(port int, expected []string, sni string) ProbeResult {
	running, err := d.ReadRunningConfig(port)
	if err != nil {
		return probeSkipped(err.Error())
	}
	if sni != "" && len(running.ServerNames) > 0 && !containsString(running.ServerNames, sni) {
		return probeFail(fmt.Sprintf("в конфигурации Xray другой sni: %s", strings.Join(running.ServerNames, ", ")))
	}
	wanted := append([]string(nil), expected...)
	sort.Strings(wanted)
	if !equalStrings(wanted, running.Emails) {
		return probeFail(fmt.Sprintf("файл конфигурации содержит пользователей [%s], а 3x-ui — [%s]",
			strings.Join(running.Emails, ", "), strings.Join(wanted, ", ")))
	}
	return probeOK("файл конфигурации совпадает с 3x-ui")
}

// ClientCompatibility checks the REALITY minimum client version. Xray-core
// 26.7.11 defaults it to 26.3.27 when the field is empty, which rejects every
// older client. Rejected clients are relayed to the decoy site instead of being
// disconnected, so the user sees a connected VPN that carries no traffic.
func (d Diagnostics) ClientCompatibility(port int) ProbeResult {
	running, err := d.ReadRunningConfig(port)
	if err != nil {
		return probeSkipped(err.Error())
	}
	switch running.MinClientVer {
	case "":
		return probeFail("minClientVer не задан: Xray отвергает клиенты старее 26.3.27")
	case "0.0.0":
		return probeOK("принимаются клиенты любых версий")
	default:
		return ProbeResult{OK: true, Detail: "минимальная версия клиента: " + running.MinClientVer}
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// XrayBinary returns the path of the bundled Xray core for an architecture.
func XrayBinary(architecture string) string {
	return filepath.Join(programDir, "bin", "xray-linux-"+architecture)
}
