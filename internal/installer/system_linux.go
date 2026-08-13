//go:build linux

package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
	RunInput(context.Context, []byte, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return ExecRunner{}.RunInput(ctx, nil, name, args...)
}

func (ExecRunner) RunInput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive"}
	command.Stdin = bytes.NewReader(input)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return output.Bytes(), fmt.Errorf("%s failed: %w", filepath.Base(name), err)
	}
	if output.Len() > 1<<20 {
		return nil, errors.New("command output exceeded limit")
	}
	return output.Bytes(), nil
}

type FirewallSnapshot struct {
	Active          bool   `json:"active"`
	DefaultIncoming string `json:"defaultIncoming"`
	ManagedSHA256   string `json:"managedSha256,omitempty"`
}

type Firewall struct {
	Runner       Runner
	DefaultsPath string
}

func (f Firewall) Preflight(ctx context.Context) (FirewallSnapshot, error) {
	if _, err := f.Runner.Run(ctx, "ufw", "version"); err != nil {
		return FirewallSnapshot{}, errors.New("ufw is required")
	}
	output, err := f.Runner.Run(ctx, "nft", "list", "ruleset")
	if err != nil {
		return FirewallSnapshot{}, errors.New("nftables ruleset could not be inspected")
	}
	text := string(output)
	if strings.Contains(text, "hook input") && !strings.Contains(text, "ufw-before-input") {
		return FirewallSnapshot{}, errors.New("non-UFW nftables input policy detected")
	}
	defaultsPath := f.DefaultsPath
	if defaultsPath == "" {
		defaultsPath = "/etc/default/ufw"
	}
	ufwDefaults, err := os.ReadFile(defaultsPath)
	if err != nil || !strings.Contains(string(ufwDefaults), "IPV6=yes") {
		return FirewallSnapshot{}, errors.New("UFW IPv6 support must be enabled")
	}
	output, err = f.Runner.Run(ctx, "ufw", "status", "verbose")
	if err != nil {
		return FirewallSnapshot{}, err
	}
	text = string(output)
	defaultIncoming, err := parseDefaultInputPolicy(string(ufwDefaults))
	if err != nil {
		return FirewallSnapshot{}, err
	}
	return FirewallSnapshot{Active: strings.Contains(text, "Status: active"), DefaultIncoming: defaultIncoming}, nil
}

func parseDefaultInputPolicy(content string) (string, error) {
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key != "DEFAULT_INPUT_POLICY" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), "\"")
		switch value {
		case "DROP":
			return "deny", nil
		case "REJECT":
			return "reject", nil
		case "ACCEPT":
			return "allow", nil
		}
	}
	return "", errors.New("UFW default input policy is unsupported or missing")
}

func (f Firewall) Plan(ctx context.Context, sshPort, vpnPort int) ([]string, error) {
	if sshPort == vpnPort {
		return nil, errors.New("SSH and VPN ports must differ")
	}
	output, err := f.Runner.Run(ctx, "ufw", "status")
	if err != nil {
		return nil, err
	}
	text := string(output)
	owned := []string{}
	if !ufwAllows(text, sshPort) {
		owned = append(owned, firewallRule(strconv.Itoa(sshPort)+"/tcp", "vpnctl-ssh"))
	}
	if ufwAllows(text, vpnPort) {
		return nil, errors.New("VPN port already has an unmanaged UFW allow rule")
	}
	owned = append(owned, firewallRule(strconv.Itoa(vpnPort)+"/tcp", "vpnctl-vless"))
	return owned, nil
}

func (f Firewall) Apply(ctx context.Context, owned []string, sshPort int, snapshot FirewallSnapshot) (returned error) {
	defer func() {
		if returned != nil {
			returned = errors.Join(returned, f.RemoveRules(ctx, owned, snapshot))
		}
	}()
	for _, inventory := range owned {
		rule, comment, err := parseFirewallRule(inventory)
		if err != nil {
			return err
		}
		if _, err := f.Runner.Run(ctx, "ufw", "allow", rule, "comment", comment); err != nil {
			return err
		}
	}
	if _, err := f.Runner.Run(ctx, "ufw", "default", "deny", "incoming"); err != nil {
		return err
	}
	_, err := f.Runner.Run(ctx, "ufw", "--force", "enable")
	return err
}

