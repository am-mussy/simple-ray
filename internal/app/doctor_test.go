package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mussy/simple-ray/internal/domain"
	"github.com/mussy/simple-ray/internal/installer"
	"github.com/mussy/simple-ray/internal/state"
	"github.com/mussy/simple-ray/internal/xui"
)

const doctorRawLink = "vless://11111111-2222-3333-4444-555555555555@127.0.0.1:1?" +
	"encryption=none&flow=xtls-rprx-vision&fp=chrome&" +
	"pbk=cTwKVesuotkAtmlKvQGGFqCfK9sL-8OaHHsLAbapxFQ&security=reality&" +
	"sid=d38043ca18700534&sni=example.com&spx=%2Fabc&type=tcp#vpnctl"

// stubProbes reports a healthy machine unless a test overrides one probe.
type stubProbes struct {
	drift installer.ProbeResult
}

func okProbe() installer.ProbeResult { return installer.ProbeResult{OK: true, Detail: "ok"} }

func (stubProbes) ServiceActive(context.Context) installer.ProbeResult         { return okProbe() }
func (stubProbes) ClockSynchronized(context.Context) installer.ProbeResult     { return okProbe() }
func (stubProbes) PortListening(int) installer.ProbeResult                     { return okProbe() }
func (stubProbes) FirewallPortOpen(context.Context, int) installer.ProbeResult { return okProbe() }
func (stubProbes) InternetReachable(context.Context) installer.ProbeResult     { return okProbe() }
func (stubProbes) DNSWorking(context.Context) installer.ProbeResult            { return okProbe() }
func (stubProbes) RealityTargetHealthy(context.Context, string, string) installer.ProbeResult {
	return okProbe()
}
func (stubProbes) DiskSpace() installer.ProbeResult              { return okProbe() }
func (stubProbes) ClientCompatibility(int) installer.ProbeResult { return okProbe() }
func (p stubProbes) ConfigMatchesUsers(int, []string, string) installer.ProbeResult {
	if p.drift != (installer.ProbeResult{}) {
		return p.drift
	}
	return okProbe()
}

type doctorAPI struct {
	emptyLinksAPI
	links   []string
	clients []xui.ClientRecord
}

func (a doctorAPI) ListClients(context.Context) ([]xui.ClientRecord, error) {
	if a.clients != nil {
		return a.clients, nil
	}
	return []xui.ClientRecord{{Email: "alice", Enable: true, InboundIDs: []int64{1}}}, nil
}

func (a doctorAPI) ClientLinks(context.Context, string) ([]string, error) {
	if a.links != nil {
		return a.links, nil
	}
	return []string{doctorRawLink}, nil
}

func (doctorAPI) Status(context.Context) (xui.ServerStatus, error) {
	var status xui.ServerStatus
	status.Xray.State = "running"
	return status, nil
}

