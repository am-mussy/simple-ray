//go:build !linux

package installer

import (
	"context"
	"errors"
	"path/filepath"
)

type ProbeResult struct {
	OK      bool
	Skipped bool
	Detail  string
}

type RunningConfig struct {
	Port         int
	ServerNames  []string
	ShortIDs     []string
	Emails       []string
	UUIDs        []string
	MinClientVer string
}

type Diagnostics struct{}

func NewDiagnostics() Diagnostics { return Diagnostics{} }

func unsupported() ProbeResult {
	return ProbeResult{Skipped: true, Detail: "требуется Linux"}
}

func (Diagnostics) ServiceActive(context.Context) ProbeResult     { return unsupported() }
func (Diagnostics) ClockSynchronized(context.Context) ProbeResult { return unsupported() }
func (Diagnostics) PortListening(int) ProbeResult                 { return unsupported() }
func (Diagnostics) FirewallPortOpen(context.Context, int) ProbeResult {
	return unsupported()
}
func (Diagnostics) InternetReachable(context.Context) ProbeResult { return unsupported() }
func (Diagnostics) DNSWorking(context.Context) ProbeResult        { return unsupported() }
func (Diagnostics) RealityTargetHealthy(context.Context, string, string) ProbeResult {
	return unsupported()
}
func (Diagnostics) DiskSpace() ProbeResult { return unsupported() }
func (Diagnostics) ReadRunningConfig(int) (RunningConfig, error) {
	return RunningConfig{}, errors.New("Linux required")
}
func (Diagnostics) ConfigMatchesUsers(int, []string, string) ProbeResult {
	return unsupported()
}
func (Diagnostics) ClientCompatibility(int) ProbeResult { return unsupported() }

func XrayBinary(architecture string) string {
	return filepath.Join("/usr/local/x-ui", "bin", "xray-linux-"+architecture)
}