func (f Firewall) RemoveRules(ctx context.Context, owned []string, snapshot FirewallSnapshot) error {
	var joined error
	if err := f.VerifyManaged(ctx, snapshot); err != nil {
		return err
	}
	status, err := f.Runner.Run(ctx, "ufw", "status")
	if err != nil {
		return err
	}
	for _, inventory := range owned {
		rule, comment, parseErr := parseFirewallRule(inventory)
		if parseErr != nil {
			return parseErr
		}
		if ufwOwns(string(status), rule, comment) {
			_, err := f.Runner.Run(ctx, "ufw", "--force", "delete", "allow", rule)
			joined = errors.Join(joined, err)
		}
	}
	switch snapshot.DefaultIncoming {
	case "allow", "deny", "reject":
		_, err := f.Runner.Run(ctx, "ufw", "default", snapshot.DefaultIncoming, "incoming")
		joined = errors.Join(joined, err)
	default:
		joined = errors.Join(joined, errors.New("recorded UFW input policy is invalid"))
	}
	if snapshot.Active {
		_, err := f.Runner.Run(ctx, "ufw", "--force", "enable")
		joined = errors.Join(joined, err)
	} else {
		_, err := f.Runner.Run(ctx, "ufw", "--force", "disable")
		joined = errors.Join(joined, err)
	}
	return joined
}

func (f Firewall) VerifyManaged(ctx context.Context, snapshot FirewallSnapshot) error {
	if snapshot.ManagedSHA256 == "" {
		return nil
	}
	current, err := f.Fingerprint(ctx)
	if err != nil {
		return err
	}
	if current != snapshot.ManagedSHA256 {
		return errors.New("UFW configuration changed after installation; refusing to overwrite administrator changes")
	}
	return nil
}

func (f Firewall) Fingerprint(ctx context.Context) (string, error) {
	output, err := f.Runner.Run(ctx, "ufw", "status", "verbose")
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes.TrimSpace(output))
	return hex.EncodeToString(sum[:]), nil
}

func firewallRule(rule, comment string) string { return rule + "|" + comment }

func parseFirewallRule(inventory string) (string, string, error) {
	rule, comment, ok := strings.Cut(inventory, "|")
	if !ok || (comment != "vpnctl-ssh" && comment != "vpnctl-vless") {
		return "", "", errors.New("recorded UFW rule is invalid")
	}
	port, err := strconv.Atoi(strings.TrimSuffix(rule, "/tcp"))
	if err != nil || port < 1 || port > 65535 || rule != strconv.Itoa(port)+"/tcp" {
		return "", "", errors.New("recorded UFW rule is invalid")
	}
	return rule, comment, nil
}

func ufwOwns(status, rule, comment string) bool {
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == rule && fields[1] == "ALLOW" && strings.Contains(line, "# "+comment) {
			return true
		}
	}
	return false
}

func ufwAllows(status string, port int) bool {
	prefix := strconv.Itoa(port) + "/tcp"
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == prefix && fields[1] == "ALLOW" {
			return true
		}
	}
	return false
}

func detectSSHPort(ctx context.Context, runner Runner, explicit int) (int, error) {
	connectionPort, connected, err := sshConnectionPort()
	if err != nil {
		return 0, err
	}
	if explicit < 0 || explicit > 65535 {
		return 0, errors.New("некорректный порт SSH")
	}
	if explicit > 0 {
		if connected && connectionPort != explicit {
			return 0, errors.New("--ssh-port не совпадает с активным SSH-соединением")
		}
		return explicit, nil
	}
	if connected {
		return connectionPort, nil
	}
	ports, err := sshListenerPorts(ctx, runner)
	if err != nil {
		return 0, err
	}
	if len(ports) == 1 {
		return ports[0], nil
	}
	if len(ports) > 1 {
		return 0, errors.New("обнаружено несколько SSH-портов; передай --ssh-port")
	}
	return 0, errors.New("не удалось определить порт SSH; передай --ssh-port")
}