func newDoctorService(t *testing.T, api API, probes Probes, tunnel TunnelProbe) *Service {
	t.Helper()
	store := state.New(t.TempDir())
	if err := store.SaveState(validState()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSecrets(domain.Secrets{SchemaVersion: domain.SchemaVersion, APIToken: "token"}); err != nil {
		t.Fatal(err)
	}
	service := New(store, filepath.Join(t.TempDir(), "runtime", "lock"))
	service.APIFactory = func(domain.State, domain.Secrets) (API, error) { return api, nil }
	service.Diagnostics = probes
	service.Tunnel = tunnel
	return service
}

func findCheck(t *testing.T, checks []domain.Check, name string) domain.Check {
	t.Helper()
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("check %q was not run", name)
	return domain.Check{}
}

func countStatus(checks []domain.Check, status string) int {
	total := 0
	for _, check := range checks {
		if check.Status == status {
			total++
		}
	}
	return total
}

func workingTunnel(context.Context, string, domain.ClientLink) (string, error) {
	return "203.0.113.1", nil
}

func TestDoctorPassesOnHealthyServer(t *testing.T) {
	service := newDoctorService(t, doctorAPI{}, stubProbes{}, workingTunnel)
	checks := service.Doctor(context.Background(), false)
	if failed := countStatus(checks, statusFailed); failed != 0 {
		t.Fatalf("healthy server reported %d failures", failed)
	}
	if findCheck(t, checks, "сквозная проверка трафика").Status != statusPassed {
		t.Fatal("tunnel check did not pass")
	}
}

// Regression test for the failure that motivated this work: every static check
// passes and the client connects, yet no traffic flows because Reality relays
// unauthenticated clients to its decoy site.
func TestDoctorFailsWhenTunnelCarriesNoTraffic(t *testing.T) {
	broken := func(context.Context, string, domain.ClientLink) (string, error) {
		return "", errors.New("трафик через туннель не прошёл")
	}
	service := newDoctorService(t, doctorAPI{}, stubProbes{}, broken)
	checks := service.Doctor(context.Background(), false)

	tunnel := findCheck(t, checks, "сквозная проверка трафика")
	if tunnel.Status != statusFailed {
		t.Fatalf("broken tunnel reported as %q", tunnel.Status)
	}
	if !strings.Contains(tunnel.Hint, "vpnctl qr") {
		t.Fatalf("hint does not tell the user to re-import the link: %q", tunnel.Hint)
	}
}

func TestDoctorRejectsLinkPointingElsewhere(t *testing.T) {
	stale := strings.Replace(doctorRawLink, "sni=example.com", "sni=www.microsoft.com", 1)
	service := newDoctorService(t, doctorAPI{links: []string{stale}}, stubProbes{}, workingTunnel)
	checks := service.Doctor(context.Background(), false)

	link := findCheck(t, checks, "ссылка для клиента")
	if link.Status != statusFailed || link.Hint == "" {
		t.Fatalf("stale link check = %+v", link)
	}
}

// Traffic flows and a stale config file is normal 3x-ui behaviour, so neither
// may be reported to the user as a broken VPN.
func TestDoctorWarnsInsteadOfFailing(t *testing.T) {
	elsewhere := func(context.Context, string, domain.ClientLink) (string, error) {
		return "198.51.100.9", nil
	}
	probes := stubProbes{drift: installer.ProbeResult{Detail: "файл конфигурации отстал"}}
	service := newDoctorService(t, doctorAPI{}, probes, elsewhere)
	checks := service.Doctor(context.Background(), false)

	if findCheck(t, checks, "сквозная проверка трафика").Status != statusUnavailable {
		t.Fatal("differing exit address should warn, not fail")
	}
	if findCheck(t, checks, "конфигурация Xray синхронна").Status != statusUnavailable {
		t.Fatal("config drift should warn, not fail")
	}
	if failed := countStatus(checks, statusFailed); failed != 0 {
		t.Fatalf("warnings escalated to %d failures", failed)
	}
}

func TestDoctorQuickSkipsTunnelProbe(t *testing.T) {
	called := false
	tunnel := func(context.Context, string, domain.ClientLink) (string, error) {
		called = true
		return "203.0.113.1", nil
	}
	service := newDoctorService(t, doctorAPI{}, stubProbes{}, tunnel)
	if service.Doctor(context.Background(), true); called {
		t.Fatal("--quick still ran the slow tunnel probe")
	}
}

// Someone who does not know how Reality works needs an action for every
// failure, not just a red line.
func TestDoctorGivesAnActionForEveryFailure(t *testing.T) {
	broken := func(context.Context, string, domain.ClientLink) (string, error) {
		return "", errors.New("нет трафика")
	}
	service := newDoctorService(t, doctorAPI{clients: []xui.ClientRecord{}}, stubProbes{}, broken)
	checks := service.Doctor(context.Background(), false)

	failures := 0
	for _, check := range checks {
		if check.Status != statusFailed {
			continue
		}
		failures++
		if check.Hint == "" || check.Reason == "" {
			t.Fatalf("check %q failed without a reason or an action: %+v", check.Name, check)
		}
	}
	if failures == 0 {
		t.Fatal("expected at least one failure to inspect")
	}
}