func sshConnectionPort() (int, bool, error) {
	fields := strings.Fields(os.Getenv("SSH_CONNECTION"))
	if len(fields) != 4 {
		return 0, false, nil
	}
	port, err := strconv.Atoi(fields[3])
	if err != nil || port < 1 || port > 65535 {
		return 0, false, errors.New("некорректный порт SSH")
	}
	return port, true, nil
}

func requiredTools(ctx context.Context, runner Runner) error {
	checks := []struct {
		name string
		args []string
	}{
		{name: "systemctl", args: []string{"--version"}},
		{name: "ip", args: []string{"-Version"}},
		{name: "nft", args: []string{"--version"}},
		{name: "runuser", args: []string{"--version"}},
		{name: "ss", args: []string{"--version"}},
	}
	for _, check := range checks {
		if _, err := runner.Run(ctx, check.name, check.args...); err != nil {
			return fmt.Errorf("required host tool %s is unavailable", check.name)
		}
	}
	return nil
}

func verifySSHListener(ctx context.Context, runner Runner, port int) error {
	connectionPort, connected, err := sshConnectionPort()
	if err != nil {
		return err
	}
	if connected && connectionPort != port {
		return errors.New("--ssh-port не совпадает с активным SSH-соединением")
	}
	ports, err := sshListenerPorts(ctx, runner)
	if err != nil {
		return err
	}
	for _, candidate := range ports {
		if candidate == port {
			return nil
		}
	}
	return errors.New("активный порт SSH не прослушивается")
}

func sshListenerPorts(ctx context.Context, runner Runner) ([]int, error) {
	output, err := runner.Run(ctx, "ss", "-H", "-ltnp")
	if err != nil {
		return nil, errors.New("не удалось проверить SSH-порты")
	}
	unique := map[int]struct{}{}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(line, `"sshd"`) {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		separator := strings.LastIndexByte(parts[3], ':')
		if separator < 0 {
			continue
		}
		port, conversionErr := strconv.Atoi(parts[3][separator+1:])
		if conversionErr == nil && port >= 1 && port <= 65535 {
			unique[port] = struct{}{}
		}
	}
	ports := make([]int, 0, len(unique))
	for port := range unique {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports, nil
}

func bootstrapPanel(ctx context.Context, runner Runner, executable, binary, namespace, username, password string) error {
	if _, err := runner.Run(ctx, "ip", "netns", "add", filepath.Base(namespace)); err != nil {
		return err
	}
	defer runner.Run(context.Background(), "ip", "netns", "delete", filepath.Base(namespace))
	if _, err := runner.Run(ctx, "ip", "-n", filepath.Base(namespace), "link", "set", "lo", "up"); err != nil {
		return err
	}
	panel := exec.CommandContext(ctx, "ip", "netns", "exec", filepath.Base(namespace), binary)
	panel.Dir = filepath.Dir(binary)
	panel.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	panel.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := panel.Start(); err != nil {
		return err
	}
	defer func() { _ = syscall.Kill(-panel.Process.Pid, syscall.SIGTERM); _, _ = panel.Process.Wait() }()
	secret, _ := json.Marshal(map[string]string{"username": username, "password": password})
	_, err := runner.RunInput(ctx, secret, "ip", "netns", "exec", filepath.Base(namespace), executable, "__panel-bootstrap", namespace)
	return err
}

func waitService(ctx context.Context, runner Runner) error {
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := runner.Run(ctx, "systemctl", "is-active", "--quiet", "x-ui.service"); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("x-ui service did not become active")
		case <-ticker.C:
		}
	}
}
